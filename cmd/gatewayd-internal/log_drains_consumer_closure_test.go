package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type closureGatewayTestStore interface {
	inventoryGatewayTestStore
	state.ApplicationStandardLogConsumerClosureStore
	state.ApplicationStandardConsumerRosterStore
}

type closureGatewayRecorder struct {
	closureGatewayTestStore
	called  chan struct{}
	failure error
}

func (s *closureGatewayRecorder) CloseApplicationStandardLogConsumer(ctx context.Context, c state.ApplicationStandardLogConsumerSession) (state.ApplicationStandardLogConsumerClosure, error) {
	select {
	case s.called <- struct{}{}:
	default:
	}
	if s.failure != nil {
		return state.ApplicationStandardLogConsumerClosure{}, s.failure
	}
	return s.closureGatewayTestStore.CloseApplicationStandardLogConsumer(ctx, c)
}

func TestStandardLogConsumerClosureRunJoinsBeforeShutdown(t *testing.T) {
	standardLogConsumerClosureRun(t, state.NewMemStore(), nil)
}

func TestStandardLogConsumerClosureStorageFailureRemainsPending(t *testing.T) {
	standardLogConsumerClosureRun(t, state.NewMemStore(), errors.New("private storage configuration"))
}

func standardLogConsumerClosureRun(t *testing.T, base closureGatewayTestStore, failure error) {
	t.Helper()
	s := &closureGatewayRecorder{closureGatewayTestStore: base, called: make(chan struct{}, 4), failure: failure}
	f := newInventoryGatewayFixture(t, s)
	n, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "closing-gateway-" + uuid.NewString(), TargetURL: "unix:///tmp/closing-gateway", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	var release sync.Once
	q := &inventoryQuietLogs{gate: gate, entered: make(chan struct{}, 1)}
	m := newAppLogDrainManager(s, q, func([]byte) (string, error) { return "Authorization: Bearer company", nil }, nil, nil)
	m.spoolRoot = t.TempDir()
	m.standardNode = newLocalNodeID(s, n.Name)
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	go func() { m.Run(ctx); close(finished) }()
	t.Cleanup(func() {
		cancel()
		release.Do(func() { close(gate) })
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("manager failed to stop")
		}
	})
	select {
	case <-q.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("source worker did not start")
	}
	session := standardLogConsumerClosureWaitSession(t, s, f, n.ID)
	cancel()
	// Recv sees cancellation but deliberately stays alive behind gate.
	select {
	case <-s.called:
		t.Fatal("shutdown acknowledged before source worker joined")
	case <-time.After(30 * time.Millisecond):
	}
	if err := s.CheckApplicationStandardLogConsumer(t.Context(), session); err != nil {
		t.Fatal("worker exit was falsely acknowledged")
	}
	next := newAppLogDrainManager(s, nil, nil, nil, nil)
	next.spoolRoot = m.spoolRoot
	if err := next.acquireLogSpoolLease(); err == nil {
		_ = next.spoolLease.Unlock()
		t.Fatal("spool released while worker still exits")
	}
	release.Do(func() { close(gate) })
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not join worker")
	}
	select {
	case <-s.called:
	default:
		t.Fatal("Run failed to report joined shutdown")
	}
	currentErr := s.CheckApplicationStandardLogConsumer(t.Context(), session)
	if failure == nil && !errors.Is(currentErr, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("closed startup still qualifies: %v", currentErr)
	}
	if failure != nil && currentErr != nil {
		t.Fatal("failed storage call fabricated shutdown")
	}
	if err := next.acquireLogSpoolLease(); err != nil {
		t.Fatal(err)
	}
	defer next.spoolLease.Unlock()
	e, err := s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatal("gateway shutdown advanced application convergence")
	}
}

func standardLogConsumerClosureWaitSession(t *testing.T, s closureGatewayTestStore, f inventoryGatewayFixture, id string) state.ApplicationStandardLogConsumerSession {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.GetApplicationStandardConsumerRoster(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range r.Nodes {
			if node.NodeID == uuid.MustParse(id).String() && node.LoggingSession != nil {
				return *node.LoggingSession
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Run did not register logging startup")
	return state.ApplicationStandardLogConsumerSession{}
}
