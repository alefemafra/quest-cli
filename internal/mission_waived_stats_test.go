package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMissionState_CountsWaivedContractAsDoneException(t *testing.T) {
	missionDir := t.TempDir()
	manifest := FeaturesManifest{
		Project: "p",
		Owner:   "o",
		Features: []Feature{
			{
				ID:         "F11",
				Status:     "blocked",
				Resolution: ResolutionWaivedContract,
			},
		},
		FixFeatures: []Feature{},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missionDir, "features.json"), raw, 0o644); err != nil {
		t.Fatalf("write features.json: %v", err)
	}

	state := ReadMissionState(missionDir)
	if !state.Exists {
		t.Fatalf("expected mission state to exist")
	}
	if state.Stats.DoneWaived != 1 {
		t.Fatalf("expected DoneWaived=1, got %d", state.Stats.DoneWaived)
	}
	if state.Stats.Done != 1 {
		t.Fatalf("expected Done=1, got %d", state.Stats.Done)
	}
	if state.Stats.BlockedWaived != 1 {
		t.Fatalf("expected BlockedWaived=1, got %d", state.Stats.BlockedWaived)
	}
	if state.Stats.BlockedUnresolved != 0 {
		t.Fatalf("expected BlockedUnresolved=0, got %d", state.Stats.BlockedUnresolved)
	}
}

