package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardLogClosureTestStore = state.ContractTestStore

func TestMemApplicationStandardLogConsumerClosure(t *testing.T) {
	standardLogConsumerClosureLifecycle(t, state.NewMemStore())
}

func standardLogConsumerClosureLifecycle(t *testing.T, s standardLogClosureTestStore) {
	t.Helper()
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	i := state.StandardLogInventoryCurrent(t, s, f, 1)
	d := state.StandardLogDeliveryDrain(t, s, f)
	event := state.ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"}
	state.StandardLogInventoryWrite(t, s, c, i)
	standardLogHealthWrite(t, s, c, d, event)
	before := time.Now().Add(-time.Second)
	closure, err := s.CloseApplicationStandardLogConsumer(ctx, c)
	if err != nil || closure.ApplicationStandardLogConsumerSession != c || closure.StoppedAt.Before(before) || closure.StoppedAt.After(time.Now()) {
		t.Fatalf("closure: %+v %v", closure, err)
	}
	retry, err := s.CloseApplicationStandardLogConsumer(ctx, c)
	if err != nil || retry != closure {
		t.Fatalf("closure retry changed storage clock: %+v %v", retry, err)
	}
	if err := s.CheckApplicationStandardLogConsumer(ctx, c); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("closed reporter remained current: %v", err)
	}
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, i); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("closed inventory writer: %v", err)
	}
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, event); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("closed health writer: %v", err)
	}
	rows, err := s.ListApplicationStandardLogInventories(ctx, i.OrgID, i.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("closed reporter retained inventory evidence")
	}
	standardLogHealthRead(t, s, f, 0, "")
	if _, err := s.RegisterApplicationStandardLogConsumer(ctx, c.NodeID, c.SessionID); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("closed startup reactivated: %v", err)
	}
	standardLogConsumerClosureRestart(t, s, f, c, closure)
}

func standardLogConsumerClosureRestart(t *testing.T, s standardLogClosureTestStore, f state.StandardLocalIntentFixture, c state.ApplicationStandardLogConsumerSession, closure state.ApplicationStandardLogConsumerClosure) {
	t.Helper()
	ctx := t.Context()
	if err := s.SetComputeNodeActive(ctx, c.NodeID, false); err != nil {
		t.Fatal(err)
	}
	retry, err := s.CloseApplicationStandardLogConsumer(ctx, c)
	if err != nil || retry != closure {
		t.Fatalf("inactive current session could not acknowledge shutdown: %+v %v", retry, err)
	}
	if err := s.SetComputeNodeActive(ctx, c.NodeID, true); err != nil {
		t.Fatal(err)
	}
	next := state.StandardLogInventorySession(t, s, c.NodeID)
	if next.Generation != c.Generation+1 {
		t.Fatal("restart did not advance generation")
	}
	if _, err := s.CloseApplicationStandardLogConsumer(ctx, c); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("late old shutdown closed replacement: %v", err)
	}
	if err := s.CheckApplicationStandardLogConsumer(ctx, next); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetApplicationStandardConsumerRoster(ctx, (*f.Owner()).PersonalOrg.ID, (*f.App()).ID)
	if err != nil {
		t.Fatal(err)
	}
	node := state.StandardRosterNode(t, r, c.NodeID)
	if node.LoggingSession == nil || *node.LoggingSession != next || node.LoggingStoppedAt != nil {
		t.Fatal("replacement inherited closure")
	}
	if err := s.SetComputeNodeActive(ctx, c.NodeID, false); err != nil {
		t.Fatal(err)
	}
	stopped, err := s.CloseApplicationStandardLogConsumer(ctx, next)
	if err != nil || stopped.StoppedAt.IsZero() {
		t.Fatalf("first shutdown on draining node failed: %v", err)
	}
	if err := s.DeleteComputeNode(ctx, c.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloseApplicationStandardLogConsumer(ctx, next); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatal("erased node acknowledged shutdown")
	}
}

func TestMemApplicationStandardLogConsumerClosureCancellationAndOwnership(t *testing.T) {
	m := state.NewMemStore()
	c := state.StandardLogInventorySession(t, m, state.StandardLogInventoryNode(t, m).ID)
	ctx, cancel := context.WithCancel(t.Context())
	entered, finished := make(chan struct{}), make(chan error, 1)
	(*m.Mu()).Lock()
	go func() { close(entered); _, err := m.CloseApplicationStandardLogConsumer(ctx, c); finished <- err }()
	<-entered
	cancel()
	(*m.Mu()).Unlock()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("canceled mutex wait closed reporter")
	}
	if err := m.CheckApplicationStandardLogConsumer(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	bad := c
	bad.SessionID = uuid.NewString()
	if _, err := m.CloseApplicationStandardLogConsumer(t.Context(), bad); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatal("unowned session closed reporter")
	}
	if _, err := m.CloseApplicationStandardLogConsumer(t.Context(), state.ApplicationStandardLogConsumerSession{}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("invalid closure input")
	}
	if _, err := m.CloseApplicationStandardLogConsumer(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteComputeNode(t.Context(), c.NodeID); err != nil {
		t.Fatal(err)
	}
	if len((*m.ApplicationStandardLogConsumerClosures())) != 0 {
		t.Fatal("erased node retained private closure")
	}
}

func TestMemApplicationStandardLogConsumerClosureConcurrentStopAndReads(t *testing.T) {
	m := state.NewMemStore()
	f := state.NewStandardLocalIntentFixture(t.Context(), t, m)
	c := state.StandardLogInventorySession(t, m, state.StandardLogInventoryNode(t, m).ID)
	var joined sync.WaitGroup
	closed := make(chan state.ApplicationStandardLogConsumerClosure, 8)
	for n := 0; n < 8; n++ {
		joined.Add(1)
		go func() {
			defer joined.Done()
			o, err := m.CloseApplicationStandardLogConsumer(t.Context(), c)
			if err != nil {
				t.Error(err)
				return
			}
			closed <- o
			for range 10 {
				r, err := m.GetApplicationStandardConsumerRoster(t.Context(), (*f.Owner()).PersonalOrg.ID, (*f.App()).ID)
				if err != nil {
					t.Error(err)
					return
				}
				node := state.StandardRosterNode(t, r, c.NodeID)
				if node.LoggingSession == nil || *node.LoggingSession != c || node.LoggingStoppedAt == nil || !node.LoggingStoppedAt.Equal(o.StoppedAt) {
					t.Error("torn session/closure snapshot")
				}
			}
		}()
	}
	joined.Wait()
	close(closed)
	var first state.ApplicationStandardLogConsumerClosure
	for o := range closed {
		if first.StoppedAt.IsZero() {
			first = o
		}
		if first != o {
			t.Fatal("concurrent shutdown retry changed event clock")
		}
	}
}
