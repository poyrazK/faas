// adr: 20261112193306
package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type orderedTelemetryStore interface {
	laneLeaseStore
	QueueBindingTelemetry(context.Context, string, string, int) (state.QueueStats, error)
}

// Binding telemetry separates messages waiting behind their key from
// runnable backlog, and reports a key whose failed head holds later messages.
func TestQueueBindingTelemetryReportsBlockedKeys(t *testing.T) {
	stores := map[string]func(*testing.T) orderedTelemetryStore{
		"mem": func(*testing.T) orderedTelemetryStore { return state.NewMemStore() },
		"pg": func(t *testing.T) orderedTelemetryStore {
			s, _, _ := pgStoreWithPool(t)
			return s
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := open(t)
			acct, err := s.CreateAccount(ctx, "blocked-"+name+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "blocked-worker", Type: state.AppTypeApp,
				WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			binding, err := s.CreateQueueBinding(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs",
				Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 10})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := s.UpsertAppWorkPolicy(ctx, acct.ID, app.ID, workpolicy.OrderedPolicy())
			if err != nil {
				t.Fatal(err)
			}
			base := state.Invocation{AccountID: acct.ID, AppID: app.ID, Source: state.InvocationQueue,
				QueueName: "jobs", QueueBindingID: binding.ID, DueAt: time.Now().Add(-time.Second), Payload: json.RawMessage(`{}`)}
			heads := map[string]bool{}
			keyed := func(key string) {
				inv := base
				inv.WorkPolicyRevision = policy.Revision
				canonical, err := workpolicy.CanonicalScalar(json.RawMessage(key))
				if err != nil {
					t.Fatal(err)
				}
				out, err := s.EnqueueKeyedInvocation(ctx, inv, policy.Policy, canonical)
				if err != nil {
					t.Fatal(err)
				}
				if key == `"a"` && len(heads) == 0 {
					heads[out.ID] = true
				}
			}
			keyed(`"a"`)
			keyed(`"a"`)
			keyed(`"a"`)
			keyed(`"b"`)
			if _, err := s.EnqueueInvocation(ctx, base); err != nil {
				t.Fatal(err)
			}

			stats, err := s.QueueBindingTelemetry(ctx, app.ID, binding.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			if stats.Depth != 5 || stats.WaitingBehindKey != 2 || stats.RunnableDepth() != 3 || stats.BlockedLanes != 0 {
				t.Fatalf("before any failure: depth=%d waiting=%d runnable=%d blocked=%d; want 5, 2, 3, 0",
					stats.Depth, stats.WaitingBehindKey, stats.RunnableDepth(), stats.BlockedLanes)
			}

			// Key a's head fails once and is due again: it blocks a's later
			// messages, while b and the unkeyed message stay runnable.
			leases, err := s.LeaseQueueMessages(ctx, state.QueueLeaseRequest{AccountID: acct.ID, AppID: app.ID,
				QueueName: "jobs", MaxMessages: 10, VisibilitySeconds: 60})
			if err != nil || len(leases) != 3 {
				t.Fatalf("leased %d (%v), want the three runnable messages", len(leases), err)
			}
			var headReceipt string
			for _, l := range leases {
				if heads[l.Invocation.ID] {
					headReceipt = l.Receipt
				}
			}
			if _, err := s.NackQueueLeases(ctx, acct.ID, app.ID, "jobs", []string{headReceipt}, 0, "boom"); err != nil {
				t.Fatal(err)
			}
			stats, err = s.QueueBindingTelemetry(ctx, app.ID, binding.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			if stats.WaitingBehindKey != 2 || stats.BlockedLanes != 1 || stats.OldestBlockedAt.IsZero() {
				t.Fatalf("after a's head failed: waiting=%d blocked=%d oldest=%v; want 2, 1, set",
					stats.WaitingBehindKey, stats.BlockedLanes, stats.OldestBlockedAt)
			}
			if stats.RunnableDepth() != stats.Depth-2 {
				t.Fatalf("runnable %d of depth %d", stats.RunnableDepth(), stats.Depth)
			}
		})
	}
}
