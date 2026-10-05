// adr: 590
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFullProjectEnvironmentCloneAdmissionDoesNotCreatePartialTarget(t *testing.T) {
	for _, dedicated := range []bool{true, false} {
		t.Run(map[bool]string{true: "dedicated", false: "legacy"}[dedicated], func(t *testing.T) {
			srv, store, acct, project, _ := newProjectLifecycleFixture(t)
			req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environment-clones", project.Slug,
				[]byte(`{"slug":"stage","from_environment":"production","full":true}`))
			if dedicated {
				srv.createFullProjectEnvironmentClone(rec, req, acct)
			} else {
				srv.createProjectEnvironment(rec, req, acct)
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
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
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
	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environment-clones/"+op.ID, project.Slug, nil)
	req.SetPathValue("clone", op.ID)
	srv.getProjectEnvironmentCloneOperation(rec, req, acct)
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
	req, rec = projectRequest(http.MethodGet, "/v1/projects/other/environment-clones/"+op.ID, otherProject.Slug, nil)
	req.SetPathValue("clone", op.ID)
	srv.getProjectEnvironmentCloneOperation(rec, req, acct)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project lookup = %d %s", rec.Code, rec.Body.String())
	}
	other, err := store.CreateAccount(ctx, "clone-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environment-clones/"+op.ID, project.Slug, nil)
	req.SetPathValue("clone", op.ID)
	srv.getProjectEnvironmentCloneOperation(rec, req, other)
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
				srv, store, acct, project, _ := newProjectLifecycleFixture(t)
				coverageStore := &cloneSchemaCoverageTestStore{MemStore: store, report: state.ProjectEnvironmentCloneSchemaCoverage{Blockers: []state.ProjectEnvironmentCloneCoverageBlocker{
					{Table: "queue_bindings", Code: "isolated_strategy_unavailable"},
					{Table: "app_envs", Column: "new_setting", Code: "unregistered_column"},
				}}}
				if failed {
					coverageStore.err = errors.New("private-state-reader-error")
				}
				srv.store = coverageStore
				req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environment-clones", project.Slug,
					[]byte(`{"slug":"stage","from_environment":"production","full":true}`))
				if dedicated {
					srv.createFullProjectEnvironmentClone(rec, req, acct)
				} else {
					srv.createProjectEnvironment(rec, req, acct)
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
