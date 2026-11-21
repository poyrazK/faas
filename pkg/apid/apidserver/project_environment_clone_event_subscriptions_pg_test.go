//go:build !no_pg

// adr: 590
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

func (d fullCloneFixture) subscriptions(appID, scope string) map[string]state.EventSubscription {
	d.t.Helper()
	all, err := (*d.f.Store()).ListEventSubscriptionsForApp(d.t.Context(), appID)
	if err != nil {
		d.t.Fatal(err)
	}
	out := map[string]state.EventSubscription{}
	for _, s := range all {
		if s.DeploymentScope == scope {
			out[s.Type] = s
		}
	}
	return out
}

// A full clone copies the source's event subscriptions into the stage
// paused; resuming the stage's automation enables the ones the source ran
// and leaves production's alone (ADR-20261104120000 amendment 2026-11-14).
func TestPGFullCloneCopiesEventSubscriptionsPausedAndResumes(t *testing.T) {
	d := newFullCloneFixture(t, false)
	d.open(runtimeconfig.KeyFullEnvironmentClones, fullCloneAdmissionKey)
	ctx := t.Context()
	store := *d.f.Store()
	app := (*d.f.Apps())[0]
	running, _, err := store.UpsertEventSubscription(ctx, d.acct.ID, app.ID, "orders", "order.created", json.RawMessage(`{"region":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	off, _, err := store.UpsertEventSubscription(ctx, d.acct.ID, app.ID, "orders", "order.cancelled", nil)
	if err != nil {
		t.Fatal(err)
	}
	reconcile, ok := any(store).(state.EventSubscriptionReconcileStore)
	if !ok {
		t.Fatal("store cannot disable a subscription")
	}
	if err := reconcile.SetEventSubscriptionEnabled(ctx, off.ID, d.acct.ID, app.ID, false); err != nil {
		t.Fatal(err)
	}

	op := d.admitted("stage", "subscriptions")
	d.publish(op.OperationID)

	staged := d.subscriptions(app.ID, "stage")
	created, cancelled := staged["order.created"], staged["order.cancelled"]
	if len(staged) != 2 || created.Enabled || !created.ClonePaused || cancelled.Enabled || cancelled.ClonePaused ||
		created.ID == running.ID || created.EnvironmentID == "" || string(created.Filter) != string(running.Filter) {
		t.Fatalf("stage subscriptions = %+v", staged)
	}
	if production := d.subscriptions(app.ID, ""); !production["order.created"].Enabled || production["order.created"].ID != running.ID {
		t.Fatalf("the clone changed production's subscriptions: %+v", production)
	}

	resume := func() (int, api.ResumeEnvironmentAutomationResponse) {
		req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/"+d.project.Slug+"/environments/stage/automation/resume", d.project.Slug, nil)
		req.SetPathValue("environment", "stage")
		(*d.f.Srv()).ResumeEnvironmentAutomation(rec, req, d.acct)
		var out api.ResumeEnvironmentAutomationResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := resume(); code != http.StatusOK || out.EventSubscriptionsResumed != 1 || len(out.EventSubscriptionIDs) != 1 || out.EventSubscriptionIDs[0] != created.ID {
		t.Fatalf("resume = %d %+v", code, out)
	}
	staged = d.subscriptions(app.ID, "stage")
	if !staged["order.created"].Enabled || staged["order.created"].ClonePaused || staged["order.cancelled"].Enabled {
		t.Fatalf("after resume: %+v", staged)
	}
	if code, out := resume(); code != http.StatusOK || out.EventSubscriptionsResumed != 0 {
		t.Fatalf("a second resume = %d %+v", code, out)
	}
}
