package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runShFixture lays out a throwaway plugin root holding a copy of run.sh and a
// plugin.json, plus a stub bin dir that shadows uname/curl/go. The stub curl
// serves files out of releaseDir and records its argv; the stub go reports the
// source fallback instead of compiling.
type runShFixture struct {
	runSh      string
	home       string
	stubBin    string
	releaseDir string
	curlLog    string
}

func newRunShFixture(t *testing.T, unameS, unameM string) *runShFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised through a POSIX sh")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	root := t.TempDir()
	f := &runShFixture{
		runSh:      filepath.Join(root, "plugin", "mcp-server", "run.sh"),
		home:       filepath.Join(root, "home"),
		stubBin:    filepath.Join(root, "bin"),
		releaseDir: filepath.Join(root, "release"),
		curlLog:    filepath.Join(root, "curl.log"),
	}
	src, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeRunShFile(t, f.runSh, string(src), 0755)
	writeRunShFile(t, filepath.Join(root, "plugin", ".claude-plugin", "plugin.json"), `{"name":"corezoid","version": "9.9.9"}`, 0644)
	for _, d := range []string{f.home, f.releaseDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	writeRunShFile(t, filepath.Join(f.stubBin, "uname"), `#!/bin/sh
case "$1" in
  -s) echo "`+unameS+`" ;;
  -m) echo "`+unameM+`" ;;
esac
`, 0755)
	writeRunShFile(t, filepath.Join(f.stubBin, "curl"), `#!/bin/sh
echo "$@" >> "`+f.curlLog+`"
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift ;;
    http*) url="$1" ;;
  esac
  shift
done
src="`+f.releaseDir+`/${url##*/}"
[ -f "$src" ] || exit 22
cp "$src" "$out"
`, 0755)
	writeRunShFile(t, filepath.Join(f.stubBin, "go"), "#!/bin/sh\necho go-source-fallback\n", 0755)
	return f
}

func writeRunShFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// publish places a fake prebuilt binary named asset in the stub release and
// writes a matching checksums.txt in sha256sum's format.
func (f *runShFixture) publish(t *testing.T, asset string) {
	t.Helper()
	bin := "#!/bin/sh\necho prebuilt-binary\n"
	writeRunShFile(t, filepath.Join(f.releaseDir, asset), bin, 0755)
	sum := sha256.Sum256([]byte(bin))
	writeRunShFile(t, filepath.Join(f.releaseDir, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+asset+"\n", 0644)
}

func (f *runShFixture) run(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("sh", f.runSh, "--version")
	cmd.Env = []string{
		"HOME=" + f.home,
		"PATH=" + f.stubBin + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run.sh failed: %v\n%s", err, out)
	}
	return string(out)
}

// On Git Bash / MSYS2 the published windows binary must be used; before the
// fix run.sh only knew darwin/linux and every Windows start fell through to a
// `go run .` compile that overran the client's startup timeout.
func TestRunSh_WindowsUsesPrebuiltBinary(t *testing.T) {
	for _, uname := range []string{"MINGW64_NT-10.0-22631", "MSYS_NT-10.0-22631", "CYGWIN_NT-10.0"} {
		t.Run(uname, func(t *testing.T) {
			f := newRunShFixture(t, uname, "x86_64")
			f.publish(t, "convctl-windows-amd64.exe")
			out := f.run(t)
			if !strings.Contains(out, "prebuilt-binary") {
				t.Fatalf("expected the prebuilt windows binary to run, got:\n%s", out)
			}
			cached := filepath.Join(f.home, ".cache", "corezoid-mcp", "9.9.9", "convctl-windows-amd64.exe")
			if _, err := os.Stat(cached); err != nil {
				t.Fatalf("binary not cached at %s: %v", cached, err)
			}
		})
	}
}

func TestRunSh_LinuxUsesPrebuiltBinary(t *testing.T) {
	f := newRunShFixture(t, "Linux", "aarch64")
	f.publish(t, "convctl-linux-arm64")
	if out := f.run(t); !strings.Contains(out, "prebuilt-binary") {
		t.Fatalf("expected the prebuilt linux binary to run, got:\n%s", out)
	}
}

// A stalled release download must not hold startup open indefinitely: curl
// gets a hard deadline well inside the client's 30-second budget.
func TestRunSh_DownloadIsTimeBounded(t *testing.T) {
	f := newRunShFixture(t, "Linux", "x86_64")
	f.publish(t, "convctl-linux-amd64")
	f.run(t)
	log, err := os.ReadFile(f.curlLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		if !strings.Contains(line, "--max-time") || !strings.Contains(line, "--connect-timeout") {
			t.Errorf("curl invoked without a timeout: %s", line)
		}
	}
}

func TestRunSh_FallsBackToSourceWithoutRelease(t *testing.T) {
	f := newRunShFixture(t, "Linux", "x86_64")
	if out := f.run(t); !strings.Contains(out, "go-source-fallback") {
		t.Fatalf("expected go source fallback, got:\n%s", out)
	}
}
