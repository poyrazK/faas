package pgintegration_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsPromotionPreviewHasNoAuthorityAndRejectsChangedObservations(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		from, desired := seed(t, basic)
		definition := desired.Definition
		workload := definition.Workloads["api"]
		workload.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("a", 64)}
		definition.Workloads["api"] = workload
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		from, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(from, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := basic.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: from.AccountID, ProjectID: from.ProjectID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		spec := from.Spec
		spec.ManifestPath = "environments/staging.yaml"
		to, err := basic.CreateEnvironmentGitSource(t.Context(), from.AccountID, from.ProjectID, "staging", spec)
		if err != nil {
			t.Fatal(err)
		}
		definition.Environment = "staging"
		target, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		request := api.PreviewEnvironmentGitPromotionRequest{FromEnvironment: "production", TargetCommitSHA: strings.Repeat("c", 40)}
		store := basic.(state.EnvironmentGitOpsPromotionStore)
		out, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), from, to, request, target)
		if err != nil || !out.ReadOnly || !out.ValuesRedacted || !out.RequiresTargetQualification || len(out.PlanHash) != 64 || !slices.Contains(out.BlockingReasons, "promotion_source_release_unproven") {
			t.Fatalf("preview: %+v %v", out, err)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), `"MODE":"production"`) {
			t.Fatal("preview leaked variables")
		}
		for _, source := range []state.EnvironmentGitSource{from, to} {
			current, err := basic.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
			if err != nil || current.Generation != source.Generation || current.IntentVersion != source.IntentVersion || current.ApprovedRevisionID != source.ApprovedRevisionID || current.AppliedRevisionID != source.AppliedRevisionID {
				t.Fatal("preview changed source authority", err)
			}
			runs, err := basic.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
			if err != nil || len(runs) != 0 {
				t.Fatal("preview created runs", err)
			}
		}
		if _, err := basic.AppBySlug(t.Context(), "shop-api"); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("preview reserved workload", err)
		}
		foreign := from
		foreign.AccountID = uuid.NewString()
		if _, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), foreign, to, request, target); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("mixed-account scopes accepted", err)
		}
		request.ExpectedPlanHash = strings.Repeat("f", 64)
		if _, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), from, to, request, target); !errors.Is(err, state.ErrConflict) {
			t.Fatal("stale observation hash accepted", err)
		}
		request.ExpectedPlanHash = out.PlanHash
		if _, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), from, to, request, target); err != nil {
			t.Fatal("unchanged observation rejected", err)
		}
		suspended := true
		if _, err := basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), to.AccountID, to.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: to.Generation, Suspended: &suspended}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), from, to, request, target); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed source accepted", err)
		}
	})
}

// Uses control-plane receipt fixtures; this is not native VM acceptance.
func TestPgGitOpsPromotionExportsExactServingImagesAndFencesReleaseEvidence(t *testing.T) {
	store := state.NewPgStore(pgtest.OpenMigrated(t))
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
	spec := from.Spec
	spec.ManifestPath = "environments/staging.yaml"
	to, err := store.CreateEnvironmentGitSource(t.Context(), from.AccountID, from.ProjectID, "staging", spec)
	if err != nil {
		t.Fatal(err)
	}
	definition.Environment = "staging"
	for name, workload := range definition.Workloads {
		workload.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/old@sha256:" + strings.Repeat("f", 64)}
		workload.Variables = map[string]string{"MODE": "TARGET_PRIVATE"}
		definition.Workloads[name] = workload
	}
	target, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	to, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(to, target, strings.Repeat("c", 40)))
	if err != nil {
		t.Fatal(err)
	}
	adoptWorkloadIntent(t, store, to)
	// Poll through the public state contract rather than setting verification timestamps.
	now := time.Now().UTC()
	for range 2 {
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
	from, err = store.EnvironmentGitSource(t.Context(), from.AccountID, from.ProjectID, "production")
	if err != nil {
		t.Fatal(err)
	}
	to, err = store.EnvironmentGitSource(t.Context(), to.AccountID, to.ProjectID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	request := api.PreviewEnvironmentGitPromotionRequest{FromEnvironment: "production", TargetCommitSHA: strings.Repeat("c", 40)}
	out, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), from, to, request, target)
	if err != nil || len(out.BlockingReasons) != 0 || out.Origin.ReleaseSetID != release.ID || len(out.Artifacts) != len(requests) || len(out.Changes) != len(requests) {
		t.Fatalf("serving image proposal: %+v %v", out, err)
	}
	exported, err := promotion.ExportManifest(out, target.Definition)
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
	if _, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), from, to, request, target); err != nil {
		t.Fatal("unchanged serving observation rejected", err)
	}
	// A changed live roster revokes serving proof even before the reconciler runs.
	registerServingGateway(t, store, "replacement-edge")
	if _, err := store.PreviewEnvironmentGitOpsPromotion(t.Context(), from, to, request, target); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed serving evidence accepted the reviewed hash", err)
	}
}
