// adr: 20261104120000 — event subscriptions per environment.
package sched_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stage subscription's delivery is the stage's own work, pinned to the
// stage's live deployment, and a production event never reaches it.
func TestEventFanoutDeliversStageEventsAsStageWork(t *testing.T) {
	t.Run("memory", func(t *testing.T) { stageEventDelivery(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { stageEventDelivery(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func stageEventDelivery(t *testing.T, store state.Store) {
	ctx := t.Context()
	label := "stage-events-" + uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, label+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: label})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: label, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 4, IdleTimeoutS: 60, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	var stageDeployment state.Deployment
	for _, scope := range []string{"production", "staging"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, scope, 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}); err != nil {
			t.Fatal(err)
		}
		if scope == "staging" {
			stageDeployment = dep
		}
	}
	stageSub, _, err := store.(state.EventSubscriptionScopeStore).UpsertEventSubscriptionInScope(ctx, account.ID, app.ID, "staging", "orders", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var lastEventID string
	publish := func(environmentID string) {
		t.Helper()
		lastEventID = uuid.NewString()
		envelope, err := (events.Envelope{ID: lastEventID, Source: "orders", Type: "order.created",
			Data: json.RawMessage(`{}`), EnvironmentID: environmentID}).Normalize(account.ID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
			t.Fatal(err)
		}
		loop.XRunEventFanoutSweep(ctx)
	}

	publish("")
	if invocations, err := store.ListInvocationsForApp(ctx, app.ID); err != nil || len(invocations) != 0 {
		t.Fatalf("a production event reached the stage subscription: %+v, %v", invocations, err)
	}
	publish(stageSub.EnvironmentID)
	invocations, err := store.ListInvocationsForApp(ctx, app.ID)
	if err != nil || len(invocations) != 1 {
		var receipts []state.EventReceiptRecipient
		if receiptStore, ok := store.(state.EventReceiptStore); ok {
			if receipt, rErr := receiptStore.EventReceipt(ctx, account.ID, "orders", lastEventID, state.EventReceiptCursor{}, 10); rErr == nil {
				receipts = receipt.Recipients
			}
		}
		t.Fatalf("stage event invocations = %+v, %v; want one for %s (recipients %+v)", invocations, err, stageSub.ID, receipts)
	}
	inv := invocations[0]
	if inv.DeploymentScope != "staging" {
		t.Fatalf("stage delivery is not stage work: scope %q", inv.DeploymentScope)
	}
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	if headers["X-Gregale-Revision"] != stageDeployment.ID && headers["X-Gregale-Release"] == "" {
		t.Fatalf("stage delivery is not pinned to the stage: headers %+v", headers)
	}
}
