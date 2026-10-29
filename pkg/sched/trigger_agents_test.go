// adr: 20261027090010 — a broker trigger that targets an agent admits one agent run
// per record, idempotently per broker message, instead of invoking its app.
package sched_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// agentTrigger creates an enabled kafka trigger pointed at an agent.
func agentTrigger(t *testing.T, store state.Store) sqlc.Trigger {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "agent-target-" + uuid.NewString()[:8], RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "orders", false, []byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.TriggerAgentStore).SetTriggerAgent(ctx, trigger.ID.String(), "order-review"); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if trigger, err = store.UpdateTrigger(ctx, trigger.ID.String(), &enabled, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return trigger
}

// triggerAgentStore runs test on Postgres, where broker trigger records
// live; MemStore has no broker record path.
func triggerAgentStore(t *testing.T, test func(*testing.T, state.Store)) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	test(t, state.NewPgStore(pool))
}

func kafkaRecord(item, stable, payload string) sched.SourceRecord {
	return sched.SourceRecord{ItemIdentifier: item, StableIdentifier: stable, Payload: []byte(payload),
		Headers: map[string]string{"source": "orders"}, ReceivedAt: time.Now()}
}

func TestAgentTriggerAdmitsOneRunPerRecord(t *testing.T) {
	triggerAgentStore(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		trigger := agentTrigger(t, store)
		poller := &workIdentityTestPoller{deliveries: []sched.SourceRecord{
			kafkaRecord("0-8-10", "orders/0/8", `{"order_id":"one"}`),
			kafkaRecord("0-8-15", "orders/0/8", `{"order_id":"one"}`),
			kafkaRecord("0-9-1", "", `{"order_id":"two"}`),
		}}
		sched.InstallPollerFactory(t, "kafka", func(sqlc.Trigger) (sched.TriggerSource, error) { return poller, nil })
		loop := sched.MakeLoopForDLQ()

		// Without agents the trigger is not polled: its records stay with
		// the broker.
		if err := loop.DispatchOneTrigger(ctx, trigger, store, nil); err != nil || len(poller.deliveries) != 3 {
			t.Fatalf("dispatch without agents: %v, %d left", err, len(poller.deliveries))
		}
		agents := &fakeAgents{}
		loop.WithTriggerAgents(agents)
		for range 3 {
			if err := loop.DispatchOneTrigger(ctx, trigger, store, nil); err != nil {
				t.Fatal(err)
			}
		}
		if len(agents.admissions) != 3 || len(poller.acks) != 3 || len(poller.nacks) != 0 {
			t.Fatalf("admissions %d acks %v nacks %v", len(agents.admissions), poller.acks, poller.nacks)
		}
		first, redelivered, other := agents.admissions[0], agents.admissions[1], agents.admissions[2]
		if first.Agent != "order-review" || first.Trigger != "trigger" || first.AccountID != trigger.AccountID.String() ||
			first.Limits != agentstore.PlanLimits(api.PlanPro) || !strings.HasPrefix(first.DedupeKey, "trigger:") {
			t.Fatalf("admission %+v", first)
		}
		// The redelivery carries the same broker message identity; the other
		// record has none, so its trigger_records row names it.
		if redelivered.DedupeKey != first.DedupeKey || other.DedupeKey == first.DedupeKey {
			t.Fatalf("dedupe keys %q %q %q", first.DedupeKey, redelivered.DedupeKey, other.DedupeKey)
		}
		var inputs struct {
			Trigger struct{ ID, Kind, Slug string } `json:"trigger"`
			Record  struct {
				ItemIdentifier string            `json:"item_identifier"`
				Payload        json.RawMessage   `json:"payload"`
				Headers        map[string]string `json:"headers"`
			} `json:"record"`
		}
		if err := json.Unmarshal(first.Inputs, &inputs); err != nil || inputs.Trigger.ID != trigger.ID.String() || inputs.Trigger.Kind != "kafka" ||
			inputs.Trigger.Slug != "orders" || string(inputs.Record.Payload) != `{"order_id":"one"}` || inputs.Record.Headers["source"] != "orders" {
			t.Fatalf("inputs %s %v", first.Inputs, err)
		}
	})
}

func TestAgentTriggerRecordOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		err      error
		admitted int
		nacked   bool
		acked    bool
		reason   string
	}{
		// A plan limit passes: the record goes back to the broker for a retry.
		{"plan limit", `{}`, &agentstore.LimitError{Limit: "concurrent_runs", Max: 2, Observed: 2}, 0, true, false, ""},
		// A refusal no retry can fix dead-letters the record.
		{"agent gone", `{}`, agentstore.ErrNotFound, 0, true, false, "customer_disabled"},
		{"too large", `{"blob":"` + strings.Repeat("x", api.AgentRunInputsMaxBytes) + `"}`, nil, 0, true, false, "payload_too_large"},
		{"admitted", `{}`, nil, 1, false, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			triggerAgentStore(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				trigger := agentTrigger(t, store)
				poller := &workIdentityTestPoller{deliveries: []sched.SourceRecord{kafkaRecord("0-1-1", "orders/0/1", tc.payload)}}
				sched.InstallPollerFactory(t, "kafka", func(sqlc.Trigger) (sched.TriggerSource, error) { return poller, nil })
				agents := &fakeAgents{err: tc.err}
				loop := sched.MakeLoopForDLQ().WithTriggerAgents(agents)
				if err := loop.DispatchOneTrigger(ctx, trigger, store, nil); err != nil {
					t.Fatal(err)
				}
				if len(agents.admissions) != tc.admitted || (len(poller.nacks) == 1) != tc.nacked || (len(poller.acks) == 1) != tc.acked {
					t.Fatalf("admissions %d acks %v nacks %v", len(agents.admissions), poller.acks, poller.nacks)
				}
				dead, err := store.(interface {
					ListTriggerDeadLetter(context.Context, string, int32) ([]sqlc.TriggerDeadLetter, error)
				}).ListTriggerDeadLetter(ctx, trigger.ID.String(), 10)
				if err != nil || len(dead) != map[bool]int{true: 1}[tc.reason != ""] || (tc.reason != "" && dead[0].Reason != tc.reason) {
					t.Fatalf("dead letters %+v %v, want reason %q", dead, err, tc.reason)
				}
			})
		})
	}
}
