package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func auditEventData(t *testing.T, e apidserver.TestEnv, kind string) map[string]any {
	t.Helper()
	events, err := (*e.Store()).ListEvents(context.Background(), (*e.Acct()).ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == kind {
			var data map[string]any
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	return nil
}

// production-us hunt #9: four renames (which free a public hostname for any
// tenant) and every automation publish/delete left no audit event.
func TestRenameAppIsAudited(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "audit-old")
	if rec := e.Do(t, "POST", "/v1/apps/audit-old/rename", api.RenameAppRequest{NewSlug: "audit-new"}, nil); rec.Code != 200 {
		t.Fatalf("rename status %d: %s", rec.Code, rec.Body)
	}
	data := auditEventData(t, e, "app.renamed")
	if data == nil || data["from"] != "audit-old" || data["to"] != "audit-new" {
		t.Fatalf("app.renamed audit = %v", data)
	}
}

func TestStoredWorkflowMutationsAreAudited(t *testing.T) {
	for action, kind := range map[string]string{
		"save": "app.stored_workflow_saved", "publish": "app.stored_workflow_published",
		"delete": "app.stored_workflow_deleted",
	} {
		t.Run(action, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			r := httptest.NewRequest("POST", "/", nil)
			(*e.S()).AuditStoredWorkflowMutation(r, (*e.Acct()), "app-1", "nightly", state.StoredWorkflowMutation{Action: action}, state.StoredWorkflow{Version: 3, PublishedVersion: 3})
			data := auditEventData(t, e, kind)
			if data == nil || data["stored_workflow"] != "nightly" || data["app_id"] != "app-1" {
				t.Fatalf("%s audit = %v", kind, data)
			}
		})
	}
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).AuditStoredWorkflowMutation(httptest.NewRequest("POST", "/", nil), (*e.Acct()), "app-1", "nightly", state.StoredWorkflowMutation{Action: "enable", Enabled: false}, state.StoredWorkflow{})
	if auditEventData(t, e, "app.stored_workflow_disabled") == nil {
		t.Fatal("disabling an stored workflow was not audited")
	}
}
