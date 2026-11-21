// adr: 20261118154632
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/builderslots"
)

// builderd reads the node's provisioned reserved builder slots from the
// environment node_join writes, and runs a notification worker per reserved
// slot, never fewer than today's two.
func TestBuilderReservedSlotsFromEnvironment(t *testing.T) {
	for _, tc := range []struct {
		env         string
		wantSlots   int
		wantWorkers int
	}{
		{env: "", wantSlots: 1, wantWorkers: 2},
		{env: "bogus", wantSlots: 1, wantWorkers: 2},
		{env: "2", wantSlots: 2, wantWorkers: 2},
		{env: " 4 ", wantSlots: 4, wantWorkers: 4},
		{env: "12", wantSlots: 4, wantWorkers: 4},
	} {
		t.Setenv(builderslots.ReservedSlotsEnv, tc.env)
		cfg, err := LoadConfig("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BuilderReservedSlots != tc.wantSlots {
			t.Errorf("%s=%q: reserved slots %d, want %d", builderslots.ReservedSlotsEnv, tc.env, cfg.BuilderReservedSlots, tc.wantSlots)
		}
		if got := notificationWorkers(cfg.BuilderReservedSlots); got != tc.wantWorkers {
			t.Errorf("%s=%q: notification workers %d, want %d", builderslots.ReservedSlotsEnv, tc.env, got, tc.wantWorkers)
		}
	}
}
