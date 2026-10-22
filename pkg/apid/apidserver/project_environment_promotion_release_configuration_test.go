package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type snapshotPromotionTestStore struct {
	MemStore  *state.MemStore
	interrupt bool
	corrupt   bool
}

func (s *snapshotPromotionTestStore) UpdateProjectEnvironmentPromotionWorkload(ctx context.Context, account, promotion, workload, status, deployment, message string) (state.ProjectEnvironmentPromotionWorkload, error) {
	if s.interrupt && status == "promoted" {
		s.interrupt = false
		return state.ProjectEnvironmentPromotionWorkload{}, errors.New("checkpoint unavailable")
	}
	return s.MemStore.UpdateProjectEnvironmentPromotionWorkload(ctx, account, promotion, workload, status, deployment, message)
}

func (s *snapshotPromotionTestStore) UpdateProjectEnvironmentPromotionVerification(ctx context.Context, account, promotion, status, message string, started, completed *time.Time) (state.ProjectEnvironmentPromotion, error) {
	p, err := s.MemStore.UpdateProjectEnvironmentPromotionVerification(ctx, account, promotion, status, message, started, completed)
	if err == nil && s.corrupt && status == "verifying" {
		_, workloads, readErr := s.MemStore.ProjectEnvironmentPromotionByID(ctx, account, p.ProjectSlug, p.ToEnvironment, promotion)
		if readErr != nil {
			return p, readErr
		}
		err = s.MemStore.UpdateDeploymentStatus(ctx, workloads[0].TargetDeploymentID, state.DeployFailed, "verification fixture failure")
	}
	return p, err
}

func TestSnapshotPromotionAPIApprovalResumeAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "resume", "verification_failure", "stale_preview", "binding_variables"} {
		t.Run(scenario, func(t *testing.T) {
			srv, store, account, project, app := newProjectLifecycleFixture(t)
			ctx := t.Context()
			mustSnapshotPromotionAPI(t, func() error {
				_, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
				return err
			}())
			app.Manifest.RevisionPinTTLSeconds = 3600
			app, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &app.Manifest})
			mustSnapshotPromotionAPI(t, err)
			create := func(scope, key string) state.Deployment {
				d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("a", 64)})
				mustSnapshotPromotionAPI(t, err)
				mustSnapshotPromotionAPI(t, store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+key, "apps/"+key, 42))
				mustSnapshotPromotionAPI(t, store.MarkDeploymentLive(ctx, d.ID))
				return d
			}
			mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "MODE", "old-production"))
			mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "staging", "MODE", "destination-variable"))
			old, previous := create("production", "old"), create("staging", "stage")
			baseline, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
			mustSnapshotPromotionAPI(t, err)
			target, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previous.ID}})
			mustSnapshotPromotionAPI(t, err)
			raw := json.RawMessage(`{"env":{"api":{"MODE":"tested-variable"}}}`)
			if scenario == "binding_variables" {
				raw, _ = json.Marshal(map[string]any{"env": map[string]any{"api": map[string]string{"MODE": "tested-variable", api.ServiceBindingEnvPrefix + "BACKEND" + api.ServiceBindingEnvSuffix: "https://stage-only.example"}}})
			}
			configuration, err := store.PreviewProjectReleaseConfiguration(ctx, account.ID, project.ID, baseline.ID, raw)
			mustSnapshotPromotionAPI(t, err)
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Configuration: configuration}
			attempt := strings.Repeat("c", 32)
			_, err = store.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("d", 64), api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", SelectionComplete: true, Status: "complete", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}})
			mustSnapshotPromotionAPI(t, err)
			mustSnapshotPromotionAPI(t, store.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, raw))
			sourceCtx := state.ContextWithProjectReleaseConfigurationAttempt(state.ContextWithDeploymentSubmission(ctx, state.ProjectWorkloadAttempt(attempt, app.ID)), attempt)
			source, err := store.CreateDeployment(sourceCtx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("b", 64), TrafficPercentExplicit: true})
			mustSnapshotPromotionAPI(t, err)
			mustSnapshotPromotionAPI(t, store.SetDeploymentRootfs(ctx, source.ID, "/rootfs/tested", "apps/tested", 84))
			mustSnapshotPromotionAPI(t, store.MarkDeploymentLiveDark(ctx, source.ID))
			_, err = store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
			mustSnapshotPromotionAPI(t, err)
			values, hash, err := api.NormalizeProjectEnvironmentConfig(raw)
			mustSnapshotPromotionAPI(t, err)
			_, err = store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", Values: values, ConfigHash: hash})
			mustSnapshotPromotionAPI(t, err)
			plan, problem := srv.buildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "production", "staging", true)
			if scenario == "binding_variables" {
				if problem != nil || plan.Preview.CanPromote {
					t.Fatal("generated source binding URL could cross environments")
				}
				return
			}
			if problem != nil || !plan.Preview.CanPromote || plan.ReleaseConfiguration == nil {
				t.Fatalf("snapshot preview blocked: %+v %v", plan.Preview, problem)
			}
			change := plan.Preview.Changes[0]
			if change.ReleaseConfigurationHash == "" || len(change.EnvironmentChanges) != 1 || change.EnvironmentChanges[0].Key != "MODE" || len(change.EnvironmentChanges[0].After) != 0 {
				t.Fatal("preview omitted variable identity or exposed values")
			}
			encoded, _ := json.Marshal(plan.Preview.Changes)
			if strings.Contains(string(encoded), "tested-variable") || strings.Contains(string(encoded), "destination-variable") {
				t.Fatal("workload preview exposed variable values")
			}
			if scenario == "stale_preview" {
				mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "staging", "MODE", "operator-change"))
			}
			wrapped := &snapshotPromotionTestStore{MemStore: store, interrupt: scenario == "resume", corrupt: scenario == "verification_failure"}
			srv.store = statetest.NewMem(wrapped)
			body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
			execute := func() int {
				req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/staging/promote", project.Slug, body)
				req.SetPathValue("environment", "staging")
				req.Header.Set("Idempotency-Key", "snapshot-api")
				srv.promoteProjectEnvironment(rec, req, account)
				if rec.Code != 200 {
					t.Logf("promotion: %d %s", rec.Code, rec.Body)
				}
				return rec.Code
			}
			status := execute()
			if scenario == "stale_preview" {
				if status != http.StatusConflict {
					t.Fatal("stale variable preview accepted")
				}
				return
			}
			if scenario == "resume" {
				if status != http.StatusServiceUnavailable {
					t.Fatalf("interruption status=%d", status)
				}
				status = execute()
			}
			p, workloads, err := store.ProjectEnvironmentPromotionByIdempotencyKey(ctx, account.ID, project.Slug, "snapshot-api")
			mustSnapshotPromotionAPI(t, err)
			if scenario == "verification_failure" {
				if status != http.StatusConflict || p.RollbackStatus != "rolled_back" {
					t.Fatalf("automatic rollback failed: %d %+v", status, p)
				}
			} else {
				if status != http.StatusOK || p.VerificationStatus != "verified" || len(workloads) != 1 {
					t.Fatalf("promotion failed: %d %+v", status, p)
				}
				snapshot, err := store.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, workloads[0].TargetDeploymentID)
				mustSnapshotPromotionAPI(t, err)
				if len(snapshot.Values) != 1 || snapshot.Values[0].Value != "tested-variable" {
					t.Fatal("promotion did not transfer pinned source variables")
				}
				saved, err := store.ProjectEnvironmentPromotionReleaseConfiguration(ctx, account.ID, p.ID)
				mustSnapshotPromotionAPI(t, err)
				if saved.Hash != plan.ReleaseConfiguration.Hash {
					t.Fatal("retry changed approved snapshot")
				}
				rollback, rec := projectRequest(http.MethodPost, "/rollback", project.Slug, nil)
				rollback.SetPathValue("environment", "staging")
				rollback.SetPathValue("promotion", p.ID)
				rollback.Header.Set("Idempotency-Key", "snapshot-api-rollback")
				srv.rollbackProjectEnvironmentPromotion(rec, rollback, account)
				if rec.Code != http.StatusOK {
					t.Fatalf("rollback: %d %s", rec.Code, rec.Body)
				}
			}
			active, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "staging")
			mustSnapshotPromotionAPI(t, err)
			if active.ID == target.ID || active.Members[0].DeploymentID != previous.ID {
				t.Fatal("rollback did not restore prior graph")
			}
			snapshot, err := store.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, previous.ID)
			mustSnapshotPromotionAPI(t, err)
			if snapshot.Values[0].Value != "destination-variable" {
				t.Fatal("rollback did not restore prior variables")
			}
		})
	}
}

func mustSnapshotPromotionAPI(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
