package main

// adr: 627
import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

type regionalRecoverySeeder struct{ err error }

func (s regionalRecoverySeeder) SeedLedger(context.Context) error { return s.err }

func TestRegionalRecoveryBlocksSchedulerStartup(t *testing.T) {
	unavailable := errors.New("resume journal unavailable")
	for _, scenario := range []string{"complete", "ordinary-error", "unknown-regional-resume"} {
		t.Run(scenario, func(t *testing.T) {
			var seedErr error
			switch scenario {
			case "ordinary-error":
				seedErr = unavailable
			case "unknown-regional-resume":
				seedErr = errors.Join(sched.ErrRegionalRuntimeRecoveryUnresolved, unavailable)
			}
			err := seedSchedulerLedger(t.Context(), regionalRecoverySeeder{err: seedErr}, discardLog())
			if scenario == "unknown-regional-resume" {
				if !errors.Is(err, sched.ErrRegionalRuntimeRecoveryUnresolved) || !errors.Is(err, unavailable) {
					t.Fatal("incomplete accounting started admission", err)
				}
			} else if err != nil {
				t.Fatal("independent startup behavior changed", err)
			}
		})
	}
}
