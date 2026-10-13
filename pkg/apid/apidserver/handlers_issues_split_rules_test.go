//go:build !no_pg

package apidserver_test

import (
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1116
func TestIssueSplitRulesConserveHistoryPreserveProvenanceAndRouteRetries(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "split-rules-history")
	old, fixed := apidserver.IssueSeedDeployment(t, e, app, 1), apidserver.IssueSeedDeployment(t, e, app, 2)
	token := apidserver.IssueCreateToken(t, e, app.Slug, old)
	requestID := uuid.NewString()
	apidserver.IssueSeedRequestAttribution(t, e, app, old, requestID)
	event := func(route, code string) api.IssueEvent {
		return api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "payment failed", FingerprintOverride: "legacy-payment", Route: route, ErrorCode: code, SourceKind: "exception", RequestID: requestID}
	}
	send := func(tok api.IssueIngestToken, ev api.IssueEvent) api.IssueEventResponse {
		t.Helper()
		return apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, tok, ev), http.StatusAccepted)
	}
	var source string
	var cutoff time.Time
	var movedEvent api.IssueEvent
	for i := 0; i < 7; i++ {
		ev := event("/checkout", "DECLINED")
		if i >= 5 {
			ev = event("/refund", "REFUND")
		}
		source = send(token, ev).IssueID
		if i == 1 {
			cutoff = time.Now()
		}
		if i == 2 {
			movedEvent = ev
		}
	}
	if err := sqlc.New().IssuePurgeEvents(t.Context(), (*e.Pool()), sqlc.IssuePurgeEventsParams{AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Before: pgtype.Timestamptz{Time: cutoff, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/issues/"
	get := func(id string) api.IssueDetail {
		t.Helper()
		return apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+id, nil, nil), http.StatusOK)
	}
	act := func(id string, in api.IssueActionRequest) api.Issue {
		t.Helper()
		return apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+id+"/actions", in, nil), http.StatusOK)
	}
	act(source, api.IssueActionRequest{Action: "assign", AssigneeAccountID: e.Acct().ID})
	act(source, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID})
	before := get(source)
	matcher := api.IssueSplitMatcher{Route: "/checkout", ErrorCode: "DECLINED"}
	create := func() api.IssueSplitRule {
		t.Helper()
		return apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "POST", base+strings.ToUpper(source)+"/split-rules", matcher, nil), http.StatusOK)
	}
	rule := create()
	if rule.SourceIssueID != source || rule.TargetIssueID == source || rule.Priority != 1 || !rule.Enabled || rule.InitialMovedEventCount != 3 || rule.UnavailableHistoryEventCount != 2 {
		t.Fatalf("wrong rule accounting: %+v", rule)
	}
	original, branch := get(source), get(rule.TargetIssueID)
	if original.Issue.EventCount != 4 || branch.Issue.EventCount != 3 || original.Issue.State != "resolved" || branch.Issue.State != "open" || branch.Issue.AssigneeAccountID != e.Acct().ID || branch.Issue.SplitSourceIssueID != source {
		t.Fatalf("wrong split counts or lifecycle: original=%+v branch=%+v", original.Issue, branch.Issue)
	}
	if !original.Issue.FirstSeenAt.Equal(before.Issue.FirstSeenAt) || !branch.Issue.LastSeenAt.Before(rule.CreatedAt) {
		t.Fatal("historical bounds were reset")
	}
	if len(original.Releases) != 1 || len(branch.Releases) != 1 || original.Releases[0].EventCount != 4 || branch.Releases[0].EventCount != 3 || original.Releases[0].DeploymentID != old.ID || branch.Releases[0].ImageDigest != old.ImageDigest {
		t.Fatalf("release counts or metadata lost: %+v %+v", original.Releases, branch.Releases)
	}
	if original.Impact.ObservedEvents != 2 || branch.Impact.ObservedEvents != 3 || original.Impact.IdentifiedCustomers != 1 || branch.Impact.IdentifiedCustomers != 1 {
		t.Fatalf("verified attribution changed: %+v %+v", original.Impact, branch.Impact)
	}
	ids := map[string]string{}
	for _, occ := range before.Events {
		ids[occ.EventID] = occ.ID
	}
	for _, occ := range branch.Events {
		if occ.ID != ids[occ.EventID] || occ.OriginalIssueID != source || occ.GroupingExplanation == nil || occ.GroupingExplanation.DiscriminatorValue != "legacy-payment" || occ.Route != "/checkout" || occ.Attribution != "verified" {
			t.Fatalf("provenance or evidence changed: %+v", occ)
		}
	}
	if len(branch.SplitRules) != 1 || branch.SplitRules[0].ID != rule.ID || len(original.SplitRules) != 1 {
		t.Fatal("source/branch detail omitted routing controls")
	}
	if same := create(); same.ID != rule.ID || get(rule.TargetIssueID).Issue.EventCount != 3 {
		t.Fatal("repeated creation moved evidence twice")
	}
	retry := send(token, movedEvent)
	if !retry.Duplicate || retry.IssueID != rule.TargetIssueID || get(rule.TargetIssueID).Issue.EventCount != 3 {
		t.Fatal("moved occurrence retry changed identity or count")
	}
	update := func(enabled bool) api.IssueSplitRule {
		t.Helper()
		return apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "PATCH", base+source+"/split-rules/"+rule.ID, api.IssueSplitRuleUpdate{Enabled: enabled}, nil), http.StatusOK)
	}
	update(false)
	if same := create(); same.Enabled || same.ID != rule.ID {
		t.Fatal("creation silently re-enabled existing rule")
	}
	off := event("/checkout", "DECLINED")
	if got := send(token, off); got.IssueID != source || got.Regressed {
		t.Fatalf("disabled rule routed capture or regressed old release: %+v", got)
	}
	if got := send(token, movedEvent); !got.Duplicate || got.IssueID != rule.TargetIssueID {
		t.Fatal("disabled rule reclassified existing retry")
	}
	update(true)
	if got := send(token, off); !got.Duplicate || got.IssueID != source {
		t.Fatal("re-enabled rule reclassified a source retry")
	}
	act(rule.TargetIssueID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID})
	later := apidserver.IssueSeedDeployment(t, e, app, 3)
	laterToken := apidserver.IssueCreateToken(t, e, app.Slug, later)
	fresh := event("/checkout", "DECLINED")
	fresh.RequestID = ""
	if got := send(laterToken, fresh); got.IssueID != rule.TargetIssueID || !got.Regressed {
		t.Fatalf("branch did not recur independently: %+v", got)
	}
	if get(source).Issue.State != "resolved" || get(source).Issue.EventCount != 5 || get(rule.TargetIssueID).Issue.EventCount != 4 || get(rule.TargetIssueID).Issue.RegressionCount != 1 {
		t.Fatal("future routing changed source lifecycle or lost counts")
	}
	act(rule.TargetIssueID, api.IssueActionRequest{Action: "merge", TargetIssueID: source})
	if got := send(laterToken, event("/checkout", "DECLINED")); got.IssueID != source {
		t.Fatal("routing ignored canonical branch membership")
	}
	if merged := get(source); merged.Issue.EventCount != 10 || len(merged.GroupMembers) != 2 || len(merged.SplitRules) != 1 {
		t.Fatalf("merge lost routed history: %+v", merged)
	}
	act(source, api.IssueActionRequest{Action: "split", MemberIssueID: rule.TargetIssueID})
	if got := send(laterToken, event("/checkout", "DECLINED")); got.IssueID != rule.TargetIssueID {
		t.Fatal("detached branch no longer received matching captures")
	}
	// Retention can remove evidence without resetting transferred release/lifetime counts.
	if err := sqlc.New().IssuePurgeEvents(t.Context(), (*e.Pool()), sqlc.IssuePurgeEventsParams{AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Before: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	original, branch = get(source), get(rule.TargetIssueID)
	if original.Issue.EventCount+branch.Issue.EventCount != 11 || len(original.Events) != 0 || len(branch.Events) != 0 {
		t.Fatal("retention reset split lifetime totals")
	}
	var releases int64
	for _, detail := range []api.IssueDetail{original, branch} {
		for _, release := range detail.Releases {
			releases += release.EventCount
		}
	}
	if releases != 11 {
		t.Fatalf("release conservation after retention: %d", releases)
	}
}

// adr: 1116
func TestIssueSplitRulesUseStablePriorityAndDoNotReplayOnReenable(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "split-rule-priority")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	send := func(route, code string) (api.IssueEvent, api.IssueEventResponse) {
		t.Helper()
		ev := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failed", FingerprintOverride: "priority", Route: route, ErrorCode: code}
		return ev, apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, ev), http.StatusAccepted)
	}
	_, first := send("/checkout", "DECLINED")
	source := first.IssueID
	base := "/v1/apps/" + app.Slug + "/issues/" + source + "/split-rules"
	create := func(m api.IssueSplitMatcher) api.IssueSplitRule {
		t.Helper()
		return apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "POST", base, m, nil), http.StatusOK)
	}
	a := create(api.IssueSplitMatcher{ErrorCode: "DECLINED"})
	apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "PATCH", base+"/"+a.ID, api.IssueSplitRuleUpdate{Enabled: false}, nil), http.StatusOK)
	old, _ := send("/checkout", "DECLINED")
	send("/checkout", "APPROVED")
	send("/refund", "APPROVED")
	apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "PATCH", base+"/"+a.ID, api.IssueSplitRuleUpdate{Enabled: true}, nil), http.StatusOK)
	preview := apidserver.IssueDecode[api.IssueSplitPreview](t, e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issues/"+source+"/split-preview", api.IssueSplitMatcher{Route: "/checkout"}, nil), 200)
	if preview.MatchingEventCount != 2 || preview.EligibleEventCount != 1 || preview.ShadowedEventCount != 1 || len(preview.EligibleEventIDs) != 1 || preview.ApplicationStatus != "new-rule" {
		t.Fatalf("priority preview disagrees with apply: %+v", preview)
	}
	b := create(api.IssueSplitMatcher{Route: "/checkout"})
	duplicatePreview := apidserver.IssueDecode[api.IssueSplitPreview](t, e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issues/"+source+"/split-preview", api.IssueSplitMatcher{Route: "/checkout"}, nil), 200)
	if duplicatePreview.ApplicationStatus != "existing-rule" || duplicatePreview.ExistingRuleID != b.ID || duplicatePreview.ExistingTargetIssueID != b.TargetIssueID || duplicatePreview.EligibleEventCount != 0 {
		t.Fatal("repeat creation preview promises a replay")
	}
	branchPreview := apidserver.IssueDecode[api.IssueSplitPreview](t, e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issues/"+b.TargetIssueID+"/split-preview", api.IssueSplitMatcher{Route: "/checkout"}, nil), 200)
	if branchPreview.ApplicationStatus != "derived-branch" || branchPreview.EligibleEventCount != 0 {
		t.Fatal("nested branch preview offers apply")
	}
	if b.Priority != 2 || b.InitialMovedEventCount != 1 {
		t.Fatalf("creation stole matches from an earlier enabled rule: %+v", b)
	}
	if _, got := send("/checkout", "DECLINED"); got.IssueID != a.TargetIssueID {
		t.Fatal("first enabled matching rule did not win")
	}
	if _, got := send("/checkout", "APPROVED"); got.IssueID != b.TargetIssueID {
		t.Fatal("later eligible rule did not route")
	}
	retry := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, old), http.StatusAccepted)
	if !retry.Duplicate || retry.IssueID != source {
		t.Fatal("reenable/create retrospectively stole a source occurrence")
	}
	apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "PATCH", base+"/"+a.ID, api.IssueSplitRuleUpdate{Enabled: false}, nil), http.StatusOK)
	if _, got := send("/checkout", "DECLINED"); got.IssueID != b.TargetIssueID {
		t.Fatal("disabled first rule blocked later rule")
	}
}

// adr: 1116
func TestIssueSplitRulesEnforceScopesBoundsAndImmutableDefinitions(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "split-rule-bounds")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	ev := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failed", FingerprintOverride: "bounds", Route: "/original"}
	source := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, ev), http.StatusAccepted).IssueID
	base := "/v1/apps/" + app.Slug + "/issues/"
	var first api.IssueSplitRule
	for i := 0; i < api.IssueSplitRulesPerIssueMax; i++ {
		rule := apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "POST", base+source+"/split-rules", api.IssueSplitMatcher{Route: fmt.Sprintf("/future-%d", i)}, nil), http.StatusOK)
		if i == 0 {
			first = rule
		}
		if rule.Priority != i+1 || rule.InitialMovedEventCount != 0 {
			t.Fatal("empty prospective rule was not bounded and ordered")
		}
	}
	if res := e.Do(t, "POST", base+source+"/split-rules", api.IssueSplitMatcher{Route: "/overflow"}, nil); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "8") {
		t.Fatalf("rule bound: %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, "POST", base+first.TargetIssueID+"/split-rules", api.IssueSplitMatcher{Route: "/nested"}, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("nested rule accepted: %d", res.Code)
	}
	other := apidserver.SeedPGApp(t, e, "split-rule-other")
	if res := e.Do(t, "PATCH", "/v1/apps/"+other.Slug+"/issues/"+source+"/split-rules/"+first.ID, api.IssueSplitRuleUpdate{Enabled: false}, nil); res.Code != http.StatusNotFound {
		t.Fatal("cross-app rule toggle was allowed")
	}
	for _, body := range []any{map[string]any{}, map[string]any{"enabled": nil}, map[string]any{"enabled": true, "route": "/edit"}} {
		if res := e.Do(t, "PATCH", base+source+"/split-rules/"+first.ID, body, nil); res.Code != http.StatusBadRequest {
			t.Fatalf("invalid update body accepted: %d", res.Code)
		}
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `UPDATE issue_split_rules SET route='/tampered' WHERE id=$1`, first.ID); err == nil {
		t.Fatal("database allowed immutable matcher edit")
	} else {
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatalf("unexpected guard error: %v", err)
		}
	}
	ev.EventID = uuid.NewString()
	ev.FingerprintOverride = "quota-source"
	second := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, ev), http.StatusAccepted).IssueID
	count, err := sqlc.New().IssueCount(t.Context(), (*e.Pool()), pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.PlanHobby.IssueLimits()
	limits.IssuesPerApp = int(count)
	_, err = (*e.Store()).(state.IssueStore).CreateIssueSplitRule(t.Context(), app.ID, second, e.Acct().ID, api.IssueSplitMatcher{Route: "/original"}, limits, time.Now())
	var limit *state.IssueLimitError
	if !errors.As(err, &limit) || limit.Resource != "issues" {
		t.Fatalf("branch ignored storage quota: %v", err)
	}
	after, err := sqlc.New().IssueCount(t.Context(), (*e.Pool()), pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true})
	if err != nil || after != count {
		t.Fatal("rejected rules created partial branches")
	}
	// Mutation forms require the authenticated Issues CSRF token.
	page := httptest.NewRequest("GET", "/dashboard/apps/"+app.Slug+"/issues?issue="+source+"&split_preview=1&split_route=/original", nil)
	e.AddAdminSession(t, page)
	rendered := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rendered, page)
	if rendered.Code != http.StatusOK || strings.Contains(rendered.Body.String(), "Apply split rule") || !strings.Contains(rendered.Body.String(), "maximum eight rules") || !strings.Contains(rendered.Body.String(), "Disable future routing") {
		t.Fatalf("missing routing UI: %d %s", rendered.Code, rendered.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range rendered.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing Issues CSRF cookie")
	}
	form := url.Values{"enabled": {"false"}}
	request := httptest.NewRequest("POST", "/dashboard/apps/"+app.Slug+"/issues/"+source+"/split-rules/"+first.ID, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	e.AddAdminSession(t, request)
	request.AddCookie(csrf)
	rejected := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rejected, request)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("missing CSRF accepted: %d", rejected.Code)
	}
	form.Set("csrf_token", csrf.Value)
	request = httptest.NewRequest("POST", "/dashboard/apps/"+app.Slug+"/issues/"+source+"/split-rules/"+first.ID, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	e.AddAdminSession(t, request)
	request.AddCookie(csrf)
	accepted := httptest.NewRecorder()
	(*e.H()).ServeHTTP(accepted, request)
	if accepted.Code != http.StatusSeeOther || !strings.Contains(accepted.Header().Get("Location"), first.TargetIssueID) {
		t.Fatalf("valid routing form failed: %d %s", accepted.Code, accepted.Body.String())
	}
}

// adr: 1116
func TestIssueSplitRuleCreationSerializesWithCaptures(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "split-rule-race")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	send := func() api.IssueEventResponse {
		ev := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failed", FingerprintOverride: "race", Route: "/checkout"}
		return apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, ev), http.StatusAccepted)
	}
	source := send().IssueID
	for i := 0; i < 9; i++ {
		send()
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; send() }()
	}
	close(start)
	rule := apidserver.IssueDecode[api.IssueSplitRule](t, e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issues/"+source+"/split-rules", api.IssueSplitMatcher{Route: "/checkout"}, nil), http.StatusOK)
	wg.Wait()
	get := func(id string) api.IssueDetail {
		return apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+id, nil, nil), http.StatusOK)
	}
	original, branch := get(source), get(rule.TargetIssueID)
	if original.Issue.EventCount != 0 || len(original.Releases) != 0 || branch.Issue.EventCount != 20 || len(branch.Events) != 20 || len(branch.Releases) != 1 || branch.Releases[0].EventCount != 20 {
		t.Fatalf("concurrent split lost or duplicated events: source=%+v branch=%+v", original.Issue, branch.Issue)
	}
	for _, occ := range branch.Events {
		if occ.OriginalIssueID != source || occ.GroupingExplanation == nil {
			t.Fatal("concurrent capture lost original identity")
		}
	}
}
