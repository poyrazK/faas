//go:build !no_pg

package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1115
func TestIssueSplitPreviewAccountsForRetainedAndUnavailableEvidence(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-preview")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	var original string
	var partialRetentionCutoff time.Time
	for i := 0; i < 15; i++ {
		route := "/checkout"
		if i >= 12 {
			route = "/refund"
		}
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "declined", FingerprintOverride: "same-fingerprint", Route: route, ErrorCode: "DECLINED"}
		result := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), http.StatusAccepted)
		original = result.IssueID
		if i == 2 {
			partialRetentionCutoff = time.Now()
		}
	}
	sibling := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "other", FingerprintOverride: "other-fingerprint", Route: "/checkout", ErrorCode: "DECLINED"}), http.StatusAccepted).IssueID
	base := "/v1/apps/" + app.Slug + "/issues/"

	apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+original+"/actions", api.IssueActionRequest{Action: "merge", TargetIssueID: sibling}, nil), http.StatusOK)
	before := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+sibling, nil, nil), http.StatusOK)
	preview := func(m api.IssueSplitMatcher) api.IssueSplitPreview {
		return apidserver.IssueDecode[api.IssueSplitPreview](t, e.Do(t, "POST", base+strings.ToUpper(original)+"/split-preview", m, nil), http.StatusOK)
	}
	got := preview(api.IssueSplitMatcher{Route: "/checkout", ErrorCode: "DECLINED", SourceKind: "exception"})
	if got.OriginalIssueID != original || got.CanonicalIssueID != sibling || got.LifetimeEventCount != 15 || got.RetainedEventCount != 15 || got.MatchingEventCount != 12 || got.NonmatchingEventCount != 3 || got.UnavailableHistoryEventCount != 0 || len(got.MatchingEventIDs) != api.IssueSplitPreviewSamplesMax {
		t.Fatalf("wrong original-only preview: %+v", got)
	}
	if got := preview(api.IssueSplitMatcher{Route: "/check"}); got.MatchingEventCount != 0 {
		t.Fatalf("route was treated as prefix: %+v", got)
	}
	if got := preview(api.IssueSplitMatcher{Route: "/checkout", ErrorCode: "OTHER"}); got.MatchingEventCount != 0 {
		t.Fatalf("matchers did not use conjunction: %+v", got)
	}
	for _, matcher := range []api.IssueSplitMatcher{{}, {SourceKind: "other"}, {ErrorCode: strings.Repeat("x", api.IssueMaxTypeBytes+1)}} {
		if res := e.Do(t, "POST", base+original+"/split-preview", matcher, nil); res.Code != http.StatusBadRequest {
			t.Fatalf("invalid matcher status %d: %s", res.Code, res.Body.String())
		}
	}
	if res := e.Do(t, "POST", base+original+"/split-preview", map[string]string{"route": "/checkout", "message": "raw search"}, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown selector status %d", res.Code)
	}
	page := httptest.NewRequest("GET", "/dashboard/apps/"+app.Slug+"/issues?issue="+sibling+"&split_preview=1&split_original_issue_id="+original+"&split_route=/checkout", nil)
	e.AddAdminSession(t, page)
	rendered := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rendered, page)
	if rendered.Code != http.StatusOK || !strings.Contains(rendered.Body.String(), "12 matching") || !strings.Contains(rendered.Body.String(), "0 events have unavailable") {
		t.Fatalf("dashboard preview %d: %s", rendered.Code, rendered.Body.String())
	}
	other := apidserver.SeedPGApp(t, e, "preview-other")
	if res := e.Do(t, "POST", "/v1/apps/"+other.Slug+"/issues/"+original+"/split-preview", api.IssueSplitMatcher{Route: "/checkout"}, nil); res.Code != http.StatusNotFound {
		t.Fatalf("cross-app preview status %d", res.Code)
	}
	after := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+sibling, nil, nil), http.StatusOK)
	if after.Issue.EventCount != 16 || len(after.GroupMembers) != 2 || len(after.Activity) != len(before.Activity) {
		t.Fatalf("preview changed history or membership: %+v", after)
	}
	if err := sqlc.New().IssuePurgeEvents(t.Context(), (*e.Pool()), sqlc.IssuePurgeEventsParams{AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Before: pgtype.Timestamptz{Time: partialRetentionCutoff, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	got = preview(api.IssueSplitMatcher{Route: "/checkout"})
	if got.LifetimeEventCount != 15 || got.RetainedEventCount != 12 || got.MatchingEventCount != 9 || got.NonmatchingEventCount != 3 || got.UnavailableHistoryEventCount != 3 {
		t.Fatalf("partially expired evidence was classified: %+v", got)
	}
	if err := sqlc.New().IssuePurgeEvents(t.Context(), (*e.Pool()), sqlc.IssuePurgeEventsParams{AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Before: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	got = preview(api.IssueSplitMatcher{Route: "/checkout"})
	if got.LifetimeEventCount != 15 || got.RetainedEventCount != 0 || got.MatchingEventCount != 0 || got.NonmatchingEventCount != 0 || got.UnavailableHistoryEventCount != 15 || len(got.MatchingEventIDs) != 0 {
		t.Fatalf("expired evidence was classified: %+v", got)
	}
}
