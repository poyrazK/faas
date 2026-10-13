//go:build !no_pg

package apidserver_test

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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/receivers"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestIssueOTLPThroughProductionRouter(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-otlp")
	token := apidserver.IssueCreateToken(t, e, app.Slug, apidserver.IssueSeedDeployment(t, e, app, 1))
	body := map[string]any{"resourceLogs": []any{map[string]any{"scopeLogs": []any{map[string]any{"logRecords": []any{map[string]any{"timeUnixNano": fmt.Sprint(time.Now().UnixNano()), "attributes": []any{map[string]any{"key": "exception.type", "value": map[string]string{"stringValue": "DateError"}}, map[string]any{"key": "exception.message", "value": map[string]string{"stringValue": "invalid format"}}}}}}}}}}
	path := "/v1/apps/" + app.Slug + "/issue-events/otlp/logs"
	for range 2 {
		if w := e.Do(t, "POST", path, body, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 200 {
			t.Fatalf("export %d %s", w.Code, w.Body.String())
		}
	}
	list := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues", nil, nil), 200)
	if len(list.Items) != 1 || list.Items[0].EventCount != 1 {
		t.Fatalf("export retry not idempotent: %+v", list)
	}
	if w := e.Do(t, "POST", path, map[string]any{"resourceLogs": "invalid"}, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 400 {
		t.Fatal("invalid OTLP export accepted")
	}
}

func TestIssueCredentialContextAndQuotaBoundaries(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-boundary")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	now := time.Now().UTC()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "failed"}
	for _, field := range []string{"account_id", "deployment_id", "customer_id", "tenant_id", "locals"} {
		raw, _ := json.Marshal(event)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		body[field] = uuid.NewString()
		if w := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", body, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 400 {
			t.Fatalf("accepted forged %s: %d", field, w.Code)
		}
	}
	other := apidserver.SeedPGApp(t, e, "issue-worker-other")
	inv, err := (*e.Store()).EnqueueInvocation(t.Context(), state.Invocation{AppID: other.ID, AccountID: (*e.Acct()).ID, Source: state.InvocationAsyncInvoke, State: state.InvocationPending, Method: "POST", Path: "/", Payload: json.RawMessage(`{}`), Headers: json.RawMessage(`{}`), DueAt: now})
	if err != nil {
		t.Fatal(err)
	}
	event.InvocationID = inv.ID
	if w := apidserver.IssueSend(t, e, app.Slug, token, event); w.Code != 404 {
		t.Fatalf("foreign worker accepted %d %s", w.Code, w.Body.String())
	}
	event.InvocationID = ""
	lim := (*e.Acct()).Plan.IssueLimits()
	lim.EventsPerApp = 1
	normalized, fp, title, err := issues.Normalize(event, now, lim)
	if err != nil {
		t.Fatal(err)
	}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: (*e.Acct()).ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: normalized, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(event), GroupingVersion: issues.GroupingVersion, Limits: lim, Now: now}
	st := (*e.Store()).(state.IssueStore)
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
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-dashboard")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: `<script>alert("x")</script>`}
	result := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	unassigned := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues?assignee=unassigned", nil, nil), 200)
	if len(unassigned.Items) != 1 || unassigned.Items[0].ID != result.IssueID {
		t.Fatalf("unassigned issue filter = %+v", unassigned.Items)
	}
	mine := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues?assignee=me", nil, nil), 200)
	if len(mine.Items) != 0 {
		t.Fatalf("mine filter included unassigned issue: %+v", mine.Items)
	}
	page := "/dashboard/apps/" + app.Slug + "/issues?issue=" + result.IssueID + "&assignee=unassigned"
	r := httptest.NewRequest("GET", page, nil)
	e.AddAdminSession(t, r)
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("dashboard %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `<script>alert`) || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("exception HTML not escaped")
	}
	for _, want := range []string{"<th>Owner</th>", "<th>Verified customers (24h)</th>", "<th>Events (24h)</th>", "<th>Unattributed events (24h)</th>", "<th>Recurrences</th>", "Most recently seen", "Most verified customers (24h)", "Minimum verified customers (24h)", "issue.impact_threshold_reached", "Customer-impact webhook alert", "Automatic issue ownership", "rules_json", "Unassigned", `value="unassigned" selected`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("issues inbox missing %q", want)
		}
	}
	var csrf *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
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
		(*e.H()).ServeHTTP(rec, req)
		if valid && rec.Code != 303 {
			t.Fatalf("valid form %d %s", rec.Code, rec.Body.String())
		}
		if !valid && rec.Code < 400 {
			t.Fatal("CSRF bypass")
		}
	}
	form := url.Values{"csrf_token": {csrf.Value}, "minimum_customers": {"3"}}
	policyRequest := httptest.NewRequest("POST", "/dashboard/apps/"+app.Slug+"/issues/impact-alert-policy", strings.NewReader(form.Encode()))
	policyRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range r.Cookies() {
		policyRequest.AddCookie(c)
	}
	policyRequest.AddCookie(csrf)
	policyResponse := httptest.NewRecorder()
	(*e.H()).ServeHTTP(policyResponse, policyRequest)
	if policyResponse.Code != http.StatusSeeOther {
		t.Fatalf("policy form %d %s", policyResponse.Code, policyResponse.Body.String())
	}
	policy := apidserver.IssueDecode[api.IssueImpactAlertPolicy](t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issue-impact-alert-policy", nil, nil), http.StatusOK)
	if !policy.Enabled || policy.MinimumCustomers != 3 {
		t.Fatalf("dashboard did not persist impact policy: %+v", policy)
	}
	rulesJSON, err := json.Marshal(api.IssueOwnershipRules{Rules: []api.IssueOwnershipRule{{SourceKind: "worker", AssigneeAccountID: (*e.Acct()).ID}}})
	if err != nil {
		t.Fatal(err)
	}
	ruleForm := url.Values{"csrf_token": {csrf.Value}, "rules_json": {string(rulesJSON)}}
	ruleRequest := httptest.NewRequest("POST", "/dashboard/apps/"+app.Slug+"/issues/ownership-rules", strings.NewReader(ruleForm.Encode()))
	ruleRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range r.Cookies() {
		ruleRequest.AddCookie(c)
	}
	ruleRequest.AddCookie(csrf)
	ruleResponse := httptest.NewRecorder()
	(*e.H()).ServeHTTP(ruleResponse, ruleRequest)
	if ruleResponse.Code != http.StatusSeeOther {
		t.Fatalf("ownership rules form %d %s", ruleResponse.Code, ruleResponse.Body.String())
	}
	configuredRules := apidserver.IssueDecode[api.IssueOwnershipRules](t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issue-ownership-rules", nil, nil), http.StatusOK)
	if len(configuredRules.Rules) != 1 || configuredRules.Rules[0].SourceKind != "worker" {
		t.Fatalf("dashboard did not persist ownership rules: %+v", configuredRules)
	}
}

func TestIssueDashboardReplayQueuesMetadataOnlyMirrorInvocation(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "issue-replay")
	source, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("a", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployPending, CreatedAt: time.Now().UTC(), Revision: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(t.Context(), source.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(source): %v", err)
	}
	target, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("b", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployPending, CreatedAt: time.Now().UTC(), Revision: 43, Scope: "mirror-replay",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(t.Context(), target.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(target): %v", err)
	}
	rule, err := (*e.Store()).CreateMirrorRuleIfUnderQuota(t.Context(), state.CreateMirrorRuleParams{
		AccountID: (*e.Acct()).ID, AppID: app.ID, SourceDeploymentID: source.ID,
		MirrorDeploymentID: target.ID, Percent: 100, Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateMirrorRuleIfUnderQuota: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	traceID := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := (*e.Store()).InsertRequestTelemetry(t.Context(), sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: uuid.MustParse((*e.Acct()).ID), Valid: true},
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(source.ID), Valid: true},
		Route:        "GET /exports", Method: "GET", Status: 500, LatencyMs: 120,
		TraceID:    pgtype.Text{String: traceID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: now, Valid: true}, Count: 1,
		UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
	}); err != nil {
		t.Fatalf("InsertRequestTelemetry: %v", err)
	}
	token := apidserver.IssueCreateToken(t, e, app.Slug, source)
	event := api.IssueEvent{
		EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "DateFormatError",
		Message: "export date format rejected", TraceID: traceID,
	}
	created := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issues/"+created.IssueID, nil, nil), http.StatusOK)
	if len(detail.Events) != 1 || detail.Events[0].DebugRequestID == "" {
		t.Fatalf("issue occurrence missing linked request: %+v", detail.Events)
	}
	occurrence := detail.Events[0]
	pageURL := "/dashboard/apps/" + app.Slug + "/issues?issue=" + created.IssueID
	pageReq := httptest.NewRequest(http.MethodGet, pageURL, nil)
	e.AddAdminSession(t, pageReq)
	pageRec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("issue page %d: %s", pageRec.Code, pageRec.Body.String())
	}
	pageBody := pageRec.Body.String()
	if !strings.Contains(pageBody, "Replay metadata to mirror") || !strings.Contains(pageBody, target.ID) || !strings.Contains(pageBody, "request bodies and credentials are excluded") {
		t.Fatalf("issue replay form is missing target or safety copy: %s", pageBody)
	}
	var replayCSRF *http.Cookie
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == apidserver.DashboardDebugReplayCSRFCookie {
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
	e.AddAdminSession(t, postReq)
	postReq.AddCookie(replayCSRF)
	postRec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(postRec, postReq)
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
	invocation, err := (*e.Store()).InvocationByID(t.Context(), replayID)
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
	e.AddAdminSession(t, receiptReq)
	receiptRec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(receiptRec, receiptReq)
	if receiptRec.Code != http.StatusOK {
		t.Fatalf("issue receipt %d: %s", receiptRec.Code, receiptRec.Body.String())
	}
	if body := receiptRec.Body.String(); !strings.Contains(body, "Mirror replay queued") || !strings.Contains(body, target.ID) || !strings.Contains(body, "Open replay in the request debugger") {
		t.Fatalf("queued receipt is incomplete: %s", body)
	}
}

func TestIssueAutomaticHTTPSource(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-http")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	req := &apidpb.IncrementAppErrorRequest{AccountId: (*e.Acct()).ID, AppId: app.ID, DeploymentId: dep.ID, RequestId: uuid.NewString(), ReceivedAtUnixMs: time.Now().UnixMilli(), HttpStatus: 500, ErrorClass: "HTTPError", Fingerprint: strings.Repeat("a", 64), RouteTemplate: "/exports"}
	if err := receivers.RecordHTTPIssue(t.Context(), (*e.Store()), req); err != nil {
		t.Fatal(err)
	}
	if err := receivers.RecordHTTPIssue(t.Context(), (*e.Store()), req); err != nil {
		t.Fatal(err)
	}
	list := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues", nil, nil), 200)
	if len(list.Items) != 1 || list.Items[0].EventCount != 1 {
		t.Fatal("HTTP observation retry inflated issues")
	}
}

func TestIssueLaterReleaseAndRetention(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-maintain")
	fixed := apidserver.IssueSeedDeployment(t, e, app, 1)
	later := apidserver.IssueSeedDeployment(t, e, app, 2)
	token := apidserver.IssueCreateToken(t, e, app.Slug, fixed)
	otherToken := apidserver.IssueCreateToken(t, e, app.Slug, later)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failed"}
	out := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	base := "/v1/apps/" + app.Slug + "/issues/" + out.IssueID
	apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID}, nil), 200)
	event.EventID = uuid.NewString()
	event.OccurredAt = time.Now().UTC()
	if result := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, otherToken, event), 202); !result.Regressed {
		t.Fatal("newer release did not reopen")
	}
	until := time.Now().Add(time.Minute)
	apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "ignore", IgnoredUntil: &until}, nil), 200)
	maintenance := (*e.Store()).(interface {
		MaintainIssues(ctx context.Context, now time.Time) error
	})
	if err := maintenance.MaintainIssues(t.Context(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base, nil, nil), 200)
	if detail.Issue.State != "open" {
		t.Fatal("ignore expiry did not reopen")
	}
	if err := maintenance.MaintainIssues(t.Context(), time.Now().AddDate(0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	detail = apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base, nil, nil), 200)
	if len(detail.Events) != 0 || detail.Issue.EventCount != 2 || len(detail.Releases) != 2 {
		t.Fatal("retention removed durable history or retained occurrences")
	}
}

func TestIssueLateAttributionAndHistoryPagination(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-late")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	request, tenant, consumer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DateError", Message: "invalid format", RequestID: request}
	out := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	base := "/v1/apps/" + app.Slug + "/issues/" + out.IssueID
	if detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base, nil, nil), 200); detail.Impact.UnattributedEvents != 1 {
		t.Fatal("invented early attribution")
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,'late','late')`, tenant, (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `INSERT INTO api_consumers(id,account_id,app_id,external_ref,name,platform_tenant_id) VALUES($1,$2,$3,'late','late',$4)`, consumer, (*e.Acct()).ID, app.ID, tenant); err != nil {
		t.Fatal(err)
	}
	auditEvent := uuid.NewString()
	if _, err := (*e.Pool()).Exec(t.Context(), `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units,platform_tenant_id) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0,$5)`, auditEvent, (*e.Acct()).ID, app.ID, consumer, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,platform_tenant_id,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id) VALUES($1,$2,$3,$4,$5,'/exports','POST',500,10,$6,now(),$7)`, auditEvent, (*e.Acct()).ID, app.ID, consumer, tenant, dep.ID, request); err != nil {
		t.Fatal(err)
	}
	maintenance := (*e.Store()).(interface {
		MaintainIssues(context.Context, time.Time) error
	})
	if err := maintenance.MaintainIssues(t.Context(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base, nil, nil), 200)
	if detail.Impact.IdentifiedCustomers != 1 || detail.Events[0].VerifiedPlatformTenantID != tenant {
		t.Fatal("late verified attribution missing")
	}
	for i := 0; i < 51; i++ {
		event.EventID = uuid.NewString()
		apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
		assignee := ""
		if i%2 == 0 {
			assignee = (*e.Acct()).ID
		}
		apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "assign", AssigneeAccountID: assignee}, nil), 200)
	}
	detail = apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base, nil, nil), 200)
	if len(detail.Events) != 50 || detail.NextEventCursor == "" || len(detail.Activity) != 50 || detail.NextActivityCursor == "" {
		t.Fatal("history truncated without cursors")
	}
	page := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+"?event_cursor="+url.QueryEscape(detail.NextEventCursor)+"&activity_cursor="+url.QueryEscape(detail.NextActivityCursor), nil, nil), 200)
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
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	(*(*e.S()).Store()) = unavailableIssueCredentialStore{(*e.Store()), (*e.Store()).(state.IssueStore)}
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error"}
	w := e.Do(t, "POST", "/v1/apps/any-app/issue-events", event, map[string]string{"Authorization": "Bearer g_issue_test"})
	if w.Code != 503 {
		t.Fatalf("credential lookup outage would drop SDK events: %d %s", w.Code, w.Body.String())
	}
}

func TestIssueClockSkewPagination(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-clock")
	token := apidserver.IssueCreateToken(t, e, app.Slug, apidserver.IssueSeedDeployment(t, e, app, 1))
	for i := 0; i <= api.IssuePageSize; i++ {
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC().Add(api.IssueMaxClockSkew - time.Minute), ExceptionType: fmt.Sprintf("ClockError%d", i)}
		apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	}
	path := "/v1/apps/" + app.Slug + "/issues"
	first := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", path, nil, nil), 200)
	if len(first.Items) != api.IssuePageSize || first.NextCursor == "" {
		t.Fatal("no issue continuation cursor")
	}
	second := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", path+"?cursor="+url.QueryEscape(first.NextCursor), nil, nil), 200)
	if len(second.Items) != 1 {
		t.Fatal("accepted clock skew cannot be paginated")
	}
	invalid := state.EncodeIssueCursor(state.IssueCursor{Time: time.Now().Add(api.IssueMaxClockSkew + time.Minute), ID: uuid.NewString()})
	if w := e.Do(t, "GET", path+"?cursor="+url.QueryEscape(invalid), nil, nil); w.Code != 400 {
		t.Fatal("unbounded future cursor accepted")
	}
}
