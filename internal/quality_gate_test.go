package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectQualityCommands_NodeScripts(t *testing.T) {
	projectDir := t.TempDir()
	packageJSON := `{
  "name": "quality-sample",
  "packageManager": "pnpm@9.0.0",
  "scripts": {
    "lint": "eslint .",
    "test": "vitest run",
    "test:unit": "vitest run --dir test/unit"
  }
}`
	if err := os.WriteFile(filepath.Join(projectDir, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	plan := DetectQualityCommands(projectDir)
	if len(plan.LintCommands) == 0 {
		t.Fatalf("expected at least one lint command")
	}
	if len(plan.TestCommands) == 0 {
		t.Fatalf("expected at least one test command")
	}

	if got := plan.LintCommands[0].Command; got != "pnpm run lint" {
		t.Fatalf("expected pnpm lint command, got %q", got)
	}
	if got := plan.TestCommands[0].Command; got != "pnpm run test:unit" {
		t.Fatalf("expected pnpm unit test command first, got %q", got)
	}
}

func TestRunQualityGate_TargetedThenRootFallback(t *testing.T) {
	projectDir := t.TempDir()
	plan := QualityCommandPlan{
		LintCommands: []QualityCommandCandidate{
			{Command: "false", Scope: "targeted", Source: "test"},
			{Command: "true", Scope: "root", Source: "fallback"},
		},
		TestCommands: []QualityCommandCandidate{
			{Command: "true", Scope: "targeted", Source: "test"},
		},
	}

	result := RunQualityGate(projectDir, plan)
	if !result.Passed {
		t.Fatalf("expected quality gate to pass with root fallback")
	}
	if !result.LintPassed {
		t.Fatalf("expected lint stage to pass via fallback")
	}
	if !result.TestPassed {
		t.Fatalf("expected test stage to pass")
	}
	if len(result.LintRuns) != 2 {
		t.Fatalf("expected 2 lint runs, got %d", len(result.LintRuns))
	}
	if result.LintRuns[0].Passed {
		t.Fatalf("expected first lint run to fail")
	}
	if !result.LintRuns[1].Passed {
		t.Fatalf("expected second lint run to pass")
	}
}

func TestNormalizeValidatorVerdict_DowngradesInconsistentPass(t *testing.T) {
	report := &ValidatorReport{
		Verdict: "PASS",
		Assertions: []ValidatorAssertion{
			{ID: "data.1", Result: "PASS"},
			{ID: "data.2", Result: "FAIL"},
		},
	}

	normalizeValidatorVerdict(report)
	if report.Verdict != "FAIL" {
		t.Fatalf("expected verdict to downgrade to FAIL, got %q", report.Verdict)
	}
	joinedNotes := strings.Join(report.Notes, "\n")
	if !strings.Contains(joinedNotes, "VALIDATOR_VERDICT_OVERRIDDEN") {
		t.Fatalf("expected override note, got %q", joinedNotes)
	}
}
