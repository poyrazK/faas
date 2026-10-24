//go:build !no_pg

// adr: 590
package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

const fullCloneAdmissionKey = "full_environment_clone_admission_enabled"

type datalessCloneFixture struct {
	t       *testing.T
	f       apidserver.CloneCoordinatorFixture
	acct    state.Account
	project state.Project
}

// The source has two live HTTP workloads with environment values and no
// managed data; the configuration write fence stays off, as in production.
func newDatalessCloneFixture(t *testing.T) datalessCloneFixture {
	f := apidserver.NewCloneSourceFixture(t, false, false)
	return datalessCloneFixture{t: t, f: f, acct: *f.Account(), project: *f.Project()}
}

func (d datalessCloneFixture) open(keys ...string) {
	d.t.Helper()
	for _, key := range keys {
		if err := (*(*d.f.Srv()).RuntimeConfig()).Apply(key, json.RawMessage("true")); err != nil {
			d.t.Fatal(err)
		}
	}
}

func (d datalessCloneFixture) admit(target, key string) *httptest.ResponseRecorder {
	d.t.Helper()
	req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/"+d.project.Slug+"/environment-clones", d.project.Slug,
		[]byte(`{"slug":"`+target+`","from_environment":"production","full":true}`))
	req.Header.Set("Idempotency-Key", key)
	(*d.f.Srv()).CreateFullProjectEnvironmentClone(rec, req, d.acct)
	return rec
}

func (d datalessCloneFixture) admitted(target, key string) api.ProjectEnvironmentCloneOperationResponse {
	d.t.Helper()
	rec := d.admit(target, key)
	var env api.ProjectEnvironmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || rec.Code != http.StatusAccepted || env.Slug != target || env.CloneOperation == nil ||
		env.CloneOperation.Status != state.CloneOperationPending || env.CloneOperation.TargetEnvironment != target ||
		rec.Header().Get("Location") != "/v1/projects/"+d.project.Slug+"/environment-clones/"+env.CloneOperation.OperationID {
		d.t.Fatalf("admission = %d %s, %v", rec.Code, rec.Body.String(), err)
	}
	return *env.CloneOperation
}

// step runs one coordinator pass for the operation, ignoring retryable errors.
func (d datalessCloneFixture) step(operationID string) state.ProjectEnvironmentCloneOperation {
	d.t.Helper()
	ctx := d.t.Context()
	if _, err := (*d.f.Pool()).Exec(ctx, "update project_environment_clone_operations set next_attempt_at=clock_timestamp() where id=$1 and lease_token is null", operationID); err != nil {
		d.t.Fatal(err)
	}
	_ = (*d.f.Srv()).ProcessNextProjectEnvironmentClone(ctx, *d.f.Store())
	op, err := (*d.f.Store()).ProjectEnvironmentCloneOperationByID(ctx, d.acct.ID, d.project.ID, operationID)
	if err != nil {
		d.t.Fatal(err)
	}
	return op
}

func (d datalessCloneFixture) targets(operationID string) []state.ProjectEnvironmentCloneWorkload {
	d.t.Helper()
	views, err := (*d.f.Store()).ProjectEnvironmentCloneWorkloads(d.t.Context(), d.acct.ID, d.project.ID, operationID)
	if err != nil {
		d.t.Fatal(err)
	}
	return views
}

// A data-less full clone is admitted only with both switches on, then
// captured, copied and published by the coordinator: the stage runs the
// source's artifacts with its own copy of the values, and production keeps
// its own (ADR-20261024123122).
func TestPGDatalessFullCloneAdmitsCopiesAndPublishes(t *testing.T) {
	d := newDatalessCloneFixture(t)
	ctx := t.Context()
	store := *d.f.Store()
	for _, keys := range [][]string{nil, {runtimeconfig.KeyFullEnvironmentClones}} {
		d.open(keys...)
		if rec := d.admit("stage", "closed"); rec.Code != http.StatusConflict {
			t.Fatalf("admission opened with %v: %d %s", keys, rec.Code, rec.Body.String())
		}
	}
	d.open(fullCloneAdmissionKey)
	op := d.admitted("stage", "stage-key")
	if replay := d.admitted("stage", "stage-key"); replay.OperationID != op.OperationID {
		t.Fatalf("an idempotent retry created a second clone: %s != %s", replay.OperationID, op.OperationID)
	}

	current := d.step(op.OperationID)
	if current.Status != state.CloneOperationCopying {
		t.Fatalf("capture and copy: %+v", current)
	}
	for _, view := range d.targets(op.OperationID) {
		if view.TargetDeploymentID == "" {
			t.Fatalf("workload %s has no target deployment", view.WorkloadSlug)
		}
		if err := store.MarkDeploymentLiveDark(ctx, view.TargetDeploymentID); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if current = d.step(op.OperationID); current.Status == state.CloneOperationReady {
			break
		}
	}
	if current.Status != state.CloneOperationReady || current.TargetReleaseSetID == "" || current.ErrorCode != "" {
		t.Fatalf("the clone did not publish: %+v", current)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, d.acct.ID, d.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	for _, view := range d.targets(op.OperationID) {
		source, err := store.DeploymentByID(ctx, view.SourceDeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		target, err := store.DeploymentByID(ctx, view.TargetDeploymentID)
		if err != nil || target.Scope != "stage" || target.Status != state.DeployLive || target.ImageDigest != source.ImageDigest || target.RootfsPath != source.RootfsPath {
			t.Fatalf("target deployment %+v does not run the source artifact %+v: %v", target, source, err)
		}
		if err := store.UpsertAppEnvInScope(ctx, d.acct.ID, view.AppID, "stage", "CAPTURED", "stage-edit"); err != nil {
			t.Fatal(err)
		}
		production, err := store.ListAppEnvInScope(ctx, d.acct.ID, view.AppID, "production")
		if err != nil || len(production) != 1 || production[0].Value != "original" {
			t.Fatalf("a stage edit reached production: %+v, %v", production, err)
		}
		if source, err = store.DeploymentByID(ctx, view.SourceDeploymentID); err != nil || source.Status != state.DeployLive {
			t.Fatalf("the source deployment changed: %+v, %v", source, err)
		}
	}
}

// A source that uses a table the clone cannot copy is refused with that table
// named, and nothing is created.
func TestPGDatalessFullCloneRefusesUncopyableSource(t *testing.T) {
	d := newDatalessCloneFixture(t)
	d.open(runtimeconfig.KeyFullEnvironmentClones, fullCloneAdmissionKey)
	app := (*d.f.Apps())[0]
	if _, err := (*d.f.Store()).CreateCron(d.t.Context(), app.ID, "*/5 * * * *", "/tick", true); err != nil {
		t.Fatal(err)
	}
	rec := d.admit("stage", "cron")
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != api.CodeFullEnvironmentCloneUnavailable ||
		len(problem.Errors) != 1 || problem.Errors[0].Field != "resource_coverage.crons" || problem.Errors[0].Got != state.CloneBlockerStrategyUnavailable {
		t.Fatalf("refusal = %d %s, %v", rec.Code, rec.Body.String(), err)
	}
	if _, err := (*d.f.Store()).ProjectEnvironmentBySlug(d.t.Context(), d.acct.ID, d.project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a refused clone created its target: %v", err)
	}
}

// A target deployment that fails ends the clone: its deployments are
// cancelled, the target environment is removed and the slug is free again.
func TestPGDatalessFullCloneCompensatesFailedTarget(t *testing.T) {
	d := newDatalessCloneFixture(t)
	ctx := t.Context()
	store := *d.f.Store()
	d.open(runtimeconfig.KeyFullEnvironmentClones, fullCloneAdmissionKey)
	op := d.admitted("stage", "failing")
	if current := d.step(op.OperationID); current.Status != state.CloneOperationCopying {
		t.Fatalf("capture and copy: %+v", current)
	}
	views := d.targets(op.OperationID)
	if err := store.UpdateDeploymentStatus(ctx, views[0].TargetDeploymentID, state.DeployFailed, "prime failed"); err != nil {
		t.Fatal(err)
	}
	current := d.step(op.OperationID)
	if current.Status != state.CloneOperationCompensating || current.ErrorCode != "target_deployment_failed" {
		t.Fatalf("a failed target deployment must end the clone: %+v", current)
	}
	if current = d.step(op.OperationID); current.Status != state.CloneOperationCompensated || current.ErrorCode != "target_deployment_failed" {
		t.Fatalf("compensation did not finish: %+v", current)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, d.acct.ID, d.project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("the failed target environment remains: %v", err)
	}
	for _, view := range views {
		target, err := store.DeploymentByID(ctx, view.TargetDeploymentID)
		if err != nil || (target.Status != state.DeployFailed && target.Status != state.DeployCancelled) {
			t.Fatalf("target deployment still runnable: %+v, %v", target, err)
		}
	}
	if again := d.admitted("stage", "retry"); again.OperationID == op.OperationID {
		t.Fatal("a retry replayed the compensated clone")
	}
}
