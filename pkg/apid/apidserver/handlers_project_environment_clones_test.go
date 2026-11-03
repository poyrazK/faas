// adr: 590
package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFullProjectEnvironmentCloneAdmissionDoesNotCreatePartialTarget(t *testing.T) {
	for _, dedicated := range []bool{true, false} {
		t.Run(map[bool]string{true: "dedicated", false: "legacy"}[dedicated], func(t *testing.T) {
			srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
			req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environment-clones", project.Slug,
				[]byte(`{"slug":"stage","from_environment":"production","full":true}`))
			if dedicated {
				srv.CreateFullProjectEnvironmentClone(rec, req, acct)
			} else {
				srv.CreateProjectEnvironment(rec, req, acct)
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != api.CodeFullEnvironmentCloneUnavailable || len(problem.Errors) != 4 {
				t.Fatalf("response = %d %s, %v", rec.Code, rec.Body.String(), err)
			}
			if _, err := store.ProjectEnvironmentBySlug(context.Background(), acct.ID, project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("partial target exists: %v", err)
			}
		})
	}
}

func TestProjectEnvironmentCloneStatusIsScopedAndExcludesPrivateCapture(t *testing.T) {
	srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", SourceRevisionHash: strings.Repeat("a", 64), IdempotencyKey: "private-idempotency-key"})
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := store.ClaimNextProjectEnvironmentClone(ctx, token, time.Minute); err != nil {
		t.Fatal(err)
	}
	req, rec := apidserver.ProjectRequest(http.MethodGet, "/v1/projects/shop/environment-clones/"+op.ID, project.Slug, nil)
	req.SetPathValue("clone", op.ID)
	srv.GetProjectEnvironmentCloneOperation(rec, req, acct)
	var response api.ProjectEnvironmentCloneOperationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != http.StatusOK || response.OperationID != op.ID || response.Status != "pending" || response.Resources == nil {
		t.Fatalf("status = %d %s, %v", rec.Code, rec.Body.String(), err)
	}
	for _, private := range []string{token, "private-idempotency-key", "account_id", "lease", "ciphertext", "values"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatalf("status includes %q", private)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("status can be cached")
	}
	otherProject, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "other", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	req, rec = apidserver.ProjectRequest(http.MethodGet, "/v1/projects/other/environment-clones/"+op.ID, otherProject.Slug, nil)
	req.SetPathValue("clone", op.ID)
	srv.GetProjectEnvironmentCloneOperation(rec, req, acct)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project lookup = %d %s", rec.Code, rec.Body.String())
	}
	other, err := store.CreateAccount(ctx, "clone-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	req, rec = apidserver.ProjectRequest(http.MethodGet, "/v1/projects/shop/environment-clones/"+op.ID, project.Slug, nil)
	req.SetPathValue("clone", op.ID)
	srv.GetProjectEnvironmentCloneOperation(rec, req, other)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account lookup = %d %s", rec.Code, rec.Body.String())
	}
}

type cloneSchemaCoverageTestStore struct {
	*state.MemStore
	report               state.ProjectEnvironmentCloneSchemaCoverage
	err                  error
	accountID, projectID string
	calls                int
}

func (s *cloneSchemaCoverageTestStore) ProjectEnvironmentCloneSchemaCoverage(_ context.Context, accountID, projectID string) (state.ProjectEnvironmentCloneSchemaCoverage, error) {
	s.accountID, s.projectID = accountID, projectID
	s.calls++
	return s.report, s.err
}

func TestFullProjectEnvironmentCloneNamesSchemaBlockersAndFailsClosed(t *testing.T) {
	for _, dedicated := range []bool{true, false} {
		for _, failed := range []bool{false, true} {
			name := map[bool]string{true: "dedicated", false: "legacy"}[dedicated] + "/" + map[bool]string{true: "read_failure", false: "blockers"}[failed]
			t.Run(name, func(t *testing.T) {
				srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
				coverageStore := &cloneSchemaCoverageTestStore{MemStore: store, report: state.ProjectEnvironmentCloneSchemaCoverage{Blockers: []state.ProjectEnvironmentCloneCoverageBlocker{
					{Table: "queue_bindings", Code: "isolated_strategy_unavailable"},
					{Table: "app_envs", Column: "new_setting", Code: "unregistered_column"},
				}}}
				if failed {
					coverageStore.err = errors.New("private-state-reader-error")
				}
				(*srv.Store()) = coverageStore
				req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environment-clones", project.Slug,
					[]byte(`{"slug":"stage","from_environment":"production","full":true}`))
				if dedicated {
					srv.CreateFullProjectEnvironmentClone(rec, req, acct)
				} else {
					srv.CreateProjectEnvironment(rec, req, acct)
				}
				if coverageStore.calls != 1 || coverageStore.accountID != acct.ID || coverageStore.projectID != project.ID {
					t.Fatalf("coverage was not scoped to the loaded project: %+v", coverageStore)
				}
				if failed {
					if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "private-state-reader-error") {
						t.Fatalf("coverage failure was hidden or exposed: %d %s", rec.Code, rec.Body.String())
					}
				} else {
					var problem api.Problem
					if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != api.CodeFullEnvironmentCloneUnavailable || len(problem.Errors) != 6 {
						t.Fatalf("named coverage response = %d %s, %v", rec.Code, rec.Body.String(), err)
					}
					if problem.Errors[4].Field != "resource_coverage.queue_bindings" || problem.Errors[4].Got != "isolated_strategy_unavailable" ||
						problem.Errors[5].Field != "resource_coverage.app_envs.new_setting" || problem.Errors[5].Got != "unregistered_column" {
						t.Fatalf("coverage blockers were dropped: %+v", problem.Errors)
					}
				}
				if _, err := store.ProjectEnvironmentBySlug(context.Background(), acct.ID, project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("partial target exists: %v", err)
				}
			})
		}
	}
}

func TestFullProjectEnvironmentCloneHidesInternalHostingBlockers(t *testing.T) {
	for _, dedicated := range []bool{true, false} {
		for _, enabled := range []bool{false, true} {
			for _, operator := range []bool{false, true} {
				name := map[bool]string{true: "dedicated", false: "legacy"}[dedicated] + "/" +
					map[bool]string{true: "enabled", false: "disabled"}[enabled] + "/" +
					map[bool]string{true: "operator", false: "customer"}[operator]
				t.Run(name, func(t *testing.T) {
					srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
					srv.WithRuntimeConfigManager(apidserver.NewRuntimeConfigManager(func(key string) string {
						if key == "FAAS_FRONTEND_HOSTING_ENABLED" && enabled {
							return "true"
						}
						return ""
					}))
					if operator {
						srv.WithAdminAllowlist(acct.Email)
					}
					(*srv.Store()) = &cloneSchemaCoverageTestStore{MemStore: store, report: state.ProjectEnvironmentCloneSchemaCoverage{Blockers: []state.ProjectEnvironmentCloneCoverageBlocker{
						{Table: "queue_bindings", Code: "isolated_strategy_unavailable"},
						{Table: "hosting_sites", Code: "isolated_strategy_unavailable"},
						{Table: "hosting_releases", Column: "artifact_bytes", Code: "isolated_data_strategy_unavailable"},
					}}}
					req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environment-clones", project.Slug,
						[]byte(`{"slug":"stage","from_environment":"production","full":true}`))
					if dedicated {
						srv.CreateFullProjectEnvironmentClone(rec, req, acct)
					} else {
						srv.CreateProjectEnvironment(rec, req, acct)
					}
					var problem api.Problem
					if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != api.CodeFullEnvironmentCloneUnavailable {
						t.Fatalf("clone admission = %d %s, %v", rec.Code, rec.Body.String(), err)
					}
					for _, table := range []string{"hosting_sites", "hosting_releases"} {
						if strings.Contains(rec.Body.String(), table) != (enabled && operator) {
							t.Fatalf("unexpected internal table visibility: %s", rec.Body.String())
						}
					}
					if !strings.Contains(rec.Body.String(), "resource_coverage.queue_bindings") {
						t.Fatalf("public coverage blocker missing: %s", rec.Body.String())
					}
					if _, err := store.ProjectEnvironmentBySlug(context.Background(), acct.ID, project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("partial target exists: %v", err)
					}
				})
			}
		}
	}
}

// Shared organization project access never admits a full clone, even with
// both switches on: a member must not copy the owner's values, secrets or
// databases into a stage they control (the /environments rule).
func TestFullProjectEnvironmentCloneRefusesSharedOrganizationAccess(t *testing.T) {
	srv, store, actor, _, _ := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	for _, key := range []string{runtimeconfig.KeyFullEnvironmentClones, "full_environment_clone_admission_enabled"} {
		if err := (*srv.RuntimeConfig()).Apply(key, json.RawMessage("true")); err != nil {
			t.Fatal(err)
		}
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "clone-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: org.ID, OrgID: org.ID, Slug: "company-shop"})
	if err != nil {
		t.Fatal(err)
	}
	req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/orgs/"+org.Slug+"/projects/"+project.Slug+"/environment-clones", project.Slug,
		[]byte(`{"slug":"stage","from_environment":"production","full":true}`))
	req.Pattern = "POST /v1/orgs/{organization}/projects/{slug}/environment-clones"
	req.SetPathValue("organization", org.Slug)
	req.Header.Set("Idempotency-Key", "org-clone")
	req = req.WithContext(authmw.WithPrincipal(ctx, actor, nil, nil))
	projectaccess.Wrap(store, nil, srv.CreateFullProjectEnvironmentClone)(rec, req, actor)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusConflict {
		t.Fatalf("shared organization clone = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := store.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, org.ID, project.ID, "org-clone"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("shared organization access admitted a clone: %v", err)
	}
}

// Refusals that last until the customer changes the source are 409s naming the
// cause, never a 404 for an environment that exists or a 503 that never clears.
func TestFullProjectEnvironmentCloneAdmissionMapsSourceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
		field  string
	}{
		{"idempotency key", state.ErrInvalidProjectEnvironmentCloneOperation, http.StatusBadRequest, api.CodeValidation, ""},
		{"undeployed workload", state.ErrProjectEnvironmentCloneSourceNotDeployed, http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "source.workloads"},
		{"release pinned", state.ErrReleaseConfigurationTransferUnsupported, http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "source.release_configuration"},
		{"project secrets", state.ErrProjectSecretCloneTransferUnsupported, http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "source.project_secrets"},
		{"legacy stage queues", state.ErrProjectEnvironmentQueueCollectionUnavailable, http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "source.queue_bindings"},
		{"work policies", state.ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable, http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "source.work_policies"},
		{"missing source", state.ErrNotFound, http.StatusNotFound, "", ""},
		{"transient", errors.New("connection reset"), http.StatusServiceUnavailable, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problem := apidserver.FullProjectEnvironmentCloneAdmissionProblem("shop", "production", fmt.Errorf("capture: %w", tc.err))
			if problem.Status != tc.status || (tc.code != "" && problem.Code != tc.code) {
				t.Fatalf("problem = %d %s, want %d %s", problem.Status, problem.Code, tc.status, tc.code)
			}
			if tc.field != "" && (len(problem.Errors) != 1 || problem.Errors[0].Field != tc.field) {
				t.Fatalf("errors = %+v, want one naming %s", problem.Errors, tc.field)
			}
		})
	}
}
