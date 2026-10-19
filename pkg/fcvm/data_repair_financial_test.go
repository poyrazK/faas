//go:build linux || darwin

package fcvm

// adr: 566
// adr: 20261016 — unsupported repair VMs cannot borrow ordinary app authority.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/financial"
)

func TestDataRepairRehearsalAdmissionPrecedesNativeClaim(t *testing.T) {
	for _, name := range []string{"strict_budget", "authority_outage", "contained_app"} {
		t.Run(name, func(t *testing.T) {
			m, v, frame, input, guest := dataRepairNativeFixture(t)
			calls := 0
			var refused error
			if name == "contained_app" {
				m.WithRehearsalNetworkPolicies(&rehearsalPolicyStub{run: rehearsalNetworkTestRun})
			} else {
				refused = financial.ErrComputeUnavailable
				if name == "authority_outage" {
					refused = errors.New("financial authority unavailable")
				}
				m.WithFinancialComputeBypassGuard(func(_ context.Context, app string) error {
					calls++
					if app != frame.Scope.AppID {
						t.Fatal("repair admission lost its original application identity")
					}
					return refused
				})
			}
			evidence, err := m.RunDataRepairRehearsal(t.Context(), frame, input)
			if err == nil || refused != nil && !errors.Is(err, refused) {
				t.Fatalf("repair bypassed admission: evidence=%+v err=%v", evidence, err)
			}
			if refused != nil && calls != 1 {
				t.Fatal("repair did not consult financial authority exactly once")
			}
			if evidence.Passed || m.LeasedCount() != 0 || m.LiveCount() != 0 || len(guest.coldBootSpecs) != 0 {
				t.Fatal("refused repair reached native execution or retained capacity")
			}
			path := filepath.Join(v.nativeRecovery.journal.root, "data-repair", frame.TaskID+".json")
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused repair claimed an immutable native attempt: %v", err)
			}
		})
	}
}
