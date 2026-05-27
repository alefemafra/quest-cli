package internal

import "testing"

func TestParseAssertionDeltaJSON_ParsesWrappedSchema(t *testing.T) {
	input := `{"assertion_delta":{"upsert":[{"id":"ui.2","category":"ui","assertion":"ui.2: New assertion"}],"remove":["ui.1"]}}`
	delta, ok := ParseAssertionDeltaJSON(input)
	if !ok {
		t.Fatalf("expected assertion delta to parse")
	}
	if len(delta.Upsert) != 1 || delta.Upsert[0].ID != "ui.2" {
		t.Fatalf("unexpected upsert payload: %#v", delta.Upsert)
	}
	if len(delta.Remove) != 1 || delta.Remove[0] != "ui.1" {
		t.Fatalf("unexpected remove payload: %#v", delta.Remove)
	}
}

func TestApplyAssertionDelta_MergesDeterministically(t *testing.T) {
	existing := []Assertion{
		{Category: "ui", Items: []string{"ui.1: old one", "ui.3: old three"}},
		{Category: "api", Items: []string{"api.1: old api"}},
	}
	delta := AssertionDelta{
		Upsert: []AssertionDeltaItem{
			{ID: "ui.2", Category: "ui", Assertion: "ui.2: inserted"},
			{ID: "api.1", Category: "api", Assertion: "api.1: updated api"},
		},
		Remove: []string{"ui.1"},
	}

	merged := ApplyAssertionDelta(existing, delta)
	if len(merged) != 2 {
		t.Fatalf("expected 2 assertion categories, got %d", len(merged))
	}

	ui := merged[1]
	if ui.Category != "ui" {
		ui = merged[0]
	}
	if len(ui.Items) != 2 {
		t.Fatalf("expected 2 ui assertions after merge, got %#v", ui.Items)
	}
	if ui.Items[0] != "ui.2: inserted" || ui.Items[1] != "ui.3: old three" {
		t.Fatalf("unexpected ui ordering/items: %#v", ui.Items)
	}
}

func TestParseFeatureDeltaJSON_AcceptsEmptyDelta(t *testing.T) {
	input := `{"feature_delta":{"upsert":[],"remove":[]}}`
	delta, ok := ParseFeatureDeltaJSON(input)
	if !ok {
		t.Fatalf("expected empty delta schema to parse")
	}
	if len(delta.Upsert) != 0 || len(delta.Remove) != 0 {
		t.Fatalf("expected empty delta, got %#v", delta)
	}
}

func TestApplyFeatureDelta_PreservesDoneFeatures(t *testing.T) {
	existing := []Feature{
		{ID: "F01", Title: "Done feature", Phase: 0, Status: "done"},
		{ID: "F02", Title: "Pending feature", Phase: 1, Status: "pending"},
	}
	delta := FeatureDelta{
		Upsert: []Feature{
			{ID: "F03", Title: "New feature", Phase: 1, Status: "pending", Scope: "Implement new scope detail covering behavior.", ValidationRefs: []string{"ui.1"}},
		},
		Remove: []string{"F01", "F02"},
	}

	merged := ApplyFeatureDelta(existing, delta, true)
	if len(merged) != 2 {
		t.Fatalf("expected done F01 + new F03, got %#v", merged)
	}

	hasF01 := false
	hasF03 := false
	for _, f := range merged {
		if f.ID == "F01" && f.Status == "done" {
			hasF01 = true
		}
		if f.ID == "F03" {
			hasF03 = true
		}
		if f.ID == "F02" {
			t.Fatalf("expected F02 removed, got %#v", merged)
		}
	}
	if !hasF01 || !hasF03 {
		t.Fatalf("missing expected merged features: %#v", merged)
	}
}

func TestMergeFeatureExecutionMetadata_PreservesEnrichedFieldsWhenPlannerOmits(t *testing.T) {
	existing := Feature{
		ID:                  "F01",
		Title:               "Old",
		Status:              "in_progress",
		Scope:               "Existing scope",
		Description:         "Existing description with context and boundaries.",
		RootCauseHypothesis: "Existing root cause",
		Evidence:            []string{"validator says X"},
		DoneWhen:            []string{"condition met"},
		NonGoals:            []string{"no refactor"},
		RegressionGuards:    []string{"test A"},
	}
	planned := Feature{
		ID:     "F01",
		Title:  "New title",
		Status: "pending",
		Scope:  "New scope",
	}

	merged := mergeFeatureExecutionMetadata(existing, planned)
	if merged.Description != existing.Description {
		t.Fatalf("expected description preserved, got %q", merged.Description)
	}
	if merged.RootCauseHypothesis != existing.RootCauseHypothesis {
		t.Fatalf("expected root cause preserved")
	}
	if len(merged.Evidence) != 1 || merged.Evidence[0] != "validator says X" {
		t.Fatalf("expected evidence preserved, got %#v", merged.Evidence)
	}
	if merged.Status != "in_progress" {
		t.Fatalf("expected runtime status preserved, got %q", merged.Status)
	}
}

func TestMergeFeatureExecutionMetadata_UsesPlannerEnrichedFieldsWhenProvided(t *testing.T) {
	existing := Feature{
		ID:                  "F01",
		Description:         "Old description",
		RootCauseHypothesis: "Old cause",
	}
	planned := Feature{
		ID:                  "F01",
		Description:         "New description for worker",
		RootCauseHypothesis: "New root cause",
	}
	merged := mergeFeatureExecutionMetadata(existing, planned)
	if merged.Description != "New description for worker" {
		t.Fatalf("expected planner description, got %q", merged.Description)
	}
	if merged.RootCauseHypothesis != "New root cause" {
		t.Fatalf("expected planner root cause, got %q", merged.RootCauseHypothesis)
	}
}
