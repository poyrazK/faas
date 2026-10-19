// adr: 1151 — a wake's bookkeeping commits with its RUNNING publication or
// not at all.
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimePublicationBookkeeping(t *testing.T) {
	testRuntimePublicationBookkeeping(t, state.NewMemStore())
}

func transitionBookkeeping() *state.RuntimePublicationBookkeeping {
	return &state.RuntimePublicationBookkeeping{EventKind: "frozen_thaw", EventData: []byte(`{"from":"frozen","to":"running"}`)}
}

func testRuntimePublicationBookkeeping(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	node := runtimeSecretNodeForTest(t, store)
	dep := f.deployments["production"]
	eventsFor := func(t *testing.T, instanceID string) []state.Event {
		t.Helper()
		events, err := store.ListEvents(t.Context(), instanceID, 10)
		if err != nil {
			t.Fatal(err)
		}
		return events
	}

	refused := seedRuntimeInstancePublication(t, store, f, dep, node)
	refused.WakeID = uuid.NewString()
	refused.Bookkeeping = transitionBookkeeping()
	if _, err := store.PublishOwnedInstanceRuntime(ctx, refused); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("publication with another wake id = %v, want ErrConflict", err)
	}
	if events := eventsFor(t, refused.InstanceID); len(events) != 0 {
		t.Fatalf("a refused publication recorded %d events", len(events))
	}

	invalid := seedRuntimeInstancePublication(t, store, f, dep, node)
	invalid.Bookkeeping = &state.RuntimePublicationBookkeeping{EventKind: "frozen_thaw", EventData: []byte(`{`)}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("event data the database would refuse = %v, want ErrInvalidArgument", err)
	}
	assertRuntimePublicationUnchanged(ctx, t, store, invalid)

	for _, tc := range []struct {
		name       string
		missBefore bool
	}{
		{name: "no snapshot miss recorded"},
		{name: "snapshot miss recorded", missBefore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.missBefore {
				if err := store.DeploymentRecordSnapshotMiss(t.Context(), dep.ID, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			p := seedRuntimeInstancePublication(t, store, f, dep, node)
			p.Bookkeeping = transitionBookkeeping()
			if _, err := store.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
				t.Fatalf("publish: %v", err)
			}
			if p.Bookkeeping.SnapshotBackoffRecorded != tc.missBefore {
				t.Fatalf("snapshot backoff recorded = %v, want %v", p.Bookkeeping.SnapshotBackoffRecorded, tc.missBefore)
			}
			events := eventsFor(t, p.InstanceID)
			if len(events) != 1 || events[0].Kind != "frozen_thaw" || events[0].Actor != "schedd" {
				t.Fatalf("events for the published instance = %+v, want one schedd frozen_thaw", events)
			}
		})
	}
}
