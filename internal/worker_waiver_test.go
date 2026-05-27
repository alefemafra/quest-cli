package internal

import "testing"

func TestShouldWaiveContractOnNoFixes_ContractConflictSignal(t *testing.T) {
	report := ValidatorReport{
		Verdict: "BLOCKED",
		Assertions: []ValidatorAssertion{
			{
				ID:       "telemetry.13",
				Result:   "BLOCKED",
				Evidence: "Clicking bulk action button is impossible because the spec marks the control as disabled by design.",
			},
		},
		Notes: []string{
			"Contract mismatch: requirement conflicts with current spec behavior.",
		},
	}

	if !shouldWaiveContractOnNoFixes(report, "") {
		t.Fatalf("expected contract-conflict blocked report to be waived")
	}
}

func TestShouldWaiveContractOnNoFixes_DoesNotWaiveGenericBlocked(t *testing.T) {
	report := ValidatorReport{
		Verdict: "BLOCKED",
		Assertions: []ValidatorAssertion{
			{
				ID:       "api.4",
				Result:   "BLOCKED",
				Evidence: "Request timed out while waiting for server response.",
			},
		},
		Notes: []string{
			"Temporary timeout while running validator.",
		},
	}

	if shouldWaiveContractOnNoFixes(report, "") {
		t.Fatalf("expected generic blocked report to stay unresolved")
	}
}

func TestShouldWaiveContractOnNoFixes_DoesNotWaiveWhenFailPresent(t *testing.T) {
	report := ValidatorReport{
		Verdict: "BLOCKED",
		Assertions: []ValidatorAssertion{
			{ID: "ui.1", Result: "FAIL", Evidence: "Expected element missing"},
			{ID: "ui.2", Result: "BLOCKED", Evidence: "Could not continue"},
		},
		Notes: []string{
			"Spec and contract disagree in one branch.",
		},
	}

	if shouldWaiveContractOnNoFixes(report, "") {
		t.Fatalf("expected FAIL+BLOCKED report not to be auto-waived")
	}
}

func TestShouldWaiveContractOnNoFixes_AcceptsTopLevelFailWhenAssertionsOnlyBlocked(t *testing.T) {
	report := ValidatorReport{
		Verdict: "FAIL",
		Assertions: []ValidatorAssertion{
			{
				ID:       "telemetry.13",
				Result:   "BLOCKED",
				Evidence: "Contract mismatch by design: disabled stubs cannot emit click behavior under current spec.",
			},
		},
		Notes: []string{
			"Spec/contract contradiction detected for current feature scope.",
		},
	}

	if !shouldWaiveContractOnNoFixes(report, "") {
		t.Fatalf("expected top-level FAIL with blocked-only assertions to be waived")
	}
}

