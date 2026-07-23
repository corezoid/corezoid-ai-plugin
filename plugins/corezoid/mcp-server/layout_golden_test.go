package main

// Golden coordinate files freeze the engine's output per fixture: invariants
// catch correctness, goldens catch UNINTENDED churn — the failure mode of an
// accidental map iteration. Regenerate after an intentional algorithm change:
//
//	go test -run TestLayoutGolden -update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLayoutGoldenCoordinates(t *testing.T) {
	for _, fx := range allFixtures() {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			nodes := deepCopyNodes(t, fx.nodes)
			coords, rep := (&layoutEngine{density: "medium"}).computeLayout(nodes)

			got := map[string]interface{}{"strategy": rep.Strategy}
			placed := map[string][2]int{}
			for id, p := range coords {
				placed[id] = [2]int{p.X, p.Y}
			}
			got["coords"] = placed

			path := filepath.Join("testdata", "golden", "layout_"+fx.name+".json")
			if *updateGolden {
				b, err := json.MarshalIndent(got, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(b, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("golden file missing (run with -update): %v", err)
			}
			var want struct {
				Strategy string            `json:"strategy"`
				Coords   map[string][2]int `json:"coords"`
			}
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if want.Strategy != rep.Strategy {
				t.Errorf("strategy drifted: golden %q, got %q", want.Strategy, rep.Strategy)
			}
			if !reflect.DeepEqual(want.Coords, placed) {
				diff := 0
				for id, w := range want.Coords {
					if g, ok := placed[id]; !ok || g != w {
						diff++
					}
				}
				t.Errorf("coordinates drifted from golden (%d of %d nodes differ) — if intentional, regenerate with -update", diff, len(want.Coords))
			}
		})
	}
}
