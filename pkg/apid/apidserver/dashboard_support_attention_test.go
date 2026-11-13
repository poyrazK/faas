package apidserver_test

import (
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

var attentionCSRFField = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// ADR-20261113123000: only support staff see customers who may be stuck,
// and dismissing a signal is a stepped-up staff write.
func TestSupportAttentionStaffPage(t *testing.T) {
	pool := pgtest.Open(t)
	ddl, err := os.ReadFile("../support/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../migrations/20261113122715352_customer_attention_signals.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(strings.SplitN(string(migration), "-- +goose Up", 2)[1], "-- +goose Down", 2)[0]
	for _, sql := range []string{string(ddl), up} {
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
	var id string
	if err := pool.QueryRow(t.Context(), `INSERT INTO customer_attention_signals(account_id,kind,subject,detail,state,observed,first_seen_at,last_seen_at)
		VALUES($1,'deploy_failing','shop','<b>build_failed</b>','open',3,now(),now()) RETURNING id::text`, acct.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	request := func(method string, form url.Values, cookies []*http.Cookie, fresh bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/dashboard/admin/support/attention", strings.NewReader(form.Encode()))
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
	dismissForm := func(page *httptest.ResponseRecorder, reason string) url.Values {
		t.Helper()
		m := attentionCSRFField.FindStringSubmatch(page.Body.String())
		if m == nil {
			t.Fatalf("dismiss form missing: %s", page.Body)
		}
		return url.Values{"csrf_token": {m[1]}, "signal_id": {id}, "reason": {reason}}
	}

	check(request("GET", nil, nil, true), http.StatusForbidden)
	srv.WithAdminAllowlist(acct.Email)
	page := request("GET", nil, nil, true)
	check(page, http.StatusOK)
	body := page.Body.String()
	if page.Header().Get("Cache-Control") != "no-store" || !strings.Contains(body, "shop") ||
		strings.Contains(body, "<b>build_failed</b>") || !strings.Contains(body, "Dismiss signal") {
		t.Fatalf("staff page: %s", body)
	}

	form := dismissForm(page, "customer already contacted")
	check(request("POST", form, nil, true), http.StatusBadRequest)
	stale := request("POST", form, page.Result().Cookies(), false)
	check(stale, http.StatusForbidden)
	if !strings.Contains(stale.Body.String(), "Verify your identity") {
		t.Fatalf("step-up prompt missing: %s", stale.Body)
	}
	form = dismissForm(stale, "customer already contacted")
	check(request("POST", form, stale.Result().Cookies(), true), http.StatusSeeOther)
	var state, by, reason string
	if err := pool.QueryRow(t.Context(), `SELECT state, dismissed_by::text, dismiss_reason FROM customer_attention_signals WHERE id=$1::bigint`, id).Scan(&state, &by, &reason); err != nil {
		t.Fatal(err)
	}
	if state != api.AttentionStateDismissed || strings.ReplaceAll(by, "-", "") != strings.ReplaceAll(acct.ID, "-", "") || reason != "customer already contacted" {
		t.Fatalf("dismissed row state=%s by=%s reason=%q", state, by, reason)
	}
	check(request("POST", form, stale.Result().Cookies(), true), http.StatusConflict)

	if _, err := pool.Exec(t.Context(), `INSERT INTO support_staff(account_id,revision,role,profile) VALUES($1,1,'revoked','{}')`, acct.ID); err != nil {
		t.Fatal(err)
	}
	check(request("GET", nil, nil, true), http.StatusForbidden)
}
