// adr: 20261023111825
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// queueDeliveryRun enrolls a primary and a worker preview app in one contained
// run with the given delivery mode; the worker has one live deployment.
func queueDeliveryRun(t *testing.T, backend, delivery string) (state.Store, state.Account, state.App, state.App, string) {
	t.Helper()
	ctx := t.Context()
	var store state.Store = state.NewMemStore()
	if backend == "postgres" {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatal(err)
		}
		store = state.NewPgStore(pool)
	}
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@delivery.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	preview := func(name string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: name + "-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256,
			PreviewOfSlug: name, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	primary, worker := preview("primary"), preview("worker")
	run := strings.Repeat("d", 32)
	members := []state.ScenarioTestMember{
		{AppID: primary.ID, Workload: "primary", WebhookMode: state.ScenarioWebhookCaptureMode, NetworkMode: state.ScenarioNetworkContained, QueueDelivery: delivery},
		{AppID: worker.ID, Workload: "worker", WebhookMode: state.ScenarioWebhookCaptureMode, NetworkMode: state.ScenarioNetworkContained, QueueDelivery: delivery},
	}
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, members); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: worker.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:worker", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	return store, account, primary, worker, run
}

func TestScenarioQueueDeliveryReachesSameRunMembersOnly(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			store, account, primary, worker, run := queueDeliveryRun(t, backend, api.ScenarioQueueDeliverySameRun)
			queues := store.(state.ScenarioQueueCaptureStore)
			reads := store.(state.ScenarioCapturedReadStore)
			send := func(slug, queueName, body string) string {
				t.Helper()
				id, captured, err := queues.CaptureScenarioQueueMessage(ctx, primary.ID, state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindQueue, Slug: slug, QueueName: queueName,
					Message: json.RawMessage(body)})
				if err != nil || !captured {
					t.Fatalf("send to %s: %v %v", slug, captured, err)
				}
				return id
			}
			delivered := send("worker", "", `{"payload":{"order":7},"retry_policy":{"max_attempts":2}}`)
			inv, err := store.InvocationByID(ctx, delivered)
			if err != nil || inv.AppID != worker.ID || inv.Source != state.InvocationQueue || inv.State != state.InvocationPending || inv.QueueName != "" ||
				state.CompactEffectJSON(inv.Payload) != `{"order":7}` {
				t.Fatalf("delivered invocation = %+v %v", inv, err)
			}
			if record, err := reads.ScenarioCapturedQueueMessage(ctx, account.ID, run, delivered); err != nil || record.Delivery != api.ScenarioQueueDelivered ||
				record.DeliveryState != string(state.InvocationPending) {
				t.Fatalf("delivered capture = %+v %v", record, err)
			}
			for _, unsupported := range []struct{ queue, body string }{
				{"jobs", `{"payload":{},"queue_name":"jobs"}`},
				{"", `{"payload":{},"work":{"key":"k"}}`},
				{"", `{"inbox":{"type":"t","data":{}}}`},
			} {
				id := send("worker", unsupported.queue, unsupported.body)
				if _, err := store.InvocationByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("unsupported send %s was delivered: %v", unsupported.body, err)
				}
				if record, err := reads.ScenarioCapturedQueueMessage(ctx, account.ID, run, id); err != nil || record.Delivery != api.ScenarioQueueUnsupported {
					t.Fatalf("unsupported capture = %+v %v", record, err)
				}
			}
			// A send to the primary itself has no live deployment to reach.
			if record, err := reads.ScenarioCapturedQueueMessage(ctx, account.ID, run, send("primary", "", `{"payload":{}}`)); err != nil || record.Delivery != api.ScenarioQueueUnsupported {
				t.Fatalf("send without a live receiver = %+v %v", record, err)
			}
			// The delivery bound counts deliveries, not captures.
			var last string
			for i := 1; i < api.MaxScenarioDeliveredQueueMessages; i++ {
				last = send("worker", "", `{"payload":{}}`)
			}
			if record, _ := reads.ScenarioCapturedQueueMessage(ctx, account.ID, run, last); record.Delivery != api.ScenarioQueueDelivered {
				t.Fatalf("delivery within the bound = %+v", record)
			}
			over := send("worker", "", `{"payload":{}}`)
			if record, _ := reads.ScenarioCapturedQueueMessage(ctx, account.ID, run, over); record.Delivery != api.ScenarioQueueOverflow {
				t.Fatalf("delivery over the bound = %+v", record)
			}
			if _, err := store.InvocationByID(ctx, over); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("delivery over the bound reached the worker: %v", err)
			}
		})
	}
}

func TestScenarioQueueDeliveryNeedsTheOptIn(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			store, account, primary, _, run := queueDeliveryRun(t, backend, "")
			id, _, err := store.(state.ScenarioQueueCaptureStore).CaptureScenarioQueueMessage(ctx, primary.ID, state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindQueue,
				Slug: "worker", Message: json.RawMessage(`{"payload":{}}`)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.InvocationByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("a run without queue delivery delivered: %v", err)
			}
			if record, err := store.(state.ScenarioCapturedReadStore).ScenarioCapturedQueueMessage(ctx, account.ID, run, id); err != nil || record.Delivery != "" {
				t.Fatalf("capture = %+v %v", record, err)
			}
		})
	}
}

func TestScenarioQueueDeliveryRequiresAPlainContainedRun(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@delivery.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "primary-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256,
		PreviewOfSlug: "primary", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []state.ScenarioTestMember{
		{QueueDelivery: "everywhere", NetworkMode: state.ScenarioNetworkContained},
		{QueueDelivery: api.ScenarioQueueDeliverySameRun},
	} {
		member.AppID, member.Workload, member.WebhookMode = app.ID, "primary", state.ScenarioWebhookCaptureMode
		if err := store.RegisterScenarioTestMembers(ctx, account.ID, strings.Repeat("e", 32), []state.ScenarioTestMember{member}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("registered %+v: %v", member, err)
		}
	}
}

// A delivery never passes the plan's queue caps (ADR-20261022105723): at the
// app's pending cap or the account's byte cap the send is not delivered and
// the run's queue evidence is incomplete.
func TestScenarioQueueDeliveryHonoursPlanQueueCaps(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro)
	for _, tc := range []struct {
		name                         string
		backlog, backlogBytes, prior int64
		want                         string
	}{
		{"room", int64(limits.MaxQueueDepth) - 1, limits.MaxQueueBacklogBytesPerAccount - 1, 0, api.ScenarioQueueDelivered},
		{"app at its pending cap", int64(limits.MaxQueueDepth), 0, 0, api.ScenarioQueueOverflow},
		{"account at its byte cap", 0, limits.MaxQueueBacklogBytesPerAccount, 0, api.ScenarioQueueOverflow},
		{"run at its delivery bound", 0, 0, api.MaxScenarioDeliveredQueueMessages, api.ScenarioQueueOverflow},
	} {
		if got := state.ScenarioQueueDeliveryResult(api.PlanPro, tc.backlog, tc.backlogBytes, tc.prior); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := state.ScenarioQueueDeliveryResult(api.PlanFree, 0, 0, 0); got != api.ScenarioQueueUnsupported {
		t.Errorf("a plan without queues: %s", got)
	}
}
