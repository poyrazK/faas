package supportportal

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
)

func TestFormAdapterPreservesScopeRevisionsAndClaimActor(t *testing.T) {
	ticket := api.SupportTicket{ID: uuid.NewString(), Revision: 12}
	key := uuid.NewString()
	form := url.Values{"idempotency_key": {key}, "action": {"note"}, "expected_revision": {"12"}, "body": {"internal"}}
	d := Data{PortalView: support.PortalView{Ticket: &ticket}, ActorID: uuid.NewString()}
	if _, _, err := formPayload(form, &d); err == nil {
		t.Fatal("customer form accepted private note")
	}
	d.StaffMode = true
	endpoint, payload, err := formPayload(form, &d)
	if err != nil || endpoint != "note" || payload.(support.Change).ExpectedRevision != 12 {
		t.Fatalf("note adapter: %s %#v %v", endpoint, payload, err)
	}
	form.Set("action", "claim")
	form.Set("assignee_id", uuid.NewString())
	form.Set("priority", "high")
	form.Set("required_skills", "billing deployments")
	form.Set("override_capacity", "true")
	endpoint, payload, err = formPayload(form, &d)
	if err != nil {
		t.Fatal(err)
	}
	claim := payload.(support.Change)
	if endpoint != "triage" || claim.AssigneeID != d.ActorID || claim.OverrideCapacity || len(claim.RequiredSkills) != 2 {
		t.Fatalf("unsafe claim adapter: %+v", claim)
	}
	form.Set("expected_revision", "9223372036854775808")
	if _, _, err := formPayload(form, &d); err == nil {
		t.Fatal("revision overflow accepted")
	}
}

func TestCompactStaffIDCanManageItsAssignedTicket(t *testing.T) {
	actor := uuid.NewString()
	ticket := support.StaffTicket{Ticket: api.SupportTicket{ID: uuid.NewString()}, AssigneeID: actor}
	r := httptest.NewRequest("GET", "/dashboard/admin/support/"+ticket.Ticket.ID, nil)
	d := newData(r, strings.ReplaceAll(actor, "-", ""), true, support.PortalView{Role: "agent", Ticket: &ticket.Ticket, StaffTicket: &ticket})
	if !d.CanManage() || d.StaffName(actor) != "You" {
		t.Fatal("compact ID hid agent controls")
	}
}

func TestFailedFormKeepsOriginalKeyBodyAndRevision(t *testing.T) {
	ticket := api.SupportTicket{ID: uuid.NewString(), Revision: 14}
	r := httptest.NewRequest("GET", "/dashboard/orgs/acme/support/"+ticket.ID+"?after=50", nil)
	r.SetPathValue("org", "acme")
	d := newData(r, uuid.NewString(), false, support.PortalView{Ticket: &ticket, Messages: api.SupportMessagePage{NextCursor: 100}})
	key := uuid.NewString()
	d.Failed, d.Form = "reply", url.Values{"expected_revision": {"12"}, "body": {"Draft kept"}}
	d.Keys["reply"] = key
	fields := d.Fields("reply")
	if fields.Key != key || fields.Revision != 12 || d.Value("reply", "body", "") != "Draft kept" {
		t.Fatal("failed action lost retry state")
	}
	if d.Fields("close").Revision != 14 {
		t.Fatal("unrelated action inherited stale revision")
	}
	if d.URL() != "/dashboard/orgs/acme/support/"+ticket.ID || d.NextMessages != d.URL()+"?after=100#conversation" {
		t.Fatal("pagination lost organization scope")
	}
}
