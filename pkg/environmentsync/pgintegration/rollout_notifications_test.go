// adr: 20261017190813 — current graph outcomes and atomic delivery across both stores.
package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func rolloutNotificationReceiver(t *testing.T, store gitOpsTestStore, source state.EnvironmentGitSource) state.AppWebhook {
	t.Helper()
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: source.AccountID, Scope: state.AppWebhookScopeAccount,
		TargetURL: "https://example.test/gitops-rollout/" + uuid.NewString(), SecretSealed: []byte("sealed"), EventFilter: []string{"environment.gitops.changed"}, Enabled: true}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func rolloutNotifications(t *testing.T, store gitOpsTestStore, source state.EnvironmentGitSource, hook state.AppWebhook) []api.EnvironmentGitOpsChangedWebhookPayload {
	t.Helper()
	rows, _, err := store.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, hook.ID, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []api.EnvironmentGitOpsChangedWebhookPayload
	for _, row := range rows {
		var payload api.EnvironmentGitOpsChangedWebhookPayload
		if json.Unmarshal(row.Payload, &payload) != nil || row.AppID != "" || payload.TransitionID == "" || strings.Contains(string(row.Payload), "PRIVATE") {
			t.Fatalf("unsafe notification: %+v", row)
		}
		if payload.Axis == "rollout" {
			outcomes = append(outcomes, payload)
		}
	}
	return outcomes
}

func rolloutDesired(t *testing.T, lease state.EnvironmentGitOpsLease) environmentsync.DesiredState {
	t.Helper()
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	return desired
}

func failedRolloutNotificationFixture(t *testing.T, basic gitOpsTestStore) (state.EnvironmentGitOpsLease, environmentsync.DesiredState, state.AppWebhook) {
	t.Helper()
	_, lease, _, _, plan := appliedSourceCandidateFixture(t, basic, "source")
	hook := rolloutNotificationReceiver(t, basic, lease.Source)
	preparer := basic.(state.EnvironmentGitOpsPreparationStore)
	requests, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
	if err != nil || len(requests) != 1 {
		t.Fatalf("source request: %+v %v", requests, err)
	}
	candidates, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: sourceArtifact(t, requests[0])})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidate: %+v %v", candidates, err)
	}
	claim, err := basic.ClaimQueuedBuild(t.Context(), candidates[0].BuildID)
	if err != nil {
		t.Fatal(err)
	}
	if err := basic.FailBuild(t.Context(), claim, state.FailureUserError, "PRIVATE build output"); err != nil {
		t.Fatal(err)
	}
	return lease, rolloutDesired(t, lease), hook
}

func TestGitOpsRolloutNotificationsCurrentBuildFailureAndApprovalSupersession(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, desired, hook := failedRolloutNotificationFixture(t, basic)
		secondReceiver := rolloutNotificationReceiver(t, basic, lease.Source)
		store := basic.(intentTestStore)
		now := time.Now().UTC()
		currentPlan := claimedIntentPlan(t, store, lease, desired)
		raw, _ := json.Marshal(currentPlan)
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "partial", raw, []byte("[]"), "", now, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		outcomes := rolloutNotifications(t, basic, lease.Source, hook)
		if len(outcomes) != 1 || outcomes[0].Status != "failed" || outcomes[0].CommitSHA != lease.Revision.CommitSHA || outcomes[0].ApprovedRevision != lease.Revision.ID ||
			outcomes[0].GraphID == "" || len(outcomes[0].Diagnostics) == 0 {
			t.Fatalf("current failure not delivered: %+v", outcomes)
		}
		second := rolloutNotifications(t, basic, lease.Source, secondReceiver)
		if len(second) != 1 || second[0].TransitionID != outcomes[0].TransitionID {
			t.Fatal("receiver fan-out changed the outcome identity", outcomes, second)
		}
		finishNotificationReport(t, store, desired, now.Add(2*time.Second), func() {})
		if got := rolloutNotifications(t, basic, lease.Source, hook); len(got) != 1 {
			t.Fatal("retry duplicated failure", got)
		}
		lateReceiver := rolloutNotificationReceiver(t, basic, lease.Source)
		finishNotificationReport(t, store, desired, now.Add(4*time.Second), func() {})
		if got := rolloutNotifications(t, basic, lease.Source, lateReceiver); len(got) != 0 {
			t.Fatal("new receiver replayed a completed outcome", got)
		}
		source, err := basic.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40)))
		if err != nil {
			t.Fatal(err)
		}
		if got := rolloutNotifications(t, basic, source, hook); len(got) != 1 {
			t.Fatal("completed failure was superseded again", got)
		}
		pending := source
		source, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("d", 40)))
		if err != nil {
			t.Fatal(err)
		}
		outcomes = rolloutNotifications(t, basic, source, hook)
		found := false
		for _, outcome := range outcomes {
			found = found || outcome.Status == "superseded" && outcome.Generation == pending.Generation && outcome.ApprovedRevision == pending.ApprovedRevisionID && outcome.CommitSHA == strings.Repeat("c", 40)
		}
		if len(outcomes) != 2 || !found {
			t.Fatal("pending approval supersession lost exact identity", outcomes)
		}
		suspended := true
		if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Suspended: &suspended}); err != nil {
			t.Fatal(err)
		}
		if got := rolloutNotifications(t, basic, source, hook); len(got) != 3 {
			t.Fatal("suspension lost pending outcome", got)
		}
	})
}

func TestGitOpsRolloutNotificationsServingRequiresCurrentReportAndAllReceipts(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, requests := preparedRequestModeQualificationFixture(t, basic)
		desired := rolloutDesired(t, lease)
		plan := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
		hook := rolloutNotificationReceiver(t, basic, lease.Source)
		poller := basic.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		pollLease, err := poller.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := poller.FinishEnvironmentGitSourcePoll(t.Context(), pollLease, state.EnvironmentGitSourcePollResult{CommitSHA: strings.Repeat("e", 40), Digest: desired.Digest}, now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		activation := basic.(state.EnvironmentGitOpsGraphActivationStore)
		if _, activated, err := activation.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || activated {
			t.Fatalf("unqualified graph activated: %t %v", activated, err)
		}
		for _, request := range requests {
			completeRequestQualificationReceipts(t, basic.(requestQualificationReceiptStore), request)
		}
		if _, activated, err := activation.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !activated {
			t.Fatalf("graph activation: %t %v", activated, err)
		}
		if got := rolloutNotifications(t, basic, lease.Source, hook); len(got) != 0 {
			t.Fatal("qualification or activation emitted success", got)
		}
		registerServingGateway(t, basic, "edge-rollout")
		servingStore := basic.(state.EnvironmentGitOpsWorkloadServingStore)
		receipt, complete, err := servingStore.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"edge-rollout"})
		if err != nil || complete || len(receipt.Routes) != len(requests) {
			t.Fatalf("serving preparation: %+v %t %v", receipt, complete, err)
		}
		for _, route := range receipt.Routes {
			if _, _, err := servingStore.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID, route.Generation, "edge-rollout"); err != nil {
				t.Fatal(err)
			}
		}
		current, err := basic.(state.EnvironmentGitOpsCurrentWorkloadEvidenceStore).CurrentEnvironmentGitOpsWorkloadEvidence(t.Context(), lease.Source.AccountID, lease.Source.ID)
		if err != nil || current == nil || !current.Serving || current.IntentVersion != plan.ObservedVersion {
			t.Fatalf("fixture needs a complete current serving graph: %+v %v", current, err)
		}
		// An earlier observation must not notify, even though the independent
		// current graph now has complete serving receipts.
		stalePlan := plan
		stalePlan.ObservedVersion--
		raw, _ := json.Marshal(stalePlan)
		now = time.Now().UTC()
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "partial", raw, []byte("[]"), "", now, now); err != nil {
			t.Fatal(err)
		}
		if got := rolloutNotifications(t, basic, lease.Source, hook); len(got) != 0 {
			t.Fatal("stale report emitted serving", got)
		}
		// An expired override can change a plan without a new intent version.
		// Even a correctly scoped report needs the independently current hash.
		mismatched, err := basic.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), now.Add(time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		wrongPlan := claimedIntentPlan(t, basic.(intentTestStore), mismatched, desired)
		wrongPlan.Hash = strings.Repeat("f", 64)
		raw, _ = json.Marshal(wrongPlan)
		if err := basic.FinishEnvironmentGitOps(t.Context(), mismatched, "partial", raw, []byte("[]"), "", now.Add(time.Second), now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if got := rolloutNotifications(t, basic, lease.Source, hook); len(got) != 0 {
			t.Fatal("mismatched plan hash emitted serving", got)
		}
		finishNotificationReport(t, basic.(intentTestStore), desired, now.Add(2*time.Second), func() {})
		outcomes := rolloutNotifications(t, basic, lease.Source, hook)
		if len(outcomes) != 1 || outcomes[0].Status != "serving" || outcomes[0].CommitSHA != lease.Revision.CommitSHA || outcomes[0].CommitSHA == strings.Repeat("e", 40) ||
			outcomes[0].GraphID != receipt.GraphID || outcomes[0].ObservedVersion != receipt.IntentVersion {
			t.Fatalf("exact serving outcome missing: %+v", outcomes)
		}
		finishNotificationReport(t, basic.(intentTestStore), desired, now.Add(4*time.Second), func() {})
		if got := rolloutNotifications(t, basic, lease.Source, hook); len(got) != 1 {
			t.Fatal("serving retry duplicated outcome", got)
		}
	})
}

func TestPgGitOpsRolloutNotificationFailureRollsBackAndSurvivesRestart(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, desired, hook := failedRolloutNotificationFixture(t, store)
	plan := claimedIntentPlan(t, store, lease, desired)
	raw, _ := json.Marshal(plan)
	if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_deliveries ADD CONSTRAINT reject_rollout_test CHECK (payload->>'axis' <> 'rollout')"); err != nil {
		t.Fatal(err)
	}
	readBaseline := func() string {
		t.Helper()
		var baseline string
		if err := pool.QueryRow(t.Context(), "SELECT notification_state::text FROM environment_git_sources WHERE id=$1", lease.Source.ID).Scan(&baseline); err != nil {
			t.Fatal(err)
		}
		return baseline
	}
	before := readBaseline()
	now := time.Now().UTC()
	if err := store.FinishEnvironmentGitOps(t.Context(), lease, "partial", raw, []byte("[]"), "", now, now); err == nil {
		t.Fatal("failed enqueue committed report")
	}
	if after := readBaseline(); after != before {
		t.Fatal("failed enqueue consumed outcome baseline", before, after)
	}
	runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), lease.Source.AccountID, lease.Source.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].CompletedAt != nil {
		t.Fatal("failed enqueue completed report", runs, err)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_deliveries DROP CONSTRAINT reject_rollout_test"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitOps(t.Context(), lease, "partial", raw, []byte("[]"), "", now, now); err != nil {
		t.Fatal(err)
	}
	first := rolloutNotifications(t, store, lease.Source, hook)
	restarted := state.NewPgStore(pool)
	finishNotificationReport(t, restarted, desired, now.Add(time.Second), func() {})
	after := rolloutNotifications(t, restarted, lease.Source, hook)
	if len(first) != 1 || len(after) != 1 || first[0].TransitionID != after[0].TransitionID {
		t.Fatal("restart duplicated or lost outcome", first, after)
	}
	if err := restarted.FinishEnvironmentGitOps(t.Context(), lease, "partial", raw, []byte("[]"), "", now, now); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old report lease reused", err)
	}
}

func TestPgGitOpsRolloutSupersessionRollsBackApprovalAndControl(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	source, desired := seedMode(t, store, "enforce")
	var err error
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	hook := rolloutNotificationReceiver(t, store, source)
	reject := func() {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_deliveries ADD CONSTRAINT reject_rollout_test CHECK (payload->>'axis' <> 'rollout') NOT VALID"); err != nil {
			t.Fatal(err)
		}
	}
	allow := func() {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_deliveries DROP CONSTRAINT reject_rollout_test"); err != nil {
			t.Fatal(err)
		}
	}
	reject()
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40))); err == nil {
		t.Fatal("failed supersession enqueue committed approval")
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if err != nil || current.Generation != source.Generation || current.ApprovedRevisionID != source.ApprovedRevisionID {
		t.Fatal("failed supersession changed approval", current, err)
	}
	allow()
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
	if err != nil {
		t.Fatal(err)
	}
	if got := rolloutNotifications(t, store, source, hook); len(got) != 1 || got[0].CommitSHA != strings.Repeat("a", 40) || got[0].Status != "superseded" {
		t.Fatal("approval retry lost previous pending outcome", got)
	}
	reject()
	suspended := true
	update := state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Suspended: &suspended}
	if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, update); err == nil {
		t.Fatal("failed supersession enqueue committed suspension")
	}
	current, err = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if err != nil || current.Suspended || current.Generation != source.Generation {
		t.Fatal("failed supersession changed controls", current, err)
	}
	allow()
	if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, update); err != nil {
		t.Fatal(err)
	}
	if got := rolloutNotifications(t, store, source, hook); len(got) != 2 {
		t.Fatal("suspension retry lost pending outcome", got)
	}
}

func TestGitOpsRolloutNotificationsProtectedApprovalTracksExactPendingRevision(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seedModePolicy(t, store, "enforce", "protected_branch")
		hook := rolloutNotificationReceiver(t, store, source)
		poller := store.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		poll := func(sha string) state.EnvironmentGitSource {
			t.Helper()
			lease, err := poller.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			evidence := reviewedMergeFixture(source, sha)
			evidence.ReviewedDefinitionDigest = desired.Digest
			if err := poller.FinishEnvironmentGitSourcePoll(t.Context(), lease, state.EnvironmentGitSourcePollResult{
				CommitSHA: sha, Digest: desired.Digest, Desired: &desired, Approval: &evidence}, now, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			now = now.Add(2 * time.Second)
			current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
			if err != nil {
				t.Fatal(err)
			}
			return current
		}
		source = poll(strings.Repeat("a", 40))
		previous := source
		source = poll(strings.Repeat("a", 40))
		if got := rolloutNotifications(t, store, source, hook); len(got) != 0 || source.Generation != previous.Generation {
			t.Fatal("identical protected approval repeated pending outcome", got)
		}
		source = poll(strings.Repeat("c", 40))
		outcomes := rolloutNotifications(t, store, source, hook)
		if len(outcomes) != 1 || outcomes[0].Status != "superseded" || outcomes[0].CommitSHA != strings.Repeat("a", 40) ||
			outcomes[0].ApprovedRevision != previous.ApprovedRevisionID || outcomes[0].Generation != previous.Generation {
			t.Fatal("protected approval lost previous revision identity", outcomes)
		}
	})
}
