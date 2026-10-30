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
	"time"
)

// stageArchive is a stage export as Corezoid ships it: a wrapper directory
// named like the download, holding the <id>_<name>.stage tree.
func stageArchive(t *testing.T) []byte {
	return stageArchiveWith(t, nil)
}

// stageArchiveWith is stageArchive plus extra entries, keyed by their path
// inside the 42_demo.stage directory.
func stageArchiveWith(t *testing.T, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := map[string]string{
		"stage_42_1700000000000.zip/42_demo.stage/100_orders.folder/555_proc.conv.json": `{"obj_id":555,"uuid":"u","scheme":{"nodes":[{"id":"n1","uuid":"x"}]}}`,
		"stage_42_1700000000000.zip/42_demo.stage/556_top.conv.json":                    `{"obj_id":556,"scheme":{"nodes":[{"id":"n2"}]}}`,
	}
	for name, content := range extra {
		entries["stage_42_1700000000000.zip/42_demo.stage/"+name] = content
	}
	for name, content := range entries {
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

// A pull killed before its deferred cleanup leaves its staging directory, with
// a copy of every process, in the workspace. The next pull removes it, but
// not a recent one that may belong to a pull still running.
func TestNewPullStaging_RemovesAbandonedStaging(t *testing.T) {
	dest := t.TempDir()
	abandoned := filepath.Join(dest, pullStagingPrefix+"abandoned")
	live := filepath.Join(dest, pullStagingPrefix+"live")
	for _, d := range []string{abandoned, live} {
		if err := os.MkdirAll(filepath.Join(d, "42_demo.stage"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * stalePullStagingAge)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatal(err)
	}

	staging, err := newPullStaging(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staging)

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("abandoned staging directory was kept (stat err: %v)", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("recent staging directory was removed: %v", err)
	}
}

// A staging directory's name sorts ahead of every "<id>_<name>" path, so a
// stale copy inside it used to win the lookup push-process does by id.
func TestProcessFileForID_IgnoresStagingCopy(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	proc := `{"obj_id":555,"title":"p","scheme":{"nodes":[]}}`
	for _, p := range []string{
		filepath.Join(pullStagingPrefix+"abandoned", "42_demo.stage", "100_orders", "555_proc.conv.json"),
		filepath.Join("100_orders", "555_proc.conv.json"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(proc), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := processFileForID(555), filepath.Join("100_orders", "555_proc.conv.json"); got != want {
		t.Errorf("processFileForID(555) = %q, want %q", got, want)
	}
	n := recordPulledBaselines(".", map[int]baselineEntry{555: {}}, nil)
	if n != 1 {
		t.Errorf("recordPulledBaselines recorded %d baselines, want 1 (the staging copy must be ignored)", n)
	}
}

// Formatting used to fail the whole pull on a file it could not parse; then it
// published such a file as is, with only a log line. A process file that is
// not valid JSON is now left out of the pull and reported as skipped.
func TestDownloadStageRecursively_UnparsableProcessIsSkipped(t *testing.T) {
	dest := t.TempDir()
	e := stageExportServer(t, stageArchiveWith(t, map[string]string{
		"100_orders.folder/557_broken.conv.json": `{"obj_id":557,`,
	}))

	skipped, err := downloadStageRecursively(e, 42, dest)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if len(skipped) != 1 || skipped[0].processID() != 557 {
		t.Fatalf("skipped = %v, want the broken process 557", skipped)
	}
	if _, err := os.Stat(filepath.Join(dest, "100_orders", "557_broken.conv.json")); !os.IsNotExist(err) {
		t.Errorf("unparsable process was published (stat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "100_orders", "555_proc.conv.json")); err != nil {
		t.Errorf("valid process missing: %v", err)
	}
}

// A pull replaces each folder as a whole. A process it skipped inside one used
// to lose its earlier local copy, baseline and merge ancestor along with the
// old folder, while the pull reported success. They are now carried into the
// new folder, and the copy keeps the baseline it had.
func TestDownloadStageRecursively_SkippedProcessKeepsLocalCopy(t *testing.T) {
	dest := t.TempDir()
	orders := filepath.Join(dest, "100_orders")
	if err := os.MkdirAll(orders, 0755); err != nil {
		t.Fatal(err)
	}
	local := `{"obj_id":557,"title":"my edits"}`
	if err := os.WriteFile(filepath.Join(orders, "557_broken.conv.json"), []byte(local), 0644); err != nil {
		t.Fatal(err)
	}
	old := baselineEntry{ChangeTime: 111}
	if err := writeBaseline(orders, 557, old); err != nil {
		t.Fatal(err)
	}
	if err := writeAncestorScheme(orders, 557, `{"obj_id":557}`); err != nil {
		t.Fatal(err)
	}

	e := stageExportServer(t, stageArchiveWith(t, map[string]string{
		"100_orders.folder/557_broken.conv.json": `{"obj_id":557,`,
	}))
	skipped, err := downloadStageRecursively(e, 42, dest)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	snapshot := map[int]baselineEntry{555: {ChangeTime: 222}, 557: {ChangeTime: 222}}
	recordPulledBaselines(dest, snapshot, skipped)

	if b, err := os.ReadFile(filepath.Join(orders, "557_broken.conv.json")); err != nil || string(b) != local {
		t.Errorf("local copy of the skipped process = %q, %v; want it kept unchanged", b, err)
	}
	if got, ok, err := lookupBaseline(orders, 557); err != nil || !ok || got.ChangeTime != old.ChangeTime {
		t.Errorf("baseline of the skipped process = %+v, %v, %v; want the old one kept", got, ok, err)
	}
	if got, ok := readAncestorScheme(orders, 557); !ok || got != `{"obj_id":557}` {
		t.Errorf("ancestor of the skipped process = %q, %v; want the old one kept", got, ok)
	}
	if got, ok, _ := lookupBaseline(orders, 555); !ok || got.ChangeTime != 222 {
		t.Errorf("baseline of the pulled process = %+v, %v; want the fresh one", got, ok)
	}
}
