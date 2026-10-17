package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// Control-plane receipt fixtures do not establish native VM acceptance.
func TestGitOpsRollbackSelectsHistoricalServingImagesAndPreservesTarget(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(interface {
			intentTestStore
			requestQualificationReceiptStore
			state.EnvironmentGitOpsGraphActivationStore
			state.EnvironmentGitOpsWorkloadServingStore
			state.EnvironmentGitOpsRollbackStore
			state.EnvironmentGitOpsRecoveryStore
			state.EnvironmentGitSourcePollStore
		})
		lease, requests := preparedRequestModeQualificationFixture(t, store)
		var definition api.EnvironmentDefinition
		if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
			t.Fatal(err)
		}
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		for _, request := range requests {
			completeRequestQualificationReceipts(t, store, request)
		}
		release, activated, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
		if err != nil || !activated {
			t.Fatal("activate source graph", err)
		}
		registerServingGateway(t, store, "promotion-edge")
		receipt, _, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"promotion-edge"})
		if err != nil {
			t.Fatal(err)
		}
		for _, route := range receipt.Routes {
			if _, _, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID, route.Generation, "promotion-edge"); err != nil {
				t.Fatal(err)
			}
		}
		from, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		definition.Environment = "production"
		for name, workload := range definition.Workloads {
			workload.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/new@sha256:" + strings.Repeat("f", 64)}
			workload.Variables = map[string]string{"MODE": "TARGET_PRIVATE"}
			definition.Workloads[name] = workload
		}
		target, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		to, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(from, target, strings.Repeat("c", 40)))
		if err != nil {
			t.Fatal(err)
		}
		adoptWorkloadIntent(t, store, to)
		// Poll through the public state contract rather than setting verification timestamps.
		now := time.Now().UTC()
		for range 1 {
			poll, err := store.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			result := state.EnvironmentGitSourcePollResult{CommitSHA: lease.Revision.CommitSHA, Digest: desired.Digest}
			if poll.Source.ID == to.ID {
				result.CommitSHA, result.Digest = strings.Repeat("c", 40), target.Digest
			}
			if err := store.FinishEnvironmentGitSourcePoll(t.Context(), poll, result, now, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		to, err = store.EnvironmentGitSource(t.Context(), to.AccountID, to.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		request := api.PreviewEnvironmentGitRollbackRequest{ReleaseSetID: release.ID, TargetCommitSHA: strings.Repeat("c", 40)}
		out, err := store.PreviewEnvironmentGitOpsRollback(t.Context(), to, request, target)
		if err != nil || len(out.BlockingReasons) != 0 || out.Origin.ReleaseSetID != release.ID || len(out.Artifacts) != len(requests) || len(out.Changes) != len(requests) {
			t.Fatalf("serving image proposal: %+v %v", out, err)
		}
		catalog, err := store.ListEnvironmentGitReleases(t.Context(), to, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: request.TargetCommitSHA}, target)
		if err != nil || len(catalog.Releases) != 1 || rollback.ValidateCatalog(catalog) != nil {
			t.Fatalf("recovery catalog: %+v %v", catalog, err)
		}
		entry := catalog.Releases[0]
		if entry.ReleaseSetID != release.ID || entry.Eligibility != "eligible" || entry.PlanHash != out.PlanHash || entry.CommitSHA != out.Origin.CommitSHA || len(entry.Artifacts) != len(out.Artifacts) {
			t.Fatalf("catalog diverged from rollback planner: %+v %+v", entry, out)
		}
		last, err := store.ListEnvironmentGitReleases(t.Context(), to, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: request.TargetCommitSHA, Before: release.ID}, target)
		if err != nil || len(last.Releases) != 0 || last.NextBefore != "" {
			t.Fatal("catalog cursor", last, err)
		}
		if _, err := store.ListEnvironmentGitReleases(t.Context(), to, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: request.TargetCommitSHA, Before: uuid.NewString()}, target); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign cursor", err)
		}
		exported, err := rollback.ExportManifest(out, target.Definition)
		if err != nil {
			t.Fatal(err)
		}
		var proposed api.EnvironmentDefinition
		if err := json.Unmarshal(exported, &proposed); err != nil {
			t.Fatal(err)
		}
		for name, workload := range proposed.Workloads {
			if *workload.Source != *desired.Definition.Workloads[name].Source || workload.Variables["MODE"] != "TARGET_PRIVATE" {
				t.Fatal("promotion changed target settings or selected another artifact")
			}
		}
		request.ExpectedPlanHash = out.PlanHash
		if _, err := store.PreviewEnvironmentGitOpsRollback(t.Context(), to, request, target); err != nil {
			t.Fatal("unchanged serving observation rejected", err)
		}
		// Old serving evidence is historical: changing today's gateway roster must
		// not rewrite the selected release's completed receipt.
		registerServingGateway(t, store, "replacement-edge")
		if _, err := store.PreviewEnvironmentGitOpsRollback(t.Context(), to, request, target); err != nil {
			t.Fatal("historical proof depended on current roster", err)
		}
		// Changing current intent invalidates the reviewed hash; no live effects are requested.
		changed := definition
		w := changed.Workloads["api"]
		w.Variables = map[string]string{"MODE": "CHANGED"}
		changed.Workloads["api"] = w
		changedDesired, err := environmentsync.Compile(changed)
		if err != nil {
			t.Fatal(err)
		}
		newer, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(to, changedDesired, strings.Repeat("d", 40)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PreviewEnvironmentGitOpsRollback(t.Context(), to, request, target); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed source accepted", err)
		}
		request.ExpectedPlanHash = ""
		missing := request
		missing.ReleaseSetID = uuid.NewString()
		if _, err := store.PreviewEnvironmentGitOpsRollback(t.Context(), newer, missing, changedDesired); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("unknown release accepted", err)
		}
		if _, err := store.ListEnvironmentGitReleases(t.Context(), to, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: request.TargetCommitSHA}, target); !errors.Is(err, state.ErrConflict) {
			t.Fatal("catalog accepted stale source", err)
		}
		suspended := true
		paused, err := store.UpdateEnvironmentGitSource(t.Context(), newer.AccountID, newer.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: newer.Generation, Suspended: &suspended})
		if err != nil {
			t.Fatal(err)
		}
		blockedPlan, err := store.PreviewEnvironmentGitOpsRollback(t.Context(), paused, request, changedDesired)
		if err != nil || len(blockedPlan.BlockingReasons) == 0 {
			t.Fatal("suspended rollback was eligible", blockedPlan, err)
		}
		reader := basic.(state.ProjectReleaseSetReader)
		beforeRelease, err := reader.ProjectReleaseSetByID(t.Context(), paused.AccountID, paused.ProjectID, paused.EnvironmentSlug, release.ID)
		if err != nil {
			t.Fatal(err)
		}
		blockedCatalog, err := store.ListEnvironmentGitReleases(t.Context(), paused, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: request.TargetCommitSHA}, changedDesired)
		if err != nil || len(blockedCatalog.Releases) != 1 || blockedCatalog.Releases[0].Eligibility != "blocked" || blockedCatalog.Releases[0].PlanHash != blockedPlan.PlanHash {
			t.Fatal("blocked catalog diverged from planner", blockedCatalog, err)
		}
		beforeBytes, _ := json.Marshal(beforeRelease)
		afterRelease, err := reader.ProjectReleaseSetByID(t.Context(), paused.AccountID, paused.ProjectID, paused.EnvironmentSlug, release.ID)
		afterBytes, _ := json.Marshal(afterRelease)
		catalogBytes, _ := json.Marshal(blockedCatalog)
		if err != nil || string(beforeBytes) != string(afterBytes) || strings.Contains(string(catalogBytes), "TARGET_PRIVATE") || strings.Contains(string(catalogBytes), "CHANGED") {
			t.Fatal("catalog changed retention or exposed values", err)
		}
		foreign := paused
		foreign.AccountID = uuid.NewString()
		if _, err := store.PreviewEnvironmentGitOpsRollback(t.Context(), foreign, request, changedDesired); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-account history accepted", err)
		}
		if _, err := store.ListEnvironmentGitReleases(t.Context(), foreign, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: request.TargetCommitSHA}, changedDesired); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("catalog cross-account history accepted", err)
		}

	})
}
