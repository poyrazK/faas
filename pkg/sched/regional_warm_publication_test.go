package sched

// adr: 755
// adr: 791
import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type regionalWarmPublicationStore struct {
	state.Store
	current     state.Instance
	publishErr  error
	publishHook func()
	fence       *state.RuntimeAppConfigFence
}

func (s regionalWarmPublicationStore) PublishOwnedInstanceRuntime(context.Context, state.RuntimeInstancePublication) (state.Instance, error) {
	if s.publishHook != nil {
		s.publishHook()
	}
	return s.current, s.publishErr
}

func (s regionalWarmPublicationStore) InstanceByID(context.Context, string) (state.Instance, error) {
	return s.current, nil
}

func (s regionalWarmPublicationStore) InstanceRuntimeConfigFence(ctx context.Context, accountID, appID, instanceID string) (state.RuntimeAppConfigFence, error) {
	if s.fence != nil {
		return *s.fence, nil
	}
	return s.Store.InstanceRuntimeConfigFence(ctx, accountID, appID, instanceID)
}

// These are publication protocol simulations; no admission head is activated.
func TestRegionalWarmPublicationRecoversOnlySameResident(t *testing.T) {
	for _, outcome := range []string{"committed", "peer-committed", "store-error", "still-warm", "different-run", "different-node", "different-runtime", "different-config", "fenced-during-publish"} {
		t.Run(outcome, func(t *testing.T) {
			f := newRegionalWarmFixture(t)
			p := f.prepare(t)
			current := f.warm
			current.State = string(state.StateRunning)
			s := regionalWarmPublicationStore{Store: f.store, current: current, publishErr: state.ErrConflict}
			switch outcome {
			case "committed":
				s.publishErr = nil
			case "store-error":
				s.publishErr = errors.New("publication unavailable")
			case "still-warm":
				s.current.State = string(state.StateWarm)
			case "different-run":
				s.current.WakeID = uuid.NewString()
			case "different-node":
				s.current.NodeID = uuid.NewString()
			case "different-runtime":
				s.current.GuestUID++
			case "different-config":
				fence := regionalWarmCapturedFence(t, f)
				fence.Fingerprint = "different configuration"
				s.fence = &fence
			case "fenced-during-publish":
				s.publishHook = func() {
					if _, err := f.store.FenceRegionalRuntimeServingSeat(t.Context(), f.app.ID, p.Seat.ID, uuid.NewString()); err != nil {
						t.Fatal(err)
					}
				}
			}
			f.engine.store = s
			_, committed, err := f.engine.publishRegionalWarmRuntime(t.Context(), f.acct.ID, f.warm, p, regionalWarmCapturedFence(t, f))
			ok := outcome == "committed" || outcome == "peer-committed"
			if (err == nil) != ok || committed != (outcome == "committed") {
				t.Fatal("publication recovery crossed resident identity", committed, err)
			}
			if f.engine.ledger.Concurrency(f.app.ID) != 1 || f.vmm.destroys != 0 {
				t.Fatal("publication conflict freed uncertain capacity")
			}
		})
	}
	f := newRegionalWarmFixture(t)
	p := f.prepare(t)
	p.Permit.RunID = uuid.NewString()
	if _, _, err := f.engine.publishRegionalWarmRuntime(t.Context(), f.acct.ID, f.warm, p, regionalWarmCapturedFence(t, f)); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("publication accepted unbound placement", err)
	}
}

func regionalWarmCapturedFence(t *testing.T, f regionalWarmFixture) state.RuntimeAppConfigFence {
	t.Helper()
	fence, err := f.store.InstanceRuntimeConfigFence(t.Context(), f.acct.ID, f.app.ID, f.warm.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fence
}
