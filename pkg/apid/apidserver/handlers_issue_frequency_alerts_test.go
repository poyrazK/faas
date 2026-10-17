//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueFrequencyAlertsPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "frequency-alerts")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	store := apidserver.IssueNativeStore(e)
	frequency := store.(state.IssueFrequencyAlertStore)
	issueStore := store.(state.IssueStore)
	endpoint := "/v1/apps/" + app.Slug + "/issue-frequency-alert-policy"
	policy := apidserver.IssueDecode[api.IssueFrequencyAlertPolicy](t, e.Do(t, http.MethodGet, endpoint, nil, nil), 200)
	if policy.Revision != 0 || len(policy.Rules) != 0 {
		t.Fatalf("default=%+v", policy)
	}
	zero := int64(0)
	if _, err := frequency.SetIssueFrequencyAlertPolicy(t.Context(), app.ID, app.AccountID, api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &zero}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("nil rules accepted by state: %v", err)
	}
	rules := []api.IssueFrequencyAlertRule{
		{Key: "burst", Enabled: true, Environment: "application", MinimumEvents: 3, WindowSeconds: 300, CooldownSeconds: 600},
		{Key: "staging", Enabled: true, Environment: "staging", MinimumEvents: 1, WindowSeconds: 60, CooldownSeconds: 60},
	}
	put := func(revision int64, rules []api.IssueFrequencyAlertRule, status int) api.IssueFrequencyAlertPolicy {
		w := e.Do(t, http.MethodPut, endpoint, api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &revision, Rules: rules}, nil)
		if status != 200 {
			if w.Code != status {
				t.Fatalf("save status=%d want=%d: %s", w.Code, status, w.Body.String())
			}
			return api.IssueFrequencyAlertPolicy{}
		}
		return apidserver.IssueDecode[api.IssueFrequencyAlertPolicy](t, w, 200)
	}
	if w := e.Do(t, http.MethodPut, endpoint, api.UpdateIssueFrequencyAlertPolicyRequest{Rules: rules}, nil); w.Code != 400 {
		t.Fatalf("missing revision=%d", w.Code)
	}
	if w := e.Do(t, http.MethodPut, endpoint, map[string]any{"expected_revision": 0}, nil); w.Code != 400 {
		t.Fatalf("missing rules=%d", w.Code)
	}
	unauthenticated := httptest.NewRecorder()
	(*e.H()).ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, endpoint, nil))
	if unauthenticated.Code != 401 {
		t.Fatalf("unauthenticated policy read=%d", unauthenticated.Code)
	}
	if w := e.Do(t, http.MethodPut, endpoint, api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &zero, Rules: rules}, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 401 {
		t.Fatalf("ingest credential configured rules: %d", w.Code)
	}
	invalid := append([]api.IssueFrequencyAlertRule{}, rules...)
	invalid[0].WindowSeconds = 59
	put(0, invalid, 400)
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	if w := e.Do(t, http.MethodGet, endpoint, nil, nil); w.Code != 402 {
		t.Fatalf("free plan frequency access=%d %s", w.Code, w.Body.String())
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET plan='pro' WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	policy = put(0, rules, 200)
	put(0, rules, 409)
	if policy.Revision != 1 {
		t.Fatalf("first revision=%d", policy.Revision)
	}
	reordered := []api.IssueFrequencyAlertRule{rules[1], rules[0]}
	if unchanged := put(1, reordered, 200); unchanged.Revision != 1 {
		t.Fatal("no-op reset revision")
	}
	if _, err := frequency.GetIssueFrequencyAlertPolicy(t.Context(), app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign read=%v", err)
	}
	if _, err := frequency.SetIssueFrequencyAlertPolicy(t.Context(), app.ID, uuid.NewString(), api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &zero, Rules: rules}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign save=%v", err)
	}
	hook, err := store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, Enabled: true, TargetURL: "https://example.com/frequency", EventFilter: []string{"issue.frequency_threshold_reached"}, SecretSealed: []byte("test-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	params := func(at, occurred time.Time, key string) state.RecordIssueParams {
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: occurred, ExceptionType: "FrequencyError", Message: "repeated failure", FingerprintOverride: key}
		normalized, fp, title, err := issues.Normalize(event, at, api.PlanPro.IssueLimits())
		if err != nil {
			t.Fatal(err)
		}
		return state.RecordIssueParams{Credential: state.IssueCredential{AccountID: app.AccountID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: normalized, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(event), GroupingVersion: issues.GroupingVersionFor(normalized), Limits: api.PlanPro.IssueLimits(), Now: at}
	}
	capture := func(p state.RecordIssueParams) api.IssueEventResponse {
		out, err := issueStore.RecordIssue(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	alerts := func(want int) {
		t.Helper()
		var got int
		if err := (*e.Pool()).QueryRow(t.Context(), `SELECT count(*) FROM issue_activity a JOIN app_issues i ON i.id=a.issue_id WHERE i.app_id=$1 AND a.action='frequency_threshold_reached'`, app.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("frequency alerts=%d want=%d", got, want)
		}
	}
	first := capture(params(base, base, "main"))
	capture(params(base, base, "main"))
	alerts(0)
	third := params(base, base, "main")
	capture(third)
	alerts(1)
	if duplicate := capture(third); !duplicate.Duplicate {
		t.Fatal("exact retry was not duplicate")
	}
	capture(params(base.Add(time.Second), base.Add(time.Second), "main"))
	alerts(1)
	// New occurrences can repeat after cooldown even after old evidence expired.
	next := base.Add(600 * time.Second)
	capture(params(next, next, "main"))
	capture(params(next, next, "main"))
	alerts(1)
	capture(params(next, next, "main"))
	alerts(2)
	capture(params(next, next.Add(-301*time.Second), "main"))
	alerts(2)
	capture(params(next, next.Add(time.Second), "main"))
	alerts(2)
	// Ignore suppresses a fresh threshold, but retains evidence. Reopening sends
	// nothing; the next qualifying event can alert outside the prior cooldown.
	until := base.Add(3000 * time.Second)
	detailURL := "/v1/apps/" + app.Slug + "/issues/" + first.IssueID
	apidserver.IssueDecode[api.Issue](t, e.Do(t, http.MethodPost, detailURL+"/actions", api.IssueActionRequest{Action: "ignore", IgnoredUntil: &until}, nil), 200)
	next = base.Add(1200 * time.Second)
	for range 3 {
		capture(params(next, next, "main"))
	}
	alerts(2)
	apidserver.IssueDecode[api.Issue](t, e.Do(t, http.MethodPost, detailURL+"/actions", api.IssueActionRequest{Action: "reopen"}, nil), 200)
	alerts(2)
	next = next.Add(time.Second)
	capture(params(next, next, "main"))
	alerts(3)
	// Toggle and unrelated rule edits preserve burst's receipt.
	rules[0].Enabled = false
	policy = put(policy.Revision, rules, 200)
	capture(params(next.Add(time.Second), next.Add(time.Second), "main"))
	alerts(3)
	rules[0].Enabled = true
	policy = put(policy.Revision, rules, 200)
	capture(params(next.Add(2*time.Second), next.Add(2*time.Second), "main"))
	alerts(3)
	rules[1].MinimumEvents = 2
	policy = put(policy.Revision, rules, 200)
	capture(params(next.Add(3*time.Second), next.Add(3*time.Second), "main"))
	alerts(3)
	rules[0].MinimumEvents = 2
	policy = put(policy.Revision, rules, 200)
	next = next.Add(4 * time.Second)
	capture(params(next, next.Add(-301*time.Second), "main"))
	alerts(3)
	capture(params(next, next.Add(time.Second), "main"))
	alerts(3)
	capture(params(next, next, "main"))
	alerts(4)
	// Concurrent captures after a cooldown produce one transition under app lock.
	next = next.Add(600 * time.Second)
	requests := make([]state.RecordIssueParams, 6)
	for i := range requests {
		requests[i] = params(next, next, "main")
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(requests))
	for _, p := range requests {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := issueStore.RecordIssue(t.Context(), p); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	alerts(5)
	// A failed receipt write rolls back capture, activity and snapshotted outbox.
	failureTime := next.Add(time.Second)
	capture(params(failureTime, failureTime, "rollback"))
	failed := params(failureTime, failureTime, "rollback")
	if _, err := (*e.Pool()).Exec(t.Context(), `CREATE FUNCTION fail_frequency_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$; CREATE TRIGGER fail_frequency_receipt BEFORE INSERT ON issue_frequency_alert_receipts FOR EACH ROW EXECUTE FUNCTION fail_frequency_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err := issueStore.RecordIssue(t.Context(), failed); err == nil {
		t.Fatal("receipt failure was ignored")
	}
	alerts(5)
	var exists bool
	if err := (*e.Pool()).QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM issue_events WHERE app_id=$1 AND event_id=$2)", app.ID, failed.Event.EventID).Scan(&exists); err != nil || exists {
		t.Fatalf("failed capture persisted=%v err=%v", exists, err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "DROP TRIGGER fail_frequency_receipt ON issue_frequency_alert_receipts; DROP FUNCTION fail_frequency_receipt()"); err != nil {
		t.Fatal(err)
	}
	capture(failed)
	alerts(6)
	// Merge combines evidence and respects the latest cooldown among members.
	source := capture(params(failureTime, failureTime, "merge-source"))
	capture(params(failureTime, failureTime, "merge-source"))
	target := capture(params(failureTime, failureTime, "merge-target"))
	capture(params(failureTime, failureTime, "merge-target"))
	alerts(8)
	if _, err := issueStore.ActOnIssue(t.Context(), app.ID, source.IssueID, app.AccountID, api.IssueActionRequest{Action: "merge", TargetIssueID: target.IssueID}, failureTime); err != nil {
		t.Fatal(err)
	}
	capture(params(failureTime.Add(time.Second), failureTime.Add(time.Second), "merge-source"))
	alerts(8)
	// Restarts use persisted receipts, and the durable relay emits each confirmed
	// transition once without contacting the external webhook target.
	relay := store.(state.AppWebhookEventOutboxStore)
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 8 {
		t.Fatalf("relay=%d err=%v", n, err)
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 0 {
		t.Fatalf("repeated relay=%d err=%v", n, err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 20, "")
	if err != nil || len(deliveries) != 8 {
		t.Fatalf("deliveries=%d err=%v", len(deliveries), err)
	}
	for _, delivery := range deliveries {
		var payload struct {
			Type    string            `json:"type"`
			Details map[string]string `json:"details"`
		}
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type != "issue.frequency_threshold_reached" {
			t.Fatalf("delivery type=%q", payload.Type)
		}
		for key, want := range map[string]string{"rule_key": "burst", "window_seconds": "300", "cooldown_seconds": "600", "environment": "application"} {
			if payload.Details[key] != want {
				t.Fatalf("payload %s=%q want=%q", key, payload.Details[key], want)
			}
		}
		if payload.Details["observed_events"] == "" || payload.Details["minimum_events"] == "" || payload.Details["policy_revision"] == "" {
			t.Fatalf("missing threshold metadata: %+v", payload.Details)
		}
	}

	// Removing rules prunes receipt state. A new all-environment rule also works
	// through the public ingestion router, and no configuration write backfills.
	policy = put(policy.Revision, []api.IssueFrequencyAlertRule{}, 200)
	alerts(8)
	var receipts int
	if err := (*e.Pool()).QueryRow(t.Context(), "SELECT count(*) FROM issue_frequency_alert_receipts WHERE app_id=$1", app.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("pruned receipts=%d err=%v", receipts, err)
	}
	policy = put(policy.Revision, []api.IssueFrequencyAlertRule{{Key: "all", Enabled: true, MinimumEvents: 1, WindowSeconds: 60, CooldownSeconds: 60}}, 200)
	alerts(8)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "PublicFrequencyError", Message: "public capture"}
	apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	alerts(9)
	// Dashboard controls use named CSRF and optimistic revision.
	get := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+app.Slug+"/issues", nil)
	e.AddAdminSession(t, get)
	page := httptest.NewRecorder()
	(*e.H()).ServeHTTP(page, get)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Event-frequency alerts") {
		t.Fatalf("dashboard=%d %s", page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == apidserver.DashboardIssueCookie {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("no dashboard CSRF token")
	}
	form := url.Values{"action": {"save"}, "expected_revision": {fmt.Sprint(policy.Revision)}, "key": {"all"}, "enabled": {"on"}, "minimum_events": {"2"}, "window_seconds": {"60"}, "cooldown_seconds": {"60"}}
	post := func(valid bool) *httptest.ResponseRecorder {
		if valid {
			form.Set("csrf_token", csrf.Value)
		} else {
			form.Del("csrf_token")
		}
		req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/"+app.Slug+"/issues/frequency-alerts", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, cookie := range get.Cookies() {
			req.AddCookie(cookie)
		}
		req.AddCookie(csrf)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, req)
		return w
	}
	if w := post(false); w.Code != 400 {
		t.Fatalf("CSRF bypass=%d", w.Code)
	}
	if w := post(true); w.Code != 303 {
		t.Fatalf("dashboard save=%d %s", w.Code, w.Body.String())
	}
	if w := post(true); w.Code != 409 {
		t.Fatalf("stale form=%d %s", w.Code, w.Body.String())
	}
	// Concurrent policy writers cannot overwrite each other.
	current, err := frequency.GetIssueFrequencyAlertPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	configErrors := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := current.Rules[0]
			r.MinimumEvents = int64(10 + i)
			_, err := frequency.SetIssueFrequencyAlertPolicy(t.Context(), app.ID, app.AccountID, api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &current.Revision, Rules: []api.IssueFrequencyAlertRule{r}})
			configErrors <- err
		}()
	}
	wg.Wait()
	close(configErrors)
	wins, conflicts := 0, 0
	for err := range configErrors {
		if err == nil {
			wins++
		} else if errors.Is(err, state.ErrIssueFrequencyAlertRevision) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent policy wins=%d conflicts=%d", wins, conflicts)
	}
}
