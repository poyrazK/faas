// adr: 1237 — realtime channels as function triggers.
package sched

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type realtimeTriggerFixture struct {
	pool    *pgxpool.Pool
	store   *state.PgStore
	account state.Account
	appID   string
	cfg     api.RealtimeTriggerConfig
	trigger sqlc.Trigger
	groupID string
}

// seedRealtimeTrigger creates an endpoint with n retained messages, a group
// with the given lease, and a realtime trigger bound to it.
func seedRealtimeTrigger(t *testing.T, n, leaseSeconds int) realtimeTriggerFixture {
	t.Helper()
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "realtime-trigger@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "orders-fn", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		AccountID: account.ID, AppID: app.ID, CallbackURL: "https://example.com/realtime", ConnectPath: "/c",
		MessagePath: "/m", DisconnectPath: "/d", CallbackAuthTokenSealed: []byte("sealed"), AuthTokenSealed: []byte("sealed"), Enabled: true,
	}, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		if _, err := store.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "orders", []byte("order-"+strconv.Itoa(i)), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	group, err := store.CreateRealtimeConsumerGroup(ctx, endpoint.ID, "orders", "billing",
		state.RealtimeConsumerGroupConfig{AfterSequence: "0", LeaseSeconds: leaseSeconds, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.RealtimeTriggerConfig{EndpointID: endpoint.ID, Channel: "orders", Group: "billing"}
	raw, _ := json.Marshal(cfg)
	limits, _ := api.LimitsFor(api.PlanPro)
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, string(api.TriggerKindRealtime), "orders", true, raw, "",
		1, 1000, 1, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
	if err != nil {
		t.Fatal(err)
	}
	return realtimeTriggerFixture{pool: pool, store: store, account: account, appID: app.ID, cfg: cfg, trigger: trigger, groupID: group.GroupID}
}

func (f realtimeTriggerFixture) message(t *testing.T, sequence int) (status string, attempts int, failure string) {
	t.Helper()
	err := f.pool.QueryRow(t.Context(), `select status,attempts,failure_code from managed_realtime_consumer_group_messages
 where group_id=$1 and sequence=$2`, f.groupID, sequence).Scan(&status, &attempts, &failure)
	if err != nil {
		return "absent", 0, ""
	}
	return status, attempts, failure
}

func (f realtimeTriggerFixture) ledgerStates(t *testing.T) map[string]int {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `select state,count(*) from trigger_records where trigger_id=$1 group by state`, f.trigger.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		out[s] = n
	}
	return out
}

func TestRealtimeTriggerDispatchSettlesConsumerGroup(t *testing.T) {
	f := seedRealtimeTrigger(t, 3, api.RealtimeTriggerMinLeaseSeconds)
	ctx := t.Context()
	if err := f.store.BindRealtimeConsumerGroupTrigger(ctx, f.account.ID, f.trigger.ID.String(), f.cfg); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// One gateway outcome per tick: success, handler failure, platform failure.
	outcomes := []triggerDispatchResult{{Status: "succeeded"}, {Status: "retry", Code: "function_failed"}, {Status: "retry", Code: "invoke_error"}}
	var bodies []realtimeTriggerRecord
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope triggerDispatchRequest
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil || len(envelope.Records) != 1 || len(outcomes) == 0 {
			t.Errorf("envelope: %+v %v", envelope, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body realtimeTriggerRecord
		payload, _ := base64.StdEncoding.DecodeString(envelope.Records[0].PayloadB64)
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("record body: %v", err)
		}
		bodies = append(bodies, body)
		result := outcomes[0]
		outcomes = outcomes[1:]
		result.ItemIdentifier = envelope.Records[0].ItemIdentifier
		_ = json.NewEncoder(w).Encode(triggerDispatchResponse{Results: []triggerDispatchResult{result}})
	}))
	defer gateway.Close()
	loop := makeLoopForDLQ()
	loop.gatewayHTTPClient, loop.gatewayBaseURL = gateway.Client(), gateway.URL
	poller, err := newRealtimePoller(f.store, f.trigger)
	if err != nil {
		t.Fatal(err)
	}
	loop.triggerPollers = map[string]triggerSource{f.trigger.ID.String(): poller}
	tick := func() {
		t.Helper()
		_ = loop.dispatchOneTrigger(ctx, f.trigger, f.store, func(string) api.Plan { return api.PlanPro })
	}
	// Tick 1: success completes the group message (its row is removed).
	tick()
	if len(bodies) != 1 || bodies[0].Sequence != "1" || bodies[0].Channel != "orders" || bodies[0].Group != "billing" || bodies[0].Attempt != 1 {
		t.Fatalf("first invocation body = %+v", bodies)
	}
	if status, _, _ := f.message(t, 1); status != "absent" {
		t.Fatalf("succeeded message status = %s, want completed", status)
	}
	// Tick 2: a handler failure retries with the gateway's code.
	tick()
	if status, attempts, failure := f.message(t, 2); status != "ready" || attempts != 1 || failure != "function_failed" {
		t.Fatalf("handler failure = %s/%d/%q, want ready/1/function_failed", status, attempts, failure)
	}
	// Tick 3: the default group retry delay is zero, so message 2 is
	// redelivered as attempt 2; a platform failure returns that attempt.
	tick()
	if len(bodies) != 3 || bodies[2].Sequence != "2" || bodies[2].Attempt != 2 {
		t.Fatalf("redelivery body = %+v, want sequence 2 attempt 2", bodies[2])
	}
	if status, attempts, _ := f.message(t, 2); status != "ready" || attempts != 1 {
		t.Fatalf("platform failure = %s/%d, want ready with attempt 2 returned", status, attempts)
	}
	if states := f.ledgerStates(t); states["succeeded"] != 1 || states["superseded"] != 2 || len(states) != 2 {
		t.Fatalf("trigger ledger = %v, want 1 succeeded and 2 superseded", states)
	}

	// A bound group refuses direct consumers.
	if _, err := f.store.ClaimRealtimeConsumerGroup(ctx, f.cfg.EndpointID, "orders", "billing", "worker"); !errors.Is(err, state.ErrRealtimeConsumerGroupBoundToTrigger) {
		t.Fatalf("direct claim on bound group err = %v", err)
	}
	// Deleting the trigger unbinds the group and keeps its messages.
	if err := f.store.DeleteTrigger(ctx, f.trigger.ID.String(), f.appID); err != nil {
		t.Fatal(err)
	}
	groups, err := f.store.ListRealtimeConsumerGroups(ctx, f.cfg.EndpointID, "orders")
	if err != nil || len(groups) != 1 || groups[0].TriggerID != "" || groups[0].Ready != 1 || groups[0].Unassigned != "1" {
		t.Fatalf("group after trigger delete = %+v, %v", groups, err)
	}
	if _, err := f.store.ClaimRealtimeConsumerGroup(ctx, f.cfg.EndpointID, "orders", "billing", "worker"); err != nil {
		t.Fatalf("direct claim after unbind: %v", err)
	}
}

func TestBindRealtimeConsumerGroupTriggerRules(t *testing.T) {
	short := seedRealtimeTrigger(t, 0, 30)
	ctx := t.Context()
	var lease *state.RealtimeTriggerLeaseError
	if err := short.store.BindRealtimeConsumerGroupTrigger(ctx, short.account.ID, short.trigger.ID.String(), short.cfg); !errors.As(err, &lease) || lease.Observed != 30 {
		t.Fatalf("short lease bind err = %v, want lease error", err)
	}

	f := seedRealtimeTrigger(t, 0, api.RealtimeTriggerMinLeaseSeconds)
	if err := f.store.BindRealtimeConsumerGroupTrigger(ctx, f.account.ID, f.trigger.ID.String(), f.cfg); err != nil {
		t.Fatal(err)
	}
	if err := f.store.BindRealtimeConsumerGroupTrigger(ctx, f.account.ID, f.trigger.ID.String(), f.cfg); err != nil {
		t.Fatalf("rebinding the same trigger is idempotent: %v", err)
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	raw, _ := json.Marshal(f.cfg)
	second, err := f.store.CreateTriggerIfUnderQuota(ctx, f.appID, string(api.TriggerKindRealtime), "orders-2", false, raw, "",
		1, 1000, 1, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BindRealtimeConsumerGroupTrigger(ctx, f.account.ID, second.ID.String(), f.cfg); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second trigger bind err = %v, want ErrConflict", err)
	}
	other, err := f.store.CreateAccount(ctx, "other@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BindRealtimeConsumerGroupTrigger(ctx, other.ID, second.ID.String(), f.cfg); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account bind err = %v, want ErrNotFound", err)
	}
	// A dispatcher for an unbound trigger cannot consume the group.
	dispatch := state.WithRealtimeTriggerDispatch(ctx, second.ID.String())
	if _, err := f.store.ClaimRealtimeConsumerGroupBatch(dispatch, f.cfg.EndpointID, "orders", "billing", "trigger", 1); !errors.Is(err, state.ErrRealtimeConsumerGroupBoundToTrigger) {
		t.Fatalf("claim by unbound trigger err = %v", err)
	}
}

func TestRealtimeTriggerPublishWakesDispatcherAndReportsBacklog(t *testing.T) {
	f := seedRealtimeTrigger(t, 0, api.RealtimeTriggerMinLeaseSeconds)
	ctx := t.Context()
	if err := f.store.BindRealtimeConsumerGroupTrigger(ctx, f.account.ID, f.trigger.ID.String(), f.cfg); err != nil {
		t.Fatal(err)
	}
	conn, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN trigger_ready"); err != nil {
		t.Fatal(err)
	}
	waitNotify := func(timeout time.Duration) (string, bool) {
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		n, err := conn.Conn().WaitForNotification(waitCtx)
		if err != nil {
			return "", false
		}
		return n.Payload, true
	}
	// A channel without a bound trigger stays quiet.
	if _, err := f.store.AppendManagedRealtimeChannelMessage(ctx, f.cfg.EndpointID, "unbound", []byte("x"), false, ""); err != nil {
		t.Fatal(err)
	}
	if payload, ok := waitNotify(300 * time.Millisecond); ok {
		t.Fatalf("unbound channel notified: %s", payload)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.store.AppendManagedRealtimeChannelMessage(ctx, f.cfg.EndpointID, "orders", []byte("order"), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	payload, ok := waitNotify(5 * time.Second)
	if !ok || !strings.Contains(payload, f.trigger.ID.String()) || !strings.Contains(payload, `"source" : "realtime"`) {
		t.Fatalf("publish notification = %q (received %v)", payload, ok)
	}
	poller, err := newRealtimePoller(f.store, f.trigger)
	if err != nil {
		t.Fatal(err)
	}
	if stats := poller.BrokerStats(ctx, f.trigger); !stats.Available || stats.Lag != 2 {
		t.Fatalf("backlog = %+v, want 2 waiting messages", stats)
	}
}
