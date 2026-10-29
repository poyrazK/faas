package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// The account publish routes stamp the publisher fanout keeps inside its
// company, and a key bound to a company publishes there only while its
// holder may deploy to the company's apps.
//
// adr: 20261025211616
func TestAccountEventPublishStampsProducer(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "publish-producer", "Publish Producer", api.PlanPro)
	developer, developerKey := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerKey := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	event := func(id string) api.PublishEventRequest {
		return api.PublishEventRequest{ID: id, Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}
	}
	producer := func(account, id string) string {
		t.Helper()
		rows, err := store.ListEvents(t.Context(), account, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			var envelope events.Envelope
			if row.Kind == "event.published" && json.Unmarshal(row.Data, &envelope) == nil && envelope.ID == id {
				return envelope.Producer
			}
		}
		t.Fatalf("event %s was not recorded for %s", id, account)
		return ""
	}

	if rec := e.Do(t, "POST", "/v1/events:publish", event("personal-1"), nil); rec.Code != http.StatusAccepted {
		t.Fatalf("personal publish = %d: %s", rec.Code, rec.Body)
	}
	if got := producer((*e.Acct()).ID, "personal-1"); got != eventcontract.ProducerPersonal {
		t.Fatalf("personal producer = %q", got)
	}
	if rec := e.Do(t, "POST", "/v1/events:publish", event("company-1"), developerKey); rec.Code != http.StatusAccepted {
		t.Fatalf("developer publish = %d: %s", rec.Code, rec.Body)
	}
	if got := producer(developer.ID, "company-1"); got != eventcontract.OrgProducer(org.ID) {
		t.Fatalf("developer producer = %q, want %q", got, eventcontract.OrgProducer(org.ID))
	}
	batch := map[string]any{"events": []api.PublishEventRequest{event("company-2")}}
	if rec := e.Do(t, "POST", "/v1/events:publish-batch", batch, developerKey); rec.Code != http.StatusOK {
		t.Fatalf("developer batch = %d: %s", rec.Code, rec.Body)
	}
	if got := producer(developer.ID, "company-2"); got != eventcontract.OrgProducer(org.ID) {
		t.Fatalf("developer batch producer = %q", got)
	}
	for _, c := range []struct {
		path string
		body any
	}{{"/v1/events:publish", event("viewer-1")}, {"/v1/events:publish-batch", map[string]any{"events": []api.PublishEventRequest{event("viewer-2")}}}} {
		rec := e.Do(t, "POST", c.path, c.body, viewerKey)
		var problem api.Problem
		if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeOrgRoleForbidden {
			t.Fatalf("viewer %s = %d: %s", c.path, rec.Code, rec.Body)
		}
	}
}
