package sched

// adr: 627
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
}

func (s regionalWarmPublicationStore) PublishInstanceRuntime(context.Context, string, string, string, string, int) (state.Instance, error) {
	if s.publishHook != nil {
		s.publishHook()
	}
	return s.current, s.publishErr
}

func (s regionalWarmPublicationStore) InstanceByID(context.Context, string) (state.Instance, error) {
	return s.current, nil
}

// These are publication protocol simulations; no admission head is activated.
func TestRegionalWarmPublicationRecoversOnlySameResident(t *testing.T) {
	for _, outcome := range []string{"committed", "peer-committed", "store-error", "still-warm", "different-run", "different-node", "different-runtime", "fenced-during-publish"} {
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
			case "fenced-during-publish":
				s.publishHook = func() {
					if _, err := f.store.FenceRegionalRuntimeServingSeat(t.Context(), f.app.ID, p.Seat.ID, uuid.NewString()); err != nil {
						t.Fatal(err)
					}
				}
			}
			f.engine.store = s
			_, committed, err := f.engine.publishRegionalWarmRuntime(t.Context(), f.warm, p)
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
	if _, _, err := f.engine.publishRegionalWarmRuntime(t.Context(), f.warm, p); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("publication accepted unbound placement", err)
	}
}
