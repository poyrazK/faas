//go:build !no_pg

package apidserver

import (
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
)

// adr: 1118
func TestIssueSplitUndoPreservesEvidenceStateRetriesAndOtherRules(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "split-undo")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	send := func(route string) (api.IssueEvent, api.IssueEventResponse) {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failed", FingerprintOverride: "undo", Route: route}
		return event, issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	}
	moved, first := send("/checkout")
	source := first.IssueID
	send("/health")
	base := "/v1/apps/" + app.Slug + "/issues/"
	rule := issueDecode[api.IssueSplitRule](t, e.do(t, "POST", base+source+"/split-rules", api.IssueSplitMatcher{Route: "/checkout"}, nil), 200)
	other := issueDecode[api.IssueSplitRule](t, e.do(t, "POST", base+source+"/split-rules", api.IssueSplitMatcher{Route: "/refund"}, nil), 200)
	get := func(id string) api.IssueDetail {
		t.Helper()
		return issueDecode[api.IssueDetail](t, e.do(t, "GET", base+id, nil, nil), 200)
	}
	branch := get(rule.TargetIssueID)
	if !strings.Contains(branch.Issue.Title, "Split 1 [route=/checkout]") {
		t.Fatalf("unrecognizable branch: %s", branch.Issue.Title)
	}
	issueDecode[api.Issue](t, e.do(t, "POST", base+source+"/actions", api.IssueActionRequest{Action: "assign", AssigneeAccountID: e.acct.ID}, nil), 200)
	issueDecode[api.Issue](t, e.do(t, "POST", base+source+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, nil), 200)
	// The review is read-only and includes the destination and authenticated CSRF form.
	page := httptest.NewRequest("GET", "/dashboard/apps/"+app.Slug+"/issues?issue="+rule.TargetIssueID+"&undo_split_rule="+rule.ID, nil)
	e.addAdminSession(t, page)
	rendered := httptest.NewRecorder()
	e.h.ServeHTTP(rendered, page)
	if rendered.Code != 200 || !strings.Contains(rendered.Body.String(), "Review split-rule undo") || !strings.Contains(rendered.Body.String(), "state resolved") || !strings.Contains(rendered.Body.String(), "canonical_source_issue_id") {
		t.Fatalf("missing undo review: %d %s", rendered.Code, rendered.Body.String())
	}
	var csrf *http.Cookie
	for _, cookie := range rendered.Result().Cookies() {
		if cookie.Name == dashboardIssueCookie {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("missing CSRF cookie")
	}
	form := url.Values{"canonical_source_issue_id": {source}}
	submit := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/dashboard/apps/"+app.Slug+"/issues/"+source+"/split-rules/"+rule.ID+"/undo", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(csrf)
		e.addAdminSession(t, req)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		return w
	}
	if w := submit(); w.Code != 400 {
		t.Fatalf("missing undo CSRF accepted: %d", w.Code)
	}
	undoURL := base + source + "/split-rules/" + rule.ID + "/undo"
	if w := e.do(t, "POST", undoURL, map[string]any{}, nil); w.Code != 400 {
		t.Fatalf("missing reviewed destination accepted: %d", w.Code)
	}
	out := issueDecode[api.IssueSplitRule](t, e.do(t, "POST", undoURL, api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: strings.ToUpper(source)}, nil), 200)
	if out.Enabled {
		t.Fatal("undo retained enabled routing")
	}
	merged := get(source)
	if merged.Issue.EventCount != 2 || merged.Issue.State != "resolved" || merged.Issue.AssigneeAccountID != e.acct.ID || len(merged.GroupMembers) != 2 {
		t.Fatalf("undo lost destination state/counters: %+v", merged.Issue)
	}
	if len(merged.Events) != 2 || len(merged.Releases) != 1 || merged.Releases[0].EventCount != 2 {
		t.Fatal("undo lost evidence or release counts")
	}
	for _, occ := range merged.Events {
		if occ.OriginalIssueID != source || occ.GroupingExplanation == nil {
			t.Fatal("undo changed provenance")
		}
	}
	activities := len(merged.Activity)
	form.Set("csrf_token", csrf.Value)
	if w := submit(); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), source) {
		t.Fatalf("dashboard undo failed: %d %s", w.Code, w.Body.String())
	}
	if len(get(source).Activity) != activities {
		t.Fatal("repeated undo added audit")
	}
	retry := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, moved), 202)
	if !retry.Duplicate || retry.IssueID != source || get(source).Issue.EventCount != 2 {
		t.Fatal("moved retry changed counters/destination")
	}
	if _, future := send("/checkout"); future.IssueID != source {
		t.Fatal("undone rule still routes")
	}
	if _, future := send("/refund"); future.IssueID != other.TargetIssueID {
		t.Fatal("undo disabled another rule")
	}
}

// adr: 1118
func TestIssueSplitUndoRejectsChangedLinksAndRollsBackAtMemberLimit(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "split-undo-conflicts")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	send := func(identity string) string {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", FingerprintOverride: identity, Route: "/checkout"}
		return issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202).IssueID
	}
	source := send("source")
	destination := send("destination")
	child := send("child")
	base := "/v1/apps/" + app.Slug + "/issues/"
	rule := issueDecode[api.IssueSplitRule](t, e.do(t, "POST", base+source+"/split-rules", api.IssueSplitMatcher{Route: "/checkout"}, nil), 200)
	act := func(id string, in api.IssueActionRequest) {
		t.Helper()
		issueDecode[api.Issue](t, e.do(t, "POST", base+id+"/actions", in, nil), 200)
	}
	undoURL := base + source + "/split-rules/" + rule.ID + "/undo"
	act(source, api.IssueActionRequest{Action: "merge", TargetIssueID: destination})
	if w := e.do(t, "POST", undoURL, api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: source}, nil); w.Code != 409 {
		t.Fatalf("stale destination accepted: %d", w.Code)
	}
	act(child, api.IssueActionRequest{Action: "merge", TargetIssueID: rule.TargetIssueID})
	if w := e.do(t, "POST", undoURL, api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: destination}, nil); w.Code != 409 {
		t.Fatalf("branch family dragged into undo: %d", w.Code)
	}
	act(rule.TargetIssueID, api.IssueActionRequest{Action: "split", MemberIssueID: child})
	// An unrelated canonical destination also blocks undo until manually reviewed.
	act(rule.TargetIssueID, api.IssueActionRequest{Action: "merge", TargetIssueID: child})
	if w := e.do(t, "POST", undoURL, api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: destination}, nil); w.Code != 409 {
		t.Fatalf("unrelated branch link accepted: %d", w.Code)
	}
	act(child, api.IssueActionRequest{Action: "split", MemberIssueID: rule.TargetIssueID})
	for i := 2; i < api.IssueGroupMembersMax; i++ {
		id := send(fmt.Sprintf("member-%d", i))
		act(id, api.IssueActionRequest{Action: "merge", TargetIssueID: destination})
	}
	before := issueDecode[api.IssueDetail](t, e.do(t, "GET", base+destination, nil, nil), 200)
	if w := e.do(t, "POST", undoURL, api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: destination}, nil); w.Code != 409 {
		t.Fatalf("member bound ignored: %d %s", w.Code, w.Body.String())
	}
	after := issueDecode[api.IssueDetail](t, e.do(t, "GET", base+destination, nil, nil), 200)
	if len(after.Activity) != len(before.Activity) || len(after.GroupMembers) != api.IssueGroupMembersMax || len(after.SplitRules) != 1 || !after.SplitRules[0].Enabled {
		t.Fatal("failed undo partially changed routing/audit")
	}
	otherApp := seedPGApp(t, e, "split-undo-other")
	if w := e.do(t, "POST", "/v1/apps/"+otherApp.Slug+"/issues/"+source+"/split-rules/"+rule.ID+"/undo", api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: destination}, nil); w.Code != 404 {
		t.Fatalf("cross-app undo accepted: %d", w.Code)
	}
}

// adr: 1118
func TestIssueSplitUndoSerializesWithCaptures(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "split-undo-race")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	event := func() api.IssueEvent {
		return api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", FingerprintOverride: "undo-race", Route: "/checkout"}
	}
	source := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event()), 202).IssueID
	base := "/v1/apps/" + app.Slug + "/issues/"
	rule := issueDecode[api.IssueSplitRule](t, e.do(t, "POST", base+source+"/split-rules", api.IssueSplitMatcher{Route: "/checkout"}, nil), 200)
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event()), 202)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-gate
		issueDecode[api.IssueSplitRule](t, e.do(t, "POST", base+source+"/split-rules/"+rule.ID+"/undo", api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: source}, nil), 200)
	}()
	close(gate)
	wg.Wait()
	detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", base+source, nil, nil), 200)
	if detail.Issue.EventCount != 11 || len(detail.Events) != 11 || len(detail.Releases) != 1 || detail.Releases[0].EventCount != 11 || detail.SplitRules[0].Enabled || len(detail.GroupMembers) != 2 {
		t.Fatalf("undo race lost evidence: %+v", detail)
	}
	for _, occ := range detail.Events {
		if occ.OriginalIssueID != source {
			t.Fatal("undo race changed provenance")
		}
	}
}
