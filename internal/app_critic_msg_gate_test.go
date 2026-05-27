package internal

import "testing"

func TestInCriticGenerationFlow(t *testing.T) {
	cases := []struct {
		name     string
		model    Model
		expected bool
	}{
		{
			name:     "running critic phase",
			model:    Model{phase: PhaseRunning, genPhase: GenPhaseCritic},
			expected: true,
		},
		{
			name:     "running fix loop phase",
			model:    Model{phase: PhaseRunning, genPhase: GenPhaseFixLoop},
			expected: true,
		},
		{
			name:     "dashboard should ignore",
			model:    Model{phase: PhaseDashboard, genPhase: GenPhaseCritic},
			expected: false,
		},
		{
			name:     "running unrelated phase",
			model:    Model{phase: PhaseRunning, genPhase: GenPhaseAnalysis},
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.model.inCriticGenerationFlow()
			if got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestUpdate_IgnoresLateCriticLoopMsgOutsideGeneration(t *testing.T) {
	m := Model{
		phase:     PhaseDashboard,
		genPhase:  GenPhaseNone,
		reviewTab: ReviewTabChat,
	}

	nextModel, _ := m.Update(criticLoopMsg{
		passed:   false,
		advisory: []CriticFinding{{Criterion: "J-A1", Status: "needs-work"}},
		blocking: []CriticFinding{{Criterion: "J-D1", Status: "needs-work"}},
	})

	got, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model return type")
	}
	if got.phase != PhaseDashboard {
		t.Fatalf("expected phase to remain dashboard, got %v", got.phase)
	}
	if got.reviewTab != ReviewTabChat {
		t.Fatalf("expected review tab unchanged, got %v", got.reviewTab)
	}
	if len(got.criticBlocking) != 0 || len(got.criticAdvisory) != 0 {
		t.Fatalf("expected critic findings to be ignored outside generation flow")
	}
}
