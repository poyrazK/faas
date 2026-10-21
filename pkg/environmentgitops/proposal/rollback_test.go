package proposal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func rollbackFixture(t *testing.T, provider string) (RollbackRequest, []byte, *fakeProvider) {
	t.Helper()
	p, raw := fixture(t, provider)
	origin := p.Plan.Origin
	origin.SourceID, origin.GraphID = p.Plan.Target.SourceID, "historical-served-graph"
	served := time.Unix(1700000000, 0).UTC()
	plan := api.PreviewEnvironmentGitRollbackResponse{Project: p.Plan.Project, Environment: p.Plan.ToEnvironment,
		ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true, CurrentReleaseSetID: "3ba0aa40-2362-4c0e-aeb1-6381666bb514",
		Origin: origin, OriginServedAt: &served, Target: p.Plan.Target, Changes: p.Plan.Changes, Artifacts: p.Plan.Artifacts, BlockingReasons: []string{}}
	var err error
	plan, err = rollback.Finish(plan, p.Plan.TargetPlan)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanHash == p.Plan.PlanHash {
		t.Fatal("rollback hash reused promotion domain")
	}
	r := RollbackRequest{Plan: plan, Source: p.Source, ReleaseSetID: origin.ReleaseSetID, ExpectedPlanHash: plan.PlanHash}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	return r, raw, fakeFor(p, raw)
}

func rehashRollback(t *testing.T, r *RollbackRequest) {
	t.Helper()
	var err error
	r.Plan, err = rollback.Finish(r.Plan, r.Plan.TargetPlan)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedPlanHash = r.Plan.PlanHash
}

func TestRollbackProposalPreviewAndCreationPreserveTargetAndReviewBoundary(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			r, _, f := rollbackFixture(t, provider)
			out, err := RunRollback(t.Context(), r, f, false, nil)
			if err != nil || out.Status != "preview" || f.writes != 0 || out.ReleaseSetID != r.ReleaseSetID || out.PlanHash != r.Plan.PlanHash || out.Branch != "gregale-rollback-"+r.Plan.PlanHash || !out.RequiresTargetQualification {
				t.Fatal(out, err, f.writes)
			}
			checks := 0
			out, err = RunRollback(t.Context(), r, f, true, func(context.Context) error { checks++; return ValidateRollbackObservation(r, r) })
			if err != nil || out.Status != "created" || checks != 2 || f.writes != 2 {
				t.Fatal(out, err, checks, f.writes)
			}
			definition, err := gregalemanifest.ParseEnvironment(f.file.Content)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := environmentsync.Compile(definition)
			if err != nil || compiled.Digest != r.Plan.Target.ProposedDigest || definition.Workloads["api"].Variables["MODE"] != "production-private" || definition.Workloads["api"].SecretRefs["TOKEN"] != "secret:PRODUCTION_TOKEN" {
				t.Fatal("target configuration changed", err)
			}
			for _, comment := range []string{"# production configuration stays here", "# keep comment"} {
				if !bytes.Contains(f.file.Content, []byte(comment)) {
					t.Fatal("lost YAML comment", comment)
				}
			}
			body := f.reviews[0].Body
			for _, evidence := range []string{"gregale-rollback:" + r.Plan.PlanHash, r.ReleaseSetID, r.Plan.Origin.CommitSHA, r.Plan.Origin.GraphID, r.Plan.OriginServedAt.Format(time.RFC3339Nano), r.Plan.CurrentReleaseSetID, "fresh target qualification", "does not restore data or undo database migrations", "Completed Jobs are never replayed"} {
				if !strings.Contains(body, evidence) {
					t.Fatal("missing recovery evidence/boundary", evidence)
				}
			}
			receipt, _ := json.Marshal(out)
			if strings.Contains(body, "gregale-promotion:") || strings.Contains(body, "PRODUCTION_TOKEN") || strings.Contains(body, "production-private") || bytes.Contains(receipt, []byte("PRODUCTION_TOKEN")) {
				t.Fatal("recovery boundary or redaction lost")
			}
		})
	}
}

func TestRollbackProposalRecoversUnknownProviderWritesWithoutDuplicates(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, lost := range []string{"commit", "review"} {
			t.Run(provider+"/"+lost, func(t *testing.T) {
				r, _, f := rollbackFixture(t, provider)
				f.lostCommit, f.lostOpen = lost == "commit", lost == "review"
				check := func(context.Context) error { return ValidateRollbackObservation(r, r) }
				if _, err := RunRollback(t.Context(), r, f, true, check); err == nil {
					t.Fatal("uncertain write silently retried")
				}
				out, err := RunRollback(t.Context(), r, f, true, check)
				if err != nil || out.URL == "" || out.ReleaseSetID != r.ReleaseSetID || f.commits != 1 || f.opens != 1 {
					t.Fatal(out, err, f.commits, f.opens)
				}
				out, err = RunRollback(t.Context(), r, f, true, check)
				if err != nil || out.Status != "existing" || f.writes != 2 {
					t.Fatal(out, err, f.writes)
				}
				f.reviews[0].State = "closed"
				if _, err := RunRollback(t.Context(), r, f, true, check); err == nil || f.writes != 2 {
					t.Fatal("closed recovery proposal reused", err)
				}
			})
		}
	}
}

func TestRollbackProposalRejectsInvalidSelectionProofAndCurrentBindingBeforeWrites(t *testing.T) {
	for _, mode := range []string{"missing-selection", "other-selection", "noncanonical-selection", "hash", "proof-time", "proof-graph", "origin-source", "origin-commit", "blocked-revoked-artifact", "no-changes", "generation", "intent", "repository", "provider", "environment", "manifest", "tag", "source-outage", "detached", "suspended"} {
		t.Run(mode, func(t *testing.T) {
			r, _, f := rollbackFixture(t, "github")
			switch mode {
			case "missing-selection":
				r.ReleaseSetID = ""
			case "other-selection":
				r.ReleaseSetID = r.Plan.CurrentReleaseSetID
			case "noncanonical-selection":
				r.ReleaseSetID = strings.ToUpper(r.ReleaseSetID)
			case "hash":
				r.ExpectedPlanHash = strings.Repeat("f", 64)
			case "proof-time":
				r.Plan.OriginServedAt = nil
			case "proof-graph":
				r.Plan.Origin.GraphID = ""
			case "origin-source":
				r.Plan.Origin.SourceID = "other-source"
			case "origin-commit":
				r.Plan.Origin.CommitSHA = "main"
			case "blocked-revoked-artifact":
				r.Plan.BlockingReasons = []string{"workload/api: rollback_artifact_provenance_mismatch"}
			case "no-changes":
				r.Plan.Changes = nil
			case "generation":
				r.Source.Generation++
			case "intent":
				r.Source.IntentVersion++
			case "repository":
				r.Source.Spec.RepositoryID++
			case "provider":
				r.Plan.Origin.Provider = "gitlab"
			case "environment":
				r.Plan.Environment = "staging"
			case "manifest":
				r.Source.Spec.ManifestPath = "other.yaml"
			case "tag":
				r.Source.Spec.Ref = "refs/tags/release"
			case "source-outage":
				r.Source.SourceErrorCode = "PRIVATE_UPSTREAM_ERROR"
			case "detached":
				r.Source.Detached = true
			case "suspended":
				r.Source.Suspended = true
			}
			if mode != "hash" {
				rehashRollback(t, &r)
			}
			_, err := RunRollback(t.Context(), r, f, true, func(context.Context) error { return nil })
			if err == nil || f.writes != 0 || strings.Contains(err.Error(), "PRIVATE_UPSTREAM_ERROR") {
				t.Fatal(err, f.writes)
			}
		})
	}
}

func TestRollbackObservationBindsSelectedAndCurrentRelease(t *testing.T) {
	r, _, _ := rollbackFixture(t, "github")
	fresh := r
	fresh.Plan.ObservedAt = time.Now()
	if err := ValidateRollbackObservation(r, fresh); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"selected-release", "current-release", "binding", "served-at", "artifact-revoked"} {
		t.Run(mode, func(t *testing.T) {
			fresh := r
			switch mode {
			case "selected-release":
				fresh.ReleaseSetID = r.Plan.CurrentReleaseSetID
				fresh.Plan.Origin.ReleaseSetID = fresh.ReleaseSetID
			case "current-release":
				fresh.Plan.CurrentReleaseSetID = "4ba0aa40-2362-4c0e-aeb1-6381666bb514"
			case "binding":
				fresh.Source.Spec.Ref = "refs/heads/other"
			case "served-at":
				served := r.Plan.OriginServedAt.Add(time.Second)
				fresh.Plan.OriginServedAt = &served
			case "artifact-revoked":
				fresh.Plan.BlockingReasons = []string{"rollback_artifact_provenance_mismatch"}
			}
			rehashRollback(t, &fresh)
			if err := ValidateRollbackObservation(r, fresh); err == nil {
				t.Fatal("changed recovery observation accepted", mode)
			}
		})
	}
}

func TestRollbackProposalStopsOnStaleObservationAndMovingHeads(t *testing.T) {
	for _, mode := range []string{"missing-recheck", "before-commit", "before-review", "base-before-commit", "base-before-review", "head-before-review", "preview-base"} {
		t.Run(mode, func(t *testing.T) {
			r, _, f := rollbackFixture(t, "gitlab")
			checks := 0
			var check func(context.Context) error
			if mode != "missing-recheck" {
				check = func(context.Context) error {
					checks++
					if mode == "before-commit" || mode == "before-review" && checks == 2 {
						return errors.New("recovery plan changed")
					}
					if mode == "base-before-commit" || mode == "base-before-review" && checks == 2 {
						f.base = strings.Repeat("f", 40)
					}
					if mode == "head-before-review" && checks == 2 {
						f.head = strings.Repeat("f", 40)
					}
					return nil
				}
			}
			if mode == "preview-base" {
				f.onFile = func() { f.base = strings.Repeat("f", 40) }
			}
			if _, err := RunRollback(t.Context(), r, f, mode != "preview-base", check); err == nil || f.opens != 0 {
				t.Fatal(err, f.opens)
			}
			if (mode == "missing-recheck" || mode == "before-commit" || mode == "base-before-commit" || mode == "preview-base") && f.commits != 0 {
				t.Fatal("committed after stale proof")
			}
		})
	}
}

func TestRollbackProposalScheduledSignedBuildPreservesFutureExecutionContract(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile"} {
		t.Run(kind, func(t *testing.T) {
			r, raw, f := rollbackFixture(t, "github")
			definition, err := gregalemanifest.ParseEnvironment(raw)
			if err != nil {
				t.Fatal(err)
			}
			before := api.EnvironmentWorkloadSource{Kind: kind, Directory: "jobs/report"}
			job := definition.Workloads["api"]
			job.Source = &before
			job.Runtime = json.RawMessage(`{"execution_mode":"job","entrypoint":["/app/report","production"]}`)
			job.Schedule = &api.EnvironmentJobSchedule{Cron: "0 3 * * *", Timezone: "Europe/Istanbul"}
			job.JobSmoke = &api.EnvironmentJobSmoke{Command: []string{"/app/check", "production"}, TimeoutSeconds: 20}
			definition.Workloads["api"] = job
			base, err := environmentsync.Compile(definition)
			if err != nil {
				t.Fatal(err)
			}
			f.raw, err = json.Marshal(base.Definition)
			if err != nil {
				t.Fatal(err)
			}
			before = *base.Definition.Workloads["api"].Source
			after := before
			after.BuildArtifactID, after.BuildArtifactHash = "018f22b4-1d51-7d55-a5d8-fdc06ec7c663", strings.Repeat("f", 64)
			job.Source = &after
			definition.Workloads["api"] = job
			proposed, err := environmentsync.Compile(definition)
			if err != nil {
				t.Fatal(err)
			}
			r.Plan.Target.DefinitionDigest, r.Plan.Target.ProposedDigest = base.Digest, proposed.Digest
			r.Plan.Changes = []api.EnvironmentGitPromotionChange{{Resource: "workload/api", Before: before, After: after}}
			r.Plan.Artifacts = []api.EnvironmentGitPromotionArtifact{{Resource: "workload/api", DeploymentID: "historical-job", Kind: "github", BuildID: after.BuildArtifactID, BuildArtifactID: after.BuildArtifactID, BuildArtifactHash: after.BuildArtifactHash, ArtifactDigest: "sha256:" + strings.Repeat("e", 64), BuildCommitSHA: strings.Repeat("b", 40)}}
			r.Plan.TargetPlan.DesiredDigest = proposed.Digest
			rehashRollback(t, &r)
			if _, err := RunRollback(t.Context(), r, f, true, func(context.Context) error { return ValidateRollbackObservation(r, r) }); err != nil {
				t.Fatal(err)
			}
			got, err := gregalemanifest.ParseEnvironment(f.file.Content)
			if err != nil {
				t.Fatal(err)
			}
			result := got.Workloads["api"]
			if result.Source.BuildArtifactID != after.BuildArtifactID {
				t.Fatal("retained code identity lost")
			}
			result.Source = base.Definition.Workloads["api"].Source
			if !reflect.DeepEqual(result, base.Definition.Workloads["api"]) {
				t.Fatal("rollback replaced current Job schedule, argv, smoke or configuration")
			}
			for _, identity := range []string{after.BuildArtifactID, after.BuildArtifactHash, r.Plan.Artifacts[0].ArtifactDigest, r.Plan.Artifacts[0].BuildCommitSHA, "successful post-activation occurrence"} {
				if !strings.Contains(f.reviews[0].Body, identity) {
					t.Fatal("Job recovery provenance or boundary missing", identity)
				}
			}
		})
	}
}
