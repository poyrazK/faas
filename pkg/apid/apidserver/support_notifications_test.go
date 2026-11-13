// adr: 20261112134100
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func TestCustomerSupportNotificationRoutes(t *testing.T) {
	pool := pgtest.Open(t)
	ddl, err := os.ReadFile("../support/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	env := apidserver.Setup(t, api.PlanPro)
	srv := *env.S()
	store := *env.Store()
	operator := *env.Acct()
	customer, err := store.CreateAccount(t.Context(), "support-customer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct{ id, email string }{{operator.ID, operator.Email}, {customer.ID, customer.Email}} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO accounts VALUES($1,$2,'pro','active',now(),now())`, a.id, a.email); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	srv.WithAdminAllowlist(operator.Email)
	mailer := &apidserver.RecordingMailer{}
	*srv.Mailer() = mailer
	workerContext, stopWorker := context.WithCancel(t.Context())
	if err := srv.ConfigureCustomerSupportForTest(workerContext, pool); err != nil {
		stopWorker()
		t.Fatal(err)
	}
	stopWorker()
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body)
		}
	}
	check(env.DoSession(t, customer.ID, "GET", "/v1/support/notifications", nil, nil), 404)
	flag, _ := platformflags.CapabilityFlag("customer-support")
	allow := true
	srv.SeedPlatformFlag(flag, platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{{ID: "support-notifications", Accounts: []string{customer.ID}, Value: &allow}}})
	created := env.DoSession(t, customer.ID, "POST", "/v1/support/tickets", api.CreateSupportTicketRequest{Subject: "PRIVATE-SUBJECT", Category: "other", Body: "PRIVATE-QUESTION"}, map[string]string{"Idempotency-Key": uuid.NewString()})
	check(created, 202)
	var receipt api.SupportSubmission
	if err := json.Unmarshal(created.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	path := "/v1/admin/support/tickets/" + receipt.TicketID
	check(env.DoAdmin(t, "POST", path+"/messages", api.SupportMessageRequest{ExpectedRevision: 1, Body: "PRIVATE-ANSWER"}, map[string]string{"Idempotency-Key": uuid.NewString()}), 202)
	check(env.DoAdmin(t, "POST", path+"/notes", api.SupportMessageRequest{ExpectedRevision: 2, Body: "PRIVATE-INTERNAL-NOTE"}, map[string]string{"Idempotency-Key": uuid.NewString()}), 202)
	feedResponse := env.DoSession(t, customer.ID, "GET", "/v1/support/notifications?unread=true", nil, nil)
	check(feedResponse, 200)
	var feed api.SupportNotificationPage
	if err := json.Unmarshal(feedResponse.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if feed.UnreadCount != 1 || len(feed.Items) != 1 || feed.Items[0].Kind != "staff_reply" || strings.Contains(feedResponse.Body.String(), "PRIVATE") {
		t.Fatalf("unexpected customer feed: %s", feedResponse.Body)
	}
	for range 3 {
		if err := srv.DispatchCustomerSupportNotificationForTest(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	sent := mailer.Snapshot()
	if len(sent) != 2 {
		t.Fatalf("configured mailer did not receive staff and customer notifications: %d", len(sent))
	}
	for _, message := range sent {
		if strings.Contains(message.Subject+message.TextBody, "PRIVATE") || !strings.HasPrefix(message.MessageID, "support-notification:") || !strings.Contains(message.TextBody, "https://") {
			t.Fatalf("unsafe mail handoff: %+v", message)
		}
	}
	request := api.SupportReadRequest{ThroughSequence: feed.Items[0].Sequence}
	page := env.DoSession(t, customer.ID, "GET", "/dashboard/support/"+receipt.TicketID, nil, nil)
	check(page, 200)
	form := supportPortalForm(t, page, "read")
	browserRead := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/dashboard/support/"+receipt.TicketID, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", "application/json")
		token, err := srv.IssueDashboardSession(r, customer.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, r)
		return w
	}
	check(browserRead(nil), 400)
	acknowledged := browserRead(page.Result().Cookies())
	check(acknowledged, 202)
	if !strings.HasPrefix(acknowledged.Header().Get("Content-Type"), "application/json") || len(acknowledged.Result().Cookies()) != 0 {
		t.Fatal("background acknowledgement rotated the page's CSRF cookie")
	}
	readPath := "/v1/support/tickets/" + receipt.TicketID + "/read"
	check(env.DoSession(t, customer.ID, "POST", readPath, request, map[string]string{"Origin": "https://evil.example"}), 403)
	check(env.DoSession(t, customer.ID, "POST", readPath, request, nil), 202)
	check(env.DoSession(t, customer.ID, "POST", readPath, request, nil), 202)
	current := env.DoSession(t, customer.ID, "GET", "/v1/support/tickets/"+receipt.TicketID, nil, nil)
	check(current, 200)
	var ticket api.SupportTicket
	if err := json.Unmarshal(current.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	if ticket.Revision != 3 || ticket.UnreadCount != 0 {
		t.Fatalf("read state altered conversation: %+v", ticket)
	}
	check(env.Do(t, "GET", "/v1/admin/support/notifications", nil, nil), 403)
	staffFeed := env.DoSession(t, "", "GET", "/v1/admin/support/notifications", nil, nil)
	check(staffFeed, 200)
	if err := json.Unmarshal(staffFeed.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if feed.UnreadCount != 1 {
		t.Fatalf("staff/customer read state conflated: %s", staffFeed.Body)
	}
	staffRead := api.SupportReadRequest{ThroughSequence: feed.Items[0].Sequence}
	// A current MFA-completed session may acknowledge its own metadata without
	// fresh step-up. Conversation mutations still require the strict gate.
	check(env.DoSession(t, "", "POST", path+"/read", staffRead, nil), 202)
	check(env.DoSession(t, "", "POST", path+"/read", staffRead, map[string]string{"Origin": "https://evil.example"}), 403)
	check(env.DoSession(t, "", "POST", path+"/notes", api.SupportMessageRequest{ExpectedRevision: 3, Body: "No fresh MFA"}, map[string]string{"Idempotency-Key": uuid.NewString()}), 403)
	if _, err := pool.Exec(t.Context(), `UPDATE accounts SET email_verified_at=NULL WHERE id=$1`, customer.ID); err != nil {
		t.Fatal(err)
	}
	check(env.DoSession(t, customer.ID, "GET", "/v1/support/notifications", nil, nil), 403)
}
