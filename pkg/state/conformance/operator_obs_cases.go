package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperatorQueuedBuildsCount(t *testing.T, fx *Fixture) {
	count := func(want int) {
		t.Helper()
		got, err := fx.Store.QueuedBuildsCount(fx.Ctx)
		if err != nil || got != want {
			t.Fatalf("queued builds = %d, %v; want %d", got, err, want)
		}
	}
	count(0)
	first, second := seedQueuedBuild(t, fx), seedQueuedBuild(t, fx)
	count(2)
	for _, status := range []state.BuildStatus{state.BuildRunning, state.BuildSucceeded, state.BuildFailed, state.BuildCancelled} {
		build := seedQueuedBuild(t, fx)
		count(3)
		if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, build.ID); err != nil {
			t.Fatal(err)
		}
		if status != state.BuildRunning {
			if err := fx.Store.UpdateBuildStatus(fx.Ctx, build.ID, status, "", false, true); err != nil {
				t.Fatal(err)
			}
		}
		count(2)
	}
	if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	count(1)
	if err := fx.Store.UpdateBuildStatus(fx.Ctx, second.ID, state.BuildCancelled, "", false, true); err != nil {
		t.Fatal(err)
	}
	count(0)
	seedQueuedBuild(t, fx)
	count(1)
}

func testOperatorBuildQueueSnapshot(t *testing.T, fx *Fixture) {
	reader, ok := fx.Store.(state.BuildQueueObservationStore)
	if !ok {
		t.Fatal("store lacks coherent queue observation")
	}
	check := func(want int64, oldest time.Time) {
		t.Helper()
		got, err := reader.QueuedBuildsSnapshot(fx.Ctx)
		if err != nil || got.Queued != want || !got.OldestEnqueuedAt.Equal(oldest) {
			t.Fatalf("snapshot = %+v, %v; want %d, %v", got, err, want, oldest)
		}
	}
	check(0, time.Time{})
	first := seedQueuedBuild(t, fx)
	second := seedQueuedBuild(t, fx)
	check(2, first.EnqueuedAt)
	for range 3 {
		if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, first.ID); err != nil {
			t.Fatal(err)
		}
		check(1, second.EnqueuedAt)
		if err := fx.Store.RequeueBuild(fx.Ctx, first.ID); err != nil {
			t.Fatal(err)
		}
		check(2, first.EnqueuedAt)
	}
	if err := fx.Store.UpdateBuildStatus(fx.Ctx, first.ID, state.BuildCancelled, "", false, true); err != nil {
		t.Fatal(err)
	}
	check(1, second.EnqueuedAt)
	if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	check(0, time.Time{})
	if err := fx.Store.UpdateBuildStatus(fx.Ctx, second.ID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	check(0, time.Time{})
	failed := seedQueuedBuild(t, fx)
	if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, failed.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateBuildStatus(fx.Ctx, failed.ID, state.BuildFailed, state.FailureInfra, false, true); err != nil {
		t.Fatal(err)
	}
	check(0, time.Time{})
	fresh := seedQueuedBuild(t, fx)
	check(1, fresh.EnqueuedAt)
}

func testOperatorAlertTriageRetention(t *testing.T, fx *Fixture) {
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	rule, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	episode := state.OperatorAlertEpisode{RuleKey: rule, SeriesID: series, Name: "FaasApiDown", Component: "apid", Severity: "critical", StartedAt: now.Add(-time.Minute)}
	episode.ID = state.OperatorAlertEpisodeID(rule, series, episode.StartedAt)
	snapshot := state.OperatorAlertSnapshot{ObservedAt: now, Rules: []state.OperatorAlertRuleObservation{{Key: rule, EvaluatedAt: now, EpisodeIDs: []string{episode.ID}}}, Episodes: []state.OperatorAlertEpisode{episode}}
	if err := fx.Store.RecordOperatorAlertSnapshot(fx.Ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	collection, err := fx.Store.OperatorAlertCollectionStatus(fx.Ctx)
	if err != nil || !collection.LastSuccessAt.Equal(now) {
		t.Fatalf("collection: %+v %v", collection, err)
	}
	triage, err := fx.Store.MutateOperatorAlertTriage(fx.Ctx, state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: fx.Account.ID, Action: "acknowledge", Now: now})
	if err != nil || triage.Revision != 1 || !triage.Acknowledged {
		t.Fatalf("triage: %+v %v", triage, err)
	}
	if _, err := fx.Store.MutateOperatorAlertTriage(fx.Ctx, state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: fx.Account.ID, Action: "unacknowledge", Now: now}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale triage: %v", err)
	}
	history, err := fx.Store.OperatorAlertTriageHistory(fx.Ctx, state.OperatorAlertTriageHistoryFilter{ID: episode.ID, Limit: 1})
	if err != nil || history.SnapshotRevision != 1 || history.Coverage.State != "complete" || len(history.Items) != 1 || history.Items[0].Action != "acknowledge" {
		t.Fatalf("triage history: %+v %v", history, err)
	}
	workload, err := fx.Store.OperatorAlertWorkload(fx.Ctx, state.OperatorAlertWorkloadFilter{Now: now, CollectionEnabled: true})
	if err != nil || len(workload.Buckets) != 1 || workload.Buckets[0].Count != 1 || !workload.Buckets[0].Acknowledged || workload.Buckets[0].Status != "firing" {
		t.Fatalf("workload: %+v %v", workload, err)
	}
	timing, err := fx.Store.OperatorAlertResponseTimes(fx.Ctx, state.OperatorAlertResponseTimesFilter{Now: now, Since: now.Add(-time.Hour), CollectionEnabled: true})
	if err != nil || len(timing.Episodes) != 1 || timing.Episodes[0].FirstAcknowledgedAt == nil || timing.Episodes[0].ReceiptCoverage != "complete" {
		t.Fatalf("response timing: %+v %v", timing, err)
	}
	recurrence, err := fx.Store.OperatorAlertRecurrence(fx.Ctx, state.OperatorAlertRecurrenceFilter{Now: now, Since: now.Add(-time.Hour), CollectionEnabled: true})
	if err != nil || len(recurrence.Episodes) != 1 || recurrence.Episodes[0].ID != episode.ID || recurrence.Episodes[0].RuleKey != rule || recurrence.Episodes[0].SeriesID != series || recurrence.Episodes[0].Status != "firing" {
		t.Fatalf("recurrence: %+v %v", recurrence, err)
	}
	historyCoverage, err := fx.Store.OperatorAlertCollectionHistory(fx.Ctx, state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now})
	if err != nil || len(historyCoverage.Outcomes) != 1 || historyCoverage.Outcomes[0].Outcome != "success" {
		t.Fatalf("collection history: %+v %v", historyCoverage, err)
	}
	if err := fx.Store.RecordOperatorAlertFailure(fx.Ctx, now.Add(100*time.Millisecond), "timeout"); err != nil {
		t.Fatal(err)
	}
	rejection := api.ObsAlertCollectionRejection{Scope: "rejected_snapshot", Reason: "no_alert_rules_observed", Items: []api.ObsRuleHealthItem{}, Limit: api.ObsAlertCollectionRejectionItemsMax}
	if err := fx.Store.RecordOperatorAlertRejection(fx.Ctx, now.Add(150*time.Millisecond), rejection); err != nil {
		t.Fatal(err)
	}
	details, err := fx.Store.OperatorAlertCollectionHistory(fx.Ctx, state.OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now.Add(150 * time.Millisecond), IncludeDetails: true})
	if err != nil || len(details.Failures) != 2 || details.Failures[0].DiagnosticsState != "recorded" || details.Failures[0].Diagnostics == nil || details.Failures[0].Diagnostics.Reason != "no_alert_rules_observed" || details.Failures[1].DiagnosticsState != "unknown" {
		t.Fatalf("durable rejection: %+v %v", details, err)
	}
	retained, c, err := fx.Store.OperatorAlertEpisode(fx.Ctx, episode.ID)
	if err != nil || retained.Status(c, now) != "unknown" || !retained.Triage.Acknowledged {
		t.Fatalf("failed source: %+v %v", retained, err)
	}
	snapshot.ObservedAt = now.Add(200 * time.Millisecond)
	snapshot.Rules[0].EvaluatedAt = snapshot.ObservedAt
	if err := fx.Store.RecordOperatorAlertSnapshot(fx.Ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	page, err := fx.Store.OperatorAlertEpisodes(fx.Ctx, state.OperatorAlertFilter{Now: snapshot.ObservedAt, Limit: 1, Acknowledgment: "acknowledged"})
	if err != nil || len(page.Episodes) != 1 || page.Episodes[0].Triage.Revision != 1 || page.Episodes[0].Status(page.Collection, snapshot.ObservedAt) != "firing" {
		t.Fatalf("collection overwrote triage: %+v %v", page, err)
	}
	snapshot.ObservedAt = now.Add(300 * time.Millisecond)
	snapshot.Rules[0].EvaluatedAt = snapshot.ObservedAt
	snapshot.Rules[0].EpisodeIDs = []string{}
	snapshot.Episodes = []state.OperatorAlertEpisode{}
	if err := fx.Store.RecordOperatorAlertSnapshot(fx.Ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	maintenanceNow := now.Add(api.ObsAlertHistoryRetention + time.Second)
	maintenance, err := fx.Store.OperatorAlertRetentionStatus(fx.Ctx, maintenanceNow)
	if err != nil || maintenance.ExpiredEpisodes != 1 || maintenance.OldestExpiredAt.IsZero() {
		t.Fatalf("retention backlog: %+v %v", maintenance, err)
	}
	if err := fx.Store.RecordOperatorAlertRetentionFailure(fx.Ctx, now); err != nil {
		t.Fatal(err)
	}
	maintenance, err = fx.Store.OperatorAlertRetentionStatus(fx.Ctx, maintenanceNow)
	if err != nil || !maintenance.LastAttemptAt.Equal(now) || !maintenance.LastSuccessAt.IsZero() {
		t.Fatalf("retention failure: %+v %v", maintenance, err)
	}
	count, err := fx.Store.PruneOperatorAlertHistory(fx.Ctx, now.Add(time.Second), 1)
	if err != nil || count != 1 {
		t.Fatalf("prune: %d %v", count, err)
	}
	maintenance, err = fx.Store.OperatorAlertRetentionStatus(fx.Ctx, maintenanceNow)
	if err != nil || maintenance.ExpiredEpisodes != 0 || maintenance.LastSuccessAt.IsZero() || maintenance.LastPrunedEpisodes != 1 {
		t.Fatalf("retention success: %+v %v", maintenance, err)
	}
	if _, _, err := fx.Store.OperatorAlertEpisode(fx.Ctx, episode.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("pruned episode: %v", err)
	}
	logs, err := fx.Store.ListAuditLog(fx.Ctx, state.AuditLogFilter{KindPrefix: "operator.action.alert_triage", Limit: 10, IncludeAnonymous: true})
	if err != nil || len(logs) != 1 {
		t.Fatalf("retained audit: count=%d err=%v", len(logs), err)
	}
}

func testOperatorEvidenceProjections(t *testing.T, fx *Fixture) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	overview, err := fx.Store.OperatorOverview(fx.Ctx, now)
	if err != nil || overview.Totals.AppsTotal != 1 || overview.Totals.InstancesLive != 0 {
		t.Fatalf("overview: %+v %v", overview, err)
	}
	apps, err := fx.Store.OperatorTenantApps(fx.Ctx, fx.Account.ID)
	if err != nil || len(apps) != 1 || apps[0].ID != fx.App.ID || apps[0].Deployments != 1 {
		t.Fatalf("tenant apps: %+v %v", apps, err)
	}
	profiles, err := fx.Store.OperatorCapacityProfiles(fx.Ctx)
	var totalApps int64
	for _, profile := range profiles {
		totalApps += profile.Apps
	}
	if err != nil || totalApps != 1 {
		t.Fatalf("capacity: %+v %v", profiles, err)
	}
	if err := fx.Store.AppendEvent(fx.Ctx, "apid", "deployment.failed", &fx.Deployment.ID, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	canonical := uuid.MustParse(fx.Deployment.ID).String()
	history, err := fx.Store.OperatorIncidentEvidence(fx.Ctx, canonical, now.Add(-time.Minute), 1)
	if err != nil || len(history) != 1 || history[0].Kind != "deployment.failed" {
		t.Fatalf("evidence: %+v %v", history, err)
	}
	// The same exact resource must resolve through the context snapshot.
	snapshotNow := time.Now().UTC().Truncate(time.Microsecond)
	context, err := fx.Store.OperatorIncidentContext(fx.Ctx, state.OperatorIncidentContextFilter{Kind: "deployment", ID: canonical, Now: snapshotNow, Until: snapshotNow, Limit: 10, HistoryLimit: 10})
	if err != nil || context.IncidentID != "deployment/"+canonical || context.Scope.State != "known" || context.Scope.AppID != uuid.MustParse(fx.App.ID).String() || context.Counts == nil || context.Counts.Apps != 1 {
		t.Fatalf("context: %+v %v", context, err)
	}
}
