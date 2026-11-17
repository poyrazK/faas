package db

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// imaged's direct pool must hold, at once, the hub's LISTEN, one activation
// on its LISTEN loop, one on its replay worker, and each parallel
// preparation's activation lock. With three connections, a preparation on
// the loop left the replay worker's activation without a connection for its
// traffic guard (2026-11-17: 57 deployments waited 10-110 s).
func TestImagedDirectPoolCoversConcurrentActivations(t *testing.T) {
	t.Setenv(NotifyHubEnv, "")
	serial := 1 + 2*imagedActivationConns
	cases := []struct {
		parallel string
		want     int32
	}{
		{"", serial + api.ImagedImagePrepWorkers},
		{"false", serial},
		{"not-a-bool", serial},
		{"1", serial + api.ImagedImagePrepWorkers},
		{" true ", serial + api.ImagedImagePrepWorkers},
	}
	for _, tc := range cases {
		t.Setenv(ImagedParallelPrepEnv, tc.parallel)
		for _, name := range []string{"faas-imaged", "imaged"} {
			if got := directMaxConns(name); got != tc.want {
				t.Errorf("directMaxConns(%q) with %s=%q = %d, want %d", name, ImagedParallelPrepEnv, tc.parallel, got, tc.want)
			}
		}
	}

	t.Setenv(ImagedParallelPrepEnv, "")
	hub, loopPreparation := int32(1), int32(1)
	if need := hub + loopPreparation + imagedActivationConns; directMaxConns("faas-imaged") < need {
		t.Fatalf("imaged direct budget %d cannot run a replayed activation beside a preparation (needs %d)", directMaxConns("faas-imaged"), need)
	}
	if got := directMaxConns("faas-apid"); got != directNestedGuardHubOnMaxConns {
		t.Fatalf("apid direct budget = %d, want its nested guard budget %d", got, directNestedGuardHubOnMaxConns)
	}
}
