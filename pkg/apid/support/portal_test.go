package support

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPortalUsesLiveAuthorizationAndPublicConversation(t *testing.T) {
	f := supportDB(t)
	created := f.call(t, f.customer, false, "create", "", newID(), "", api.CreateSupportTicketRequest{Subject: "Portal case", Category: "other", Body: "Public question"}, 202)
	var receipt api.SupportSubmission
	if err := json.Unmarshal(created.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	f.call(t, f.manager, true, "note", receipt.TicketID, newID(), "", Change{ExpectedRevision: 1, Body: "PRIVATE-NOTE"}, 202)
	request := httptest.NewRequest("GET", "/dashboard/support/"+receipt.TicketID, nil)
	request.SetPathValue("ticket", receipt.TicketID)
	pub, err := f.s.Portal(request, f.customer, false)
	if err != nil {
		t.Fatal(err)
	}
	if pub.StaffTicket != nil || pub.Role != "" || len(pub.Teams) != 0 || len(pub.Staff) != 0 || len(pub.Messages.Items) != 1 || pub.Messages.Items[0].Body != "Public question" {
		t.Fatalf("private data in customer view: %+v", pub)
	}
	priv, err := f.s.Portal(request, f.manager, true)
	if err != nil {
		t.Fatal(err)
	}
	if priv.StaffTicket == nil || priv.Role != "manager" || len(priv.Messages.Items) != 2 || priv.Messages.Items[1].Author != "note" {
		t.Fatalf("staff snapshot missing private state: %+v", priv)
	}
	if _, err := f.s.Portal(request, f.other, false); err == nil {
		t.Fatal("cross-account portal access")
	}
	request.SetPathValue("org", "other-org")
	if _, err := f.s.Portal(request, f.customer, false); err == nil {
		t.Fatal("organization route fell back to personal scope")
	}
	request.SetPathValue("org", "")
	if _, err := f.pool.Exec(t.Context(), `UPDATE accounts SET email_verified_at=NULL WHERE id=$1`, f.customer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Portal(request, f.customer, false); err == nil {
		t.Fatal("revoked verification retained portal access")
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO support_staff(account_id,role,revision,profile) VALUES($1,'revoked',1,'{}')`, f.manager.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Portal(request, f.manager, true); err == nil {
		t.Fatal("revoked roster retained portal access")
	}
}

func TestPortalCompactAccountIDsKeepReceiptsAndAssignedAgentAccess(t *testing.T) {
	f := supportDB(t)
	compactCustomer := f.customer
	compactCustomer.ID = strings.ReplaceAll(compactCustomer.ID, "-", "")
	key := newID()
	in := api.CreateSupportTicketRequest{Subject: "Compact actor", Category: "deployment", Priority: "normal", Body: "Retry this case"}
	first := unpack[api.SupportSubmission](t, f.call(t, compactCustomer, false, "create", "", key, "", in, 202))
	retry := unpack[api.SupportSubmission](t, f.call(t, compactCustomer, false, "create", "", key, "", in, 202))
	if first != retry {
		t.Fatal("compact actor lost permanent receipt")
	}
	f.call(t, f.manager, true, "staff_save", "", newID(), "", Staff{AccountID: f.other.ID, Role: "agent", Profile: Profile{Available: true, Capacity: 10}}, 202)
	f.call(t, f.manager, true, "triage", first.TicketID, newID(), "", Change{ExpectedRevision: 1, Priority: "normal", AssigneeID: f.other.ID, Reason: "Assign compact actor test"}, 202)
	compactAgent := f.other
	compactAgent.ID = strings.ReplaceAll(compactAgent.ID, "-", "")
	f.call(t, compactAgent, true, "reply", first.TicketID, newID(), "", Change{ExpectedRevision: 2, Body: "Agent response"}, 202)
}
