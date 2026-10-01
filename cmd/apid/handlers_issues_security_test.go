//go:build !no_pg

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestIssueOTLPThroughProductionRouter(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-otlp")
	token := issueCreateToken(t, e, app.Slug, issueSeedDeployment(t, e, app, 1))
	body := map[string]any{"resourceLogs": []any{map[string]any{"scopeLogs": []any{map[string]any{"logRecords": []any{map[string]any{"timeUnixNano": fmt.Sprint(time.Now().UnixNano()), "attributes": []any{map[string]any{"key": "exception.type", "value": map[string]string{"stringValue": "DateError"}}, map[string]any{"key": "exception.message", "value": map[string]string{"stringValue": "invalid format"}}}}}}}}}}
	path := "/v1/apps/" + app.Slug + "/issue-events/otlp/logs"
	for range 2 {
		if w := e.do(t, "POST", path, body, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 200 {
			t.Fatalf("export %d %s", w.Code, w.Body.String())
		}
	}
	list := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues", nil, nil), 200)
	if len(list.Items) != 1 || list.Items[0].EventCount != 1 {
		t.Fatalf("export retry not idempotent: %+v", list)
	}
	if w := e.do(t, "POST", path, map[string]any{"resourceLogs": "invalid"}, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 400 {
		t.Fatal("invalid OTLP export accepted")
	}
}

func TestIssueCredentialContextAndQuotaBoundaries(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-boundary")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	now := time.Now().UTC()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "failed"}
	for _, field := range []string{"account_id", "deployment_id", "customer_id", "tenant_id", "locals"} {
		raw, _ := json.Marshal(event)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		body[field] = uuid.NewString()
		if w := e.do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", body, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 400 {
			t.Fatalf("accepted forged %s: %d", field, w.Code)
		}
	}
	other := seedPGApp(t, e, "issue-worker-other")
	inv, err := e.store.EnqueueInvocation(t.Context(), state.Invocation{AppID: other.ID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke, State: state.InvocationPending, Method: "POST", Path: "/", Payload: json.RawMessage(`{}`), Headers: json.RawMessage(`{}`), DueAt: now})
	if err != nil {
		t.Fatal(err)
	}
	event.InvocationID = inv.ID
	if w := issueSend(t, e, app.Slug, token, event); w.Code != 404 {
		t.Fatalf("foreign worker accepted %d %s", w.Code, w.Body.String())
	}
	event.InvocationID = ""
	lim := e.acct.Plan.IssueLimits()
	lim.EventsPerApp = 1
	normalized, fp, title, err := issues.Normalize(event, now, lim)
	if err != nil {
		t.Fatal(err)
	}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: e.acct.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: normalized, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(event), GroupingVersion: issues.GroupingVersion, Limits: lim, Now: now}
	st := e.store.(state.IssueStore)
	if _, err = st.RecordIssue(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	in.Event.EventID = uuid.NewString()
	in.PayloadHash = issues.PayloadDigest(in.Event)
	_, err = st.RecordIssue(t.Context(), in)
	var limit *state.IssueLimitError
	if !errors.As(err, &limit) || limit.Limit != 1 || limit.Observed != 2 {
		t.Fatalf("quota error lacks evidence: %v", err)
	}
	if _, err = st.FindIssueToken(t.Context(), []byte("wrong"), now); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unknown credential found")
	}
}

func TestIssueDashboardEscapingAndCSRF(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-dashboard")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: `<script>alert("x")</script>`}
	result := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	unassigned := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues?assignee=unassigned", nil, nil), 200)
	if len(unassigned.Items) != 1 || unassigned.Items[0].ID != result.IssueID {
		t.Fatalf("unassigned issue filter = %+v", unassigned.Items)
	}
	mine := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues?assignee=me", nil, nil), 200)
	if len(mine.Items) != 0 {
		t.Fatalf("mine filter included unassigned issue: %+v", mine.Items)
	}
	page := "/dashboard/apps/" + app.Slug + "/issues?issue=" + result.IssueID + "&assignee=unassigned"
	r := httptest.NewRequest("GET", page, nil)
	e.addAdminSession(t, r)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("dashboard %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `<script>alert`) || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("exception HTML not escaped")
	}
	for _, want := range []string{"<th>Owner</th>", "<th>Verified customers (24h)</th>", "<th>Events (24h)</th>", "<th>Unattributed events (24h)</th>", "<th>Recurrences</th>", "Most recently seen", "Most verified customers (24h)", "Minimum verified customers (24h)", "Unassigned", `value="unassigned" selected`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("issues inbox missing %q", want)
		}
	}
	var csrf *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == dashboardIssueCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("no scoped CSRF credential")
	}
	action := "/dashboard/apps/" + app.Slug + "/issues/" + result.IssueID + "/actions"
	for _, valid := range []bool{false, true} {
		form := url.Values{"action": {"resolve"}, "fixed_deployment_id": {dep.ID}}
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		req := httptest.NewRequest("POST", action, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range r.Cookies() {
			req.AddCookie(c)
		}
		req.AddCookie(csrf)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if valid && rec.Code != 303 {
			t.Fatalf("valid form %d %s", rec.Code, rec.Body.String())
		}
		if !valid && rec.Code < 400 {
			t.Fatal("CSRF bypass")
		}
	}
}

func TestIssueDashboardReplayQueuesMetadataOnlyMirrorInvocation(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	app := seedPGApp(t, e, "issue-replay")
	source, err := e.store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("a", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployPending, CreatedAt: time.Now().UTC(), Revision: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), source.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(source): %v", err)
	}
	target, err := e.store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("b", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployPending, CreatedAt: time.Now().UTC(), Revision: 43, Scope: "mirror-replay",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), target.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(target): %v", err)
	}
	rule, err := e.store.CreateMirrorRuleIfUnderQuota(t.Context(), state.CreateMirrorRuleParams{
		AccountID: e.acct.ID, AppID: app.ID, SourceDeploymentID: source.ID,
		MirrorDeploymentID: target.ID, Percent: 100, Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateMirrorRuleIfUnderQuota: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	traceID := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := e.store.InsertRequestTelemetry(t.Context(), sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: uuid.MustParse(e.acct.ID), Valid: true},
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(source.ID), Valid: true},
		Route:        "GET /exports", Method: "GET", Status: 500, LatencyMs: 120,
		TraceID:    pgtype.Text{String: traceID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: now, Valid: true}, Count: 1,
		UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
	}); err != nil {
		t.Fatalf("InsertRequestTelemetry: %v", err)
	}
	token := issueCreateToken(t, e, app.Slug, source)
	event := api.IssueEvent{
		EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "DateFormatError",
		Message: "export date format rejected", TraceID: traceID,
	}
	created := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	detail := issueDecode[api.IssueDetail](t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issues/"+created.IssueID, nil, nil), http.StatusOK)
	if len(detail.Events) != 1 || detail.Events[0].DebugRequestID == "" {
		t.Fatalf("issue occurrence missing linked request: %+v", detail.Events)
	}
	occurrence := detail.Events[0]
	pageURL := "/dashboard/apps/" + app.Slug + "/issues?issue=" + created.IssueID
	pageReq := httptest.NewRequest(http.MethodGet, pageURL, nil)
	e.addAdminSession(t, pageReq)
	pageRec := httptest.NewRecorder()
	e.h.ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("issue page %d: %s", pageRec.Code, pageRec.Body.String())
	}
	pageBody := pageRec.Body.String()
	if !strings.Contains(pageBody, "Replay metadata to mirror") || !strings.Contains(pageBody, target.ID) || !strings.Contains(pageBody, "request bodies and credentials are excluded") {
		t.Fatalf("issue replay form is missing target or safety copy: %s", pageBody)
	}
	var replayCSRF *http.Cookie
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == dashboardDebugReplayCSRFCookie {
			replayCSRF = cookie
		}
	}
	if replayCSRF == nil {
		t.Fatal("issue page did not issue debugger replay CSRF cookie")
	}

	form := url.Values{
		"csrf_token": {replayCSRF.Value}, "mirror_deployment_id": {target.ID},
		"return_issue_id": {created.IssueID}, "return_event_id": {occurrence.ID},
		"return_event_cursor": {""}, "return_since": {""},
	}
	postReq := httptest.NewRequest(http.MethodPost,
		"/dashboard/apps/"+app.Slug+"/debug/requests/"+occurrence.DebugRequestID+"/replay",
		strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	e.addAdminSession(t, postReq)
	postReq.AddCookie(replayCSRF)
	postRec := httptest.NewRecorder()
	e.h.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusSeeOther {
		t.Fatalf("replay POST %d: %s", postRec.Code, postRec.Body.String())
	}
	location, err := url.Parse(postRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("replay redirect: %v", err)
	}
	if location.Path != "/dashboard/apps/"+app.Slug+"/issues" || location.Query().Get("issue") != created.IssueID || location.Query().Get("replay_event") != occurrence.ID {
		t.Fatalf("replay redirect lost issue scope: %s", location)
	}
	replayID := location.Query().Get("replay_id")
	if _, err := uuid.Parse(replayID); err != nil {
		t.Fatalf("replay redirect id %q is not an invocation ID", replayID)
	}
	invocation, err := e.store.InvocationByID(t.Context(), replayID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if invocation.Source != state.InvocationReplay || (len(invocation.Payload) != 0 && string(invocation.Payload) != "{}") {
		t.Fatalf("replay invocation carried payload or wrong source: source=%s payload=%q", invocation.Source, invocation.Payload)
	}
	var metadata map[string]string
	if err := json.Unmarshal(invocation.Headers, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata[api.DebugReplayDeploymentIDHeader] != source.ID || metadata[api.DebugReplayMirrorRuleIDHeader] != rule.ID {
		t.Fatalf("replay metadata = %#v, want source %s and mirror rule %s", metadata, source.ID, rule.ID)
	}

	receiptReq := httptest.NewRequest(http.MethodGet, location.String(), nil)
	e.addAdminSession(t, receiptReq)
	receiptRec := httptest.NewRecorder()
	e.h.ServeHTTP(receiptRec, receiptReq)
	if receiptRec.Code != http.StatusOK {
		t.Fatalf("issue receipt %d: %s", receiptRec.Code, receiptRec.Body.String())
	}
	if body := receiptRec.Body.String(); !strings.Contains(body, "Mirror replay queued") || !strings.Contains(body, target.ID) || !strings.Contains(body, "Open replay in the request debugger") {
		t.Fatalf("queued receipt is incomplete: %s", body)
	}
}

func TestIssueAutomaticHTTPSource(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-http")
	dep := issueSeedDeployment(t, e, app, 1)
	req := &apidpb.IncrementAppErrorRequest{AccountId: e.acct.ID, AppId: app.ID, DeploymentId: dep.ID, RequestId: uuid.NewString(), ReceivedAtUnixMs: time.Now().UnixMilli(), HttpStatus: 500, ErrorClass: "HTTPError", Fingerprint: strings.Repeat("a", 64), RouteTemplate: "/exports"}
	if err := recordHTTPIssue(t.Context(), e.store, req); err != nil {
		t.Fatal(err)
	}
	if err := recordHTTPIssue(t.Context(), e.store, req); err != nil {
		t.Fatal(err)
	}
	list := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues", nil, nil), 200)
	if len(list.Items) != 1 || list.Items[0].EventCount != 1 {
		t.Fatal("HTTP observation retry inflated issues")
	}
}

func TestIssueLaterReleaseAndRetention(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-maintain")
	fixed := issueSeedDeployment(t, e, app, 1)
	later := issueSeedDeployment(t, e, app, 2)
	token := issueCreateToken(t, e, app.Slug, fixed)
	otherToken := issueCreateToken(t, e, app.Slug, later)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failed"}
	out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	base := "/v1/apps/" + app.Slug + "/issues/" + out.IssueID
	issueDecode[api.Issue](t, e.do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID}, nil), 200)
	event.EventID = uuid.NewString()
	event.OccurredAt = time.Now().UTC()
	if result := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, otherToken, event), 202); !result.Regressed {
		t.Fatal("newer release did not reopen")
	}
	until := time.Now().Add(time.Minute)
	issueDecode[api.Issue](t, e.do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "ignore", IgnoredUntil: &until}, nil), 200)
	maintenance := e.store.(interface {
		MaintainIssues(ctx context.Context, now time.Time) error
	})
	if err := maintenance.MaintainIssues(t.Context(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", base, nil, nil), 200)
	if detail.Issue.State != "open" {
		t.Fatal("ignore expiry did not reopen")
	}
	if err := maintenance.MaintainIssues(t.Context(), time.Now().AddDate(0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	detail = issueDecode[api.IssueDetail](t, e.do(t, "GET", base, nil, nil), 200)
	if len(detail.Events) != 0 || detail.Issue.EventCount != 2 || len(detail.Releases) != 2 {
		t.Fatal("retention removed durable history or retained occurrences")
	}
}

func TestIssueLateAttributionAndHistoryPagination(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-late")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	request, tenant, consumer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DateError", Message: "invalid format", RequestID: request}
	out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	base := "/v1/apps/" + app.Slug + "/issues/" + out.IssueID
	if detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", base, nil, nil), 200); detail.Impact.UnattributedEvents != 1 {
		t.Fatal("invented early attribution")
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,'late','late')`, tenant, e.acct.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO api_consumers(id,account_id,app_id,external_ref,name,platform_tenant_id) VALUES($1,$2,$3,'late','late',$4)`, consumer, e.acct.ID, app.ID, tenant); err != nil {
		t.Fatal(err)
	}
	auditEvent := uuid.NewString()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units,platform_tenant_id) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0,$5)`, auditEvent, e.acct.ID, app.ID, consumer, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,platform_tenant_id,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id) VALUES($1,$2,$3,$4,$5,'/exports','POST',500,10,$6,now(),$7)`, auditEvent, e.acct.ID, app.ID, consumer, tenant, dep.ID, request); err != nil {
		t.Fatal(err)
	}
	maintenance := e.store.(interface {
		MaintainIssues(context.Context, time.Time) error
	})
	if err := maintenance.MaintainIssues(t.Context(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", base, nil, nil), 200)
	if detail.Impact.IdentifiedCustomers != 1 || detail.Events[0].VerifiedPlatformTenantID != tenant {
		t.Fatal("late verified attribution missing")
	}
	for i := 0; i < 51; i++ {
		event.EventID = uuid.NewString()
		issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
		assignee := ""
		if i%2 == 0 {
			assignee = e.acct.ID
		}
		issueDecode[api.Issue](t, e.do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "assign", AssigneeAccountID: assignee}, nil), 200)
	}
	detail = issueDecode[api.IssueDetail](t, e.do(t, "GET", base, nil, nil), 200)
	if len(detail.Events) != 50 || detail.NextEventCursor == "" || len(detail.Activity) != 50 || detail.NextActivityCursor == "" {
		t.Fatal("history truncated without cursors")
	}
	page := issueDecode[api.IssueDetail](t, e.do(t, "GET", base+"?event_cursor="+url.QueryEscape(detail.NextEventCursor)+"&activity_cursor="+url.QueryEscape(detail.NextActivityCursor), nil, nil), 200)
	if len(page.Events) != 2 || len(page.Activity) != 2 {
		t.Fatalf("history pagination skipped rows: %d events %d activities", len(page.Events), len(page.Activity))
	}
	seen := map[string]bool{}
	for _, e := range detail.Events {
		seen[e.ID] = true
	}
	for _, e := range page.Events {
		if seen[e.ID] {
			t.Fatal("duplicate cursor occurrence")
		}
	}
}

type unavailableIssueCredentialStore struct {
	state.Store
	state.IssueStore
}

func (unavailableIssueCredentialStore) FindIssueToken(context.Context, []byte, time.Time) (state.IssueCredential, error) {
	return state.IssueCredential{}, errors.New("temporary database failure")
}

func TestIssueCredentialOutageIsRetryable(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	e.s.store = unavailableIssueCredentialStore{e.store, e.store.(state.IssueStore)}
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error"}
	w := e.do(t, "POST", "/v1/apps/any-app/issue-events", event, map[string]string{"Authorization": "Bearer g_issue_test"})
	if w.Code != 503 {
		t.Fatalf("credential lookup outage would drop SDK events: %d %s", w.Code, w.Body.String())
	}
}

func TestIssueClockSkewPagination(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-clock")
	token := issueCreateToken(t, e, app.Slug, issueSeedDeployment(t, e, app, 1))
	for i := 0; i <= api.IssuePageSize; i++ {
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC().Add(api.IssueMaxClockSkew - time.Minute), ExceptionType: fmt.Sprintf("ClockError%d", i)}
		issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	}
	path := "/v1/apps/" + app.Slug + "/issues"
	first := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", path, nil, nil), 200)
	if len(first.Items) != api.IssuePageSize || first.NextCursor == "" {
		t.Fatal("no issue continuation cursor")
	}
	second := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", path+"?cursor="+url.QueryEscape(first.NextCursor), nil, nil), 200)
	if len(second.Items) != 1 {
		t.Fatal("accepted clock skew cannot be paginated")
	}
	invalid := state.EncodeIssueCursor(state.IssueCursor{Time: time.Now().Add(api.IssueMaxClockSkew + time.Minute), ID: uuid.NewString()})
	if w := e.do(t, "GET", path+"?cursor="+url.QueryEscape(invalid), nil, nil); w.Code != 400 {
		t.Fatal("unbounded future cursor accepted")
	}
}
