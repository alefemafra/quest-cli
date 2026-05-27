package internal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func ScanSpecs(projectDir string) []SpecEntry {
	specsRoot := filepath.Join(projectDir, "docs", "specs")
	seen := make(map[string]bool)
	var specs []SpecEntry

	filepath.WalkDir(specsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), "_") {
			return filepath.SkipDir
		}

		hasSpec := fileExists(filepath.Join(path, "spec.md"))
		hasQuest := fileExists(filepath.Join(path, "quest", "features.json"))
		hasMission := fileExists(filepath.Join(path, "mission", "features.json"))
		if !hasSpec && !hasQuest && !hasMission {
			return nil
		}

		rel, _ := filepath.Rel(specsRoot, path)
		if seen[rel] {
			return nil
		}
		seen[rel] = true

		missionDir := ResolveArtifactDir(path)
		state := ReadMissionState(missionDir)
		title := extractSpecTitle(path)
		if title == "" {
			title = state.Project
		}
		if title == "" {
			title = rel
		}

		specs = append(specs, SpecEntry{
			Slug:     rel,
			SpecPath: path,
			Title:    title,
			Mission:  state,
		})
		return filepath.SkipDir
	})

	sort.Slice(specs, func(i, j int) bool {
		iActive := specs[i].Mission.Stats.InProgress > 0
		jActive := specs[j].Mission.Stats.InProgress > 0
		if iActive != jActive {
			return iActive
		}
		return specs[i].Slug < specs[j].Slug
	})

	return specs
}

func extractSpecTitle(specDir string) string {
	specPath := filepath.Join(specDir, "spec.md")
	f, err := os.Open(specPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

func ReadMissionState(missionDir string) MissionState {
	featuresPath := filepath.Join(missionDir, "features.json")

	data, err := os.ReadFile(featuresPath)
	if err != nil {
		return MissionState{Path: missionDir}
	}

	var manifest FeaturesManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return MissionState{Path: missionDir}
	}

	all := make([]Feature, 0, len(manifest.Features)+len(manifest.FixFeatures))
	all = append(all, manifest.Features...)
	all = append(all, manifest.FixFeatures...)

	tainted := loadTaintedFeatureIDs(missionDir, all)
	outcomes := buildFeatureOutcomes(all, tainted)
	for i := range all {
		if out, ok := outcomes[all[i].ID]; ok {
			all[i].Resolution = out.Resolution
			if out.ResolvedBy != "" {
				all[i].ResolvedBy = out.ResolvedBy
			}
			all[i].Tainted = out.Tainted
		}
	}

	stats := MissionStats{Total: len(all)}
	for _, f := range all {
		switch f.Status {
		case "done", "validated":
			stats.DoneDirect++
		case "in_progress":
			stats.InProgress++
		case "blocked":
			stats.Blocked++
			out := outcomes[f.ID]
			switch out.Resolution {
			case ResolutionResolvedViaFix:
				stats.DoneViaFix++
				stats.BlockedResolved++
			case ResolutionResolvedTainted:
				stats.DoneViaFix++
				stats.BlockedResolved++
				stats.BlockedTainted++
			case ResolutionWaivedContract:
				stats.DoneWaived++
				stats.BlockedResolved++
				stats.BlockedWaived++
			default:
				stats.BlockedUnresolved++
			}
		case "pending", "":
			stats.Pending++
		case "awaiting_validation":
			stats.AwaitingValidation++
		case "validating":
			stats.Validating++
		case "refining":
			stats.Refining++
		}
	}
	stats.Done = stats.DoneDirect + stats.DoneViaFix + stats.DoneWaived

	return MissionState{
		Exists:   true,
		Project:  manifest.Project,
		Owner:    manifest.Owner,
		Features: all,
		Stats:    stats,
		Path:     missionDir,
	}
}

func WriteMissionFiles(specDir, projectDir string, plan PlanData) error {
	missionDir := ResolveArtifactDir(specDir)
	runsDir := filepath.Join(missionDir, "runs")
	designsDir := filepath.Join(specDir, "designs")

	for _, d := range []string{missionDir, runsDir, designsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	for i := range plan.Features {
		if plan.Features[i].Status == "" {
			plan.Features[i].Status = "pending"
		}
	}

	specRelPath := fmt.Sprintf("docs/specs/%s/spec.md", plan.Slug)
	if plan.Spec != "" {
		specRelPath = plan.Spec
	}

	featuresPath := filepath.Join(missionDir, "features.json")
	manifest, err := buildManifestForWrite(featuresPath, plan, specRelPath)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(featuresPath, data, 0o644); err != nil {
		return err
	}

	var contract strings.Builder
	contract.WriteString(fmt.Sprintf("# Validation Contract — %s\n\n", plan.Project))
	contract.WriteString("Behavioral assertions this feature must satisfy.\n\n---\n\n")
	for _, a := range plan.Assertions {
		contract.WriteString(fmt.Sprintf("## %s\n\n", a.Category))
		for _, item := range a.Items {
			contract.WriteString(fmt.Sprintf("- **%s**\n", item))
		}
		contract.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(missionDir, "validation-contract.md"), []byte(contract.String()), 0o644); err != nil {
		return err
	}

	var kb strings.Builder
	kb.WriteString(fmt.Sprintf("# Knowledge Base — %s\n\n", plan.Project))
	kb.WriteString("Workers and validators accumulate findings here.\n\n")
	kb.WriteString("## How to contribute\n\n")
	kb.WriteString("Each entry starts with `## YYYY-MM-DD — short title`.\n")
	kb.WriteString("Workers and validators APPEND; they DO NOT edit others' entries.\n\n---\n\n")
	for _, k := range plan.Knowledge {
		kb.WriteString(fmt.Sprintf("- %s\n", k))
	}
	if err := os.WriteFile(filepath.Join(missionDir, "knowledge-base.md"), []byte(kb.String()), 0o644); err != nil {
		return err
	}

	if projectDir != "" {
		contextContent := GatherProjectContext(projectDir)
		_ = os.WriteFile(filepath.Join(missionDir, "project-context.md"), []byte(contextContent), 0o644)
	}

	specFile := filepath.Join(specDir, "spec.md")
	if isSpecTemplate(specFile) {
		specContent := generateSpecContent(plan)
		_ = os.WriteFile(specFile, []byte(specContent), 0o644)
	}

	return nil
}

func buildManifestForWrite(featuresPath string, plan PlanData, specRelPath string) (FeaturesManifest, error) {
	defaultLifecycle := []string{"pending", "in_progress", "awaiting_validation", "validating", "refining", "done", "blocked"}

	existingData, err := os.ReadFile(featuresPath)
	if err != nil {
		// New artifact write.
		return FeaturesManifest{
			Spec:            specRelPath,
			StatusLifecycle: defaultLifecycle,
			Project:         plan.Project,
			Owner:           plan.Owner,
			Features:        plan.Features,
			FixFeatures:     nil,
		}, nil
	}

	// Merge write for regenerate/edit paths.
	var existing FeaturesManifest
	if err := json.Unmarshal(existingData, &existing); err != nil {
		return FeaturesManifest{}, err
	}

	mergedFeatures := mergeRootFeatures(existing.Features, plan.Features)
	rootIDs := make(map[string]struct{}, len(mergedFeatures))
	for _, f := range mergedFeatures {
		rootIDs[f.ID] = struct{}{}
	}

	normalizedFixes, err := normalizeFixFeatures(existing.FixFeatures, rootIDs)
	if err != nil {
		return FeaturesManifest{}, err
	}

	if err := validateUniqueFeatureIDs(mergedFeatures, normalizedFixes); err != nil {
		return FeaturesManifest{}, err
	}

	project := plan.Project
	if project == "" {
		project = existing.Project
	}
	owner := plan.Owner
	if owner == "" {
		owner = existing.Owner
	}
	lifecycle := existing.StatusLifecycle
	if len(lifecycle) == 0 {
		lifecycle = defaultLifecycle
	}

	return FeaturesManifest{
		Spec:            specRelPath,
		StatusLifecycle: lifecycle,
		Project:         project,
		Owner:           owner,
		Features:        mergedFeatures,
		FixFeatures:     normalizedFixes,
	}, nil
}

func mergeRootFeatures(existing []Feature, planned []Feature) []Feature {
	if len(existing) == 0 {
		return planned
	}
	if len(planned) == 0 {
		return existing
	}

	byExistingID := make(map[string]Feature, len(existing))
	for _, f := range existing {
		byExistingID[f.ID] = f
	}

	seen := make(map[string]struct{}, len(planned))
	merged := make([]Feature, 0, len(planned)+len(existing))
	for _, next := range planned {
		if old, ok := byExistingID[next.ID]; ok {
			merged = append(merged, mergeFeatureExecutionMetadata(old, next))
		} else {
			merged = append(merged, next)
		}
		seen[next.ID] = struct{}{}
	}

	// Preserve historical/runtime entries if omitted by a planner regeneration.
	for _, old := range existing {
		if _, alreadyPresent := seen[old.ID]; alreadyPresent {
			continue
		}
		if old.Status != "" && old.Status != "pending" {
			merged = append(merged, old)
		}
	}

	return merged
}

func mergeFeatureExecutionMetadata(existing Feature, planned Feature) Feature {
	merged := planned

	// Preserve runtime status when planner emits empty/pending placeholders.
	if existing.Status != "" && (planned.Status == "" || planned.Status == "pending") {
		merged.Status = existing.Status
	}

	// Preserve planning metadata when planner emits sparse updates.
	if merged.Description == "" {
		merged.Description = existing.Description
	}
	if merged.RootCauseHypothesis == "" {
		merged.RootCauseHypothesis = existing.RootCauseHypothesis
	}
	if len(merged.Evidence) == 0 {
		merged.Evidence = existing.Evidence
	}
	if len(merged.DoneWhen) == 0 {
		merged.DoneWhen = existing.DoneWhen
	}
	if len(merged.NonGoals) == 0 {
		merged.NonGoals = existing.NonGoals
	}
	if len(merged.RegressionGuards) == 0 {
		merged.RegressionGuards = existing.RegressionGuards
	}

	// Preserve v2 resolution metadata unless planner explicitly sets it.
	if merged.Resolution == "" {
		merged.Resolution = existing.Resolution
	}
	if merged.ResolvedBy == "" {
		merged.ResolvedBy = existing.ResolvedBy
	}
	if merged.ResolvedAt == "" {
		merged.ResolvedAt = existing.ResolvedAt
	}
	if !merged.Tainted {
		merged.Tainted = existing.Tainted
	}

	return merged
}

func ParsePlanFromText(text string) *PlanData {
	text = strings.TrimSpace(text)

	if plan := tryParseJSON(text); plan != nil {
		return plan
	}

	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	if matches := re.FindStringSubmatch(text); len(matches) > 1 {
		if plan := tryParseJSON(matches[1]); plan != nil {
			return plan
		}
	}

	re2 := regexp.MustCompile(`(?s)\{[^{}]*"features"\s*:\s*\[.*\].*\}`)
	if match := re2.FindString(text); match != "" {
		if plan := tryParseJSON(match); plan != nil {
			return plan
		}
	}

	return nil
}

func tryParseJSON(text string) *PlanData {
	var plan PlanData
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &plan); err != nil {
		return nil
	}
	if len(plan.Features) == 0 {
		return nil
	}
	if plan.Slug == "" && plan.Project != "" {
		plan.Slug = slugify(plan.Project)
	}
	return &plan
}

func slugify(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func regenerateSpecIfTemplate(specDir, missionDir string) {
	specFile := filepath.Join(specDir, "spec.md")
	if !isSpecTemplate(specFile) {
		return
	}
	featuresPath := filepath.Join(missionDir, "features.json")
	data, err := os.ReadFile(featuresPath)
	if err != nil {
		return
	}
	var manifest FeaturesManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return
	}
	plan := PlanData{
		Slug:     filepath.Base(specDir),
		Project:  manifest.Project,
		Owner:    manifest.Owner,
		Features: manifest.Features,
	}
	plan.Assertions = parseAssertionsFromContract(filepath.Join(missionDir, "validation-contract.md"))
	specContent := generateSpecContent(plan)
	_ = os.WriteFile(specFile, []byte(specContent), 0o644)
}

func parseAssertionsFromContract(path string) []Assertion {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var assertions []Assertion
	var current *Assertion
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			cat := strings.TrimPrefix(line, "## ")
			assertions = append(assertions, Assertion{Category: cat})
			current = &assertions[len(assertions)-1]
		} else if strings.HasPrefix(line, "- **") && current != nil {
			item := strings.TrimPrefix(line, "- **")
			item = strings.TrimSuffix(item, "**")
			current.Items = append(current.Items, item)
		}
	}
	return assertions
}

func isSpecTemplate(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	content := string(data)
	return strings.Contains(content, "<!-- 2-4 sentences") || strings.Contains(content, "{{Name <email>}}")
}

func generateSpecContent(plan PlanData) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("---\nid: %s\ntitle: %s\nowner: %s\nstatus: draft\nrevision: 1\n---\n\n", plan.Slug, plan.Project, plan.Owner))
	b.WriteString(fmt.Sprintf("# %s\n\n", plan.Project))

	b.WriteString("## Scope\n\n")
	scopes := make(map[string]bool)
	for _, f := range plan.Features {
		if f.Scope != "" {
			scopes[f.Scope] = true
		}
	}
	for scope := range scopes {
		b.WriteString(fmt.Sprintf("- %s\n", scope))
	}
	b.WriteString("\n")

	b.WriteString("## Features\n\n")
	phases := make(map[int][]Feature)
	for _, f := range plan.Features {
		phases[f.Phase] = append(phases[f.Phase], f)
	}
	phaseNames := []string{"Foundation", "Core", "Polish", "Extras"}
	for phase := 0; phase <= 3; phase++ {
		feats, ok := phases[phase]
		if !ok {
			continue
		}
		name := fmt.Sprintf("Phase %d", phase)
		if phase < len(phaseNames) {
			name = fmt.Sprintf("Phase %d — %s", phase, phaseNames[phase])
		}
		b.WriteString(fmt.Sprintf("### %s\n\n", name))
		for _, f := range feats {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", f.ID, f.Title))
			if f.Scope != "" {
				b.WriteString(fmt.Sprintf("  - Scope: %s\n", f.Scope))
			}
			if len(f.DependsOn) > 0 {
				b.WriteString(fmt.Sprintf("  - Depends on: %s\n", strings.Join(f.DependsOn, ", ")))
			}
			if len(f.ValidationRefs) > 0 {
				b.WriteString(fmt.Sprintf("  - Validates: %s\n", strings.Join(f.ValidationRefs, ", ")))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Validation Contract\n\n")
	for _, a := range plan.Assertions {
		b.WriteString(fmt.Sprintf("### %s\n\n", a.Category))
		for _, item := range a.Items {
			b.WriteString(fmt.Sprintf("- **%s**\n", item))
		}
		b.WriteString("\n")
	}

	if len(plan.Knowledge) > 0 {
		b.WriteString("## Knowledge\n\n")
		for _, k := range plan.Knowledge {
			b.WriteString(fmt.Sprintf("- %s\n", k))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func ParseAssertionsJSON(text string) []Assertion {
	text = strings.TrimSpace(text)

	// Try direct JSON array
	var assertions []Assertion
	if err := json.Unmarshal([]byte(text), &assertions); err == nil && len(assertions) > 0 {
		return assertions
	}

	// Try extracting from code fences
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	if matches := re.FindStringSubmatch(text); len(matches) > 1 {
		if err := json.Unmarshal([]byte(matches[1]), &assertions); err == nil && len(assertions) > 0 {
			return assertions
		}
	}

	// Try finding a JSON array in the text
	re2 := regexp.MustCompile(`(?s)\[[\s\S]*"category"[\s\S]*\]`)
	if match := re2.FindString(text); match != "" {
		if err := json.Unmarshal([]byte(match), &assertions); err == nil && len(assertions) > 0 {
			return assertions
		}
	}

	return nil
}

// Deprecated: use ParseAssertionsOnlyJSON + ParseFeaturesAndKnowledgeJSON.
// Kept for standalone testing and legacy single-call flow.
func ParseSkillJSON(text string) ([]Feature, []Assertion, []string, bool) {
	plan := ParsePlanFromText(text)
	if plan != nil && len(plan.Features) > 0 && len(plan.Assertions) > 0 {
		return plan.Features, plan.Assertions, plan.Knowledge, true
	}
	features := ParseFeaturesJSON(text)
	assertions := ParseAssertionsJSON(text)
	if len(features) > 0 && len(assertions) > 0 {
		knowledge := ParseKnowledgeJSON(text)
		return features, assertions, knowledge, true
	}
	return nil, nil, nil, false
}

// ParseAssertionsOnlyJSON is the v2 Call 1 parser: expects a JSON array of
// assertion groups. Returns ok=true when at least one group with at least one
// item is parsed.
func ParseAssertionsOnlyJSON(text string) ([]Assertion, bool) {
	assertions := ParseAssertionsJSON(text)
	for _, a := range assertions {
		if len(a.Items) > 0 {
			return assertions, true
		}
	}
	return nil, false
}

func ParseAssertionDeltaJSON(text string) (AssertionDelta, bool) {
	for _, candidate := range collectJSONCandidates(text) {
		if delta, ok := parseAssertionDeltaCandidate(candidate); ok {
			return normalizeAssertionDelta(delta), true
		}
	}
	return AssertionDelta{}, false
}

func ParseFeatureDeltaJSON(text string) (FeatureDelta, bool) {
	for _, candidate := range collectJSONCandidates(text) {
		if delta, ok := parseFeatureDeltaCandidate(candidate); ok {
			return normalizeFeatureDelta(delta), true
		}
	}
	return FeatureDelta{}, false
}

func parseAssertionDeltaCandidate(candidate string) (AssertionDelta, bool) {
	var direct AssertionDelta
	if err := json.Unmarshal([]byte(candidate), &direct); err == nil &&
		(len(direct.Upsert) > 0 || len(direct.Remove) > 0 || hasTopLevelJSONKey(candidate, "upsert") || hasTopLevelJSONKey(candidate, "remove")) {
		return direct, true
	}

	var wrapped struct {
		AssertionDelta AssertionDelta `json:"assertion_delta"`
	}
	if err := json.Unmarshal([]byte(candidate), &wrapped); err == nil &&
		(len(wrapped.AssertionDelta.Upsert) > 0 || len(wrapped.AssertionDelta.Remove) > 0 || hasTopLevelJSONKey(candidate, "assertion_delta")) {
		return wrapped.AssertionDelta, true
	}

	// Backward compatibility: accept full assertion arrays and treat them as
	// pure upserts (no removals).
	var assertions []Assertion
	if err := json.Unmarshal([]byte(candidate), &assertions); err == nil && len(assertions) > 0 {
		return assertionDeltaFromAssertions(assertions), true
	}

	return AssertionDelta{}, false
}

func parseFeatureDeltaCandidate(candidate string) (FeatureDelta, bool) {
	var direct FeatureDelta
	if err := json.Unmarshal([]byte(candidate), &direct); err == nil &&
		(len(direct.Upsert) > 0 || len(direct.Remove) > 0 || hasTopLevelJSONKey(candidate, "upsert") || hasTopLevelJSONKey(candidate, "remove")) {
		return direct, true
	}

	var wrapped struct {
		FeatureDelta FeatureDelta `json:"feature_delta"`
	}
	if err := json.Unmarshal([]byte(candidate), &wrapped); err == nil &&
		(len(wrapped.FeatureDelta.Upsert) > 0 || len(wrapped.FeatureDelta.Remove) > 0 || hasTopLevelJSONKey(candidate, "feature_delta")) {
		return wrapped.FeatureDelta, true
	}

	// Backward compatibility: accept full features outputs and treat them as
	// pure upserts.
	var arrayFeatures []Feature
	if err := json.Unmarshal([]byte(candidate), &arrayFeatures); err == nil && len(arrayFeatures) > 0 {
		return FeatureDelta{Upsert: arrayFeatures}, true
	}
	var obj struct {
		Features []Feature `json:"features"`
	}
	if err := json.Unmarshal([]byte(candidate), &obj); err == nil && len(obj.Features) > 0 {
		return FeatureDelta{Upsert: obj.Features}, true
	}

	return FeatureDelta{}, false
}

func collectJSONCandidates(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var candidates []string
	appendCandidate := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, existing := range candidates {
			if existing == v {
				return
			}
		}
		candidates = append(candidates, v)
	}

	appendCandidate(text)

	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			appendCandidate(match[1])
		}
	}

	appendCandidate(extractFirstBalancedBlock(text, '{', '}'))
	appendCandidate(extractFirstBalancedBlock(text, '[', ']'))

	return candidates
}

func extractFirstBalancedBlock(text string, open, close byte) string {
	start := strings.IndexByte(text, open)
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

func hasTopLevelJSONKey(candidate, key string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate), &raw); err != nil {
		return false
	}
	_, ok := raw[key]
	return ok
}

func assertionDeltaFromAssertions(assertions []Assertion) AssertionDelta {
	var upsert []AssertionDeltaItem
	for _, a := range assertions {
		category := strings.TrimSpace(a.Category)
		for _, item := range a.Items {
			id := extractAssertionID(item)
			if id == "" {
				continue
			}
			upsert = append(upsert, AssertionDeltaItem{
				ID:        id,
				Category:  category,
				Assertion: item,
			})
		}
	}
	return AssertionDelta{Upsert: upsert}
}

func normalizeAssertionDelta(delta AssertionDelta) AssertionDelta {
	norm := AssertionDelta{}

	removeSeen := make(map[string]struct{})
	for _, id := range delta.Remove {
		id = normalizeAssertionID(id)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := removeSeen[key]; ok {
			continue
		}
		removeSeen[key] = struct{}{}
		norm.Remove = append(norm.Remove, id)
	}

	upsertSeen := make(map[string]int)
	for _, item := range delta.Upsert {
		id := normalizeAssertionID(item.ID)
		if id == "" {
			id = extractAssertionID(item.Assertion)
		}
		if id == "" {
			continue
		}
		category := strings.TrimSpace(item.Category)
		if category == "" {
			category = assertionCategoryFromID(id)
		}
		text := formatAssertionText(id, item.Assertion)
		next := AssertionDeltaItem{ID: id, Category: category, Assertion: text}
		key := strings.ToLower(id)
		if idx, exists := upsertSeen[key]; exists {
			norm.Upsert[idx] = next
			continue
		}
		upsertSeen[key] = len(norm.Upsert)
		norm.Upsert = append(norm.Upsert, next)
	}

	return norm
}

func normalizeFeatureDelta(delta FeatureDelta) FeatureDelta {
	norm := FeatureDelta{}
	removeSeen := make(map[string]struct{})
	for _, id := range delta.Remove {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := removeSeen[key]; ok {
			continue
		}
		removeSeen[key] = struct{}{}
		norm.Remove = append(norm.Remove, id)
	}

	upsertSeen := make(map[string]int)
	for _, f := range delta.Upsert {
		f.ID = strings.TrimSpace(f.ID)
		if f.ID == "" {
			continue
		}
		if f.Status == "" {
			f.Status = "pending"
		}
		key := strings.ToLower(f.ID)
		if idx, exists := upsertSeen[key]; exists {
			norm.Upsert[idx] = f
			continue
		}
		upsertSeen[key] = len(norm.Upsert)
		norm.Upsert = append(norm.Upsert, f)
	}

	return norm
}

func ApplyAssertionDelta(existing []Assertion, delta AssertionDelta) []Assertion {
	type assertionRecord struct {
		ID       string
		Category string
		Text     string
	}

	records := make(map[string]assertionRecord)
	for _, group := range existing {
		category := strings.TrimSpace(group.Category)
		for _, item := range group.Items {
			id := extractAssertionID(item)
			if id == "" {
				continue
			}
			key := strings.ToLower(id)
			records[key] = assertionRecord{
				ID:       id,
				Category: category,
				Text:     formatAssertionText(id, item),
			}
		}
	}

	delta = normalizeAssertionDelta(delta)
	for _, id := range delta.Remove {
		delete(records, strings.ToLower(id))
	}
	for _, item := range delta.Upsert {
		key := strings.ToLower(item.ID)
		records[key] = assertionRecord{
			ID:       item.ID,
			Category: strings.TrimSpace(item.Category),
			Text:     formatAssertionText(item.ID, item.Assertion),
		}
	}

	byCategory := make(map[string][]assertionRecord)
	for _, rec := range records {
		category := strings.TrimSpace(rec.Category)
		if category == "" {
			category = assertionCategoryFromID(rec.ID)
		}
		rec.Category = category
		byCategory[category] = append(byCategory[category], rec)
	}

	categories := make([]string, 0, len(byCategory))
	for category := range byCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)

	merged := make([]Assertion, 0, len(categories))
	for _, category := range categories {
		items := byCategory[category]
		sort.Slice(items, func(i, j int) bool {
			return assertionIDLess(items[i].ID, items[j].ID)
		})
		group := Assertion{Category: category, Items: make([]string, 0, len(items))}
		for _, item := range items {
			group.Items = append(group.Items, item.Text)
		}
		if len(group.Items) > 0 {
			merged = append(merged, group)
		}
	}

	return merged
}

func ApplyFeatureDelta(existing []Feature, delta FeatureDelta, preserveDone bool) []Feature {
	delta = normalizeFeatureDelta(delta)
	records := make(map[string]Feature, len(existing))
	for _, feature := range existing {
		id := strings.TrimSpace(feature.ID)
		if id == "" {
			continue
		}
		feature.ID = id
		records[strings.ToLower(id)] = feature
	}

	removeSet := make(map[string]struct{}, len(delta.Remove))
	for _, id := range delta.Remove {
		key := strings.ToLower(strings.TrimSpace(id))
		if key != "" {
			removeSet[key] = struct{}{}
		}
	}

	for key := range removeSet {
		if feature, exists := records[key]; exists {
			if preserveDone && isTerminalFeatureStatus(feature.Status) {
				continue
			}
			delete(records, key)
		}
	}

	for _, next := range delta.Upsert {
		key := strings.ToLower(next.ID)
		if existingFeature, exists := records[key]; exists {
			if preserveDone && isTerminalFeatureStatus(existingFeature.Status) {
				continue
			}
			records[key] = mergeFeatureExecutionMetadata(existingFeature, next)
			continue
		}
		records[key] = next
	}

	merged := make([]Feature, 0, len(records))
	for _, feature := range records {
		merged = append(merged, feature)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Phase != merged[j].Phase {
			return merged[i].Phase < merged[j].Phase
		}
		return merged[i].ID < merged[j].ID
	})
	return merged
}

func isTerminalFeatureStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "done", "validated":
		return true
	default:
		return false
	}
}

func extractAssertionID(item string) string {
	re := regexp.MustCompile(`^\s*([a-zA-Z][a-zA-Z0-9_-]*\.\d+)`)
	match := re.FindStringSubmatch(strings.TrimSpace(item))
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func normalizeAssertionID(id string) string {
	id = strings.TrimSpace(strings.TrimSuffix(id, ":"))
	if id == "" {
		return ""
	}
	re := regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9_-]*\.\d+)`)
	if match := re.FindStringSubmatch(id); len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func assertionCategoryFromID(id string) string {
	if idx := strings.Index(id, "."); idx > 0 {
		return strings.TrimSpace(id[:idx])
	}
	return ""
}

func formatAssertionText(id, body string) string {
	text := strings.TrimSpace(body)
	if text == "" {
		return id + ":"
	}
	if parts := strings.SplitN(text, ":", 2); len(parts) == 2 {
		prefix := normalizeAssertionID(parts[0])
		if prefix != "" && strings.EqualFold(prefix, id) {
			return fmt.Sprintf("%s: %s", id, strings.TrimSpace(parts[1]))
		}
	}
	return fmt.Sprintf("%s: %s", id, strings.TrimLeft(text, ": "))
}

func assertionIDLess(a, b string) bool {
	catA := assertionCategoryFromID(a)
	catB := assertionCategoryFromID(b)
	if catA != catB {
		return catA < catB
	}
	numA := assertionIDNumber(a)
	numB := assertionIDNumber(b)
	if numA != numB {
		return numA < numB
	}
	return a < b
}

func assertionIDNumber(id string) int {
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return 0
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return n
}

// ParseFeaturesOnlyJSON is the v3 Phase Features parser: expects either
//
//	{"features": [...]}
//
// or a bare features array. Knowledge is NOT extracted here (separate phase).
// Returns ok=true when at least one feature parses.
func ParseFeaturesOnlyJSON(text string) ([]Feature, bool) {
	text = strings.TrimSpace(text)

	// Try bare array first (most permissive — model may drop the wrapper).
	var arr []Feature
	if err := json.Unmarshal([]byte(text), &arr); err == nil && len(arr) > 0 {
		return arr, true
	}

	// Try wrapped object.
	var wrapper struct {
		Features []Feature `json:"features"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err == nil && len(wrapper.Features) > 0 {
		return wrapper.Features, true
	}

	// Try fenced JSON.
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	if matches := re.FindStringSubmatch(text); len(matches) > 1 {
		body := strings.TrimSpace(matches[1])
		if err := json.Unmarshal([]byte(body), &wrapper); err == nil && len(wrapper.Features) > 0 {
			return wrapper.Features, true
		}
		if err := json.Unmarshal([]byte(body), &arr); err == nil && len(arr) > 0 {
			return arr, true
		}
	}

	// Try balanced { ... } object containing "features".
	if start := strings.Index(text, "{"); start >= 0 {
		depth := 0
		for i := start; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					if err := json.Unmarshal([]byte(text[start:i+1]), &wrapper); err == nil && len(wrapper.Features) > 0 {
						return wrapper.Features, true
					}
					i = len(text)
				}
			}
		}
	}

	// Final fallback: existing parser (handles edge cases of bare object regex).
	features := ParseFeaturesJSON(text)
	if len(features) > 0 {
		return features, true
	}
	return nil, false
}

// Deprecated: use ParseFeaturesOnlyJSON + a separate Knowledge parse.
// ParseFeaturesAndKnowledgeJSON was the v2 combined parser.
func ParseFeaturesAndKnowledgeJSON(text string) ([]Feature, []string, bool) {
	text = strings.TrimSpace(text)

	var wrapper struct {
		Features  []Feature `json:"features"`
		Knowledge []string  `json:"knowledge"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err == nil && len(wrapper.Features) > 0 {
		return wrapper.Features, wrapper.Knowledge, true
	}

	// Try code fences
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	if matches := re.FindStringSubmatch(text); len(matches) > 1 {
		if err := json.Unmarshal([]byte(matches[1]), &wrapper); err == nil && len(wrapper.Features) > 0 {
			return wrapper.Features, wrapper.Knowledge, true
		}
	}

	// Try locating a balanced { ... } object with "features"
	if start := strings.Index(text, "{"); start >= 0 {
		depth := 0
		for i := start; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					if err := json.Unmarshal([]byte(text[start:i+1]), &wrapper); err == nil && len(wrapper.Features) > 0 {
						return wrapper.Features, wrapper.Knowledge, true
					}
					i = len(text) // break outer loop
				}
			}
		}
	}

	// Final fallback: parse features and knowledge separately
	features := ParseFeaturesJSON(text)
	knowledge := ParseKnowledgeJSON(text)
	if len(features) > 0 {
		return features, knowledge, true
	}
	return nil, nil, false
}

// CompactAssertionIDs returns a per-category map of assertion IDs (e.g. "ui.1",
// "ui.2", ...) extracted from parsed assertion groups. Used to feed Call 2
// without re-injecting the assertion text.
func CompactAssertionIDs(assertions []Assertion) map[string][]string {
	if len(assertions) == 0 {
		return nil
	}
	idRe := regexp.MustCompile(`^\s*([a-zA-Z][a-zA-Z0-9_]*\.\d+)`)
	result := make(map[string][]string, len(assertions))
	for _, a := range assertions {
		category := strings.TrimSpace(a.Category)
		if category == "" {
			continue
		}
		var ids []string
		for _, item := range a.Items {
			if m := idRe.FindStringSubmatch(item); len(m) >= 2 {
				ids = append(ids, m[1])
			}
		}
		if len(ids) > 0 {
			result[category] = ids
		}
	}
	return result
}

func ParseFeaturesJSON(text string) []Feature {
	text = strings.TrimSpace(text)

	// Try as object with "features" key
	var wrapper struct {
		Features []Feature `json:"features"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err == nil && len(wrapper.Features) > 0 {
		return wrapper.Features
	}

	// Try extracting from code fences
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	if matches := re.FindStringSubmatch(text); len(matches) > 1 {
		if err := json.Unmarshal([]byte(matches[1]), &wrapper); err == nil && len(wrapper.Features) > 0 {
			return wrapper.Features
		}
	}

	// Try finding JSON object with features
	re2 := regexp.MustCompile(`(?s)\{[^{}]*"features"\s*:\s*\[.*\].*\}`)
	if match := re2.FindString(text); match != "" {
		if err := json.Unmarshal([]byte(match), &wrapper); err == nil && len(wrapper.Features) > 0 {
			return wrapper.Features
		}
	}

	return nil
}

func ParseKnowledgeJSON(text string) []string {
	text = strings.TrimSpace(text)

	var knowledge []string
	if err := json.Unmarshal([]byte(text), &knowledge); err == nil && len(knowledge) > 0 {
		return knowledge
	}

	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	if matches := re.FindStringSubmatch(text); len(matches) > 1 {
		if err := json.Unmarshal([]byte(matches[1]), &knowledge); err == nil && len(knowledge) > 0 {
			return knowledge
		}
	}

	re2 := regexp.MustCompile(`(?s)\[[\s\S]*"[^"]+?"[\s\S]*\]`)
	if match := re2.FindString(text); match != "" {
		if err := json.Unmarshal([]byte(match), &knowledge); err == nil && len(knowledge) > 0 {
			return knowledge
		}
	}

	return nil
}

func WriteValidationContract(missionDir, project string, assertions []Assertion) error {
	var contract strings.Builder
	contract.WriteString(fmt.Sprintf("# Validation Contract — %s\n\n", project))
	contract.WriteString("Behavioral assertions this feature must satisfy.\n\n---\n\n")
	for _, a := range assertions {
		contract.WriteString(fmt.Sprintf("## %s\n\n", a.Category))
		for _, item := range a.Items {
			contract.WriteString(fmt.Sprintf("- **%s**\n", item))
		}
		contract.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(missionDir, "validation-contract.md"), []byte(contract.String()), 0o644)
}

func WriteKnowledgeBase(missionDir, project string, knowledge []string) error {
	var kb strings.Builder
	kb.WriteString(fmt.Sprintf("# Knowledge Base — %s\n\n", project))
	kb.WriteString("Workers and validators accumulate findings here.\n\n")
	kb.WriteString("## How to contribute\n\n")
	kb.WriteString("Each entry starts with `## YYYY-MM-DD — short title`.\n")
	kb.WriteString("Workers and validators APPEND; they DO NOT edit others' entries.\n\n---\n\n")
	for _, k := range knowledge {
		kb.WriteString(fmt.Sprintf("- %s\n", k))
	}
	return os.WriteFile(filepath.Join(missionDir, "knowledge-base.md"), []byte(kb.String()), 0o644)
}
