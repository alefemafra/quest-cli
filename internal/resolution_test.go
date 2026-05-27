package internal

import "testing"

func TestBuildFeatureOutcomes_BlockedWaivedContractIsEffectiveDone(t *testing.T) {
	features := []Feature{
		{
			ID:         "F11",
			Status:     "blocked",
			Resolution: ResolutionWaivedContract,
		},
	}

	outcomes := buildFeatureOutcomes(features, map[string]bool{})
	out, ok := outcomes["F11"]
	if !ok {
		t.Fatalf("expected outcome for F11")
	}
	if !out.EffectiveDone {
		t.Fatalf("expected waived feature to be effectively done")
	}
	if out.Resolution != ResolutionWaivedContract {
		t.Fatalf("expected resolution=%s, got %s", ResolutionWaivedContract, out.Resolution)
	}
	if out.ResolvedBy != "F11" {
		t.Fatalf("expected resolved_by F11, got %s", out.ResolvedBy)
	}
}

func TestBuildFeatureOutcomes_WaivedRootIgnoresUnresolvedChildFix(t *testing.T) {
	features := []Feature{
		{
			ID:         "F11",
			Status:     "blocked",
			Resolution: ResolutionWaivedContract,
		},
		{
			ID:     "F11-fix-01",
			Status: "blocked",
			Fixes:  "F11",
		},
	}

	outcomes := buildFeatureOutcomes(features, map[string]bool{})
	root := outcomes["F11"]
	if !root.EffectiveDone || root.Resolution != ResolutionWaivedContract {
		t.Fatalf("expected waived root to be effectively done, got %#v", root)
	}
	child := outcomes["F11-fix-01"]
	if child.EffectiveDone || child.Resolution != ResolutionUnresolved {
		t.Fatalf("expected unresolved child fix, got %#v", child)
	}
}

