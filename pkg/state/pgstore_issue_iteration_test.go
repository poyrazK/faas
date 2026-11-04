package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgIssueWeightedHTTPAndDeletion(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	limits := api.PlanPro.IssueLimits()
	limits.IssuesPerApp = 1
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "HTTPError", SourceKind: "http"}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "HTTPError", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, OccurrenceCount: 2000, Limits: limits, Now: now}
	out, err := s.RecordIssue(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.RecordIssue(ctx, in)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	consumer := uuid.NewString()
	if _, err = pool.Exec(ctx, `UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2 AND event_id=$3`, consumer, app.ID, event.EventID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetIssueDetail(ctx, app.ID, out.IssueID, now.Add(-time.Minute), now.Add(time.Minute), state.IssueDetailCursors{})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Issue.EventCount != 2000 || len(detail.Releases) != 1 || detail.Releases[0].EventCount != 2000 || len(detail.Events) != 1 || detail.Events[0].OccurrenceCount != 2000 || detail.Impact.ObservedEvents != 2000 || detail.Impact.UnattributedEvents != 1999 || detail.Impact.IdentifiedCustomers != 1 {
		t.Fatalf("weighted detail: %+v events=%+v releases=%+v impact=%+v", detail.Issue, detail.Events, detail.Releases, detail.Impact)
	}
	count, err := sqlc.New().IssueFrequencyCount(ctx, pool, sqlc.IssueFrequencyCountParams{IssueID: state.NewPgtypeUUID(uuid.MustParse(out.IssueID)), Since: state.NewPgtypeTime(now.Add(-time.Minute)), Until: state.NewPgtypeTime(now.Add(time.Minute))})
	if err != nil || count != 2000 {
		t.Fatalf("frequency=%d err=%v", count, err)
	}
	in.Event.EventID = uuid.NewString()
	in.Fingerprint = strings.Repeat("c", 64)
	in.PayloadHash = strings.Repeat("d", 64)
	if _, err = s.RecordIssue(ctx, in); !errors.Is(err, state.ErrIssueQuota) {
		t.Fatalf("expected group limit, got %v", err)
	}
	if err = s.DeleteIssue(ctx, app.ID, out.IssueID); err != nil {
		t.Fatal(err)
	}
	second, err := s.RecordIssue(ctx, in)
	if err != nil {
		t.Fatalf("new group after deletion: %v", err)
	}
	in.Limits.IssuesPerApp = 2
	in.Event.EventID = uuid.NewString()
	in.Fingerprint = strings.Repeat("e", 64)
	in.PayloadHash = strings.Repeat("f", 64)
	third, err := s.RecordIssue(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActOnIssue(ctx, app.ID, second.IssueID, account.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: third.IssueID}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteIssue(ctx, app.ID, third.IssueID); !errors.Is(err, state.ErrIssueGroupingConflict) {
		t.Fatalf("deleting a linked group must fail: %v", err)
	}
}

func TestPgIssueHistoricalAttributionReview(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	request := uuid.NewString()
	traceA, traceB := strings.Repeat("a", 32), strings.Repeat("b", 32)
	if _, err := pool.Exec(ctx, `INSERT INTO request_id_journal(id,account_id,app_id,request_id,trace_id,received_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), account.ID, app.ID, request, traceA, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	record := func(req string) string {
		t.Helper()
		e := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", RequestID: req, TraceID: traceB}
		_, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: e, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return e.EventID
	}
	conflict, unknown, missingInvocation := record(request), record(uuid.NewString()), record(request)
	consumer, tenant := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET verified_consumer_id=$1, verified_platform_tenant_id=$2, attribution_version=1, attribution_checked_at='infinity',received_at=$3 WHERE app_id=$4`, consumer, tenant, now.Add(-24*time.Hour), app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET payload=jsonb_set(payload,'{invocation_id}',to_jsonb($1::text)) WHERE event_id=$2`, uuid.NewString(), missingInvocation); err != nil {
		t.Fatal(err)
	}
	if err := s.MaintainIssues(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id   string
		want bool
	}{{conflict, false}, {unknown, true}, {missingInvocation, false}} {
		var has bool
		var version int
		if err := pool.QueryRow(ctx, `SELECT verified_consumer_id IS NOT NULL,attribution_version FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, item.id).Scan(&has, &version); err != nil || has != item.want || version != 2 {
			t.Fatalf("%s: has=%v version=%d err=%v", item.id, has, version, err)
		}
	}
	// A pre-upgrade writer changes attribution without setting the version.
	// The database fence must schedule the row for review again.
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET verified_consumer_id=$1, verified_platform_tenant_id=$2 WHERE app_id=$3 AND event_id=$4`, consumer, tenant, app.ID, conflict); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT attribution_version FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, conflict).Scan(&version); err != nil || version != 1 {
		t.Fatalf("old writer was not fenced: version=%d err=%v", version, err)
	}
	if err := s.MaintainIssues(ctx, now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var stillAttributed bool
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id IS NOT NULL,attribution_version FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, conflict).Scan(&stillAttributed, &version); err != nil || stillAttributed || version != 2 {
		t.Fatalf("old writer update was not reviewed: attributed=%v version=%d err=%v", stillAttributed, version, err)
	}
}

func TestPgIssueMaintenanceSkipsLockedApp(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	a1, app1 := seedPgAccountAndApp(t, s, ctx)
	dep1 := seedPgDeployment(t, s, ctx, app1)
	a2, app2 := seedPgAccountAndApp(t, s, ctx)
	dep2 := seedPgDeployment(t, s, ctx, app2)
	now := time.Now().UTC().Truncate(time.Microsecond)
	record := func(acct state.Account, app state.App, dep state.Deployment) string {
		t.Helper()
		e := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", RequestID: uuid.NewString()}
		_, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: e, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return e.EventID
	}
	first, second := record(a1, app1, dep1), record(a2, app2, dep2)
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET received_at=$1,attribution_checked_at=$1 WHERE event_id=$2`, now.Add(-3*time.Minute), first); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET received_at=$1,attribution_checked_at=$1 WHERE event_id=$2`, now.Add(-2*time.Minute), second); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app1.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = s.MaintainIssues(ctx, now); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("locked app stalled maintenance")
	}
	var checked time.Time
	if err = pool.QueryRow(ctx, `SELECT attribution_checked_at FROM issue_events WHERE event_id=$1`, second).Scan(&checked); err != nil || !checked.Equal(now) {
		t.Fatalf("other app was not reviewed: %v %v", checked, err)
	}
}

func TestPgIssueWeightedSplitPreservesCounts(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	e := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "HTTPError", SourceKind: "http"}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: e, Fingerprint: strings.Repeat("a", 64), Title: "HTTPError", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, OccurrenceCount: 2000, Limits: api.PlanPro.IssueLimits(), Now: now}
	out, err := s.RecordIssue(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	matcher := api.IssueSplitMatcher{SourceKind: "http"}
	preview, err := s.PreviewIssueSplit(ctx, app.ID, out.IssueID, matcher)
	if err != nil || preview.RetainedEventCount != 2000 || preview.MatchingEventCount != 2000 || preview.UnavailableHistoryEventCount != 0 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	rule, err := s.CreateIssueSplitRule(ctx, app.ID, out.IssueID, account.ID, matcher, in.Limits, now)
	if err != nil || rule.InitialMovedEventCount != 2000 {
		t.Fatalf("split=%+v err=%v", rule, err)
	}
	detail, err := s.GetIssueDetail(ctx, app.ID, rule.TargetIssueID, now.Add(-time.Minute), now.Add(time.Minute), state.IssueDetailCursors{})
	if err != nil || detail.Issue.EventCount != 2000 || len(detail.Releases) != 1 || detail.Releases[0].EventCount != 2000 {
		t.Fatalf("branch=%+v err=%v", detail, err)
	}
}

func TestPgIssueDeleteProtectsNativeTicketRecovery(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	newIssue := func(fingerprint string) string {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error"}
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat(fingerprint, 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return out.IssueID
	}
	id := newIssue("a")
	in := state.NativeIssueTicketReceipt{AccountID: account.ID, AppID: app.ID, IssueID: id, RequestID: uuid.NewString(), Hash: strings.Repeat("c", 64)}
	receipt, err := s.ReserveNativeIssueTicket(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNativeIssueTicket(ctx, receipt, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteIssue(ctx, app.ID, id); !errors.Is(err, state.ErrIssueTicketPending) {
		t.Fatalf("dispatching receipt did not block deletion: %v", err)
	}
	claimed.State = "created"
	claimed.TicketURL = "https://github.com/org/repo/issues/1"
	claimed.ProviderID = "1"
	created, err := s.FinishNativeIssueTicket(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteIssue(ctx, app.ID, id); !errors.Is(err, state.ErrIssueTicketPending) {
		t.Fatalf("unattached ticket did not block deletion: %v", err)
	}
	if _, err = s.DeleteIssueWithOptions(ctx, app.ID, id, true); !errors.Is(err, state.ErrIssueTicketPending) {
		t.Fatalf("explicit abandonment bypassed a confirmed ticket: %v", err)
	}
	link, err := s.PutIssueTrackerLink(ctx, app.ID, account.ID, id, account.ID, api.PutIssueTrackerLinkRequest{URL: created.TicketURL}, now)
	if err != nil {
		t.Fatal(err)
	}
	created.LinkID = link.ID
	if linked, err := s.LinkNativeIssueTicket(ctx, created); err != nil || linked.State != "linked" {
		t.Fatalf("manual attachment did not reconcile the receipt: %+v %v", linked, err)
	}
	if err = s.DeleteIssue(ctx, app.ID, id); err != nil {
		t.Fatalf("linked ticket still blocks deletion: %v", err)
	}
	// Deleting a ready request serializes with a concurrent claim; a stale claim
	// cannot start a provider request after the receipt has been removed.
	readyID := newIssue("d")
	in.IssueID = readyID
	in.RequestID = uuid.NewString()
	ready, err := s.ReserveNativeIssueTicket(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteIssue(ctx, app.ID, readyID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimNativeIssueTicket(ctx, ready, false); !errors.Is(err, state.ErrNativeIssueTicketBusy) {
		t.Fatalf("stale claim succeeded: %v", err)
	}
	uncertainID := newIssue("e")
	in.IssueID, in.RequestID = uncertainID, uuid.NewString()
	uncertain, err := s.ReserveNativeIssueTicket(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	uncertain, err = s.ClaimNativeIssueTicket(ctx, uncertain, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteIssueWithOptions(ctx, app.ID, uncertainID, true); !errors.Is(err, state.ErrIssueTicketPending) {
		t.Fatalf("active provider dispatch abandoned: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE native_issue_ticket_receipts SET recover_after=now()-interval '1 second' WHERE request_id=$1`, in.RequestID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteIssue(ctx, app.ID, uncertainID); !errors.Is(err, state.ErrIssueTicketPending) {
		t.Fatalf("default deletion abandoned uncertain dispatch: %v", err)
	}
	abandoned, err := s.DeleteIssueWithOptions(ctx, app.ID, uncertainID, true)
	if err != nil || len(abandoned) != 1 || abandoned[0] != in.RequestID {
		t.Fatalf("explicit abandonment=%v err=%v", abandoned, err)
	}
	uncertain.State = "created"
	uncertain.TicketURL, uncertain.ProviderID = "https://github.com/org/repo/issues/2", "2"
	if _, err := s.FinishNativeIssueTicket(ctx, uncertain); !errors.Is(err, state.ErrNativeIssueTicketBusy) {
		t.Fatalf("stale dispatch resurrected deleted receipt: %v", err)
	}
}

// The lazy counter remains exact across old-style writes, deletes, rollbacks,
// and a deletion that races with first initialization.
func TestPgIssueEventUsageTracksConcurrentAndLegacyWrites(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	base := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error"}, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
	out, err := s.RecordIssue(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	count := func() {
		t.Helper()
		var stored, actual int64
		var ready bool
		err := pool.QueryRow(ctx, `SELECT retained_count,initialized,(SELECT count(*) FROM issue_events WHERE app_id=$1) FROM issue_event_usage WHERE app_id=$1`, app.ID).Scan(&stored, &ready, &actual)
		if err != nil || !ready || stored != actual {
			t.Fatalf("usage=%d ready=%v actual=%d err=%v", stored, ready, actual, err)
		}
	}
	count()
	if _, err = s.RecordIssue(ctx, base); err != nil {
		t.Fatal(err)
	}
	count()
	if _, err = pool.Exec(ctx, `UPDATE issue_event_usage SET initialized=false,retained_count=0 WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate an old writer, which does not know about the usage table.
	if _, err = pool.Exec(ctx, `INSERT INTO issue_events(app_id,deployment_id,event_id,issue_id,payload_hash,payload,occurred_at,received_at) SELECT app_id,deployment_id,gen_random_uuid(),issue_id,payload_hash,payload,occurred_at,received_at FROM issue_events WHERE app_id=$1 LIMIT 1`, app.ID); err != nil {
		t.Fatal(err)
	}
	next := base
	next.Event.EventID = uuid.NewString()
	if _, err = s.RecordIssue(ctx, next); err != nil {
		t.Fatal(err)
	}
	count()
	// A direct legacy deletion updates the initialized counter.
	if _, err = pool.Exec(ctx, `DELETE FROM issue_events WHERE event_id=$1`, base.Event.EventID); err != nil {
		t.Fatal(err)
	}
	count()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM issue_events WHERE event_id=$1`, next.Event.EventID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	count()
	// Deletion has changed a row but holds the usage lock. First initialization
	// must wait for its commit without deadlocking on the app lock.
	if _, err = pool.Exec(ctx, `UPDATE issue_event_usage SET initialized=false,retained_count=0 WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `DELETE FROM issue_events WHERE event_id=$1`, next.Event.EventID); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		later := base
		later.Event.EventID = uuid.NewString()
		_, err := s.RecordIssue(bounded, later)
		done <- err
	}()
	// Wait until ingestion holds the app lock while the old deletion holds
	// the usage lock. The deletion must still be able to commit.
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, lockErr := probe.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE NOWAIT`, app.ID)
		_ = probe.Rollback(ctx)
		var pgErr *pgconn.PgError
		if errors.As(lockErr, &pgErr) && pgErr.Code == "55P03" {
			break
		}
		if lockErr != nil || time.Now().After(deadline) {
			t.Fatalf("ingest did not acquire app lock: %v", lockErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatalf("ingest racing deletion: %v", err)
	}
	count()
	if err = s.DeleteIssue(ctx, app.ID, out.IssueID); err != nil {
		t.Fatal(err)
	}
	count()
}

func TestPgIssueFullQuotaCommitsBoundedExpiredCleanup(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	limits := api.PlanPro.IssueLimits()
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error"}, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: limits, Now: now}
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO issue_events(app_id,deployment_id,event_id,issue_id,payload_hash,payload,occurred_at,received_at) SELECT e.app_id,e.deployment_id,gen_random_uuid(),e.issue_id,e.payload_hash,e.payload,e.occurred_at,e.received_at FROM issue_events e CROSS JOIN generate_series(1,201) WHERE e.app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET received_at=$1 WHERE app_id=$2`, now.AddDate(0, 0, -31), app.ID); err != nil {
		t.Fatal(err)
	}
	in.Event.EventID = uuid.NewString()
	in.Limits.EventsPerApp = 1
	for i := 0; i < 2; i++ {
		if _, err := s.RecordIssue(ctx, in); !errors.Is(err, state.ErrIssueQuota) {
			t.Fatalf("attempt %d: expected quota, got %v", i, err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue_events WHERE app_id=$1`, app.ID).Scan(&n); err != nil || n != 202-(i+1)*api.IssueIngestPurgeBatch {
			t.Fatalf("attempt %d: cleanup count=%d err=%v", i, n, err)
		}
	}
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatalf("bounded cleanup did not eventually admit event: %v", err)
	}
}

func TestPgIssueLegacyHTTPWeightIsReportedAsUnknown(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "HTTPError", SourceKind: "http"}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "HTTPError", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
	out, err := s.RecordIssue(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	// Mimic an older writer: it knows neither the weight nor its marker.
	if _, err = pool.Exec(ctx, `INSERT INTO issue_events(app_id,deployment_id,event_id,issue_id,payload_hash,payload,occurred_at,received_at) SELECT app_id,deployment_id,gen_random_uuid(),issue_id,payload_hash,payload,occurred_at,received_at FROM issue_events WHERE app_id=$1 LIMIT 1`, app.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetIssueDetail(ctx, app.ID, out.IssueID, now.Add(-time.Minute), now.Add(time.Minute), state.IssueDetailCursors{})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Impact.ObservedEvents != 2 || detail.Impact.UnknownHTTPBatches != 1 || len(detail.Events) != 2 {
		t.Fatalf("impact=%+v events=%+v", detail.Impact, detail.Events)
	}
	exact := 0
	for _, e := range detail.Events {
		if e.OccurrenceCountExact {
			exact++
		}
	}
	if exact != 1 {
		t.Fatalf("want one exact and one unknown occurrence: %+v", detail.Events)
	}
	for _, sort := range []string{"", "impact"} {
		list, err := s.ListIssues(ctx, app.ID, state.IssueListFilter{Sort: sort}, state.IssueCursor{})
		if err != nil || len(list.Items) != 1 || list.Items[0].Impact24h == nil || list.Items[0].Impact24h.UnknownHTTPBatches != 1 {
			t.Fatalf("sort=%q list=%+v err=%v", sort, list, err)
		}
	}
	health, err := sqlc.New().IssueReleaseHealth(ctx, pool, sqlc.IssueReleaseHealthParams{AccountID: state.NewPgtypeUUID(uuid.MustParse(account.ID)), AppID: state.NewPgtypeUUID(uuid.MustParse(app.ID)), DeploymentID: state.NewPgtypeUUID(uuid.MustParse(dep.ID)), Environment: "application", WindowStart: state.NewPgtypeTime(now.Add(-time.Minute)), WindowEnd: state.NewPgtypeTime(now.Add(time.Minute))})
	if err != nil || health.UnknownHttpBatches != 1 {
		t.Fatalf("release coverage=%+v err=%v", health, err)
	}
}

func TestPgNativeTicketQuotaExcludesTerminalReceipts(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error"}
	out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	// A rejected provider request returns to ready with its claim ID retained.
	_, err = pool.Exec(ctx, `INSERT INTO native_issue_ticket_receipts(account_id,request_id,app_id,issue_id,request_hash,state,claim_id)
 SELECT $1,gen_random_uuid(),$2,$3,$4,'ready',gen_random_uuid() FROM generate_series(1,100)`, account.ID, app.ID, out.IssueID, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO native_issue_ticket_receipts(account_id,request_id,app_id,issue_id,request_hash,state,ticket_url,provider_id,link_id)
 SELECT $1,gen_random_uuid(),$2,$3,$4,'linked','https://github.com/org/repo/issues/1','1',gen_random_uuid() FROM generate_series(1,100)`, account.ID, app.ID, out.IssueID, strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	in := state.NativeIssueTicketReceipt{AccountID: account.ID, AppID: app.ID, IssueID: out.IssueID, RequestID: uuid.NewString(), Hash: strings.Repeat("e", 64)}
	if _, err := s.ReserveNativeIssueTicket(ctx, in); err != nil {
		t.Fatalf("terminal receipts exhausted unresolved slots: %v", err)
	}
	var retained int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM native_issue_ticket_receipts WHERE issue_id=$1`, out.IssueID).Scan(&retained); err != nil || retained != 201 {
		t.Fatalf("idempotency receipts were discarded: %d %v", retained, err)
	}
	var rejectedID string
	if err := pool.QueryRow(ctx, `SELECT request_id::text FROM native_issue_ticket_receipts WHERE issue_id=$1 AND state='ready' AND claim_id IS NOT NULL LIMIT 1`, out.IssueID).Scan(&rejectedID); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO native_issue_ticket_receipts(account_id,request_id,app_id,issue_id,request_hash) SELECT $1,gen_random_uuid(),$2,$3,$4 FROM generate_series(1,99)`, account.ID, app.ID, out.IssueID, strings.Repeat("f", 64))
	if err != nil {
		t.Fatal(err)
	}
	rejected := state.NativeIssueTicketReceipt{AccountID: account.ID, AppID: app.ID, IssueID: out.IssueID, RequestID: rejectedID}
	var limit *state.IssueLimitError
	if _, err := s.ClaimNativeIssueTicket(ctx, rejected, false); !errors.As(err, &limit) || limit.Resource != "native_ticket_requests" {
		t.Fatalf("retry bypassed unresolved quota: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM native_issue_ticket_receipts WHERE request_id=$1`, in.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNativeIssueTicket(ctx, rejected, false); err != nil {
		t.Fatalf("rejected request could not reacquire freed slot: %v", err)
	}
}

func TestPgHTTPTimeBucketsRespectWindowBoundaries(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	base := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Second)
	sample := base.Add(100 * time.Millisecond)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: sample, ExceptionType: "HTTPError", SourceKind: "http"}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "HTTPError", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, OccurrenceCount: 5, TimeBuckets: [][2]int64{{sample.UnixMilli(), 1}, {base.Add(400 * time.Millisecond).UnixMilli(), 4}}, Limits: api.PlanPro.IssueLimits(), Now: base.Add(2 * time.Second)}
	out, err := s.RecordIssue(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	start, end := base.Add(300*time.Millisecond), base.Add(900*time.Millisecond)
	detail, err := s.GetIssueDetail(ctx, app.ID, out.IssueID, start, end, state.IssueDetailCursors{})
	if err != nil || detail.Impact.ObservedEvents != 4 || detail.Impact.UnattributedEvents != 4 {
		t.Fatalf("partial window impact=%+v err=%v", detail.Impact, err)
	}
	if want := base.Add(400 * time.Millisecond); !detail.Issue.LastSeenAt.Equal(want) || len(detail.Releases) != 1 || !detail.Releases[0].LastSeenAt.Equal(want) {
		t.Fatalf("latest failure was not reflected in issue and release recency: %+v %+v", detail.Issue, detail.Releases)
	}
	q := sqlc.New()
	issueID := state.NewPgtypeUUID(uuid.MustParse(out.IssueID))
	count, err := q.IssueFrequencyCount(ctx, pool, sqlc.IssueFrequencyCountParams{IssueID: issueID, Since: state.NewPgtypeTime(start), Until: state.NewPgtypeTime(end)})
	if err != nil || count != 4 {
		t.Fatalf("frequency count=%d err=%v", count, err)
	}
	count, err = q.IssueFrequencyCount(ctx, pool, sqlc.IssueFrequencyCountParams{IssueID: issueID, Since: state.NewPgtypeTime(base.Add(400 * time.Millisecond)), Until: state.NewPgtypeTime(end)})
	if err != nil || count != 0 {
		t.Fatalf("exclusive boundary count=%d err=%v", count, err)
	}
	health, err := q.IssueReleaseHealth(ctx, pool, sqlc.IssueReleaseHealthParams{AccountID: state.NewPgtypeUUID(uuid.MustParse(account.ID)), AppID: state.NewPgtypeUUID(uuid.MustParse(app.ID)), DeploymentID: state.NewPgtypeUUID(uuid.MustParse(dep.ID)), Environment: "application", WindowStart: state.NewPgtypeTime(start), WindowEnd: state.NewPgtypeTime(end)})
	if err != nil || health.ObservedIssueEvents != 4 {
		t.Fatalf("release count=%d err=%v", health.ObservedIssueEvents, err)
	}
	rule, err := s.CreateIssueSplitRule(ctx, app.ID, out.IssueID, account.ID, api.IssueSplitMatcher{SourceKind: "http"}, in.Limits, in.Now)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := s.GetIssueDetail(ctx, app.ID, rule.TargetIssueID, start, end, state.IssueDetailCursors{})
	if err != nil || !branch.Issue.LastSeenAt.Equal(base.Add(400*time.Millisecond)) || len(branch.Releases) != 1 || !branch.Releases[0].LastSeenAt.Equal(base.Add(400*time.Millisecond)) || branch.Impact.ObservedEvents != 4 {
		t.Fatalf("split lost bucket timing: %+v %+v %v", branch.Issue, branch.Releases, err)
	}
	in.Event.EventID = uuid.NewString()
	in.TimeBuckets[1][1] = 3
	if _, err := s.RecordIssue(ctx, in); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid histogram accepted: %v", err)
	}
}
