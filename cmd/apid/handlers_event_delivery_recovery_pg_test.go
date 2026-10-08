//go:build !no_pg

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sched" //nolint:depguard // ADR-607 acceptance drives the scheduler; production apid only records intent.
	"github.com/onebox-faas/faas/pkg/state"
)

// Public publish/receipt/replay handlers, real fanout and drain, PostgreSQL,
// and the production HTTP synth server/client run together. Running instances
// and an HTTP consumer replace VM/guest execution; this is not metal acceptance.
func TestEventDeliveryRecoveryPostgresHTTP(t *testing.T) {
	for _, recoveryKind := range []string{"dead_letter_replay", "handler_replay"} {
		t.Run(recoveryKind, func(t *testing.T) { eventDeliveryRecoveryPostgresHTTP(t, recoveryKind) })
	}
}

type eventDeliverySettlementLossStore struct {
	*state.PgStore
	target string
	lost   atomic.Bool
	claim  state.InvocationClaim
}

func (s *eventDeliverySettlementLossStore) CompleteInvocationClaim(ctx context.Context, id string, claim state.InvocationClaim, result json.RawMessage) error {
	if id == s.target && s.lost.CompareAndSwap(false, true) {
		s.claim = claim
		return errors.New("simulated scheduler exit after handler response before settlement")
	}
	return s.PgStore.CompleteInvocationClaim(ctx, id, claim, result)
}

type eventDeliveryHTTPFixture struct {
	t              *testing.T
	account        string
	roles          map[string]string
	apps           map[string]string
	recoveryKind   string
	poisonResolved atomic.Bool
	mu             sync.Mutex
	calls          map[string]int
	effects        map[string]int
	seen           map[string]bool
}

func (f *eventDeliveryHTTPFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subscription := r.Header.Get("x-gregale-event-subscription-id")
	role, ok := f.roles[subscription]
	appID := f.apps[subscription]
	var envelope events.Envelope
	if !ok || json.NewDecoder(r.Body).Decode(&envelope) != nil || envelope.ID != "delivery-recovery" || envelope.Source != "orders" || envelope.AccountID != f.account ||
		r.Method != http.MethodPost || r.URL.Path != "/" || r.Header.Get(api.InvocationIDHeader) == "" {
		f.t.Errorf("unexpected HTTP consumer delivery: %s %s envelope=%+v", r.Method, r.URL, envelope)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls[role]++
	call := f.calls[role]
	f.mu.Unlock()
	status := http.StatusOK
	result := map[string]any{"ok": true}
	if role == "retry" && call == 1 || role == "poison" && !f.poisonResolved.Load() {
		status = http.StatusServiceUnavailable
		result["ok"] = false
		if role == "poison" && f.recoveryKind == "handler_replay" {
			status = http.StatusInternalServerError
			result["error"] = "handler_error" // runner-caught exception, requiring operator replay
		}
	}
	if status == http.StatusOK {
		// The test consumer deduplicates logical event identity, even when a
		// scheduler dies after the side effect and before durable settlement.
		key := appID + ":" + envelope.Source + ":" + envelope.ID
		f.mu.Lock()
		if !f.seen[key] {
			f.seen[key] = true
			f.effects[role]++
		}
		f.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

// Reuse the existing local VM HTTP bridge, including durable request reload
// and selected-instance checking. A runner exception is terminal evidence.
type eventDeliveryRecoveryDispatcher struct {
	*operationsAcceptanceDispatcher
}

func (d *eventDeliveryRecoveryDispatcher) InvokeWithTargetStatus(ctx context.Context, app string, inv state.Invocation, target gateway.Target) (state.Invocation, int, error) {
	out, status, err := d.operationsAcceptanceDispatcher.InvokeWithTargetStatus(ctx, app, inv, target)
	var result struct {
		Error string `json:"error"`
	}
	if err == nil && status == http.StatusInternalServerError && json.Unmarshal(out.Result, &result) == nil && result.Error == "handler_error" {
		out.State = state.InvocationFailed
	}
	return out, status, err
}

func eventDeliveryRecoveryPostgresHTTP(t *testing.T, recoveryKind string) {
	e := setupPGHandler(t, api.PlanHobby)
	store := e.store.(*state.PgStore)
	ctx := t.Context()
	fixture := &eventDeliveryHTTPFixture{t: t, account: e.acct.ID, roles: map[string]string{}, apps: map[string]string{}, recoveryKind: recoveryKind,
		calls: map[string]int{}, effects: map[string]int{}, seen: map[string]bool{}}
	apps, subscriptions := seedEventDeliveryRecoveryConsumers(t, store, e.acct.ID, fixture)
	consumer := httptest.NewServer(fixture)
	t.Cleanup(consumer.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := &eventDeliveryRecoveryDispatcher{&operationsAcceptanceDispatcher{store: store, address: strings.TrimPrefix(consumer.URL, "http://")}}
	endpoint := httptest.NewServer(gateway.NewSynthServer("", dispatcher, log).Mux())
	t.Cleanup(endpoint.Close)
	gateway, err := sched.DialGatewaySynthTarget("tcp://"+strings.TrimPrefix(endpoint.URL, "http://"), nil, log)
	if err != nil {
		t.Fatal(err)
	}
	published := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: "delivery-recovery", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}, nil)
	if published.Code != http.StatusAccepted {
		t.Fatalf("publish: %d %s", published.Code, published.Body)
	}
	engine, err := sched.NewEngine(ctx, store, sched.NewNodeLedger(), nil, nil, "1.10.0", log)
	if err != nil {
		t.Fatal(err)
	}
	stop := startEventDeliveryRecoveryScheduler(t, e, engine, log, true)
	waitEventRecoveryCondition(t, func() bool {
		receipt := readEventDeliveryRecoveryReceipt(t, e)
		return receipt.RoutingMode == "recipient" && receipt.RoutingSummary["enqueued"] == 3 && receipt.RoutingSettledAt != nil
	})
	// Capture original deterministic IDs; the healthy consumer never receives
	// a new invocation or another handler call during anyone else's recovery.
	ids := make([]string, len(apps))
	for i, app := range apps {
		rows, err := store.ListInvocationsForApp(ctx, app.ID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("routing admission: rows=%+v err=%v", rows, err)
		}
		ids[i] = rows[0].ID
	}
	loss := &eventDeliverySettlementLossStore{PgStore: store, target: ids[1]}
	drain := sched.NewDrain(loss, engine, sched.WithDrainGatewaySynth(gateway), sched.WithDrainWakeLease(1), sched.WithDrainRetryAfter(1), sched.WithDrainLogger(log))
	drain.Tick(ctx)
	initial := readEventDeliveryRecoveryReceipt(t, e)
	assertEventRecoveryConsumerState(t, initial, subscriptions[0].ID, "completed", 1)
	assertEventRecoveryConsumerState(t, initial, subscriptions[1].ID, "pending", 1)
	waitEventRecoveryCondition(t, func() bool { drain.Tick(ctx); return loss.lost.Load() })
	// A new store/engine/drain must reclaim the abandoned attempt. Its
	// history says unknown; the handler's deduplication absorbs the repeat.
	stop()
	store = state.NewPgStore(e.pool)
	engine, err = sched.NewEngine(ctx, store, sched.NewNodeLedger(), nil, nil, "1.10.0", log)
	if err != nil {
		t.Fatal(err)
	}
	startEventDeliveryRecoveryScheduler(t, e, engine, log, false)
	drain = sched.NewDrain(store, engine, sched.WithDrainGatewaySynth(gateway), sched.WithDrainWakeLease(1), sched.WithDrainRetryAfter(1), sched.WithDrainLogger(log))
	waitEventRecoveryCondition(t, func() bool {
		drain.Tick(ctx)
		retry, err := store.InvocationByID(ctx, ids[1])
		if err != nil {
			t.Fatal(err)
		}
		poison, err := store.InvocationByID(ctx, ids[2])
		if err != nil {
			t.Fatal(err)
		}
		return retry.State == state.InvocationCompleted && (poison.State == state.InvocationFailed || poison.State == state.InvocationDeadLetter)
	})
	if err := store.CompleteInvocationClaim(ctx, ids[1], loss.claim, json.RawMessage(`{}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old scheduler settled a reclaimed attempt: %v", err)
	}
	attempts := readEventDeliveryRecoveryAttempts(t, e, subscriptions[1].ID)
	assertEventRecoveryOutcomes(t, attempts, map[string]int{"retry": 1, "unknown": 1, "succeeded": 1})
	before := readEventDeliveryRecoveryReceipt(t, e)
	var action api.EventReceiptRecoveryAction
	for _, recipient := range before.Recipients {
		if recipient.SubscriptionID == subscriptions[2].ID {
			action = recipient.RecoveryActions[0]
		}
	}
	if action.Kind != recoveryKind {
		t.Fatalf("recovery action=%+v want=%s", action, recoveryKind)
	}
	poisonAttempts := api.MustLimitsFor(api.PlanHobby).MaxQueueAttempts
	if recoveryKind == "handler_replay" {
		poisonAttempts = 1
	}
	poisonOutcomes := map[string]int{"failed": 1}
	if recoveryKind == "dead_letter_replay" {
		poisonOutcomes = map[string]int{"retry": poisonAttempts - 1, "dead_letter": 1}
	}
	assertEventRecoveryOutcomes(t, readEventDeliveryRecoveryAttempts(t, e, subscriptions[2].ID), poisonOutcomes)
	fixture.poisonResolved.Store(true)
	replayed := e.do(t, action.Method, action.URL, action.Body, nil)
	if replayed.Code != http.StatusAccepted && replayed.Code != http.StatusOK {
		t.Fatalf("selective recovery: %d %s", replayed.Code, replayed.Body)
	}
	if recoveryKind == "handler_replay" {
		duplicate := e.do(t, action.Method, action.URL, action.Body, nil)
		var first, second api.AsyncInvokeResponse
		if json.Unmarshal(replayed.Body.Bytes(), &first) != nil || json.Unmarshal(duplicate.Body.Bytes(), &second) != nil || first.ID == "" || first.ID != second.ID {
			t.Fatalf("duplicate plain recovery: first=%s second=%s", replayed.Body, duplicate.Body)
		}
	}
	drain.Tick(ctx)
	after := readEventDeliveryRecoveryReceipt(t, e)
	assertEventRecoveryConsumerState(t, after, subscriptions[0].ID, "completed", 1)
	assertEventRecoveryConsumerState(t, after, subscriptions[1].ID, "completed", 3)
	assertEventRecoveredPoison(t, after, subscriptions[2].ID, recoveryKind, ids[2])
	poisonHistory := readEventDeliveryRecoveryAttempts(t, e, subscriptions[2].ID)
	poisonOutcomes["succeeded"] = 1
	assertEventRecoveryOutcomes(t, poisonHistory, poisonOutcomes)
	assertEventRecoveryReplayEvidence(t, e, store, apps[2], subscriptions[2].ID, ids[2], recoveryKind, poisonHistory)
	duplicate := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: "delivery-recovery", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}, nil)
	if duplicate.Code != http.StatusAccepted {
		t.Fatalf("duplicate publication: %d %s", duplicate.Code, duplicate.Body)
	}
	drain.Tick(ctx)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, role := range []string{"healthy", "retry", "poison"} {
		wantCalls := 1
		if role == "retry" {
			wantCalls = 3
		} else if role == "poison" {
			wantCalls = poisonAttempts + 1
		}
		if fixture.calls[role] != wantCalls || fixture.effects[role] != 1 {
			t.Errorf("%s handler calls=%d want=%d effects=%d", role, fixture.calls[role], wantCalls, fixture.effects[role])
		}
	}
	for i, app := range apps {
		rows, err := store.ListInvocationsForApp(ctx, app.ID)
		want := 1
		if i == 2 && recoveryKind == "handler_replay" {
			want = 2
		}
		if err != nil || len(rows) != want {
			t.Errorf("%s invocation count=%d want=%d err=%v", app.Slug, len(rows), want, err)
		}
	}
}

func startEventDeliveryRecoveryScheduler(t *testing.T, e pgHandlerEnv, engine *sched.Engine, log *slog.Logger, adopt bool) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sched.NewLoop(e.pool, engine, log).WithEventRecipientClaims(adopt).Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("fanout loop: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("fanout loop did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func seedEventDeliveryRecoveryConsumers(t *testing.T, store *state.PgStore, accountID string, fixture *eventDeliveryHTTPFixture) ([]state.App, []state.EventSubscription) {
	t.Helper()
	ctx := t.Context()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	var subscriptions []state.EventSubscription
	for _, role := range []string{"healthy", "retry", "poison"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "delivery-" + uuid.NewString()[:8] + "-" + role, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), app.RAMMB, node.ID, ""); err != nil {
			t.Fatal(err)
		}
		subscription, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		apps, subscriptions = append(apps, app), append(subscriptions, subscription)
		fixture.roles[subscription.ID], fixture.apps[subscription.ID] = role, app.ID
	}
	return apps, subscriptions
}

func waitEventRecoveryCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("delivery recovery condition did not become true")
		case <-tick.C:
		}
	}
}

func readEventDeliveryRecoveryReceipt(t *testing.T, e pgHandlerEnv) api.EventReceiptResponse {
	t.Helper()
	rec := e.do(t, http.MethodGet, eventReceiptURL("orders", "delivery-recovery"), nil, nil)
	var receipt api.EventReceiptResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &receipt) != nil || len(receipt.Recipients) != 3 {
		t.Fatalf("receipt: %d %s", rec.Code, rec.Body)
	}
	return receipt
}

func readEventDeliveryRecoveryAttempts(t *testing.T, e pgHandlerEnv, subscription string) []api.InvocationAttemptResponse {
	t.Helper()
	rec := e.do(t, http.MethodGet, eventReceiptAttemptURL("orders", "delivery-recovery", subscription), nil, nil)
	var history api.EventReceiptAttemptHistoryResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &history) != nil || history.Coverage != "recorded_attempts_only" {
		t.Fatalf("attempt history: %d %s", rec.Code, rec.Body)
	}
	return history.Attempts
}

func assertEventRecoveryConsumerState(t *testing.T, receipt api.EventReceiptResponse, subscription, status string, attempts int) {
	t.Helper()
	for _, recipient := range receipt.Recipients {
		if recipient.SubscriptionID == subscription {
			if recipient.Execution == nil || recipient.Execution.State != status || recipient.Execution.Attempts != attempts || len(recipient.RecoveryActions) != 0 {
				t.Fatalf("consumer %s: %+v want=%s attempts=%d", subscription, recipient, status, attempts)
			}
			return
		}
	}
	t.Fatalf("consumer %s missing", subscription)
}

func assertEventRecoveryOutcomes(t *testing.T, attempts []api.InvocationAttemptResponse, want map[string]int) {
	t.Helper()
	got := map[string]int{}
	for _, attempt := range attempts {
		got[attempt.Outcome]++
	}
	if len(got) != len(want) {
		t.Fatalf("attempt outcomes=%v want=%v", got, want)
	}
	for outcome, count := range want {
		if got[outcome] != count {
			t.Fatalf("attempt outcomes=%v want=%v", got, want)
		}
	}
}

func assertEventRecoveredPoison(t *testing.T, receipt api.EventReceiptResponse, subscription, kind, originalID string) {
	t.Helper()
	for _, recipient := range receipt.Recipients {
		if recipient.SubscriptionID != subscription {
			continue
		}
		if len(recipient.RecoveryActions) != 0 || recipient.Execution == nil || recipient.Execution.InvocationID != originalID {
			t.Fatalf("recovered receipt: %+v", recipient)
		}
		if kind == "dead_letter_replay" {
			if recipient.Execution.State != "completed" || recipient.Execution.ReplayGeneration != 1 || recipient.Execution.Attempts != 1 {
				t.Fatalf("in-place replay: %+v", recipient)
			}
		} else if recipient.Execution.State != "failed" || recipient.Recovery == nil || recipient.Recovery.LatestReplay.State != "completed" || recipient.Recovery.LatestReplay.InvocationID == originalID {
			t.Fatalf("plain replay: %+v", recipient)
		}
		return
	}
	t.Fatalf("recovered consumer missing: %s", subscription)
}

func assertEventRecoveryReplayEvidence(t *testing.T, e pgHandlerEnv, store *state.PgStore, app state.App, subscription, originalID, kind string, attempts []api.InvocationAttemptResponse) {
	t.Helper()
	if kind == "dead_letter_replay" {
		deadLetters, err := store.ListDeadLetterEvents(t.Context(), app.ID, 10, "")
		if err != nil || len(deadLetters) != 1 || deadLetters[0].ReplayedAt == nil || deadLetters[0].SourceID != originalID {
			t.Fatalf("DLQ audit=%+v err=%v", deadLetters, err)
		}
		for _, attempt := range attempts {
			if attempt.Outcome == "succeeded" && (attempt.InvocationID != originalID || attempt.ReplayGeneration != 1) {
				t.Fatalf("DLQ replay attempt identity: %+v", attempt)
			}
		}
		return
	}
	rec := e.do(t, http.MethodGet, eventReceiptReplayURL("orders", "delivery-recovery", subscription), nil, nil)
	var history api.EventReceiptReplayHistoryResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &history) != nil || len(history.Replays) != 1 || history.Replays[0].ReplayedFromInvocationID != originalID || history.Replays[0].State != "completed" {
		t.Fatalf("plain replay lineage: %d %s", rec.Code, rec.Body)
	}
}
