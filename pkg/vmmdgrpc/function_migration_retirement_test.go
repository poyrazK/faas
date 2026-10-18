package vmmdgrpc

// adr: 20261018030615
// Current rollback authority permits only pinned physical retirement.

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/peer"
)

func TestFunctionMigrationRefusedRecoveryRetirement(t *testing.T) {
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("qualification-only physical retirement")
	}
	for _, scenario := range []string{"missing fence", "wrong wake", "unauthenticated", "cleanup failure", "confirmed"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, v, source := functionMigrationHandlerFixture(t, true)
			ctx := peer.NewContext(t.Context(), &peer.Peer{Addr: &net.UnixAddr{Name: "/tmp/schedd-retirement.sock", Net: "unix"}})
			if scenario != "missing fence" {
				if _, err := store.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "wrong wake" {
				source.SourceWakeID = uuid.NewString()
			}
			if scenario == "unauthenticated" {
				ctx = t.Context()
			}
			if scenario == "cleanup failure" {
				v.cleanupErr = io.EOF
			}
			reply, err := s.retireRefusedFunctionSource(ctx, source, v)
			if scenario == "confirmed" {
				if err != nil || !reply.GetSourceRetired() || v.retirements != 1 || v.recoveries != 0 {
					t.Fatal("retirement resumed resources or omitted its physical result", err)
				}
			} else if err == nil || reply != nil {
				t.Fatal("uncertain retirement returned confirmation")
			}
			if scenario == "missing fence" || scenario == "wrong wake" || scenario == "unauthenticated" {
				if v.retirements != 0 {
					t.Fatal("retirement bypassed source authority")
				}
			}
			ins, err := store.MigrationInstanceByID(t.Context(), source.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			expectedState := state.StateMigrating
			if scenario == "missing fence" {
				expectedState = state.StateRunning
			}
			if ins.State != string(expectedState) {
				t.Fatal("vmmd wrote instance completion")
			}
		})
	}
}
func TestFunctionMigrationTransientRecoveryFailureRetainsSource(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, runtimeadmission.ErrUnavailable, state.ErrConflict, io.EOF} {
		if refusedFunctionSourceRecovery(err) {
			t.Fatal("transient failure authorized retirement", err)
		}
	}
	for _, err := range []error{runtimeadmission.ErrStale, runtimeadmission.ErrExpired, state.ErrApplicationStandardRuntimeStale} {
		if !refusedFunctionSourceRecovery(errors.Join(io.EOF, err)) {
			t.Fatal("refused permission could not retire its pinned owner", err)
		}
	}
}
