package internal

import "testing"

func TestMergeFixFeatureEntries_MergesEnrichedMetadata(t *testing.T) {
	base := Feature{
		ID:               "F01-fix-01",
		Status:           "in_progress",
		Scope:            "old scope",
		Description:      "old description",
		ValidationRefs:   []string{"ui.1"},
		RegressionGuards: []string{"old guard"},
	}
	incoming := Feature{
		ID:                  "F01-fix-01",
		Status:              "blocked",
		Scope:               "new scope",
		Description:         "new description with richer context",
		ValidationRefs:      []string{"ui.2"},
		RootCauseHypothesis: "state hydration race",
		Evidence:            []string{"validator output"},
		DoneWhen:            []string{"refresh keeps session"},
		NonGoals:            []string{"no auth refactor"},
		RegressionGuards:    []string{"add refresh test"},
	}

	merged := mergeFixFeatureEntries(base, incoming)
	if merged.Scope != "new scope" || merged.Description == "" {
		t.Fatalf("expected merged scope/description, got %#v", merged)
	}
	if merged.RootCauseHypothesis != "state hydration race" {
		t.Fatalf("expected root cause merged, got %q", merged.RootCauseHypothesis)
	}
	if len(merged.RegressionGuards) != 1 || merged.RegressionGuards[0] != "add refresh test" {
		t.Fatalf("expected regression guards merged, got %#v", merged.RegressionGuards)
	}
	if merged.Status != "blocked" {
		t.Fatalf("expected higher-priority status selected, got %q", merged.Status)
	}
}

func TestMergeFixFeatureEntries_PreservesExistingEnrichedMetadata(t *testing.T) {
	base := Feature{
		ID:                  "F01-fix-01",
		Description:         "existing description",
		RootCauseHypothesis: "existing root cause",
		Evidence:            []string{"existing evidence"},
	}
	incoming := Feature{ID: "F01-fix-01"}

	merged := mergeFixFeatureEntries(base, incoming)
	if merged.Description != "existing description" {
		t.Fatalf("expected description preserved, got %q", merged.Description)
	}
	if merged.RootCauseHypothesis != "existing root cause" {
		t.Fatalf("expected root cause preserved")
	}
	if len(merged.Evidence) != 1 || merged.Evidence[0] != "existing evidence" {
		t.Fatalf("expected evidence preserved, got %#v", merged.Evidence)
	}
}
