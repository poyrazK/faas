package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func scopedPolicyStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "postgres" {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	}
	return state.NewMemStore()
}

func TestScopedEgressRuntimeAdmission(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedPolicyStore(t, backend)
			for _, override := range []bool{false, true} {
				t.Run(fmt.Sprint(override), func(t *testing.T) {
					ctx := t.Context()
					owner, e := s.(interface {
						CreateAccountWithPersonalOrg(context.Context, state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
					}).CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "audit8-policy-" + uuid.NewString() + "@example.test", Plan: api.PlanPro})
					if e != nil {
						t.Fatal(e)
					}
					p, e := s.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "policy-" + uuid.NewString()[:8]})
					if e != nil {
						t.Fatal(e)
					}
					_, e = s.(interface {
						CreateProjectEnvironment(context.Context, state.ProjectEnvironment) (state.ProjectEnvironment, error)
					}).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: owner.Account.ID, ProjectID: p.ID, Slug: "staging"})
					if e != nil {
						t.Fatal(e)
					}
					app, e := s.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: p.ID, Slug: "policy-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
					if e != nil {
						t.Fatal(e)
					}
					settings, e := state.WorkloadSettingsFromApp(app)
					if e != nil {
						t.Fatal(e)
					}
					if override {
						settings.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
					}
					_, e = s.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, owner.Account.ID, p.ID, "staging", app.ID, 0, settings)
					if e != nil {
						t.Fatal(e)
					}
					d, e := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "example/app@sha256:" + strings.Repeat("a", 64)})
					if e != nil {
						t.Fatal(e)
					}
					if e = s.MarkDeploymentLive(ctx, d.ID); e != nil {
						t.Fatal(e)
					}
					d, e = s.DeploymentByID(ctx, d.ID)
					if e != nil {
						t.Fatal(e)
					}
					effective, e := state.AppForDeployment(ctx, s, d)
					if e != nil {
						t.Fatal(e)
					}
					node, e := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
					if e != nil {
						t.Fatal(e)
					}
					i, e := s.CreateInstanceWithMode(ctx, app.ID, d.ID, "cold_booting", 128, node.ID, uuid.NewString(), "normal")
					if e != nil {
						t.Fatal(e)
					}
					e = state.CheckInstanceApplicationStandardAdmission(ctx, s.(state.InstanceApplicationStandardAdmissionStore), i.ID, effective, owner.Account, d)
					t.Logf("override=%v shared.egress=%v pinned.egress=%v admission=%v", override, app.EgressAllowlist, effective.EgressAllowlist, e)
					if e != nil {
						t.Errorf("valid scoped egress cannot pass runtime admission: %v", e)
					}
				})
			}
		})
	}
}

// Only the standard-management capabilities needed by this fixture.
type scopedPolicyStandards interface {
	PublishApplicationStandardVersion(context.Context, state.ApplicationStandardPublish) (state.ApplicationStandardVersion, error)
	PreviewApplicationStandardAssignment(context.Context, string, string, state.ApplicationStandardReviewRequest) (state.ApplicationStandardReviewPlan, error)
	ApproveApplicationStandardReview(context.Context, string, string, string, string) (state.ApplicationStandardOperation, error)
	ClaimApplicationStandardOperation(context.Context, string) (state.ApplicationStandardWorkerClaim, error)
	MaterializeNextApplicationStandardTarget(context.Context, state.ApplicationStandardWorkerClaim) (state.ApplicationStandardOperation, error)
}

func TestScopedRuntimePolicyPreservesApplicationStandards(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedPolicyStore(t, backend)
			ctx := t.Context()
			owner, err := s.(interface {
				CreateAccountWithPersonalOrg(context.Context, state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
			}).CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "scoped-mandate@example.test", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			standards := s.(scopedPolicyStandards)
			version, err := standards.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "scoped-policy", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]}}`)}})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := standards.PreviewApplicationStandardAssignment(ctx, owner.PersonalOrg.ID, owner.Account.ID, state.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
			if err != nil || len(plan.Blockers) != 0 {
				t.Fatalf("standard preview: %+v %v", plan, err)
			}
			if _, err := standards.ApproveApplicationStandardReview(ctx, plan.OrgID, owner.Account.ID, plan.ID, plan.ApprovalHash); err != nil {
				t.Fatal(err)
			}
			claim, err := standards.ClaimApplicationStandardOperation(ctx, "scoped-policy-adoption")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := standards.MaterializeNextApplicationStandardTarget(ctx, claim); err != nil {
				t.Fatal(err)
			}
			project, err := s.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "scoped-mandate"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.(interface {
				CreateProjectEnvironment(context.Context, state.ProjectEnvironment) (state.ProjectEnvironment, error)
			}).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: owner.Account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: project.ID, Slug: "scoped-mandate", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			enrollment := s.(state.ApplicationStandardAutomaticMaterializationStore)
			onboarding, err := enrollment.ClaimApplicationStandardEnrollment(ctx, "scoped-policy-onboarding")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := enrollment.MaterializeApplicationStandardEnrollment(ctx, onboarding); err != nil {
				t.Fatal(err)
			}
			app, err = s.AppByID(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			for _, conflict := range []bool{false, true} {
				t.Run(fmt.Sprint(conflict), func(t *testing.T) {
					settings, err := state.WorkloadSettingsFromApp(app)
					if err != nil {
						t.Fatal(err)
					}
					settings.EgressPorts = []int{443} // Not controlled by this standard.
					if conflict {
						settings.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("9.9.9.0/24")}
					}
					specs := s.(state.ProjectEnvironmentWorkloadSpecStore)
					rev := int64(0)
					if prior, err := specs.ProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, "staging", app.ID); err == nil {
						rev = prior.Revision
					} else if !errors.Is(err, state.ErrNotFound) {
						t.Fatal(err)
					}
					if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, "staging", app.ID, rev, settings); err != nil {
						t.Fatal(err)
					}
					dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:scoped-policy"})
					if err != nil {
						t.Fatal(err)
					}
					if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
						t.Fatal(err)
					}
					dep, err = s.DeploymentByID(ctx, dep.ID)
					if err != nil {
						t.Fatal(err)
					}
					before, err := s.ListInstancesForApp(ctx, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					ins, err := s.CreateInstanceWithMode(ctx, app.ID, dep.ID, "cold_booting", 128, node.ID, uuid.NewString(), "normal")
					if conflict {
						if !errors.Is(err, state.ErrApplicationStandardsPending) {
							t.Fatalf("mandatory egress override admitted: %v", err)
						}
						after, err := s.ListInstancesForApp(ctx, app.ID)
						if err != nil || len(after) != len(before) {
							t.Fatalf("failed policy admission leaked a row: %d -> %d, %v", len(before), len(after), err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					effective, err := state.AppForDeployment(ctx, s, dep)
					if err != nil {
						t.Fatal(err)
					}
					reader := s.(state.InstanceApplicationStandardAdmissionStore)
					if err := state.CheckInstanceApplicationStandardAdmission(ctx, reader, ins.ID, effective, owner.Account, dep); err != nil {
						t.Fatalf("compliant scoped controls: %v", err)
					}
					wrong := effective
					wrong.EgressPorts = nil
					if err := state.CheckInstanceApplicationStandardAdmission(ctx, reader, ins.ID, wrong, owner.Account, dep); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
						t.Fatalf("stale scoped caller accepted: %v", err)
					}
				})
			}
		})
	}
}
