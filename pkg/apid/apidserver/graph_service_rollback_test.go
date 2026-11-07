package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectGraphRollbackSafetyHTTP(t *testing.T) {
	for _, scenario := range []string{"compatible", "breaking", "migration", "fallback_compatible", "fallback_breaking", "fallback_migration"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
			fallback := strings.HasPrefix(scenario, "fallback_")
			kind := strings.TrimPrefix(scenario, "fallback_")
			srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
			manifest := app.Manifest
			manifest.RevisionPinTTLSeconds = 3600
			var err error
			app, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
			if err != nil {
				t.Fatal(err)
			}
			old := createRollbackContractDeployment(t, store, app, "production", false)
			var first state.ProjectReleaseSet
			if !fallback {
				first, err = store.PublishProjectReleaseSet(t.Context(), acct.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
				if err != nil {
					t.Fatal(err)
				}
			}
			next := createRollbackContractDeployment(t, store, app, "production", fallback)
			second, err := store.PublishProjectReleaseSet(t.Context(), acct.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}})
			if err != nil {
				t.Fatal(err)
			}
			writeRollbackContractSnapshot(t, store, old, rollbackContractOld)
			currentDoc := rollbackContractOld
			if kind == "breaking" {
				currentDoc = rollbackContractNew
			}
			writeRollbackContractSnapshot(t, store, next, currentDoc)
			if kind == "breaking" {
				check, e := openapidiff.CheckDeploymentRollback(t.Context(), store, app.ID, old.ID, next.ID, "production")
				if e != nil || !check.Diff.Blocking() {
					t.Fatalf("contract control: %+v %v", check.Diff, e)
				}
			}
			if kind == "migration" {
				d, e := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("3", 64), Scope: "production", Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate"}})
				if e != nil {
					t.Fatal(e)
				}
				if e = store.SetDeploymentRootfs(t.Context(), d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); e != nil {
					t.Fatal(e)
				}
				runRelease(t.Context(), t, store, acct.ID, app.ID, d.ID)
				if _, problem, e := release.RollbackGuard(t.Context(), store, app.ID, old, false); e != nil || problem == nil || problem.Code != api.CodeRollbackCrossesRelease {
					t.Fatalf("migration control: %+v %v", problem, e)
				}
			}
			promotion, _, err := store.CreateProjectEnvironmentPromotion(t.Context(), state.ProjectEnvironmentPromotion{AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: strings.Repeat("a", 64), IdempotencyKey: "guarded-graph", Status: "succeeded", ReleaseGraphMode: true, PreviousTargetReleaseSetID: first.ID, TargetReleaseSetID: second.ID}, []state.ProjectEnvironmentPromotionWorkload{{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: "source", TargetDeploymentID: next.ID, PreviousTargetDeploymentID: old.ID, PreviousTargetTrafficPercent: 100, Status: "promoted"}})
			if err != nil {
				t.Fatal(err)
			}
			req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+promotion.ID+"/rollback", "shop", nil)
			req.SetPathValue("environment", "production")
			req.SetPathValue("promotion", promotion.ID)
			req.Header.Set("Idempotency-Key", "guarded-rollback")
			srv.RollbackProjectEnvironmentPromotion(rec, req, acct)
			if kind == "compatible" && fallback {
				if rec.Code != http.StatusOK {
					t.Fatalf("fallback control: %d %s", rec.Code, rec.Body)
				}
				if _, err = store.ActiveProjectReleaseSet(t.Context(), acct.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("fallback graph remained active: %v", err)
				}
				return
			}
			_, route, err := store.ResolveProjectRelease(t.Context(), app.ID, "production", "")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "compatible" {
				if rec.Code != http.StatusOK || route != old.ID {
					t.Fatalf("normal graph rollback: %d %s route=%s", rec.Code, rec.Body, route)
				}
				return
			}
			want := http.StatusUnprocessableEntity
			wantCode := api.CodeAPIContractBreakingChange
			if kind == "migration" {
				want = http.StatusConflict
				wantCode = api.CodeRollbackCrossesRelease
			}
			var problem api.Problem
			if err = json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != want || problem.Code != wantCode || route == old.ID {
				t.Fatalf("graph refusal: HTTP=%d expected=%d selected_old=%v body=%s error=%v", rec.Code, want, route == old.ID, rec.Body, err)
			}
		})
	}
}

func TestCheckedServiceRollbackReceiptAfterNewRelease(t *testing.T) {
	for _, scenario := range []struct{ replace, retain bool }{{}, {replace: true}, {replace: true, retain: true}} {
		replace := scenario.replace
		name := "control"
		if replace {
			name = "new_release_before_receipt_sweep"
			if scenario.retain {
				name = "retained_revision_before_receipt_sweep"
			}
		}
		t.Run(name, func(t *testing.T) {
			e, app, target, current := apidserver.CheckedRollbackAPIFixture(t)
			store := *e.Store()
			acct := *e.Acct()
			manifest := app.Manifest
			manifest.ExecutionMode = api.ExecutionModeService
			manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
			var err error
			app, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
			if err != nil {
				t.Fatal(err)
			}
			op, err := store.CreateCheckedRollback(t.Context(), acct.ID, app.ID, target.ID, current.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDeploymentLive(t.Context(), target.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.CreateInstanceWithMode(t.Context(), app.ID, target.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString(), "service"); err != nil {
				t.Fatal(err)
			}
			if err = (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
				t.Fatal(err)
			}
			routed, err := store.GetCheckedRollback(t.Context(), acct.ID, app.ID, op.ID)
			if err != nil || routed.Status != "routing" {
				t.Fatalf("routing control: %+v %v", routed, err)
			}
			if _, err = store.FinalizeServiceRollout(t.Context(), target.ID); err != nil {
				t.Fatal(err)
			}
			var nextID string
			if scenario.retain {
				manifest.RevisionPinTTLSeconds = 3600
				if _, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
					t.Fatal(err)
				}
			}
			if replace {
				now := time.Now().UTC()
				next, e := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("4", 64), Status: state.DeployPending, TrafficPercent: 0, RolloutState: "rolling_out", RolloutStartedAt: &now})
				nextID = next.ID
				if e != nil {
					t.Fatal(e)
				}
				if e = store.SetDeploymentRootfs(t.Context(), next.ID, "/test/"+next.ID, "test/"+next.ID, 4096); e != nil {
					t.Fatal(e)
				}
				if e = store.MarkDeploymentLive(t.Context(), next.ID); e != nil {
					t.Fatal(e)
				}
				if _, e = store.CreateInstanceWithMode(t.Context(), app.ID, next.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString(), "service"); e != nil {
					t.Fatal(e)
				}
				if _, e = store.FinalizeServiceRollout(t.Context(), next.ID); e != nil {
					t.Fatal(e)
				}
			}
			if err = (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
				t.Fatal(err)
			}
			finished, err := store.GetCheckedRollback(t.Context(), acct.ID, app.ID, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.retain {
				if _, err = store.CreateCheckedRollback(t.Context(), acct.ID, app.ID, target.ID, nextID, "next recovery"); err != nil {
					t.Fatalf("old receipt blocked new recovery: %v", err)
				}
			}
			if finished.Status != "complete" {
				d, _ := store.DeploymentByID(t.Context(), target.ID)
				t.Fatalf("successful handoff misreported after replacement: receipt=%s code=%s target_status=%s weight=%d handoff=%s", finished.Status, finished.Code, d.Status, d.TrafficPercent, d.ServiceRolloutHandoff.Phase)
			}
		})
	}
}

func TestServiceAbortReportsLateMigrationBlocker(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "abort-release-blocker", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	predecessor := createRollbackContractDeployment(t, store, app, "default", false)
	now := time.Now().UTC()
	candidate, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("4", 64), Status: state.DeployPending, TrafficPercent: 0, RolloutState: "rolling_out", RolloutStartedAt: &now})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDeploymentRootfs(t.Context(), candidate.ID, "/test/"+candidate.ID, "test/"+candidate.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	for _, d := range []state.Deployment{predecessor, candidate} {
		if _, err = store.CreateInstanceWithMode(t.Context(), app.ID, d.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.BeginServiceRolloutCutover(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	queued, _, err := store.RequestServiceRolloutAbort(t.Context(), app.ID, candidate.ID, predecessor.ID, "restore predecessor")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("5", 64), Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDeploymentRootfs(t.Context(), migration.ID, "/test/"+migration.ID, "test/"+migration.ID, 4096); err != nil {
		t.Fatal(err)
	}
	runRelease(t.Context(), t, store, acct.ID, app.ID, migration.ID)
	cursor := 0
	if err = (*e.S()).ServiceRolloutBindingSweep(t.Context(), &cursor); err != nil {
		t.Fatal(err)
	}
	blocked, err := store.DeploymentByID(t.Context(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	gate := blocked.ServiceRolloutHandoff.BindingsCheck
	if gate == nil || gate.RequestID != queued.ServiceRolloutHandoff.BindingsCheck.RequestID || gate.Code != api.CodeRollbackCrossesRelease || gate.Status != "blocked" || blocked.TrafficPercent != 100 {
		t.Fatalf("late migration refusal was not durable: %+v target=%+v", gate, blocked)
	}
}
