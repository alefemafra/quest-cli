package internal

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func TestHandleDashboardKey_ModeConfirmRouting(t *testing.T) {
	base := Model{
		phase:      PhaseDashboard,
		activeSpec: &SpecEntry{Slug: "demo", SpecPath: "/tmp/demo"},
		mission:    MissionState{Stats: MissionStats{Pending: 1}},
	}

	nextModel, _ := base.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.confirmRegen || next.confirmDelta || next.confirmChanges || next.confirmSkipCritic {
		t.Fatalf("G should set only confirmRegen")
	}

	nextModel, _ = base.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("U")})
	next, ok = nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.confirmDelta || next.confirmRegen || next.confirmChanges || next.confirmSkipCritic {
		t.Fatalf("U should set only confirmDelta")
	}

	nextModel, _ = base.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	next, ok = nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.confirmChanges || next.confirmRegen || next.confirmDelta || next.confirmSkipCritic {
		t.Fatalf("C should set only confirmChanges")
	}

	nextModel, _ = base.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	next, ok = nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.confirmSkipCritic || next.confirmRegen || next.confirmDelta || next.confirmChanges {
		t.Fatalf("Shift+S should set only confirmSkipCritic")
	}
}

func TestHandleDashboardKey_ConfirmStartsCorrectPipelineMode(t *testing.T) {
	spec := &SpecEntry{Slug: "demo", SpecPath: "/tmp/demo"}

	m := Model{phase: PhaseDashboard, activeSpec: spec, confirmRegen: true}
	nextModel, _ := m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.fullRegenMode || next.regenMode || next.changesMode {
		t.Fatalf("confirmRegen should start fullRegen mode")
	}

	m = Model{phase: PhaseDashboard, activeSpec: spec, confirmDelta: true}
	nextModel, _ = m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	next, ok = nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.regenMode || next.fullRegenMode || next.changesMode {
		t.Fatalf("confirmDelta should start regen(delta simple) mode")
	}

	m = Model{phase: PhaseDashboard, activeSpec: spec, confirmChanges: true}
	nextModel, _ = m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	next, ok = nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.changesMode || next.fullRegenMode || next.regenMode {
		t.Fatalf("confirmChanges should start changes mode")
	}

	m = Model{phase: PhaseDashboard, confirmSkipCritic: true}
	nextModel, _ = m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	next, ok = nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.criticBypassed {
		t.Fatalf("confirmSkipCritic should start workers bypassing critic gate")
	}
}

func TestGenPhaseLabel_ModeSpecificPhases(t *testing.T) {
	if got := (Model{genPhase: GenPhaseAssertions, regenMode: true}).genPhaseLabel(); got != "opus · phase 1/4" {
		t.Fatalf("unexpected regen assertions label: %q", got)
	}
	if got := (Model{genPhase: GenPhaseAnalysis, changesMode: true}).genPhaseLabel(); got != "sonnet · phase 1/5" {
		t.Fatalf("unexpected changes analysis label: %q", got)
	}
	if got := (Model{genPhase: GenPhaseNone, fullRegenMode: true}).genPhaseLabel(); got != "opus · phase 1/3" {
		t.Fatalf("unexpected full regen idle label: %q", got)
	}
}

func TestSpinnerTick_ChangesBudgetWarnings(t *testing.T) {
	m := NewModel(t.TempDir(), false, "")
	m.phase = PhaseRunning
	m.changesMode = true
	m.claudeRunning = true
	m.genPhase = GenPhaseAnalysis
	m.claudeStartTime = time.Now().Add(-(changesPhaseSoftBudget + time.Second))
	m.discoveryMsgs = nil

	nextModel, _ := m.Update(spinner.TickMsg{Time: time.Now()})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next.changesSoftWarned {
		t.Fatalf("soft budget warning should be flagged")
	}
	foundSoft := false
	for _, msg := range next.discoveryMsgs {
		if len(msg.Text) >= len("PHASE_WARN_CHANGES:") && msg.Text[:len("PHASE_WARN_CHANGES:")] == "PHASE_WARN_CHANGES:" {
			foundSoft = true
			break
		}
	}
	if !foundSoft {
		t.Fatalf("expected PHASE_WARN_CHANGES message")
	}

	next.claudeStartTime = time.Now().Add(-(changesPhaseHardBudget + time.Second))
	next.changesTimeoutSent = false
	next.discoveryMsgs = nil

	nextModel, _ = next.Update(spinner.TickMsg{Time: time.Now()})
	next2, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}
	if !next2.changesTimeoutSent {
		t.Fatalf("hard budget timeout should be flagged")
	}
	foundHard := false
	for _, msg := range next2.discoveryMsgs {
		if len(msg.Text) >= len("PHASE_TIMEOUT_CHANGES:") && msg.Text[:len("PHASE_TIMEOUT_CHANGES:")] == "PHASE_TIMEOUT_CHANGES:" {
			foundHard = true
			break
		}
	}
	if !foundHard {
		t.Fatalf("expected PHASE_TIMEOUT_CHANGES message")
	}
}

func TestRenderReviewCritic_DoesNotPanicWhenSelectionMissing(t *testing.T) {
	m := Model{
		width:          120,
		styles:         NewStyles(),
		criticAdvisory: []CriticFinding{{Criterion: "J-A1", Status: "needs-work", Note: "n"}},
		criticSelected: nil,
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("renderReviewCritic should not panic: %v", r)
		}
	}()

	_ = m.renderReviewCritic()
}

func TestTransitionToReview_ResizesCriticSelectionState(t *testing.T) {
	m := NewModel(t.TempDir(), false, "")
	m.width = 120
	m.phase = PhaseRunning
	m.missionDir = t.TempDir()
	m.criticAdvisory = []CriticFinding{
		{Criterion: "J-A1", Status: "needs-work"},
		{Criterion: "J-A2", Status: "needs-work"},
	}
	m.criticSelected = nil
	m.criticCursor = 10

	nextModel, _ := m.transitionToReview()
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model")
	}

	if len(next.criticSelected) != len(next.criticAdvisory) {
		t.Fatalf("expected criticSelected resized to advisory len")
	}
	if next.criticCursor != len(next.criticAdvisory)-1 {
		t.Fatalf("expected criticCursor clamped, got %d", next.criticCursor)
	}
}
