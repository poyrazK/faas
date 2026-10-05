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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func issueDecode[T any](t *testing.T, w *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if w.Code != want {
		t.Fatalf("HTTP %d want %d: %s", w.Code, want, w.Body.String())
	}
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func issueSeedDeployment(t *testing.T, e pgHandlerEnv, app state.App, n int) state.Deployment {
	t.Helper()
	d, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, ImageDigest: fmt.Sprintf("sha256:%064d", n), Kind: state.DeploymentKindImage, Status: state.DeployBuilding, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func issueCreateToken(t *testing.T, e pgHandlerEnv, slug string, dep state.Deployment) api.IssueIngestToken {
	t.Helper()
	return issueDecode[api.IssueIngestToken](t, e.do(t, "POST", "/v1/apps/"+slug+"/issue-ingest-tokens", api.CreateIssueIngestTokenRequest{DeploymentID: dep.ID, Name: "test", ExpiresAt: time.Now().Add(time.Hour)}, nil), 201)
}
func issueSend(t *testing.T, e pgHandlerEnv, slug string, token api.IssueIngestToken, event api.IssueEvent) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, "POST", "/v1/apps/"+slug+"/issue-events", event, map[string]string{"Authorization": "Bearer " + token.Token})
}

func issueSeedRequestAttribution(t *testing.T, e pgHandlerEnv, app state.App, dep state.Deployment, requestID string) {
	t.Helper()
	tenant, consumer, auditEventID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,$3,$3)`, tenant, e.acct.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO api_consumers(id,account_id,app_id,external_ref,name,platform_tenant_id) VALUES($1,$2,$3,$4,$4,$5)`, consumer, e.acct.ID, app.ID, uuid.NewString(), tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units,platform_tenant_id) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0,$5)`, auditEventID, e.acct.ID, app.ID, consumer, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,platform_tenant_id,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id) VALUES($1,$2,$3,$4,$5,'/exports','POST',500,10,$6,now(),$7)`, auditEventID, e.acct.ID, app.ID, consumer, tenant, dep.ID, requestID); err != nil {
		t.Fatal(err)
	}
}

// This gate exercises the production HTTP router and real PgStore: credentials,
// grouping, idempotency, release-aware lifecycle, history, and verified impact.
func TestIssueEndToEndPostgres(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-exports")
	old := issueSeedDeployment(t, e, app, 42)
	fixed := issueSeedDeployment(t, e, app, 43)
	oldToken := issueCreateToken(t, e, app.Slug, old)
	fixedToken := issueCreateToken(t, e, app.Slug, fixed)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DateFormatError", Message: "invalid format for alice@example.com", Frames: []api.IssueFrame{{File: "/build/v42/export.ts", Function: "generateExport", Line: 12, InApp: true}}}
	first := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, oldToken, event), 202)
	duplicate := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, oldToken, event), 202)
	if !duplicate.Duplicate || duplicate.IssueID != first.IssueID {
		t.Fatal("retry not idempotent")
	}
	conflict := event
	conflict.Message = "another payload"
	if w := issueSend(t, e, app.Slug, oldToken, conflict); w.Code != 409 {
		t.Fatalf("event conflict %d %s", w.Code, w.Body.String())
	}
	base := "/v1/apps/" + app.Slug + "/issues/" + first.IssueID
	assigned := issueDecode[api.Issue](t, e.do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "assign", AssigneeAccountID: e.acct.ID}, nil), 200)
	if assigned.AssigneeAccountID != e.acct.ID {
		t.Fatal("assignment missing")
	}
	resolved := issueDecode[api.Issue](t, e.do(t, "POST", base+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID}, nil), 200)
	if resolved.State != "resolved" || resolved.ResolvedAt == nil {
		t.Fatal("resolution missing")
	}
	event.EventID = uuid.NewString()
	event.OccurredAt = time.Now().UTC()
	if out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, oldToken, event), 202); out.Regressed {
		t.Fatal("old serving deployment regressed fixed issue")
	}
	event.EventID = uuid.NewString()
	event.OccurredAt = resolved.ResolvedAt.Add(-time.Second)
	if out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, fixedToken, event), 202); out.Regressed {
		t.Fatal("late arrival regressed issue")
	}
	event.EventID = uuid.NewString()
	event.OccurredAt = time.Now().UTC()
	if out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, fixedToken, event), 202); !out.Regressed || out.IssueID != first.IssueID {
		t.Fatal("fixed-release recurrence not detected")
	}
	detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", base, nil, nil), 200)
	if detail.Issue.State != "open" || detail.Issue.EventCount != 4 || detail.Issue.RegressionCount != 1 || len(detail.Releases) != 2 || len(detail.Activity) != 4 {
		t.Fatalf("incomplete issue history: %+v", detail)
	}
	if strings.Contains(e.do(t, "GET", base, nil, nil).Body.String(), "alice@example.com") {
		t.Fatal("exception PII leaked")
	}
	if detail.Impact.UnattributedEvents != 4 || detail.Impact.IdentifiedCustomers != 0 {
		t.Fatal("invented customer impact")
	}
	list := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues", nil, nil), 200)
	if len(list.Items) != 1 {
		t.Fatal("one failure became multiple issues")
	}
	if list.Items[0].AssigneeAccountID != e.acct.ID || list.Items[0].RegressionCount != 1 {
		t.Fatalf("issue list omitted triage fields: %+v", list.Items[0])
	}
	mine := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues?assignee=me", nil, nil), 200)
	if len(mine.Items) != 1 || mine.Items[0].ID != first.IssueID {
		t.Fatalf("mine filter = %+v", mine.Items)
	}
	unassigned := issueDecode[api.ListIssuesResponse](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues?assignee=unassigned", nil, nil), 200)
	if len(unassigned.Items) != 0 {
		t.Fatalf("unassigned filter = %+v", unassigned.Items)
	}
	if w := e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues?assignee=not-an-account", nil, nil); w.Code != 400 {
		t.Fatalf("invalid assignee filter status = %d", w.Code)
	}
	other := seedPGApp(t, e, "issue-other")
	if w := issueSend(t, e, other.Slug, fixedToken, event); w.Code != 401 {
		t.Fatal("ingest token crossed app boundary")
	}
	if w := e.do(t, "GET", base, nil, map[string]string{"Authorization": "Bearer " + fixedToken.Token}); w.Code != 401 {
		t.Fatal("ingest token read customer data")
	}
	if w := e.do(t, "DELETE", "/v1/apps/"+app.Slug+"/issue-ingest-tokens/"+fixedToken.ID, nil, nil); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w := issueSend(t, e, app.Slug, fixedToken, event); w.Code != 401 {
		t.Fatal("revoked token accepted")
	}
}

func TestIssueOwnershipRulesRouteNewIssuesAndPreserveManualAssignment(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	org, err := e.store.CreateOrg(t.Context(), state.Org{Slug: "issue-routing-" + uuid.NewString()[:8], Name: "Issue Routing", Plan: api.PlanHobby, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddOrgMember(t.Context(), org.ID, e.acct.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	member, err := e.store.CreateAccount(t.Context(), "issue-routing-member-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddOrgMember(t.Context(), org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID, OrgID: org.ID, Slug: "issue-routing-app", Type: state.AppTypeApp,
		Status: state.AppActive, RequireAuthn: e.acct.Plan.RequireAuthnDefault(), PublicAuthMode: e.acct.Plan.PublicAuthModeDefault(),
	})
	if err != nil {
		t.Fatal(err)
	}
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug + "/issue-ownership-rules"
	initial := issueDecode[api.IssueOwnershipRules](t, e.do(t, http.MethodGet, base, nil, nil), http.StatusOK)
	if len(initial.Rules) != 0 {
		t.Fatalf("default ownership rules = %+v", initial)
	}
	configured := api.IssueOwnershipRules{Rules: []api.IssueOwnershipRule{
		{SourceKind: "exception", AssigneeAccountID: e.acct.ID},
		{ExceptionType: "DateFormatError", RoutePrefix: "/exports", AssigneeAccountID: member.ID},
	}}
	configured = issueDecode[api.IssueOwnershipRules](t, e.do(t, http.MethodPut, base, configured, nil), http.StatusOK)
	if len(configured.Rules) != 2 || configured.Rules[0].AssigneeAccountID != e.acct.ID {
		t.Fatalf("saved rule order = %+v", configured)
	}
	foreign, err := e.store.CreateAccount(t.Context(), "issue-routing-stranger-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if w := e.do(t, http.MethodPut, base, api.IssueOwnershipRules{Rules: []api.IssueOwnershipRule{{SourceKind: "worker", AssigneeAccountID: foreign.ID}}}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("foreign routing target status = %d: %s", w.Code, w.Body.String())
	}

	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DateFormatError", Message: "invalid date", SourceKind: "exception", Route: "/exports/42"}
	created := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	detailPath := "/v1/apps/" + app.Slug + "/issues/" + created.IssueID
	detail := issueDecode[api.IssueDetail](t, e.do(t, http.MethodGet, detailPath, nil, nil), http.StatusOK)
	if detail.Issue.AssigneeAccountID != e.acct.ID {
		t.Fatalf("first matching rule assignment = %q, want app owner %q", detail.Issue.AssigneeAccountID, e.acct.ID)
	}
	foundAutoAssignment := false
	for _, activity := range detail.Activity {
		if activity.Action == "assigned" && activity.Details["assignment_source"] == "ownership_rule" {
			foundAutoAssignment = true
			if activity.Details["assignee_account_id"] != e.acct.ID || activity.Details["source_kind"] != "exception" {
				t.Fatalf("automatic assignment details = %+v", activity.Details)
			}
		}
	}
	if !foundAutoAssignment {
		t.Fatal("automatic assignment activity is missing")
	}
	issueDecode[api.Issue](t, e.do(t, http.MethodPost, detailPath+"/actions", api.IssueActionRequest{Action: "assign", AssigneeAccountID: member.ID}, nil), http.StatusOK)
	event.EventID = uuid.NewString()
	event.OccurredAt = time.Now().UTC()
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	detail = issueDecode[api.IssueDetail](t, e.do(t, http.MethodGet, detailPath, nil, nil), http.StatusOK)
	if detail.Issue.AssigneeAccountID != member.ID {
		t.Fatalf("later event overrode manual assignment: %+v", detail.Issue)
	}

	// A route-only rule demonstrates path boundaries: /exports/45 matches,
	// while /export-service does not.
	routePolicy := api.IssueOwnershipRules{Rules: []api.IssueOwnershipRule{{RoutePrefix: "/exports", AssigneeAccountID: member.ID}}}
	issueDecode[api.IssueOwnershipRules](t, e.do(t, http.MethodPut, base, routePolicy, nil), http.StatusOK)
	for _, tc := range []struct {
		typ, route, want string
	}{{"OtherError", "/export-service", ""}, {"JSONError", "/exports/45", member.ID}} {
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: tc.typ, Message: "failure", Route: tc.route}
		created := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
		got := issueDecode[api.IssueDetail](t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issues/"+created.IssueID, nil, nil), http.StatusOK).Issue
		if got.AssigneeAccountID != tc.want {
			t.Fatalf("route %q assigned %q, want %q", tc.route, got.AssigneeAccountID, tc.want)
		}
	}
}

func TestIssueConcurrentDuplicateCountsOnce(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-race")
	token := issueCreateToken(t, e, app.Slug, issueSeedDeployment(t, e, app, 1))
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failed"}
	const n = 8
	var wg sync.WaitGroup
	outputs := make(chan *httptest.ResponseRecorder, n)
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); outputs <- issueSend(t, e, app.Slug, token, event) }()
	}
	wg.Wait()
	close(outputs)
	newEvents := 0
	var id string
	for w := range outputs {
		out := issueDecode[api.IssueEventResponse](t, w, 202)
		if !out.Duplicate {
			newEvents++
		}
		id = out.IssueID
	}
	if newEvents != 1 {
		t.Fatalf("new events = %d", newEvents)
	}
	detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+id, nil, nil), 200)
	if detail.Issue.EventCount != 1 {
		t.Fatal("concurrent retry inflated count")
	}
}

func TestIssueVerifiedCustomerImpactAndWebhookRecovery(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-impact")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	hook, err := e.store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: e.acct.ID, AppID: app.ID, TargetURL: "https://example.com/issues", Enabled: true, EventFilter: []string{"issue.created", "issue.regressed"}, SecretSealed: []byte("test-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	var issueID string
	for i := 0; i < 3; i++ {
		tenant, consumer := uuid.NewString(), uuid.NewString()
		// Numeric UUID segments must retain their exact request-audit identity.
		requestID := fmt.Sprintf("12345678-1234-4123-8123-%012d", i)
		if i < 2 {
			if _, err := e.pool.Exec(t.Context(), `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,$3,$3)`, tenant, e.acct.ID, fmt.Sprint(i)); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.Exec(t.Context(), `INSERT INTO api_consumers(id,account_id,app_id,external_ref,name,platform_tenant_id) VALUES($1,$2,$3,$4,$4,$5)`, consumer, e.acct.ID, app.ID, fmt.Sprint(i), tenant); err != nil {
				t.Fatal(err)
			}
			auditEventID := uuid.NewString()
			if _, err := e.pool.Exec(t.Context(), `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units,platform_tenant_id) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0,$5)`, auditEventID, e.acct.ID, app.ID, consumer, tenant); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.Exec(t.Context(), `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,platform_tenant_id,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id) VALUES($1,$2,$3,$4,$5,'/exports','POST',500,10,$6,now(),$7)`, auditEventID, e.acct.ID, app.ID, consumer, tenant, dep.ID, requestID); err != nil {
				t.Fatal(err)
			}
		}
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DateFormatError", Message: "invalid format", RequestID: requestID}
		out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
		issueID = out.IssueID
	}
	detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+issueID, nil, nil), 200)
	if detail.Impact.IdentifiedCustomers != 2 || detail.Impact.UnattributedEvents != 1 {
		t.Fatalf("impact = %+v", detail.Impact)
	}
	list := issueDecode[api.ListIssuesResponse](t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issues", nil, nil), http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].Impact24h == nil {
		t.Fatalf("issue inbox impact summary = %+v", list.Items)
	}
	if got := list.Items[0].Impact24h; got.IdentifiedCustomers != 2 || got.ObservedEvents != 3 || got.UnattributedEvents != 1 {
		t.Fatalf("issue inbox impact summary = %+v", got)
	}
	relay := e.store.(state.AppWebhookEventOutboxStore)
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 1 {
		t.Fatalf("recovery relay = %d %v", n, err)
	}
	deliveries, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventIssueCreated {
		t.Fatalf("deliveries = %+v %v", deliveries, err)
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 0 {
		t.Fatal("recovery delivered transition twice")
	}
	other, err := e.store.CreateAccount(t.Context(), "issue-foreign-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.(state.IssueStore).ActOnIssue(t.Context(), app.ID, issueID, e.acct.ID, api.IssueActionRequest{Action: "assign", AssigneeAccountID: other.ID}, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign assignee accepted")
	}
}

func TestIssueImpactAlertPolicyCrossingsAndLateAttributionPostgres(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-impact-alert")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug + "/issue-impact-alert-policy"
	policy := issueDecode[api.IssueImpactAlertPolicy](t, e.do(t, http.MethodGet, base, nil, nil), http.StatusOK)
	if policy.Enabled || policy.MinimumCustomers != 0 || policy.WindowSeconds != int64(api.IssueImpactAlertWindow/time.Second) {
		t.Fatalf("default policy = %+v", policy)
	}
	if w := e.do(t, http.MethodPut, base, api.UpdateIssueImpactAlertPolicyRequest{MinimumCustomers: api.IssueImpactAlertMaxCustomers + 1}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range threshold status = %d: %s", w.Code, w.Body.String())
	}
	policy = issueDecode[api.IssueImpactAlertPolicy](t, e.do(t, http.MethodPut, base, api.UpdateIssueImpactAlertPolicyRequest{MinimumCustomers: 2}, nil), http.StatusOK)
	if !policy.Enabled || policy.MinimumCustomers != 2 {
		t.Fatalf("enabled policy = %+v", policy)
	}
	hook, err := e.store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: e.acct.ID, AppID: app.ID, TargetURL: "https://example.com/impact-alert", Enabled: true, EventFilter: []string{"issue.impact_threshold_reached"}, SecretSealed: []byte("test-sealed")})
	if err != nil {
		t.Fatal(err)
	}

	requestA, requestB := uuid.NewString(), uuid.NewString()
	issueSeedRequestAttribution(t, e, app, dep, requestA)
	issueSeedRequestAttribution(t, e, app, dep, requestB)
	eventA := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DateFormatError", Message: "format failure", RequestID: requestA}
	first := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, eventA), http.StatusAccepted)
	if duplicate := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, eventA), http.StatusAccepted); !duplicate.Duplicate {
		t.Fatal("exact retry was not marked duplicate")
	}
	// A second event for the same verified tenant is not another customer and
	// must not cross the threshold.
	sameCustomer := eventA
	sameCustomer.EventID = uuid.NewString()
	sameCustomer.OccurredAt = time.Now().UTC()
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, sameCustomer), http.StatusAccepted)
	eventB := eventA
	eventB.EventID = uuid.NewString()
	eventB.OccurredAt = time.Now().UTC()
	eventB.RequestID = requestB
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, eventB), http.StatusAccepted)
	// Once above threshold, additional distinct customers in this window do
	// not produce duplicate notifications.
	requestC := uuid.NewString()
	issueSeedRequestAttribution(t, e, app, dep, requestC)
	eventC := eventB
	eventC.EventID = uuid.NewString()
	eventC.OccurredAt = time.Now().UTC()
	eventC.RequestID = requestC
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, eventC), http.StatusAccepted)

	detailURL := "/v1/apps/" + app.Slug + "/issues/" + first.IssueID
	detail := issueDecode[api.IssueDetail](t, e.do(t, http.MethodGet, detailURL, nil, nil), http.StatusOK)
	impactAlerts := 0
	for _, activity := range detail.Activity {
		if activity.Action == "impact_threshold_reached" {
			impactAlerts++
			if activity.Details["minimum_customers"] != "2" || activity.Details["identified_customers"] != "2" {
				t.Fatalf("threshold activity details = %+v", activity.Details)
			}
		}
	}
	if impactAlerts != 1 || detail.Impact.IdentifiedCustomers != 3 {
		t.Fatalf("impact transition count=%d detail impact=%+v", impactAlerts, detail.Impact)
	}
	relay := e.store.(state.AppWebhookEventOutboxStore)
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 1 {
		t.Fatalf("threshold relay = %d %v", n, err)
	}
	deliveries, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventIssueImpactThresholdReached {
		t.Fatalf("threshold deliveries = %+v %v", deliveries, err)
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 0 {
		t.Fatalf("duplicate threshold relay = %d %v", n, err)
	}

	// The same crossing must be detected if request identity becomes available
	// only after the exception event was first accepted.
	latePolicy := issueDecode[api.IssueImpactAlertPolicy](t, e.do(t, http.MethodPut, base, api.UpdateIssueImpactAlertPolicyRequest{MinimumCustomers: 1}, nil), http.StatusOK)
	if latePolicy.MinimumCustomers != 1 {
		t.Fatalf("late-attribution policy = %+v", latePolicy)
	}
	lateRequest := uuid.NewString()
	lateEvent := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "LateIdentityError", Message: "late request evidence", RequestID: lateRequest}
	lateIssue := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, lateEvent), http.StatusAccepted)
	issueSeedRequestAttribution(t, e, app, dep, lateRequest)
	if _, err := e.pool.Exec(t.Context(), `UPDATE issue_events SET attribution_checked_at=$1 WHERE app_id=$2 AND deployment_id=$3 AND event_id=$4`, time.Now().UTC().Add(-2*time.Minute), app.ID, dep.ID, lateEvent.EventID); err != nil {
		t.Fatal(err)
	}
	maintenance, ok := e.store.(interface {
		MaintainIssues(context.Context, time.Time) error
	})
	if !ok {
		t.Fatal("store does not expose issue maintenance")
	}
	if err := maintenance.MaintainIssues(t.Context(), time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	lateDetail := issueDecode[api.IssueDetail](t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issues/"+lateIssue.IssueID, nil, nil), http.StatusOK)
	lateAlerts := 0
	for _, activity := range lateDetail.Activity {
		if activity.Action == "impact_threshold_reached" {
			lateAlerts++
		}
	}
	if lateAlerts != 1 || lateDetail.Impact.IdentifiedCustomers != 1 {
		t.Fatalf("late attribution alert=%d impact=%+v", lateAlerts, lateDetail.Impact)
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 1 {
		t.Fatalf("late threshold relay = %d %v", n, err)
	}
	if err := maintenance.MaintainIssues(t.Context(), time.Now().UTC().Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 0 {
		t.Fatalf("repeat enrichment relayed %d events: %v", n, err)
	}

	disabled := issueDecode[api.IssueImpactAlertPolicy](t, e.do(t, http.MethodPut, base, api.UpdateIssueImpactAlertPolicyRequest{MinimumCustomers: 0}, nil), http.StatusOK)
	if disabled.Enabled || disabled.MinimumCustomers != 0 {
		t.Fatalf("disabled policy = %+v", disabled)
	}
	requestD, requestE := uuid.NewString(), uuid.NewString()
	issueSeedRequestAttribution(t, e, app, dep, requestD)
	issueSeedRequestAttribution(t, e, app, dep, requestE)
	disabledEvent := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DisabledAlertError", Message: "disabled policy", RequestID: requestD, FingerprintOverride: "disabled-impact-policy"}
	disabledIssue := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, disabledEvent), http.StatusAccepted)
	disabledEvent.EventID = uuid.NewString()
	disabledEvent.OccurredAt = time.Now().UTC()
	disabledEvent.RequestID = requestE
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, disabledEvent), http.StatusAccepted)
	disabledDetail := issueDecode[api.IssueDetail](t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/issues/"+disabledIssue.IssueID, nil, nil), http.StatusOK)
	for _, activity := range disabledDetail.Activity {
		if activity.Action == "impact_threshold_reached" {
			t.Fatal("disabled impact policy still emitted a threshold transition")
		}
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 0 {
		t.Fatalf("disabled threshold relay = %d %v", n, err)
	}
}

func TestIssueImpactSortAndThresholdPaginationPostgres(t *testing.T) {
	e := setupPGHandler(t, api.PlanScale)
	app := seedPGApp(t, e, "issue-impact-sort")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	baseTime := time.Now().UTC().Add(-4 * time.Minute)
	issueIDs := make([]string, api.IssuePageSize+1)
	for i := range issueIDs {
		event := api.IssueEvent{
			EventID: uuid.NewString(), OccurredAt: baseTime.Add(time.Duration(i) * time.Second),
			ExceptionType: "RankedError", Message: "issue for impact ordering",
			FingerprintOverride: fmt.Sprintf("ranked-%02d", i),
		}
		out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
		issueIDs[i] = out.IssueID
		if _, err := e.pool.Exec(t.Context(), `UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2 AND deployment_id=$3 AND event_id=$4`, uuid.NewString(), app.ID, dep.ID, event.EventID); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 2 {
		event := api.IssueEvent{
			EventID: uuid.NewString(), OccurredAt: baseTime.Add(time.Duration(i+1) * 100 * time.Millisecond),
			ExceptionType: "RankedError", Message: "higher customer impact",
			FingerprintOverride: "ranked-00",
		}
		out := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
		if out.IssueID != issueIDs[0] {
			t.Fatalf("additional event grouped into %s, want %s", out.IssueID, issueIDs[0])
		}
		if _, err := e.pool.Exec(t.Context(), `UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2 AND deployment_id=$3 AND event_id=$4`, uuid.NewString(), app.ID, dep.ID, event.EventID); err != nil {
			t.Fatal(err)
		}
	}

	base := "/v1/apps/" + app.Slug + "/issues"
	firstPage := issueDecode[api.ListIssuesResponse](t, e.do(t, http.MethodGet, base+"?sort=impact", nil, nil), http.StatusOK)
	if len(firstPage.Items) != api.IssuePageSize || firstPage.NextCursor == "" {
		t.Fatalf("impact first page = len %d cursor %t", len(firstPage.Items), firstPage.NextCursor != "")
	}
	if firstPage.Items[0].ID != issueIDs[0] {
		t.Fatalf("highest impact issue = %s, want %s", firstPage.Items[0].ID, issueIDs[0])
	}
	if got := firstPage.Items[0].Impact24h; got == nil || got.IdentifiedCustomers != 3 || got.ObservedEvents != 3 {
		t.Fatalf("highest-impact issue summary = %+v", got)
	}

	filtered := issueDecode[api.ListIssuesResponse](t, e.do(t, http.MethodGet, base+"?min_customers=2", nil, nil), http.StatusOK)
	if len(filtered.Items) != 1 || filtered.Items[0].ID != issueIDs[0] {
		t.Fatalf("minimum-customer filter = %+v", filtered.Items)
	}

	query := url.Values{"sort": {"impact"}, "cursor": {firstPage.NextCursor}}
	secondPage := issueDecode[api.ListIssuesResponse](t, e.do(t, http.MethodGet, base+"?"+query.Encode(), nil, nil), http.StatusOK)
	if len(secondPage.Items) != 1 || secondPage.Items[0].ID != issueIDs[1] || secondPage.NextCursor != "" {
		t.Fatalf("impact second page = %+v", secondPage)
	}
	firstCursor, err := state.DecodeIssueCursor(firstPage.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if firstCursor.Sort != "impact" || firstCursor.ImpactWindowEnd == nil {
		t.Fatalf("impact cursor omitted its window: %+v", firstCursor)
	}
	if w := e.do(t, http.MethodGet, base+"?sort=recent&cursor="+url.QueryEscape(firstPage.NextCursor), nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("cursor reused under different sort = %d, want 400", w.Code)
	}
}

func TestIssueSDKEndToEndPostgres(t *testing.T) {
	if os.Getenv("GREGALE_ISSUES_SDK_ACCEPTANCE") == "" {
		t.Skip("run make test-issues for SDK process acceptance")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("SDK acceptance requires enabled PostgreSQL tests")
	}
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-sdk")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	server := httptest.NewServer(e.h)
	defer server.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node SDK acceptance requires Node.js")
	}
	// Dynamic import accepts an absolute path without shell interpolation.
	script := `const {createIssueReporter}=await import(process.env.GREGALE_ISSUE_NODE_MODULE);const r=createIssueReporter({baseURL:process.env.GREGALE_ISSUE_API,app:'issue-sdk',token:process.env.GREGALE_ISSUE_TOKEN});r.captureException(new TypeError('Node SDK failure'));if(!await r.close())process.exit(2);`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script)
	cmd.Env = append(os.Environ(), "GREGALE_ISSUE_API="+server.URL, "GREGALE_ISSUE_TOKEN="+token.Token, "GREGALE_ISSUE_NODE_MODULE="+filepath.Join(root, "sdk/node/dist/issues.js"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Node capture: %v %s", err, out)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python SDK acceptance requires Python")
	}
	script = `import os
from faas_sdk.issues import IssueReporter
r=IssueReporter(os.environ['GREGALE_ISSUE_API'],'issue-sdk',os.environ['GREGALE_ISSUE_TOKEN'])
try:
 raise ValueError('Python SDK failure')
except ValueError as e:
 r.capture_exception(e)
assert r.close()
`
	cmd = exec.CommandContext(t.Context(), python, "-c", script)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "sdk/python"), "GREGALE_ISSUE_API="+server.URL, "GREGALE_ISSUE_TOKEN="+token.Token)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Python capture: %v %s", err, out)
	}
	c := api.NewClient(server.URL, e.key)
	list, err := c.ListIssues(t.Context(), app.Slug, "", "", "")
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("SDK issues = %+v %v", list, err)
	}
	for _, issue := range list.Items {
		detail, err := c.GetIssue(t.Context(), app.Slug, issue.ID, "", "")
		if err != nil || len(detail.Events) != 1 || len(detail.Events[0].Frames) == 0 || detail.Releases[0].DeploymentID != dep.ID {
			t.Fatalf("SDK evidence incomplete: %+v %v", detail, err)
		}
	}
}
