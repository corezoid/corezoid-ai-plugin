package main

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// stageArchive is a stage export as Corezoid ships it: a wrapper directory
// named like the download, holding the <id>_<name>.stage tree.
func stageArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"stage_42_1700000000000.zip/42_demo.stage/100_orders.folder/555_proc.conv.json": `{"obj_id":555,"uuid":"u","scheme":{"nodes":[{"id":"n1","uuid":"x"}]}}`,
		"stage_42_1700000000000.zip/42_demo.stage/556_top.conv.json":                    `{"obj_id":556,"scheme":{"nodes":[{"id":"n2"}]}}`,
	} {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content)) //nolint:errcheck
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stageExportServer answers every export with a download URL serving archive.
func stageExportServer(t *testing.T, archive []byte) *Executor {
	t.Helper()
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive) //nolint:errcheck
	}))
	t.Cleanup(dl.Close)
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		results := make([]interface{}, len(ops))
		for i := range ops {
			results[i] = map[string]interface{}{"proc": "ok", "download_url": dl.URL + "/dl"}
		}
		return map[string]interface{}{"request_proc": "ok", "ops": results}
	})
	e.Ctx = context.Background()
	e.StageID = 42
	return e
}

// A pull used to format every JSON file under the working directory, so one
// unrelated file it could not parse failed the whole pull after the download
// had succeeded.
func TestDownloadStageRecursively_LeavesUnrelatedJSONAlone(t *testing.T) {
	dest := t.TempDir()
	unrelated := map[string]string{
		"node_modules/pkg/package.json": "{ broken",
		"tsconfig.json":                 "{\n  // comment\n  \"strict\": true\n}\n",
		"bom.json":                      "\xef\xbb\xbf{\"a\":1}",
		"compact.json":                  `{"a":1}`,
	}
	for name, content := range unrelated {
		p := filepath.Join(dest, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	e := stageExportServer(t, stageArchive(t))
	if _, err := downloadStageRecursively(e, 42, dest); err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	for name, content := range unrelated {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != content {
			t.Errorf("%s was rewritten: %q", name, got)
		}
	}

	proc, err := os.ReadFile(filepath.Join(dest, "100_orders", "555_proc.conv.json"))
	if err != nil {
		t.Fatalf("pulled process missing: %v", err)
	}
	if strings.Contains(string(proc), "uuid") || !strings.Contains(string(proc), "\n  ") {
		t.Errorf("pulled process should be formatted without uuid, got %s", proc)
	}
	if _, err := os.Stat(filepath.Join(dest, "556_top.conv.json")); err != nil {
		t.Errorf("top-level process missing: %v", err)
	}
	assertNoPullLeftovers(t, dest)
}

// Two pulls into the same directory used to share the archive path and the
// unwrap step, so one failed on the other's half-extracted files.
func TestDownloadStageRecursively_ParallelPullsSameDir(t *testing.T) {
	dest := t.TempDir()
	archive := stageArchive(t)

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		e := stageExportServer(t, archive)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = downloadStageRecursively(e, 42, dest)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("pull %d failed: %v", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "100_orders", "555_proc.conv.json")); err != nil {
		t.Errorf("pulled process missing: %v", err)
	}
	assertNoPullLeftovers(t, dest)
}

func assertNoPullLeftovers(t *testing.T, dest string) {
	t.Helper()
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		n := en.Name()
		if strings.HasPrefix(n, ".corezoid-pull-") || strings.HasSuffix(n, ".zip") || strings.HasSuffix(n, ".stage") {
			t.Errorf("pull left %s behind in the workspace", n)
		}
	}
}

func TestFormatJSON_SkipsUnparsableFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{ nope"), 0644)                                         //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "good.json"), []byte(`{"uuid":"x","a":1}`), 0644) //nolint:errcheck

	if err := formatJSON(dir); err != nil {
		t.Fatalf("formatJSON should skip an unparsable file, got: %v", err)
	}
	if got, _ := os.ReadFile(bad); string(got) != "{ nope" {
		t.Errorf("unparsable file was rewritten: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "good.json")); strings.Contains(string(got), "uuid") {
		t.Errorf("valid file was not formatted: %s", got)
	}
}
