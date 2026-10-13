//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1114
func TestIssueRegroupingPreservesOriginalEvidenceAndLifetimeHistory(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-regroup")
	old, fixed := apidserver.IssueSeedDeployment(t, e, app, 1), apidserver.IssueSeedDeployment(t, e, app, 2)
	oldToken, fixedToken := apidserver.IssueCreateToken(t, e, app.Slug, old), apidserver.IssueCreateToken(t, e, app.Slug, fixed)
	requestID := uuid.NewString()
	apidserver.IssueSeedRequestAttribution(t, e, app, old, requestID)
	event := func(function string) api.IssueEvent {
		return api.IssueEvent{
			EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "payment failed",
			GroupingStrategy: issues.FailureSiteStrategy, ErrorCode: "PAYMENT_404", RequestID: requestID,
			Frames: []api.IssueFrame{{File: "/app/src/payments.ts", Function: function, Line: 10, InApp: true}},
		}
	}
	send := func(token api.IssueIngestToken, ev api.IssueEvent) api.IssueEventResponse {
		t.Helper()
		return apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, ev), http.StatusAccepted)
	}
	firstEvent := event("charge")
	source := send(oldToken, firstEvent).IssueID
	send(oldToken, event("charge"))
	target := send(oldToken, event("refund")).IssueID
	base := "/v1/apps/" + app.Slug + "/issues/"
	act := func(id string, in api.IssueActionRequest) api.Issue {
		t.Helper()
		return apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+id+"/actions", in, nil), http.StatusOK)
	}
	get := func(id string) api.IssueDetail {
		t.Helper()
		return apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+id, nil, nil), http.StatusOK)
	}
	original := get(source)
	act(target, api.IssueActionRequest{Action: "assign", AssigneeAccountID: e.Acct().ID})
	act(target, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID})
	merged := act(strings.ToUpper(source), api.IssueActionRequest{Action: "merge", TargetIssueID: strings.ToUpper(target)})
	if merged.ID != target || merged.EventCount != 3 || merged.State != "resolved" || merged.AssigneeAccountID != e.Acct().ID {
		t.Fatalf("merge did not adopt destination lifecycle and combine counters: %+v", merged)
	}
	list := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", strings.TrimSuffix(base, "/")+"?sort=impact&min_customers=1", nil, nil), http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].ID != target || list.Items[0].Impact24h == nil ||
		list.Items[0].Impact24h.IdentifiedCustomers != 1 || list.Items[0].Impact24h.ObservedEvents != 3 {
		t.Fatalf("merged inbox/verified impact was not deduplicated: %+v", list)
	}
	detail := get(source)
	if detail.Issue.ID != target || len(detail.GroupMembers) != 2 || len(detail.Events) != 3 ||
		len(detail.Releases) != 1 || detail.Releases[0].EventCount != 3 {
		t.Fatalf("linked bookmark lost combined evidence: %+v", detail)
	}
	for _, member := range detail.GroupMembers {
		if member.ID == source && (member.EventCount != 2 || member.Fingerprint != original.Issue.Fingerprint) {
			t.Fatal("original identity or lifetime counter changed")
		}
	}
	for _, occurrence := range detail.Events {
		if occurrence.GroupingExplanation == nil || occurrence.OriginalIssueID == "" ||
			occurrence.Frames[0].Function != occurrence.GroupingExplanation.Frames[0].Function {
			t.Fatalf("original grouping explanation lost: %+v", occurrence)
		}
	}
	activityCount := len(detail.Activity)
	act(source, api.IssueActionRequest{Action: "merge", TargetIssueID: target})
	if len(get(target).Activity) != activityCount {
		t.Fatal("repeated merge appended audit history")
	}
	if receipt := send(oldToken, firstEvent); !receipt.Duplicate || receipt.IssueID != target || get(target).Issue.EventCount != 3 {
		t.Fatal("retry did not follow current membership or changed counters")
	}
	conflict := firstEvent
	conflict.Message = "changed payload"
	if w := apidserver.IssueSend(t, e, app.Slug, oldToken, conflict); w.Code != http.StatusConflict {
		t.Fatal("regrouping weakened payload conflict detection")
	}
	if receipt := send(fixedToken, event("charge")); receipt.IssueID != target || !receipt.Regressed {
		t.Fatalf("linked failure did not recur on the canonical issue: %+v", receipt)
	}
	if receipt := send(fixedToken, event("refund")); receipt.Regressed {
		t.Fatal("one recurrence reopened the group more than once")
	}
	detail = get(target)
	if detail.Issue.State != "open" || detail.Issue.EventCount != 5 || detail.Issue.RegressionCount != 1 || len(detail.Releases) != 2 {
		t.Fatalf("merged ingestion/lifecycle counters incorrect: %+v", detail)
	}
	act(source, api.IssueActionRequest{Action: "assign"})
	for _, member := range get(target).GroupMembers {
		if member.AssigneeAccountID != "" {
			t.Fatal("assignment through a linked ID did not apply to all members")
		}
	}
	act(source, api.IssueActionRequest{Action: "assign", AssigneeAccountID: e.Acct().ID})
	until := time.Now().Add(time.Minute)
	act(target, api.IssueActionRequest{Action: "ignore", IgnoredUntil: &until})
	for _, member := range get(source).GroupMembers {
		if member.State != "ignored" || member.IgnoredUntil == nil {
			t.Fatal("ignore did not apply to all members")
		}
	}
	act(source, api.IssueActionRequest{Action: "reopen"})
	act(source, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID})
	remaining := act(target, api.IssueActionRequest{Action: "split", MemberIssueID: source})
	detached := get(source)
	if remaining.ID != target || remaining.EventCount != 2 || remaining.State != "resolved" ||
		detached.Issue.ID != source || detached.Issue.EventCount != 3 || detached.Issue.RegressionCount != 1 ||
		detached.Issue.State != "open" || detached.Issue.ResolvedAt != nil || detached.Issue.AssigneeAccountID != e.Acct().ID ||
		len(detached.GroupMembers) != 0 || len(detached.Releases) != 2 {
		t.Fatalf("split lost original identity/history or changed remaining lifecycle: remaining=%+v detached=%+v", remaining, detached)
	}
	activityCount = len(get(target).Activity)
	act(target, api.IssueActionRequest{Action: "split", MemberIssueID: source})
	if len(get(target).Activity) != activityCount {
		t.Fatal("repeated split appended audit history")
	}
	if receipt := send(oldToken, firstEvent); !receipt.Duplicate || receipt.IssueID != source {
		t.Fatal("event retry did not follow detached identity")
	}
	if receipt := send(fixedToken, event("charge")); receipt.IssueID != source || receipt.Regressed {
		t.Fatal("future captures did not follow detached original identity")
	}
	// Lifetime release totals and identities must survive occurrence retention.
	if err := sqlc.New().IssuePurgeEvents(t.Context(), (*e.Pool()), sqlc.IssuePurgeEventsParams{
		AppID:  pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		Before: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	act(source, api.IssueActionRequest{Action: "merge", TargetIssueID: target})
	detail = get(target)
	if len(detail.Events) != 0 || detail.Issue.EventCount != 6 || len(detail.Releases) != 2 ||
		detail.Releases[0].EventCount+detail.Releases[1].EventCount != 6 || detail.Impact.ObservedEvents != 0 {
		t.Fatalf("merge reconstructed lifetime history from retained events: %+v", detail)
	}
	act(target, api.IssueActionRequest{Action: "split", MemberIssueID: source})
	if got := get(source); got.Issue.EventCount != 4 || len(got.Events) != 0 || len(got.Releases) != 2 {
		t.Fatal("split after retention lost lifetime history")
	}
	// A concurrent split and captures must serialize without moving evidence or
	// losing counters. Receipts can name either root depending on commit order.
	act(source, api.IssueActionRequest{Action: "merge", TargetIssueID: target})
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 6; i++ {
		ev := event("charge")
		wg.Go(func() {
			<-start
			w := apidserver.IssueSend(t, e, app.Slug, fixedToken, ev)
			if w.Code != 202 {
				t.Errorf("concurrent capture: %d %s", w.Code, w.Body.String())
			}
		})
	}
	wg.Go(func() {
		<-start
		w := e.Do(t, "POST", base+target+"/actions", api.IssueActionRequest{Action: "split", MemberIssueID: source}, nil)
		if w.Code != 200 {
			t.Errorf("concurrent split: %d %s", w.Code, w.Body.String())
		}
	})
	close(start)
	wg.Wait()
	if got, root := get(source), get(target); got.Issue.EventCount != 10 || root.Issue.EventCount != 2 ||
		len(got.Events) != 6 || len(root.Events) != 0 {
		t.Fatal("concurrent split/capture lost original counters or occurrences")
	}
}

func TestIssueRegroupingRejectsScopeChangesCyclesAndStaleSources(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-link-bounds")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	capture := func(function string) string {
		t.Helper()
		ev := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error",
			Frames: []api.IssueFrame{{File: "/app/src/main.ts", Function: function, InApp: true}}}
		return apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, ev), 202).IssueID
	}
	a, b, c, d := capture("a"), capture("b"), capture("c"), capture("d")
	path := func(id string) string { return "/v1/apps/" + app.Slug + "/issues/" + id + "/actions" }
	for _, in := range []api.IssueActionRequest{
		{Action: "merge"}, {Action: "split"}, {Action: "merge", TargetIssueID: "bad"},
		{Action: "merge", TargetIssueID: a}, {Action: "split", MemberIssueID: a},
		{Action: "merge", TargetIssueID: b, AssigneeAccountID: e.Acct().ID},
		{Action: "resolve", FixedDeploymentID: dep.ID, MemberIssueID: b},
	} {
		if w := e.Do(t, "POST", path(a), in, nil); w.Code != 400 {
			t.Fatalf("invalid action accepted: %+v status=%d %s", in, w.Code, w.Body.String())
		}
	}
	other := apidserver.SeedPGApp(t, e, "issue-link-other")
	otherDep := apidserver.IssueSeedDeployment(t, e, other, 1)
	otherToken := apidserver.IssueCreateToken(t, e, other.Slug, otherDep)
	foreign := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, other.Slug, otherToken,
		api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Other"}), 202)
	if w := e.Do(t, "POST", path(a), api.IssueActionRequest{Action: "merge", TargetIssueID: foreign.IssueID}, nil); w.Code != 404 {
		t.Fatal("cross-app destination was exposed or accepted")
	}
	q := sqlc.New()
	staging, err := q.IssueCreate(t.Context(), (*e.Pool()), sqlc.IssueCreateParams{
		AccountID: pgtype.UUID{Bytes: uuid.MustParse(e.Acct().ID), Valid: true},
		AppID:     pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Environment: "staging",
		Fingerprint: strings.Repeat("a", 64), GroupingVersion: 1, Title: "staging",
		OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := e.Do(t, "POST", path(a), api.IssueActionRequest{Action: "merge", TargetIssueID: uuid.UUID(staging.ID.Bytes).String()}, nil); w.Code != 409 {
		t.Fatal("cross-environment merge accepted")
	}
	for _, pair := range [][2]string{{a, b}, {b, c}} {
		apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", path(pair[0]), api.IssueActionRequest{Action: "merge", TargetIssueID: pair[1]}, nil), 200)
	}
	for _, in := range []struct {
		id     string
		action api.IssueActionRequest
	}{
		{a, api.IssueActionRequest{Action: "merge", TargetIssueID: d}},
		{c, api.IssueActionRequest{Action: "merge", TargetIssueID: a}},
		{d, api.IssueActionRequest{Action: "split", MemberIssueID: a}},
	} {
		if w := e.Do(t, "POST", path(in.id), in.action, nil); w.Code != 409 {
			t.Fatalf("stale source/cycle/unrelated split accepted: %+v status=%d", in, w.Code)
		}
	}
	group := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+a, nil, nil), 200)
	if group.Issue.ID != c || len(group.GroupMembers) != 3 || group.Issue.EventCount != 3 {
		t.Fatal("merge of existing groups did not flatten history")
	}
	// The persistence guard also rejects direct nested/cyclic links.
	if err := q.IssueLinkGroup(t.Context(), (*e.Pool()), sqlc.IssueLinkGroupParams{
		SourceID: pgtype.UUID{Bytes: uuid.MustParse(c), Valid: true},
		TargetID: pgtype.UUID{Bytes: uuid.MustParse(a), Valid: true},
		AppID:    pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Environment: "application",
		Now: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err == nil {
		t.Fatal("database accepted a cycle")
	}
}

func TestIssueRegroupingMemberBoundRejectsWithoutPartialChanges(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-member-limit")
	q := sqlc.New()
	var identities []pgtype.UUID
	for i := 0; i <= api.IssueGroupMembersMax; i++ {
		row, err := q.IssueCreate(t.Context(), (*e.Pool()), sqlc.IssueCreateParams{
			AccountID: pgtype.UUID{Bytes: uuid.MustParse(e.Acct().ID), Valid: true},
			AppID:     pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Environment: "application",
			Fingerprint: strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", ""), GroupingVersion: 1, Title: "bounded identity",
			OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, row.ID)
	}
	for _, id := range identities[1:api.IssueGroupMembersMax] {
		if err := q.IssueLinkGroup(t.Context(), (*e.Pool()), sqlc.IssueLinkGroupParams{
			SourceID: id, TargetID: identities[0], AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
			Environment: "application", Now: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	root, extra := uuid.UUID(identities[0].Bytes).String(), uuid.UUID(identities[len(identities)-1].Bytes).String()
	base := "/v1/apps/" + app.Slug + "/issues/"
	w := e.Do(t, "POST", base+extra+"/actions", api.IssueActionRequest{Action: "merge", TargetIssueID: root}, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "33") {
		t.Fatalf("group member bound lacks evidence: %d %s", w.Code, w.Body.String())
	}
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+root, nil, nil), 200)
	separate := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+extra, nil, nil), 200)
	if len(detail.GroupMembers) != api.IssueGroupMembersMax || separate.Issue.ID != extra || len(detail.Activity) != 0 {
		t.Fatal("rejected merge partially changed membership or audit")
	}
}

func TestIssueRegroupingLateAttributionNotifiesCurrentCanonicalIssue(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-regroup-impact")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	store := (*e.Store()).(state.IssueStore)
	if _, err := store.SetIssueImpactAlertPolicy(t.Context(), app.ID, e.Acct().ID, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	hook, err := (*e.Store()).CreateAppWebhook(t.Context(), state.AppWebhook{
		AccountID: e.Acct().ID, AppID: app.ID, TargetURL: "https://example.com/issues", Enabled: true,
		EventFilter: []string{"issue.impact_threshold_reached"}, SecretSealed: []byte("test-sealed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceRequest, targetRequest := uuid.NewString(), uuid.NewString()
	apidserver.IssueSeedRequestAttribution(t, e, app, dep, targetRequest)
	capture := func(function, requestID string) string {
		t.Helper()
		return apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, api.IssueEvent{
			EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", RequestID: requestID,
			Frames: []api.IssueFrame{{File: "/app/src/main.ts", Function: function, InApp: true}},
		}), 202).IssueID
	}
	source, target := capture("a", sourceRequest), capture("b", targetRequest)
	if _, err := store.ActOnIssue(t.Context(), app.ID, source, e.Acct().ID,
		api.IssueActionRequest{Action: "merge", TargetIssueID: target}, time.Now()); err != nil {
		t.Fatal(err)
	}
	apidserver.IssueSeedRequestAttribution(t, e, app, dep, sourceRequest)
	if err := (*e.Store()).(*state.PgStore).MaintainIssues(t.Context(), time.Now().Add(2*api.IssueMaintenanceInterval)); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetIssueDetail(t.Context(), app.ID, source, time.Now().Add(-time.Hour), time.Now(), state.IssueDetailCursors{})
	if err != nil || detail.Issue.ID != target || detail.Impact.IdentifiedCustomers != 2 || detail.Impact.UnattributedEvents != 0 {
		t.Fatalf("late attribution ignored current membership: %+v %v", detail, err)
	}
	if n, err := (*e.Store()).(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 10); err != nil || n != 1 {
		t.Fatalf("canonical impact transition: %d %v", n, err)
	}
	deliveries, _, err := (*e.Store()).ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("canonical impact notification missing: %+v %v", deliveries, err)
	}
	raw, err := json.Marshal(deliveries[0])
	if err != nil || !strings.Contains(string(raw), target) {
		t.Fatal("impact notification did not identify canonical issue")
	}
}

func TestIssueRegroupingCombinesEarlyReleaseMetadataByDeploymentIdentity(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-release-metadata")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	q := sqlc.New()
	var groups []pgtype.UUID
	for i, commit := range []string{"", strings.Repeat("b", 40)} {
		row, err := q.IssueCreate(t.Context(), (*e.Pool()), sqlc.IssueCreateParams{
			AccountID: pgtype.UUID{Bytes: uuid.MustParse(e.Acct().ID), Valid: true},
			AppID:     pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, Environment: "application",
			Fingerprint: strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", ""), GroupingVersion: 1, Title: "release evidence",
			OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, row.ID)
		if _, err := q.IssueObserve(t.Context(), (*e.Pool()), sqlc.IssueObserveParams{
			ID: row.ID, OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
		image := ""
		if i == 1 {
			image = dep.ImageDigest
		}
		if err := q.IssueObserveRelease(t.Context(), (*e.Pool()), sqlc.IssueObserveReleaseParams{
			IssueID: row.ID, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true},
			CommitSha: commit, ImageDigest: image, OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	store := (*e.Store()).(state.IssueStore)
	source, target := uuid.UUID(groups[0].Bytes).String(), uuid.UUID(groups[1].Bytes).String()
	if _, err := store.ActOnIssue(t.Context(), app.ID, source, e.Acct().ID,
		api.IssueActionRequest{Action: "merge", TargetIssueID: target}, time.Now()); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetIssueDetail(t.Context(), app.ID, target, time.Now().Add(-time.Hour), time.Now(), state.IssueDetailCursors{})
	if err != nil || len(detail.Releases) != 1 || detail.Releases[0].DeploymentID != dep.ID ||
		detail.Releases[0].EventCount != 2 || detail.Releases[0].CommitSHA == "" || detail.Releases[0].ImageDigest != dep.ImageDigest {
		t.Fatalf("early metadata split one deployment's release history: %+v %v", detail, err)
	}
}
