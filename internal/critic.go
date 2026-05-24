package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func ExtractMechanicalScript() (string, func(), error) {
	data, err := skillsFS.ReadFile("skills/checks/run-mechanical.mjs")
	if err != nil {
		return "", nil, fmt.Errorf("embedded script not found: %w", err)
	}

	f, err := os.CreateTemp("", "run-mechanical-*.mjs")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	f.Close()

	cleanup := func() { os.Remove(f.Name()) }
	return f.Name(), cleanup, nil
}

type MechanicalResult struct {
	Passed  int
	Failed  int
	Details []MechanicalCheck
	RawOut  string
}

type MechanicalCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func RunMechanicalChecks(specDir, projectDir string) (*MechanicalResult, error) {
	scriptPath, cleanup, err := ExtractMechanicalScript()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := []string{scriptPath, "--project", specDir, "--format", "json"}
	if projectDir != "" {
		args = append(args, "--root", projectDir)
	}
	cmd := exec.Command("node", args...)
	out, runErr := cmd.CombinedOutput()

	result := &MechanicalResult{RawOut: string(out)}

	var wrapped struct {
		Results []MechanicalCheck `json:"results"`
	}
	if err := json.Unmarshal(out, &wrapped); err == nil && len(wrapped.Results) > 0 {
		for _, c := range wrapped.Results {
			if c.Status == "pass" {
				result.Passed++
			} else if strings.HasPrefix(c.ID, "M-A") {
				result.Passed++
				c.Message = "(advisory) " + c.Message
			} else {
				result.Failed++
			}
		}
		result.Details = wrapped.Results
		return result, nil
	}

	// Fallback: try flat array
	var checks []MechanicalCheck
	if err := json.Unmarshal(out, &checks); err == nil {
		for _, c := range checks {
			if c.Status == "pass" {
				result.Passed++
			} else {
				result.Failed++
			}
		}
		result.Details = checks
		return result, nil
	}

	if runErr != nil {
		result.Failed = 1
		return result, nil
	}
	result.Passed = 1
	return result, nil
}

func RunCriticGate(projectDir, missionDir string, verbose *bool, eventCh chan WorkerEvent) {
	specDir := filepath.Dir(missionDir)

	eventCh <- WorkerEvent{Role: "critic", Line: "▶ Running mechanical checks..."}

	mech, err := RunMechanicalChecks(specDir, projectDir)
	if err != nil {
		// node not available or script error — skip mechanical, proceed to judgment
		eventCh <- WorkerEvent{Role: "critic", Line: fmt.Sprintf("⚠ Mechanical checks skipped: %s", err)}
	} else if mech.Failed > 0 {
		var lines []string
		for _, d := range mech.Details {
			if d.Status != "pass" {
				lines = append(lines, fmt.Sprintf("  ✕ [%s] %s", d.ID, d.Message))
			}
		}
		eventCh <- WorkerEvent{
			Role: "critic",
			Line: fmt.Sprintf("✕ Mechanical checks: %d passed, %d failed\n%s", mech.Passed, mech.Failed, strings.Join(lines, "\n")),
		}
		eventCh <- WorkerEvent{Role: "critic", Done: true, Verdict: "FAIL"}
		return
	} else {
		eventCh <- WorkerEvent{Role: "critic", Line: fmt.Sprintf("✓ Mechanical checks: %d passed", mech.Passed)}
	}

	eventCh <- WorkerEvent{Role: "critic", Line: "▶ Running judgment checks (Claude)..."}

	prompt := BuildCriticPrompt(specDir)
	claudeCh := make(chan ClaudeStreamMsg, 64)
	cmd := StartClaude(prompt, projectDir, verbose, claudeCh, "--max-turns", "3")
	_ = cmd

	var resultText string
	var lastSessionID string
	for attempt := 0; ; attempt++ {
		gotResult := false
		for msg := range claudeCh {
			if msg.Line != "" {
				eventCh <- WorkerEvent{Role: "critic", Line: msg.Line}
			}
			if msg.Done {
				if msg.SessionID != "" {
					lastSessionID = msg.SessionID
				}
				if msg.Err != nil {
					if isTransientError(msg.Err) && attempt < maxTransientRetries {
						backoff := time.Duration(attempt+1) * 5 * time.Second
						label := ""
						if lastSessionID != "" {
							label = " (resuming session)"
						}
						eventCh <- WorkerEvent{Role: "critic", Line: fmt.Sprintf("⚠ Critic transient error, retrying (%d/%d)%s in %s...", attempt+1, maxTransientRetries, label, backoff)}
						time.Sleep(backoff)
						claudeCh = make(chan ClaudeStreamMsg, 64)
						if lastSessionID != "" {
							cmd = StartClaude(
								"An API error interrupted your evaluation. Continue from where you left off. Output ONLY the JSON result.",
								projectDir, verbose, claudeCh,
								"--resume", lastSessionID,
																"--max-turns", "2",
							)
						} else {
							cmd = StartClaude(prompt, projectDir, verbose, claudeCh, "--max-turns", "3")
						}
						_ = cmd
						break
					}
					eventCh <- WorkerEvent{Role: "critic", Line: fmt.Sprintf("✕ Critic error: %s", msg.Err)}
					eventCh <- WorkerEvent{Role: "critic", Done: true, Verdict: "FAIL"}
					return
				}
				resultText = msg.Result
				gotResult = true
			}
		}
		if gotResult {
			break
		}
	}

	report := ParseCriticReport(resultText)
	if report != nil {
		persistCriticReport(missionDir, report)
	}
	if report != nil && report.Overall == "needs-work" {
		var findings []string
		for _, f := range report.Findings {
			if f.Status == "needs-work" {
				findings = append(findings, fmt.Sprintf("  ✕ [%s] %s → %s", f.Criterion, f.Target, f.Suggestion))
			}
		}
		eventCh <- WorkerEvent{
			Role: "critic",
			Line: fmt.Sprintf("✕ Judgment: needs-work — %d blocking findings\n%s", len(report.BlockingFindings), strings.Join(findings, "\n")),
		}
		eventCh <- WorkerEvent{Role: "critic", Done: true, Verdict: "FAIL"}
		return
	}

	eventCh <- WorkerEvent{Role: "critic", Line: "✓ Critic gate passed"}
	eventCh <- WorkerEvent{Role: "critic", Done: true, Verdict: "PASS"}
}

func BuildCriticPrompt(specDir string) string {
	criticSkill := ReadSkill("mission-critic")
	missionDir := filepath.Join(specDir, "mission")
	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(specDir)))

	contract := readFileContent(filepath.Join(missionDir, "validation-contract.md"))
	features := readFileContent(filepath.Join(missionDir, "features.json"))
	projectContext := readFileContent(filepath.Join(missionDir, "project-context.md"))
	claudeMd := readFileContent(filepath.Join(projectRoot, "CLAUDE.md"))
	criteriaContent := readCriteriaMd()
	localCriteria := readFileContent(filepath.Join(missionDir, "critique-criteria.local.md"))
	priorReports := readPriorCriticReports(missionDir)
	packageJSON := readFileContent(filepath.Join(projectRoot, "package.json"))

	var parts []string
	parts = append(parts,
		"You are running the mission-critic skill. Follow it precisely.",
		"",
		"## Skill Reference",
		"",
		criticSkill,
		"",
		"## Criteria (CRITERIA.md)",
		"",
		criteriaContent,
		"",
	)

	if localCriteria != "" {
		parts = append(parts, "## Local Criteria Overrides (critique-criteria.local.md)", "", localCriteria, "")
	}

	if priorReports != "" {
		parts = append(parts, "## Prior Critic Reports", "", priorReports, "")
	}

	if projectContext != "" {
		parts = append(parts, "## Project Context", "", projectContext, "")
	}

	if packageJSON != "" {
		parts = append(parts, "## package.json", "", packageJSON, "")
	}

	parts = append(parts,
		"## Spec folder: "+specDir,
		"",
		"## Validation Contract",
		"",
		contract,
		"",
		"## Features (features.json)",
		"",
		features,
		"",
	)

	if claudeMd != "" {
		parts = append(parts,
			"## CLAUDE.md (Architecture)",
			"",
			claudeMd,
			"",
		)
	}

	parts = append(parts,
		"## Instructions",
		"",
		"CRITICAL: ALL artifacts you need are PROVIDED ABOVE in this prompt:",
		"CRITERIA.md, validation-contract.md, features.json, CLAUDE.md, project-context, package.json, prior reports, and local overrides.",
		"Do NOT use Read, Glob, Grep, Bash, WebFetch, WebSearch, or any tools. You already have EVERYTHING.",
		"Start evaluating IMMEDIATELY. Output ONLY the JSON result.",
		"",
		"Mechanical checks ([M-*] criteria) have ALREADY been run and passed by the orchestrator.",
		"Do NOT re-run run-mechanical.mjs or evaluate any [M-*] criteria yourself.",
		"",
		"Evaluate ONLY the judgment criteria [J-*] across all three phases (A, B, C).",
		"For each judgment criterion, emit pass or needs-work with concrete suggestions.",
		"",
		"Output ONLY a valid JSON object matching this schema:",
		`{"phase":"all","artifact":"<path>","started_at":"<ISO>","ended_at":"<ISO>","mechanical":{"passed":0,"failed":0},"judgment":[{"criterion":"J-S1","status":"pass","note":"..."},{"criterion":"J-S5","status":"needs-work","target":"...","suggestion":"..."}],"overall":"pass","blocking_findings":[]}`,
		"",
		"If ALL judgment criteria pass, set overall to \"pass\". If ANY is needs-work, set overall to \"needs-work\".",
		"Output ONLY the JSON, nothing else.",
	)

	return strings.Join(parts, "\n")
}

func persistCriticReport(missionDir string, report *CriticReport) {
	runDir := filepath.Join(missionDir, "runs")
	_ = os.MkdirAll(runDir, 0o755)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(runDir, "critic-report.json"), data, 0o644)
}

func RunFixCriticGate(projectDir, missionDir string, fixes []Feature, verbose *bool, eventCh chan WorkerEvent) {
	specDir := filepath.Dir(missionDir)

	eventCh <- WorkerEvent{Role: "critic", Line: fmt.Sprintf("▶ Running critic gate on %d fix features...", len(fixes))}

	prompt := BuildFixCriticPrompt(specDir, fixes)
	claudeCh := make(chan ClaudeStreamMsg, 64)
	cmd := StartClaude(prompt, projectDir, verbose, claudeCh, "--max-turns", "3")
	_ = cmd

	var resultText string
	var lastSessionID string
	for attempt := 0; ; attempt++ {
		gotResult := false
		for msg := range claudeCh {
			if msg.Line != "" {
				eventCh <- WorkerEvent{Role: "critic", Line: msg.Line}
			}
			if msg.Done {
				if msg.SessionID != "" {
					lastSessionID = msg.SessionID
				}
				if msg.Err != nil {
					if isTransientError(msg.Err) && attempt < maxTransientRetries {
						backoff := time.Duration(attempt+1) * 5 * time.Second
						label := ""
						if lastSessionID != "" {
							label = " (resuming)"
						}
						eventCh <- WorkerEvent{Role: "critic", Line: fmt.Sprintf("⚠ Fix critic transient error, retrying (%d/%d)%s...", attempt+1, maxTransientRetries, label)}
						time.Sleep(backoff)
						claudeCh = make(chan ClaudeStreamMsg, 64)
						if lastSessionID != "" {
							cmd = StartClaude(
								"An API error interrupted your evaluation. Continue from where you left off. Output ONLY the JSON result.",
								projectDir, verbose, claudeCh,
								"--resume", lastSessionID,
																"--max-turns", "2",
							)
						} else {
							cmd = StartClaude(prompt, projectDir, verbose, claudeCh, "--max-turns", "3")
						}
						_ = cmd
						break
					}
					eventCh <- WorkerEvent{Role: "critic", Line: fmt.Sprintf("⚠ Fix critic error: %s — proceeding anyway", msg.Err)}
					eventCh <- WorkerEvent{Role: "critic", Done: true, Verdict: "PASS"}
					return
				}
				resultText = msg.Result
				gotResult = true
			}
		}
		if gotResult {
			break
		}
	}

	report := ParseCriticReport(resultText)
	if report != nil {
		persistCriticReport(missionDir, report)
	}

	if report != nil && report.Overall == "needs-work" {
		var findings []string
		for _, f := range report.Findings {
			if f.Status == "needs-work" {
				findings = append(findings, fmt.Sprintf("  ✕ [%s] %s → %s", f.Criterion, f.Target, f.Suggestion))
			}
		}
		eventCh <- WorkerEvent{
			Role: "critic",
			Line: fmt.Sprintf("✕ Fix critic: needs-work\n%s", strings.Join(findings, "\n")),
		}
		eventCh <- WorkerEvent{Role: "critic", Done: true, Verdict: "FAIL"}
		return
	}

	eventCh <- WorkerEvent{Role: "critic", Line: "✓ Fix features passed critic gate"}
	eventCh <- WorkerEvent{Role: "critic", Done: true, Verdict: "PASS"}
}

func BuildFixCriticPrompt(specDir string, fixes []Feature) string {
	criticSkill := ReadSkill("mission-critic")
	missionDir := filepath.Join(specDir, "mission")

	contract := readFileContent(filepath.Join(missionDir, "validation-contract.md"))
	features := readFileContent(filepath.Join(missionDir, "features.json"))

	fixesJSON, _ := json.MarshalIndent(fixes, "", "  ")

	var parts []string
	parts = append(parts,
		"You are running a TARGETED critic check on fix features generated by refinement.",
		"",
		"## Skill Reference (Phase C only)",
		"",
		criticSkill,
		"",
		"## Context",
		"",
		"Spec folder: "+specDir,
		"",
		"## Fix Features to Review",
		"",
		string(fixesJSON),
		"",
		"## Full Features Manifest (for context)",
		"",
		features,
		"",
		"## Validation Contract",
		"",
		contract,
		"",
		"## Instructions",
		"",
		"CRITICAL: ALL artifacts are PROVIDED ABOVE. Do NOT use Read, Glob, Grep, Bash, or any file-reading tools.",
		"Start evaluating IMMEDIATELY.",
		"",
		"Run ONLY Phase C (features.json decomposition) on the fix features above.",
		"Check:",
		"- Each fix feature has a clear, testable scope",
		"- validation_refs reference real assertions",
		"- depends_on references are valid",
		"- No circular dependencies",
		"- Scope is minimum (fix, not refactor)",
		"",
		"Output ONLY a valid JSON object:",
		`{"phase":"C","artifact":"fix-features","started_at":"<ISO>","ended_at":"<ISO>","mechanical":{"passed":0,"failed":0},"judgment":[{"criterion":"J-C1","status":"pass|needs-work","note":"..."}],"overall":"pass|needs-work","blocking_findings":[]}`,
		"",
		"Output ONLY the JSON, nothing else.",
	)

	return strings.Join(parts, "\n")
}

func readCriteriaMd() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return readFileContent(filepath.Join(home, ".claude", "skills", "mission-critic", "CRITERIA.md"))
}

func readPriorCriticReports(missionDir string) string {
	pattern := filepath.Join(missionDir, "runs", "critic-*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	var parts []string
	for _, m := range matches {
		content := readFileContent(m)
		if content != "" {
			parts = append(parts, fmt.Sprintf("### %s\n\n%s", filepath.Base(m), content))
		}
	}
	return strings.Join(parts, "\n\n")
}

func ParseCriticReport(text string) *CriticReport {
	text = strings.TrimSpace(text)

	var report CriticReport
	if err := json.Unmarshal([]byte(text), &report); err == nil && report.Overall != "" {
		return &report
	}

	// Try extracting from code fences
	re := strings.Index(text, "```")
	if re >= 0 {
		end := strings.Index(text[re+3:], "```")
		if end >= 0 {
			block := text[re+3 : re+3+end]
			if nl := strings.Index(block, "\n"); nl >= 0 {
				block = block[nl+1:]
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(block)), &report); err == nil && report.Overall != "" {
				return &report
			}
		}
	}

	return nil
}
