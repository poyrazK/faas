// adr: 20261112193306
package state_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// laneLeaseStore is the narrow surface this test drives on both stores.
type laneLeaseStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateQueueBinding(context.Context, state.QueueBinding) (state.QueueBinding, error)
	UpsertAppWorkPolicy(context.Context, string, string, workpolicy.Policy) (state.AppWorkPolicy, error)
	EnqueueKeyedInvocation(context.Context, state.Invocation, workpolicy.Policy, string, ...string) (state.Invocation, error)
	EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	LeaseQueueMessages(context.Context, state.QueueLeaseRequest) ([]state.QueueLease, error)
	AckQueueLeases(context.Context, string, string, string, []string) (state.QueueLeaseOutcome, error)
	NackQueueLeases(context.Context, string, string, string, []string, time.Duration, string) (state.QueueLeaseOutcome, error)
}

// Pull leases follow the work-policy lane rule push consumers already use:
// one running message per key, in admission order, a retrying head blocking
// later messages of its key while other keys and unkeyed messages proceed.
func TestQueueLeaseHonorsWorkPolicyLanes(t *testing.T) {
	stores := map[string]func(*testing.T) laneLeaseStore{
		"mem": func(*testing.T) laneLeaseStore { return state.NewMemStore() },
		"pg": func(t *testing.T) laneLeaseStore {
			s, _, _ := pgStoreWithPool(t)
			return s
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := open(t)
			acct, err := s.CreateAccount(ctx, "lanes-"+name+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "lane-worker", Type: state.AppTypeApp,
				WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			binding, err := s.CreateQueueBinding(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs",
				Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 10})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := s.UpsertAppWorkPolicy(ctx, acct.ID, app.ID, workpolicy.Policy{Name: "ordered", MaxRunningPerKey: 1})
			if err != nil {
				t.Fatal(err)
			}
			base := state.Invocation{AccountID: acct.ID, AppID: app.ID, Source: state.InvocationQueue,
				QueueName: "jobs", QueueBindingID: binding.ID, DueAt: time.Now().Add(-time.Second), Payload: json.RawMessage(`{}`)}
			names := map[string]string{}
			keyed := func(label, key string) {
				inv := base
				inv.WorkPolicyRevision = policy.Revision
				canonical, err := workpolicy.CanonicalScalar(json.RawMessage(key))
				if err != nil {
					t.Fatal(err)
				}
				out, err := s.EnqueueKeyedInvocation(ctx, inv, policy.Policy, canonical)
				if err != nil {
					t.Fatalf("enqueue %s: %v", label, err)
				}
				names[out.ID] = label
			}
			keyed("a1", `"a"`)
			keyed("a2", `"a"`)
			keyed("a3", `"a"`)
			keyed("b1", `"b"`)
			plain, err := s.EnqueueInvocation(ctx, base)
			if err != nil {
				t.Fatal(err)
			}
			names[plain.ID] = "u"

			receipts := map[string]string{}
			lease := func() []string {
				t.Helper()
				leases, err := s.LeaseQueueMessages(ctx, state.QueueLeaseRequest{AccountID: acct.ID, AppID: app.ID,
					QueueName: "jobs", MaxMessages: 10, VisibilitySeconds: 60})
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, l := range leases {
					got = append(got, names[l.Invocation.ID])
					receipts[names[l.Invocation.ID]] = l.Receipt
				}
				slices.Sort(got)
				return got
			}
			expect := func(step string, want ...string) {
				t.Helper()
				if got := lease(); !slices.Equal(got, want) {
					t.Fatalf("%s: leased %v, want %v", step, got, want)
				}
			}
			ack := func(label string) {
				t.Helper()
				if _, err := s.AckQueueLeases(ctx, acct.ID, app.ID, "jobs", []string{receipts[label]}); err != nil {
					t.Fatal(err)
				}
			}
			nack := func(label string, delay time.Duration) {
				t.Helper()
				if _, err := s.NackQueueLeases(ctx, acct.ID, app.ID, "jobs", []string{receipts[label]}, delay, "boom"); err != nil {
					t.Fatal(err)
				}
			}

			expect("first lease: one per key plus unkeyed", "a1", "b1", "u")
			expect("lane held while a1 runs")
			nack("a1", time.Hour)
			expect("a retrying head blocks its lane")
			ack("b1")
			ack("u")
			expect("other lanes are unaffected and empty")
			// Within a lane, delivery follows admission order.
			keyed("c1", `"c"`)
			keyed("c2", `"c"`)
			keyed("c3", `"c"`)
			expect("c1 heads its lane", "c1")
			ack("c1")
			expect("then c2", "c2")
			ack("c2")
			expect("then c3", "c3")
			ack("c3")
			expect("lane a still blocked by its delayed head")
		})
	}
}
