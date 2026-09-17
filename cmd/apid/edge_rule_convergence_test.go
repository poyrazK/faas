package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type edgeRuleBarrierNotifier struct {
	mu        sync.Mutex
	events    chan db.Notification
	phases    []string
	published chan string
}

type edgeRuleLockTestStore struct {
	state.Store
	mu       sync.Mutex
	acquired []string
	released []string
}

func (s *edgeRuleLockTestStore) NextEdgeRuleGeneration(context.Context) (int64, error) {
	return 1, nil
}

func (s *edgeRuleLockTestStore) AcquireEdgeRuleMutationLock(_ context.Context, appID string) (func(), error) {
	s.mu.Lock()
	s.acquired = append(s.acquired, appID)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.released = append(s.released, appID)
		s.mu.Unlock()
	}, nil
}

func (n *edgeRuleBarrierNotifier) Notify(_ context.Context, channel, raw string) error {
	if channel != db.NotifyEdgeRuleChanged {
		return nil
	}
	var changed db.EdgeRuleChangedPayload
	if err := json.Unmarshal([]byte(raw), &changed); err != nil {
		return err
	}
	n.mu.Lock()
	n.phases = append(n.phases, changed.Phase)
	n.mu.Unlock()
	if n.published != nil {
		n.published <- changed.Phase
	}
	if changed.Phase == "apply" {
		ack, _ := json.Marshal(db.EdgeRuleAckPayload{Generation: changed.Generation, Phase: changed.Phase, Node: "node-a"})
		n.events <- db.Notification{Channel: db.NotifyEdgeRuleAck, Payload: string(ack)}
	}
	return nil
}

func TestEdgeRuleApplyWaitsForEveryServingGateway(t *testing.T) {
	notifier := &edgeRuleBarrierNotifier{
		events: make(chan db.Notification, 4), published: make(chan string, 1),
	}
	conv := &edgeRuleConvergence{
		notif: notifier, events: notifier.events, cancel: func() {}, unlock: func() {},
		generation: 42, appID: "app-1", operation: "updated",
		hosts:    []string{"api.example.com"},
		expected: map[string]struct{}{"node-a": {}, "node-b": {}},
	}
	result := make(chan error, 1)
	go func() { result <- conv.apply(context.Background(), "rule-1") }()

	if phase := <-notifier.published; phase != "apply" {
		t.Fatalf("published phase = %q, want apply", phase)
	}
	select {
	case err := <-result:
		t.Fatalf("barrier returned before node-b acknowledged: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	ack, _ := json.Marshal(db.EdgeRuleAckPayload{Generation: 42, Phase: "apply", Node: "node-b"})
	notifier.events <- db.Notification{Channel: db.NotifyEdgeRuleAck, Payload: string(ack)}
	if err := <-result; err != nil {
		t.Fatalf("apply after both acknowledgements: %v", err)
	}
}

func (n *edgeRuleBarrierNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return n.events, func() {}, nil
}

func (*edgeRuleBarrierNotifier) WaitFor(context.Context, string, func(string) bool, time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

func TestEdgeRuleApplySurvivesCanceledRequestContext(t *testing.T) {
	notifier := &edgeRuleBarrierNotifier{events: make(chan db.Notification, 1)}
	unlocked := false
	conv := &edgeRuleConvergence{
		notif: notifier, events: notifier.events, cancel: func() {},
		unlock: func() { unlocked = true }, generation: 41,
		appID: "app-1", operation: "updated", hosts: []string{"api.example.com"},
		expected: map[string]struct{}{"node-a": {}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := conv.apply(ctx, "rule-1"); err != nil {
		t.Fatalf("apply with canceled request context: %v", err)
	}
	if !unlocked {
		t.Fatal("convergence lock was not released")
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.phases) != 1 || notifier.phases[0] != "apply" {
		t.Fatalf("published phases = %v, want [apply]", notifier.phases)
	}
}

func TestEdgeRuleFleetRequirementDistinguishesSplitAndSingleBox(t *testing.T) {
	notifier := &edgeRuleBarrierNotifier{events: make(chan db.Notification, 2)}
	srv := newServer(state.NewMemStore(), slog.Default(), "example.com", notifier)
	srv.WithEdgeRuleFleetRequired(true)
	if _, err := srv.prepareEdgeRuleMutation(t.Context(), "app-1", "", "created", "api.example.com"); err == nil {
		t.Fatal("named fleet accepted a policy mutation with no serving gateways")
	}

	srv.WithEdgeRuleFleetRequired(false)
	conv, err := srv.prepareEdgeRuleMutation(t.Context(), "app-1", "", "created", "api.example.com")
	if err != nil {
		t.Fatalf("single-box mutation without compute registry: %v", err)
	}
	conv.abort(t.Context())
}

func TestPrepareEdgeRuleMutationHoldsDistributedLockUntilClose(t *testing.T) {
	store := &edgeRuleLockTestStore{Store: state.NewMemStore()}
	notifier := &edgeRuleBarrierNotifier{events: make(chan db.Notification, 2)}
	srv := newServer(store, slog.Default(), "example.com", notifier)
	srv.WithEdgeRuleFleetRequired(false)

	conv, err := srv.prepareEdgeRuleMutation(t.Context(), "app-1", "", "created", "api.example.com")
	if err != nil {
		t.Fatalf("prepare edge-rule mutation: %v", err)
	}
	store.mu.Lock()
	if got := store.acquired; len(got) != 1 || got[0] != "app-1" {
		t.Fatalf("acquired locks = %v, want [app-1]", got)
	}
	if len(store.released) != 0 {
		t.Fatalf("released locks before convergence close = %v", store.released)
	}
	store.mu.Unlock()

	conv.abort(t.Context())
	store.mu.Lock()
	defer store.mu.Unlock()
	if got := store.released; len(got) != 1 || got[0] != "app-1" {
		t.Fatalf("released locks = %v, want [app-1]", got)
	}
}
