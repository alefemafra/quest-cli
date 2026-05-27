package internal

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const qualityGateCommandTimeout = 10 * time.Minute
const qualityGateMaxOutputChars = 3000

func RunQualityGate(projectDir string, plan QualityCommandPlan) QualityGateResult {
	if len(plan.LintCommands) == 0 && len(plan.TestCommands) == 0 {
		plan = DetectQualityCommands(projectDir)
	}

	startedAt := time.Now().UTC()
	result := QualityGateResult{
		StartedAt: startedAt.Format(time.RFC3339),
		Passed:    false,
	}

	result.LintPassed, result.LintRuns = runQualityStage(projectDir, "lint", plan.LintCommands)
	result.TestPassed, result.TestRuns = runQualityStage(projectDir, "test", plan.TestCommands)
	result.Passed = result.LintPassed && result.TestPassed

	if len(plan.LintCommands) == 0 {
		result.Notes = append(result.Notes, "No lint command detected for this project.")
	}
	if len(plan.TestCommands) == 0 {
		result.Notes = append(result.Notes, "No unit-test command detected for this project.")
	}

	result.EndedAt = time.Now().UTC().Format(time.RFC3339)
	return result
}

func runQualityStage(projectDir, kind string, commands []QualityCommandCandidate) (bool, []QualityCommandRun) {
	if len(commands) == 0 {
		return false, nil
	}

	targeted, root := splitQualityCommands(commands)
	var runs []QualityCommandRun

	if ok, stageRuns := runUntilFirstPass(projectDir, kind, targeted); ok {
		runs = append(runs, stageRuns...)
		return true, runs
	} else {
		runs = append(runs, stageRuns...)
	}

	if len(root) == 0 {
		return false, runs
	}

	ok, stageRuns := runUntilFirstPass(projectDir, kind, root)
	runs = append(runs, stageRuns...)
	return ok, runs
}

func splitQualityCommands(commands []QualityCommandCandidate) ([]QualityCommandCandidate, []QualityCommandCandidate) {
	targeted := make([]QualityCommandCandidate, 0, len(commands))
	root := make([]QualityCommandCandidate, 0, len(commands))
	for _, cmd := range commands {
		scope := strings.ToLower(strings.TrimSpace(cmd.Scope))
		if scope == "root" {
			root = append(root, cmd)
			continue
		}
		targeted = append(targeted, cmd)
	}
	return targeted, root
}

func runUntilFirstPass(projectDir, kind string, commands []QualityCommandCandidate) (bool, []QualityCommandRun) {
	var runs []QualityCommandRun
	for _, candidate := range commands {
		run := executeQualityCommand(projectDir, kind, candidate)
		runs = append(runs, run)
		if run.Passed {
			return true, runs
		}
	}
	return false, runs
}

func executeQualityCommand(projectDir, kind string, candidate QualityCommandCandidate) QualityCommandRun {
	start := time.Now()
	run := QualityCommandRun{
		Command: candidate.Command,
		Scope:   candidate.Scope,
		Source:  candidate.Source,
		Kind:    kind,
		Passed:  false,
	}

	ctx, cancel := context.WithTimeout(context.Background(), qualityGateCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-lc", candidate.Command)
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	run.DurationMs = time.Since(start).Milliseconds()

	output := strings.TrimSpace(string(out))
	if output == "" && err != nil {
		output = err.Error()
	}
	run.Output = truncateQualityOutput(output)
	run.Passed = err == nil
	return run
}

func truncateQualityOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) <= qualityGateMaxOutputChars {
		return output
	}
	return output[:qualityGateMaxOutputChars] + "..."
}

func BuildQualityGateFailureContext(result QualityGateResult) string {
	var sb strings.Builder
	sb.WriteString("PRE-VALIDATION QUALITY GATE FAILED.\n")
	sb.WriteString("Both lint and unit tests must pass before validation.\n")

	if !result.LintPassed {
		sb.WriteString("\nLint stage failed.\n")
		for _, run := range result.LintRuns {
			sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", verdictMark(run.Passed), run.Command, run.Source))
			if !run.Passed && run.Output != "" {
				sb.WriteString(indentLines(run.Output, "  "))
				sb.WriteString("\n")
			}
		}
	}

	if !result.TestPassed {
		sb.WriteString("\nUnit test stage failed.\n")
		for _, run := range result.TestRuns {
			sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", verdictMark(run.Passed), run.Command, run.Source))
			if !run.Passed && run.Output != "" {
				sb.WriteString(indentLines(run.Output, "  "))
				sb.WriteString("\n")
			}
		}
	}

	for _, note := range result.Notes {
		sb.WriteString(fmt.Sprintf("\nNote: %s\n", note))
	}

	sb.WriteString("\nFix the failing commands before ending the worker session.")
	return sb.String()
}

func verdictMark(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}

func indentLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
