package apidserver_test

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

var (
	attentionCSRFField     = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	attentionKeyField      = regexp.MustCompile(`name="idempotency_key" value="([^"]+)"`)
	attentionSubjectField  = regexp.MustCompile(`name="subject" required maxlength="\d+" value="([^"]*)"`)
	attentionMessageField  = regexp.MustCompile(`(?s)<textarea name="body"[^>]*>(.*?)</textarea>`)
	attentionMigrationUpRe = regexp.MustCompile(`(?s)-- \+goose Up(.*?)-- \+goose Down`)
)

// ADR-20261113123000 / ADR-20261113180000: only support staff see customers
// who may be stuck; dismissing with an outcome and contacting the customer
// are stepped-up staff writes.
func TestSupportAttentionStaffPage(t *testing.T) {
	pool := pgtest.Open(t)
	ddls := []string{"../support/schema.sql",
		"../../../migrations/20261113122715352_customer_attention_signals.sql",
		"../../../migrations/20261113152905644_customer_attention_outcomes.sql"}
	for i, path := range ddls {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(raw)
		if i > 0 {
			sql = attentionMigrationUpRe.FindStringSubmatch(sql)[1]
		}
		if _, err := pool.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	env := apidserver.Setup(t, api.PlanPro)
	acct := *env.Acct()
	srv := *env.S()
	if err := srv.ConfigureCustomerSupportForTest(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	srv.ConfigureAttentionForTest(pool)
	if _, err := pool.Exec(t.Context(), `INSERT INTO accounts VALUES($1,$2,'pro','active',now(),now())`, acct.ID, acct.Email); err != nil {
		t.Fatal(err)
	}
	signal := func(kind, subject, detail string) string {
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO customer_attention_signals(account_id,kind,subject,detail,state,observed,first_seen_at,last_seen_at)
			VALUES($1,$2,$3,$4,'open',3,now(),now()) RETURNING id::text`, acct.ID, kind, subject, detail).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := signal("deploy_failing", "shop", "<b>build_failed</b>")
	request := func(method, query string, form url.Values, cookies []*http.Cookie, fresh bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/dashboard/admin/support/attention"+query, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if fresh {
			env.AddAdminSession(t, req)
		} else {
			token, err := srv.IssueDashboardSession(req, acct.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, req)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body)
		}
	}
	field := func(page *httptest.ResponseRecorder, re *regexp.Regexp) string {
		t.Helper()
		m := re.FindStringSubmatch(page.Body.String())
		if m == nil {
			t.Fatalf("%s missing: %s", re, page.Body)
		}
		return html.UnescapeString(m[1])
	}
	dismissForm := func(page *httptest.ResponseRecorder, outcome, note string) url.Values {
		t.Helper()
		return url.Values{"csrf_token": {field(page, attentionCSRFField)}, "action": {"dismiss"}, "signal_id": {id}, "outcome": {outcome}, "reason": {note}}
	}

	check(request("GET", "", nil, nil, true), http.StatusForbidden)
	srv.WithAdminAllowlist(acct.Email)
	page := request("GET", "", nil, nil, true)
	check(page, http.StatusOK)
	body := page.Body.String()
	if page.Header().Get("Cache-Control") != "no-store" || !strings.Contains(body, "shop") ||
		strings.Contains(body, "<b>build_failed</b>") || !strings.Contains(body, "Dismiss signal") || !strings.Contains(body, "Contact customer") {
		t.Fatalf("staff page: %s", body)
	}

	form := dismissForm(page, api.AttentionOutcomeAlreadyInTouch, "customer wrote in yesterday")
	check(request("POST", "", form, nil, true), http.StatusBadRequest)
	stale := request("POST", "", form, page.Result().Cookies(), false)
	check(stale, http.StatusForbidden)
	if !strings.Contains(stale.Body.String(), "Verify your identity") {
		t.Fatalf("step-up prompt missing: %s", stale.Body)
	}
	check(request("POST", "", dismissForm(stale, "contacted", ""), stale.Result().Cookies(), true), http.StatusBadRequest)
	form = dismissForm(stale, api.AttentionOutcomeAlreadyInTouch, "customer wrote in yesterday")
	check(request("POST", "", form, stale.Result().Cookies(), true), http.StatusSeeOther)
	var state, by, reason, outcome string
	if err := pool.QueryRow(t.Context(), `SELECT state, dismissed_by::text, dismiss_reason, outcome FROM customer_attention_signals WHERE id=$1::bigint`, id).Scan(&state, &by, &reason, &outcome); err != nil {
		t.Fatal(err)
	}
	if state != api.AttentionStateDismissed || strings.ReplaceAll(by, "-", "") != strings.ReplaceAll(acct.ID, "-", "") ||
		reason != "customer wrote in yesterday" || outcome != api.AttentionOutcomeAlreadyInTouch {
		t.Fatalf("dismissed row state=%s by=%s reason=%q outcome=%s", state, by, reason, outcome)
	}
	check(request("POST", "", form, stale.Result().Cookies(), true), http.StatusConflict)

	// Contacting opens a ticket from an edited draft and links it.
	check(request("GET", "?contact="+id, nil, nil, true), http.StatusConflict)
	oom := signal("app_oom", "api", "OOM-killed 3 times; peak 601 MB of a 512 MB plan")
	contact := request("GET", "?contact="+oom, nil, nil, true)
	check(contact, http.StatusOK)
	subject := field(contact, attentionSubjectField)
	if subject != "api is running out of memory" || !strings.Contains(field(contact, attentionMessageField), "peak 601 MB") {
		t.Fatalf("contact draft: %s", contact.Body)
	}
	send := url.Values{"csrf_token": {field(contact, attentionCSRFField)}, "action": {"contact"}, "signal_id": {oom},
		"idempotency_key": {field(contact, attentionKeyField)}, "subject": {subject}, "category": {"deployment"}, "body": {"Edited: can we help with memory?"}}
	check(request("POST", "", send, contact.Result().Cookies(), true), http.StatusSeeOther)
	var ticket, ticketSubject, message string
	if err := pool.QueryRow(t.Context(), `SELECT s.outcome, s.ticket_id::text, t.subject, m.body FROM customer_attention_signals s
		JOIN support_tickets t ON t.id = s.ticket_id JOIN support_messages m ON m.ticket_id = t.id WHERE s.id=$1::bigint`, oom).Scan(&outcome, &ticket, &ticketSubject, &message); err != nil {
		t.Fatal(err)
	}
	if outcome != api.AttentionOutcomeContacted || ticketSubject != subject || message != "Edited: can we help with memory?" {
		t.Fatalf("contacted outcome=%s ticket=%s subject=%q message=%q", outcome, ticket, ticketSubject, message)
	}
	// Resubmitting the same form replays the ticket instead of opening another.
	check(request("POST", "", send, contact.Result().Cookies(), true), http.StatusSeeOther)
	var tickets int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM support_tickets`).Scan(&tickets); err != nil || tickets != 1 {
		t.Fatalf("tickets = %d, %v", tickets, err)
	}
	page = request("GET", "?updated=contacted", nil, nil, true)
	check(page, http.StatusOK)
	if body := page.Body.String(); !strings.Contains(body, "The customer was contacted") || !strings.Contains(body, "/dashboard/admin/support/"+ticket) ||
		!strings.Contains(body, "Last 30 days") {
		t.Fatalf("after contact: %s", body)
	}

	if _, err := pool.Exec(t.Context(), `INSERT INTO support_staff(account_id,revision,role,profile) VALUES($1,1,'revoked','{}')`, acct.ID); err != nil {
		t.Fatal(err)
	}
	check(request("GET", "", nil, nil, true), http.StatusForbidden)
}
