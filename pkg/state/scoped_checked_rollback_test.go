package state_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func scopedRollbackStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "postgres" {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	}
	return state.NewMemStore()
}
func scopedRollbackFixture(t *testing.T, s state.Store, shared, scoped string, defaultReplicas ...bool) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	owner, err := s.(interface {
		CreateAccountWithPersonalOrg(context.Context, state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
	}).CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@audit9.test", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "project-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.(interface {
		CreateProjectEnvironment(context.Context, state.ProjectEnvironment) (state.ProjectEnvironment, error)
	}).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: owner.Account.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: p.ID, Slug: "rollback-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: shared, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.Manifest.ExecutionMode = scoped
	if len(defaultReplicas) > 0 && defaultReplicas[0] {
		settings.Manifest.ServiceReplicas = nil
	}
	_, err = s.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, app.AccountID, p.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	create := func() state.Deployment {
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		d, err = s.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	target, current := create(), create()
	return owner.Account, app, target, current
}
func TestScopedCheckedRollbackUsesPinnedExecutionMode(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, modes := range [][2]string{{api.ExecutionModeService, api.ExecutionModeService}, {api.ExecutionModeRequest, api.ExecutionModeService}, {api.ExecutionModeRequest, api.ExecutionModeRequest}, {api.ExecutionModeService, api.ExecutionModeRequest}, {api.ExecutionModeService, ""}} {
				t.Run(fmt.Sprint(modes), func(t *testing.T) {
					a, app, target, current := scopedRollbackFixture(t, s, modes[0], modes[1])
					ctx := t.Context()
					rstore := s.(state.CheckedRollbackStore)
					op, err := rstore.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "audit scoped rollback")
					if err != nil {
						t.Fatal(err)
					}
					if err = s.MarkDeploymentLive(ctx, target.ID); err != nil {
						t.Fatal(err)
					}
					op, err = rstore.GetCheckedRollback(ctx, a.ID, app.ID, op.ID)
					if err != nil {
						t.Fatal(err)
					}
					// A later shared-app edit cannot change this deployment's protocol.
					changed := app.Manifest
					if modes[0] == api.ExecutionModeService {
						changed.ExecutionMode = api.ExecutionModeRequest
					} else {
						changed.ExecutionMode = api.ExecutionModeService
					}
					if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &changed}); err != nil {
						t.Fatal(err)
					}
					committed, commitErr := rstore.CommitCheckedRollback(ctx, op)
					d, _ := s.DeploymentByID(ctx, target.ID)
					old, _ := s.DeploymentByID(ctx, current.ID)
					t.Logf("shared=%s scoped=%s operation.service=%v commit=%s/%v target=%s/%s/%d current=%s/%d", modes[0], modes[1], op.Service, committed.Status, commitErr, d.Status, d.RolloutState, d.TrafficPercent, old.Status, old.TrafficPercent)
					if op.Service != (modes[1] == api.ExecutionModeService) {
						t.Error("rollback protocol follows shared app rather than target's pinned execution mode")
					}
					if modes[1] == api.ExecutionModeService && !errors.Is(commitErr, state.ErrServiceRolloutNotReady) {
						t.Error("service rollback moved traffic without any ready replica")
					}
					if modes[1] == api.ExecutionModeRequest && commitErr != nil {
						t.Error("request rollback incorrectly requires service replica readiness")
					}
					if modes[1] == api.ExecutionModeService {
						node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
						if err != nil {
							t.Fatal(err)
						}
						for range 2 {
							if _, err := s.CreateInstanceWithMode(ctx, app.ID, target.ID, "running", 128, node.ID, uuid.NewString(), "service"); err != nil {
								t.Fatal(err)
							}
						}
						routed, err := rstore.CommitCheckedRollback(ctx, op)
						if err != nil || routed.Status != "routing" {
							t.Fatalf("service routing after readiness: %+v %v", routed, err)
						}
						previous, err := s.DeploymentByID(ctx, current.ID)
						if err != nil || previous.Status != state.DeployLive {
							t.Fatalf("predecessor retired before acknowledged handoff: %+v %v", previous, err)
						}
					} else if committed.Status != "complete" {
						t.Fatalf("request rollback did not complete: %+v", committed)
					}

				})
			}
		})
	}
}

func TestCheckedRollbackRefusesChangedLegacyProtocol(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			a, app, target, current := checkedRollbackFixture(t, s, false)
			ctx := t.Context()
			rstore := s.(state.CheckedRollbackStore)
			op, err := rstore.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "legacy protocol fence")
			if err != nil {
				t.Fatal(err)
			}
			changed := app.Manifest
			changed.ExecutionMode = api.ExecutionModeService
			changed.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
			if _, err = s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &changed}); err != nil {
				t.Fatal(err)
			}
			if err = s.MarkDeploymentLive(ctx, target.ID); err != nil {
				t.Fatal(err)
			}
			op, err = rstore.GetCheckedRollback(ctx, a.ID, app.ID, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = rstore.CommitCheckedRollback(ctx, op); !errors.Is(err, state.ErrCheckedRollbackChanged) {
				t.Fatalf("changed protocol committed: %v", err)
			}
			previous, err := s.DeploymentByID(ctx, current.ID)
			if err != nil || previous.Status != state.DeployLive || previous.TrafficPercent != 100 {
				t.Fatalf("changed protocol touched serving release: %+v %v", previous, err)
			}
		})
	}
}

func TestScopedCheckedRollbackUsesDefaultReplicaTarget(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			a, app, target, current := scopedRollbackFixture(t, s, api.ExecutionModeService, api.ExecutionModeService, true)
			ctx := t.Context()
			rstore := s.(state.CheckedRollbackStore)
			op, err := rstore.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "default replica target")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.MarkDeploymentLive(ctx, target.ID); err != nil {
				t.Fatal(err)
			}
			op, err = rstore.GetCheckedRollback(ctx, a.ID, app.ID, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CreateInstanceWithMode(ctx, app.ID, target.ID, "running", 128, node.ID, uuid.NewString(), "service"); err != nil {
				t.Fatal(err)
			}
			committed, err := rstore.CommitCheckedRollback(ctx, op)
			if err != nil || committed.Status != "routing" {
				t.Fatalf("default target inherited shared replica count: %+v %v", committed, err)
			}
		})
	}
}
