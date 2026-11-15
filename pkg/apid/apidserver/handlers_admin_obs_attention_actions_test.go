package apidserver_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ADR-20261115120000: the operator console dismisses, contacts and
// summarises signals through /v1, with the staff page's rules, and reads
// the customer's own health view.
func TestOperatorConsoleAttentionActions(t *testing.T) {
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
	srv.WithAdminAllowlist(acct.Email)
	if _, err := pool.Exec(t.Context(), `INSERT INTO accounts VALUES($1,$2,'pro','active',now(),now())`, acct.ID, acct.Email); err != nil {
		t.Fatal(err)
	}
	signal := func(kind, subject, detail string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO customer_attention_signals(account_id,kind,subject,detail,state,observed,first_seen_at,last_seen_at)
			VALUES($1,$2,$3,$4,'open',3,now(),now()) RETURNING id::text`, acct.ID, kind, subject, detail).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	expect := func(rec interface {
		Result() *http.Response
	}, body string, status int) {
		t.Helper()
		if code := rec.Result().StatusCode; code != status {
			t.Fatalf("status %d want %d: %s", code, status, body)
		}
	}
	key := func(k string) map[string]string { return map[string]string{"Idempotency-Key": k} }
	root := "/v1/admin/obs/attention/"

	deploy := signal("deploy_failing", "shop", "build_failed")

	// The draft is worded for the customer.
	rec := env.DoOperator(t, "GET", root+deploy+"/contact-draft", nil, nil)
	expect(rec, rec.Body.String(), http.StatusOK)
	var draft api.ObsAttentionContactDraft
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil || draft.Subject == "" || draft.Body == "" || len(draft.Categories) == 0 || draft.BodyMax == 0 {
		t.Fatalf("draft = %+v, %v", draft, err)
	}

	// Dismissing needs a known outcome and records the staff member.
	rec = env.DoOperator(t, "POST", root+deploy+"/dismiss", api.ObsAttentionDismissRequest{Outcome: "contacted"}, key("d-bad"))
	expect(rec, rec.Body.String(), http.StatusBadRequest)
	rec = env.DoOperator(t, "POST", root+deploy+"/dismiss", api.ObsAttentionDismissRequest{Outcome: api.AttentionOutcomeFalsePositive, Note: "test deploy"}, key("d-1"))
	expect(rec, rec.Body.String(), http.StatusOK)
	var action api.ObsAttentionActionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &action); err != nil || action.Signal.State != api.AttentionStateDismissed ||
		action.Signal.Outcome != api.AttentionOutcomeFalsePositive || action.Signal.DismissReason != "test deploy" {
		t.Fatalf("dismiss = %+v, %v", action, err)
	}
	rec = env.DoOperator(t, "POST", root+deploy+"/dismiss", api.ObsAttentionDismissRequest{Outcome: api.AttentionOutcomeNotNeeded}, key("d-2"))
	expect(rec, rec.Body.String(), http.StatusConflict)
	rec = env.DoOperator(t, "GET", root+deploy+"/contact-draft", nil, nil)
	expect(rec, rec.Body.String(), http.StatusConflict)

	// Contacting opens a ticket and links it; the same key replays it.
	oom := signal("app_oom", "api", "OOM-killed 3 times")
	contact := api.ObsAttentionContactRequest{Subject: "Your app api keeps running out of memory", Category: "deployment", Body: "We noticed api was stopped for memory. Want help?"}
	rec = env.DoOperator(t, "POST", root+oom+"/contact", contact, nil)
	expect(rec, rec.Body.String(), http.StatusBadRequest)
	rec = env.DoOperator(t, "POST", root+oom+"/contact", contact, key("0b6f3c7e-5d4a-4c1e-9f2b-8a7d6e5c4b3a"))
	expect(rec, rec.Body.String(), http.StatusOK)
	if err := json.Unmarshal(rec.Body.Bytes(), &action); err != nil || action.TicketID == "" || action.Signal.Outcome != api.AttentionOutcomeContacted {
		t.Fatalf("contact = %+v, %v", action, err)
	}
	first := action.TicketID
	rec = env.DoOperator(t, "POST", root+oom+"/contact", contact, key("0b6f3c7e-5d4a-4c1e-9f2b-8a7d6e5c4b3a"))
	expect(rec, rec.Body.String(), http.StatusOK)
	if err := json.Unmarshal(rec.Body.Bytes(), &action); err != nil || action.TicketID != first {
		t.Fatalf("replayed contact = %+v, %v; want ticket %s", action, err, first)
	}

	// The summary counts both outcomes.
	rec = env.DoOperator(t, "GET", root+"summary", nil, nil)
	expect(rec, rec.Body.String(), http.StatusOK)
	var summary api.ObsAttentionSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil || summary.WindowDays != 30 {
		t.Fatalf("summary = %+v, %v", summary, err)
	}
	byKind := map[string]api.ObsAttentionKindSummary{}
	for _, k := range summary.Items {
		byKind[k.Kind] = k
	}
	if byKind["deploy_failing"].FalsePositive != 1 || byKind["deploy_failing"].FalsePositivePercent != 100 || byKind["app_oom"].Contacted != 1 {
		t.Fatalf("summary items = %+v", summary.Items)
	}

	// The customer's own view shows open customer-visible signals only.
	signal("jobs_failing", "nightly", "last 3 scheduled runs failed")
	rec = env.DoOperator(t, "GET", "/v1/admin/obs/tenants/"+acct.ID+"/health", nil, nil)
	expect(rec, rec.Body.String(), http.StatusOK)
	var health api.AccountHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil || len(health.Signals) != 1 || health.Signals[0].Subject != "nightly" ||
		!strings.Contains(health.Signals[0].Title, "nightly") {
		t.Fatalf("customer view = %+v, %v", health.Signals, err)
	}
	rec = env.DoOperator(t, "GET", "/v1/admin/obs/tenants/00000000-0000-4000-8000-000000000000/health", nil, nil)
	expect(rec, rec.Body.String(), http.StatusNotFound)

	// Bearer keys cannot write, and unknown ids are 404.
	bearer := env.Do(t, "POST", root+oom+"/dismiss", api.ObsAttentionDismissRequest{Outcome: api.AttentionOutcomeNotNeeded}, key("b-1"))
	if bearer.Code < 400 {
		t.Fatalf("bearer dismiss status %d: %s", bearer.Code, bearer.Body)
	}
	rec = env.DoOperator(t, "GET", root+"999999/contact-draft", nil, nil)
	expect(rec, rec.Body.String(), http.StatusNotFound)
	rec = env.DoOperator(t, "GET", root+"x/contact-draft", nil, nil)
	expect(rec, rec.Body.String(), http.StatusBadRequest)
}
