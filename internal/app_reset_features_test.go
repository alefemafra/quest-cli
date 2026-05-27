package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResetFeatures_SkipsBlockedEffectivelyDone(t *testing.T) {
	missionDir := t.TempDir()
	manifest := FeaturesManifest{
		Project: "p",
		Owner:   "o",
		Features: []Feature{
			{
				ID:         "F01",
				Status:     "blocked",
				Resolution: ResolutionResolvedViaFix,
			},
			{
				ID:         "F02",
				Status:     "blocked",
				Resolution: ResolutionUnresolved,
			},
			{
				ID:     "F03",
				Status: "in_progress",
			},
		},
		FixFeatures: []Feature{
			{
				ID:     "F01-fix-01",
				Status: "done",
				Fixes:  "F01",
			},
		},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missionDir, "features.json"), raw, 0o644); err != nil {
		t.Fatalf("write features.json: %v", err)
	}

	model := Model{missionDir: missionDir}
	resetCount := model.resetFeatures(false)
	if resetCount != 2 {
		t.Fatalf("expected 2 features reset, got %d", resetCount)
	}

	updatedRaw, err := os.ReadFile(filepath.Join(missionDir, "features.json"))
	if err != nil {
		t.Fatalf("read updated features.json: %v", err)
	}
	var updated FeaturesManifest
	if err := json.Unmarshal(updatedRaw, &updated); err != nil {
		t.Fatalf("unmarshal updated manifest: %v", err)
	}

	byID := make(map[string]Feature)
	for _, f := range updated.Features {
		byID[f.ID] = f
	}

	if got := byID["F01"].Status; got != "blocked" {
		t.Fatalf("expected F01 to remain blocked (effectively done), got %s", got)
	}
	if got := byID["F02"].Status; got != "pending" {
		t.Fatalf("expected F02 reset to pending, got %s", got)
	}
	if got := byID["F03"].Status; got != "pending" {
		t.Fatalf("expected F03 reset to pending, got %s", got)
	}
}

