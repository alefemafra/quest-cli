package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func BuildRefinementPrompt(feature Feature, report ValidatorReport, missionDir, specDir string) string {
	refinementSkill := ReadSkill("mission-refinement")

	contract := readFileContent(filepath.Join(missionDir, "validation-contract.md"))
	knowledge := readFileContent(filepath.Join(missionDir, "knowledge-base.md"))
	projectContext := readFileContent(filepath.Join(missionDir, "project-context.md"))

	reportJSON, _ := json.MarshalIndent(report, "", "  ")

	var parts []string
	parts = append(parts,
		"You are running the mission-refinement skill. Follow it precisely.",
		"",
		"## Skill Reference",
		"",
		refinementSkill,
		"",
		"---",
		"",
	)

	if projectContext != "" {
		parts = append(parts, "## Project Context", "", projectContext, "")
	}

	parts = append(parts,
		fmt.Sprintf("## Failed Feature: %s — %s", feature.ID, feature.Title),
		fmt.Sprintf("Spec folder: %s", specDir),
		fmt.Sprintf("Scope: %s", feature.Scope),
		"",
		"## Validator Report (FAILs to address)",
		"",
		string(reportJSON),
		"",
		"## Validation Contract",
		"",
		contract,
		"",
	)

	if knowledge != "" {
		parts = append(parts, "## Knowledge Base", "", knowledge, "")
	}

	parts = append(parts,
		"## Instructions",
		"",
		"1. Analyze each FAIL assertion — find the root cause, not surface cause",
		"2. Generate minimum-scope fix features",
		"3. Each fix feature must have: id, title, status (pending), depends_on, scope, validation_refs, fixes, addresses",
		"",
		"Output ONLY a valid JSON array of fix features:",
		fmt.Sprintf(`[{"id":"%s-fix-1","title":"...","status":"pending","phase":%d,"depends_on":["%s"],"scope":"...","validation_refs":["..."],"fixes":"%s","addresses":["..."]}]`, feature.ID, feature.Phase, feature.ID, feature.ID),
		"",
		"Output ONLY the JSON array, nothing else.",
	)

	return strings.Join(parts, "\n")
}

func ParseFixFeatures(text string) []Feature {
	text = strings.TrimSpace(text)

	var features []Feature
	if err := json.Unmarshal([]byte(text), &features); err == nil && len(features) > 0 {
		return features
	}

	// Try code fence extraction
	re := strings.Index(text, "```")
	if re >= 0 {
		end := strings.Index(text[re+3:], "```")
		if end >= 0 {
			block := text[re+3 : re+3+end]
			if nl := strings.Index(block, "\n"); nl >= 0 {
				block = block[nl+1:]
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(block)), &features); err == nil && len(features) > 0 {
				return features
			}
		}
	}

	// Try finding array in text
	start := strings.Index(text, "[")
	if start >= 0 {
		depth := 0
		for i := start; i < len(text); i++ {
			if text[i] == '[' {
				depth++
			} else if text[i] == ']' {
				depth--
				if depth == 0 {
					if err := json.Unmarshal([]byte(text[start:i+1]), &features); err == nil && len(features) > 0 {
						return features
					}
					break
				}
			}
		}
	}

	return nil
}

func AddFixFeatures(missionDir string, fixes []Feature, originalID string, fileMu *sync.Mutex) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	path := filepath.Join(missionDir, "features.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var manifest FeaturesManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}

	for i := range manifest.Features {
		if manifest.Features[i].ID == originalID {
			manifest.Features[i].Status = "blocked"
		}
	}

	for i := range fixes {
		if fixes[i].Status == "" {
			fixes[i].Status = "pending"
		}
	}
	manifest.FixFeatures = append(manifest.FixFeatures, fixes...)

	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
