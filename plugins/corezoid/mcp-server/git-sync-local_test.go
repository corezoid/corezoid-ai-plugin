package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testMirrorBlock(body string) string {
	return mirrorBeginMarker + "\n" + body + "\n" + mirrorEndMarker + "\n"
}

func TestInjectMirrorBlock(t *testing.T) {
	block := testMirrorBlock("## Stage 1_dev process index\n\n- a  `a.conv.json`  (id 1)")
	notes := "# CLAUDE.md\n\nDeveloper notes.\n"

	tests := []struct {
		name     string
		existing string
		newBlock string
		want     string
	}{
		{"empty root", "", block, block},
		{"root without block", notes, block, block + "\n" + notes},
		{
			"replaces old block, keeps notes",
			testMirrorBlock("old") + "\n" + notes,
			block,
			block + "\n" + notes,
		},
		{
			"keeps content before the block",
			"intro\n" + testMirrorBlock("old") + "\n" + notes,
			block,
			"intro\n" + block + "\n" + notes,
		},
		{
			"injects only the marker region of a source carrying notes",
			testMirrorBlock("old") + "\n" + notes,
			block + "\n" + notes,
			block + "\n" + notes,
		},
		{
			"collapses the blank lines left behind the block",
			testMirrorBlock("old") + "\n\n\n\n" + notes,
			block,
			block + "\n" + notes,
		},
		{"block only, nothing after", testMirrorBlock("old"), block, block},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := injectMirrorBlock(tt.existing, tt.newBlock); got != tt.want {
				t.Errorf("injectMirrorBlock() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestInjectMirrorBlockIsIdempotent(t *testing.T) {
	block := testMirrorBlock("index")
	notes := "# CLAUDE.md\n\nDeveloper notes.\n"
	first := injectMirrorBlock(notes, block)
	got := first
	for i := 0; i < 5; i++ {
		got = injectMirrorBlock(got, block)
	}
	if got != first {
		t.Errorf("repeated injects changed the file:\nfirst %q\nlast  %q", first, got)
	}
}

// TestCopyStageCLAUDEMDDoesNotDuplicateNotes reproduces the launch-by-launch
// growth of the project-root CLAUDE.md: a stage copy that holds the merged
// root file (as generateLocalCLAUDEMD used to write it) must not add the
// developer's notes to the root again on each server start.
func TestCopyStageCLAUDEMDDoesNotDuplicateNotes(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)

	const stagePath = "1_dev"
	stageDir := filepath.Join(gitContextDir(), stagePath)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}

	notes := "# CLAUDE.md\n\nDeveloper notes.\n"
	root := testMirrorBlock("index") + "\n" + notes
	rootFile := filepath.Join(workDir, "CLAUDE.md")
	if err := os.WriteFile(rootFile, []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "CLAUDE.md"), []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		copyStageCLAUDEMD(gitContextDir(), stagePath)
	}

	data, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if n := strings.Count(got, "Developer notes."); n != 1 {
		t.Errorf("developer notes appear %d times, want 1:\n%s", n, got)
	}
	if got != root {
		t.Errorf("root CLAUDE.md changed:\n%q\nwant\n%q", got, root)
	}
}
