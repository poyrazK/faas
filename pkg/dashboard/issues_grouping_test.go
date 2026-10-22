package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1112
func TestIssueGroupingExplanationRendersEscapedEvidenceAndFallback(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	attack := "<script>alert(1)</script>"
	explanation := &api.IssueGroupingExplanation{
		GroupingVersion: 2, Strategy: "failure-site-v1", Basis: "failure-site", SourceKind: "exception",
		ExceptionType: attack, Frames: []api.IssueGroupingFrame{{File: attack, Function: attack}},
		DiscriminatorKind: "error-code", DiscriminatorValue: attack,
	}
	render := func(e *api.IssueGroupingExplanation) string {
		t.Helper()
		var body bytes.Buffer
		if err := tmpl.ExecuteTemplate(&body, "issue-grouping", e); err != nil {
			t.Fatal(err)
		}
		return body.String()
	}
	body := render(explanation)
	if strings.Contains(body, attack) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("grouping evidence was not escaped")
	}
	if !strings.Contains(body, "nearest the failure") || !strings.Contains(body, "Stable error code:") {
		t.Fatal("selected evidence not explained")
	}
	explanation.Basis, explanation.Frames, explanation.DiscriminatorKind = "fallback", nil, "message"
	explanation.Route, explanation.HTTPStatus = attack, 500
	body = render(explanation)
	if strings.Contains(body, attack) || !strings.Contains(body, "fallback evidence") || !strings.Contains(body, "500") {
		t.Fatal("fallback evidence was missing or unescaped")
	}
	if body = render(nil); !strings.Contains(body, "explanation is unavailable") || strings.Contains(body, "reproduces the saved") {
		t.Fatal("missing evidence was presented as verified")
	}
}

// adr: 1113
func TestIssueReportedFrameLocationsEscapeOriginalSourceEvidence(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	attack := "<script>alert(1)</script>"
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "issue-frames", []api.IssueFrame{
		{File: attack, Function: attack, Line: 10, InApp: true},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), attack) || !strings.Contains(body.String(), "&lt;script&gt;") ||
		!strings.Contains(body.String(), "10") || !strings.Contains(body.String(), "Application") {
		t.Fatal("reported source evidence missing or unescaped")
	}
}

// adr: 1114
func TestIssueRegroupingControlsEscapeChoicesAndCarryCSRF(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	attack := "<script>alert(1)</script>"
	root := api.Issue{ID: "root", Environment: "application", Title: "Root"}
	member := api.Issue{ID: "member", Environment: "application", Title: attack, EventCount: 3}
	data := struct {
		AppSlug, CSRF string
		Items         []api.Issue
		Detail        *api.IssueDetail
	}{
		AppSlug: "exports", CSRF: "csrf-marker",
		Items:  []api.Issue{root, {ID: "destination", Environment: "application", Title: attack}, {ID: "staging", Environment: "staging", Title: "OTHER_ENVIRONMENT"}},
		Detail: &api.IssueDetail{Issue: root, GroupMembers: []api.Issue{root, member}},
	}
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "issue-regroup", data); err != nil {
		t.Fatal(err)
	}
	html := body.String()
	if strings.Contains(html, attack) || !strings.Contains(html, "&lt;script&gt;") || strings.Contains(html, "OTHER_ENVIRONMENT") {
		t.Fatal("regrouping choices were unescaped or crossed environments")
	}
	if strings.Count(html, "name=\"csrf_token\"") != 2 || strings.Count(html, "csrf-marker") != 2 ||
		!strings.Contains(html, "value=\"member\"") || strings.Contains(html, "name=\"member_issue_id\" value=\"root\"") {
		t.Fatal("regrouping forms lost CSRF or allow detaching the canonical identity")
	}
}

// adr: 1115
func TestIssueSplitPreviewEscapesEvidenceAndSeparatesUnavailableHistory(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	attack := "<script>alert(1)</script>"
	data := struct {
		AppSlug              string
		Detail               *api.IssueDetail
		SplitMatcher         api.IssueSplitMatcher
		SplitPreview         *api.IssueSplitPreview
		SplitPreviewError    string
		SplitPreviewCanApply bool
		CSRF                 string
	}{AppSlug: "payments", CSRF: "csrf-preview", SplitPreviewCanApply: true, Detail: &api.IssueDetail{Issue: api.Issue{ID: "original", Title: attack}}, SplitMatcher: api.IssueSplitMatcher{ErrorCode: attack}, SplitPreview: &api.IssueSplitPreview{OriginalIssueID: "original", Matcher: api.IssueSplitMatcher{ErrorCode: attack}, MatchingEventCount: 12, NonmatchingEventCount: 3, LifetimeEventCount: 20, UnavailableHistoryEventCount: 5, MatchingEventIDs: []string{attack}}}
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "issue-split-preview", data); err != nil {
		t.Fatal(err)
	}
	html := body.String()
	if strings.Contains(html, attack) || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "12 matching") || !strings.Contains(html, "5 events have unavailable") || !strings.Contains(html, `method="get"`) || !strings.Contains(html, "Apply split rule") || strings.Count(html, "csrf-preview") != 1 {
		t.Fatalf("missing or unescaped preview: %s", html)
	}
}

// adr: 1116
func TestIssueSplitRuleControlsEscapeMatchersAndCarryCSRF(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	attack := "<script>alert(1)</script>"
	data := struct {
		AppSlug, CSRF string
		Detail        *api.IssueDetail
	}{AppSlug: "payments", CSRF: "csrf-rules", Detail: &api.IssueDetail{Issue: api.Issue{ID: "branch", SplitSourceIssueID: "source"}, SplitRules: []api.IssueSplitRule{
		{ID: "first", SourceIssueID: "source", TargetIssueID: "branch", Priority: 1, Enabled: true, Matcher: api.IssueSplitMatcher{ErrorCode: attack}, InitialMovedEventCount: 3, UnavailableHistoryEventCount: 2},
		{ID: "second", SourceIssueID: "source", TargetIssueID: "other", Priority: 2, Enabled: false, Matcher: api.IssueSplitMatcher{Route: attack}},
	}}}
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "issue-split-rules", data); err != nil {
		t.Fatal(err)
	}
	html := body.String()
	if strings.Contains(html, attack) || !strings.Contains(html, "&lt;script&gt;") || strings.Count(html, "csrf-rules") != 2 || !strings.Contains(html, "Disable future routing") || !strings.Contains(html, "Enable future routing") || !strings.Contains(html, "3 moved · 2 unavailable") || !strings.Contains(html, "its fingerprint source") {
		t.Fatalf("unsafe or incomplete rule controls: %s", html)
	}
}

// adr: 1117
func TestIssueCauseChainRendersEscapedAndShowsIncompleteFallback(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	attack := "<script>alert(1)</script>"
	var body bytes.Buffer
	occ := api.IssueOccurrence{IssueEvent: api.IssueEvent{CausesIncomplete: true, Causes: []api.IssueCause{{ExceptionType: attack, Message: attack, Frames: []api.IssueFrame{{File: attack, Function: attack}}}}}}
	if err = tmpl.ExecuteTemplate(&body, "issue-causes", occ); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), attack) || !strings.Contains(body.String(), "incomplete or ambiguous") {
		t.Fatal("unsafe or missing cause evidence")
	}
	zero := 0
	body.Reset()
	exp := &api.IssueGroupingExplanation{GroupingVersion: 3, Strategy: "cause-chain-v1", CauseIndex: &zero}
	if err = tmpl.ExecuteTemplate(&body, "issue-grouping", exp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), "Selected cause index: 0") {
		t.Fatal("first cause index hidden")
	}
}

// adr: 1118
func TestIssueSplitPreviewShowsPriorityAndExistingRuleStatus(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	data := struct {
		AppSlug, CSRF        string
		Detail               *api.IssueDetail
		SplitMatcher         api.IssueSplitMatcher
		SplitPreview         *api.IssueSplitPreview
		SplitPreviewError    string
		SplitPreviewCanApply bool
	}{AppSlug: "payments", Detail: &api.IssueDetail{Issue: api.Issue{ID: "source"}}, SplitPreview: &api.IssueSplitPreview{OriginalIssueID: "source", MatchingEventCount: 3, EligibleEventCount: 2, ShadowedEventCount: 1, ApplicationStatus: "new-rule"}, SplitPreviewCanApply: true}
	var body bytes.Buffer
	if err = tmpl.ExecuteTemplate(&body, "issue-split-preview", data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), "2 eligible to move") || !strings.Contains(body.String(), "excluding 1 matches") {
		t.Fatal("missing priority-aware counts")
	}
	data.SplitPreviewCanApply = false
	data.SplitPreview.ApplicationStatus = "existing-rule"
	data.SplitPreview.EligibleEventCount = 0
	data.SplitPreview.ExistingTargetIssueID = "branch"
	body.Reset()
	if err = tmpl.ExecuteTemplate(&body, "issue-split-preview", data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), "Apply split rule") || !strings.Contains(body.String(), "does not re-enable") {
		t.Fatal("existing rule promises apply")
	}
}

// adr: 1118
func TestIssueSplitUndoEscapesReviewAndBindsDestination(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	type review struct {
		Rule           api.IssueSplitRule
		Source, Branch api.Issue
		CanUndo        bool
		Reason         string
	}
	attack := "<script>alert(1)</script>"
	data := struct {
		AppSlug, CSRF string
		SplitUndo     *review
	}{AppSlug: "payments", CSRF: "csrf-undo", SplitUndo: &review{Rule: api.IssueSplitRule{ID: "rule", SourceIssueID: "original", Matcher: api.IssueSplitMatcher{Route: attack}}, Source: api.Issue{ID: "canonical", Title: attack, State: "resolved"}, Branch: api.Issue{ID: "branch", Title: attack}, CanUndo: true}}
	var body bytes.Buffer
	if err = tmpl.ExecuteTemplate(&body, "issue-split-undo", data); err != nil {
		t.Fatal(err)
	}
	html := body.String()
	if strings.Contains(html, attack) || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, `name="canonical_source_issue_id" value="canonical"`) || !strings.Contains(html, "csrf-undo") {
		t.Fatal("unsafe or unbound undo review")
	}
	data.SplitUndo.CanUndo = false
	data.SplitUndo.Reason = "Review links"
	body.Reset()
	if err = tmpl.ExecuteTemplate(&body, "issue-split-undo", data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), "<form") || !strings.Contains(body.String(), "Review links") {
		t.Fatal("conflicted branch allows undo")
	}
}
