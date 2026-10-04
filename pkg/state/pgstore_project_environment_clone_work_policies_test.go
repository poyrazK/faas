//go:build !no_pg

// adr: 568
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestPgCloneWorkPoliciesAreCapturedAtomicallyAndReadOnlyUnderLease(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "work-policy-capture@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "policy-capture"})
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	for _, name := range []string{"configured", "empty"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "policy-" + name, WorkloadName: name, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:policy"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, d.ID, "/policy.ext4", "layers/policy-"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	app := apps[0]
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, MaxRunningPerFairnessKey: 2, PendingUpdates: workpolicy.PendingKeepLatest,
		Debounce: 100 * time.Millisecond, ExpiresAfter: time.Second}
	if _, err := s.UpsertAppWorkPolicy(ctx, a.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	sub, _, err := s.UpsertEventSubscription(ctx, a.ID, app.ID, "orders", "order.updated", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEventWorkBinding(ctx, app.ID, sub.ID, policy.Name, "data.order_id", state.EventWorkBindingOptions{Action: state.EventWorkCancelPending, FairnessSelector: "data.tenant_id"}); err != nil {
		t.Fatal(err)
	}
	trigger, err := s.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "orders", false, []byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTriggerWorkBinding(ctx, app.ID, trigger.ID.String(), policy.Name, "data.order_id", "data.tenant_id"); err != nil {
		t.Fatal(err)
	}
	request := state.ProjectEnvironmentCloneCaptureRequest{AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "policy-stage", IdempotencyKey: "policy"}
	op, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || lease.Operation.ID != op.ID {
		t.Fatalf("claim policy capture: %+v %v", lease, err)
	}
	captures, err := s.ProjectEnvironmentCloneWorkPoliciesForLease(ctx, lease)
	if err != nil || len(captures) != 2 {
		t.Fatalf("read policy captures: %+v %v", captures, err)
	}
	var captured state.ProjectEnvironmentCloneWorkPolicyCapture
	for _, candidate := range captures {
		if candidate.Definitions.AppID == app.ID {
			captured = candidate
		} else if candidate.Definitions.Version != 1 || len(candidate.Definitions.Policies) != 0 || len(candidate.Definitions.EventBindings) != 0 || len(candidate.Definitions.TriggerBindings) != 0 {
			t.Fatalf("empty collection was not explicitly captured: %+v", candidate)
		}
	}
	d := captured.Definitions
	if captured.OperationID != op.ID || len(captured.Hash) != 64 || d.SourceScope != "production" || len(d.Policies) != 1 ||
		d.Policies[0].Revision != 1 || d.Policies[0].DebounceMS != 100 || d.Policies[0].ExpiresAfterMS != 1000 || d.Policies[0].MaxRunningPerFairnessKey != 2 ||
		len(d.EventBindings) != 1 || d.EventBindings[0].SubscriptionID != sub.ID || d.EventBindings[0].Action != state.EventWorkCancelPending ||
		len(d.TriggerBindings) != 1 || d.TriggerBindings[0].TriggerID != trigger.ID.String() || d.TriggerBindings[0].FairnessSelector != "data.tenant_id" {
		t.Fatalf("incomplete policy/binding graph: %+v", captured)
	}
	var root []byte
	if err := pool.QueryRow(ctx, "select configuration from project_environment_clone_configuration_captures where operation_id=$1", op.ID).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(root), "orders") || strings.Contains(string(root), "data.order_id") || strings.Contains(string(root), sub.ID) {
		t.Fatal("root contains private policy/binding definitions")
	}
	policy.Debounce = 200 * time.Millisecond
	if _, err := s.UpsertAppWorkPolicy(ctx, a.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEventWorkBinding(ctx, app.ID, sub.ID, policy.Name, "data.changed", state.EventWorkBindingOptions{Action: state.EventWorkInvoke}); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, request); err != nil || replay.ID != op.ID || replay.SourceRevisionHash != op.SourceRevisionHash {
		t.Fatalf("replay recaptured mutable production policy: %+v %v", replay, err)
	}
	if frozen, err := s.ProjectEnvironmentCloneWorkPoliciesForLease(ctx, lease); err != nil || !reflect.DeepEqual(frozen, captures) {
		t.Fatalf("worker reread production after creation: %+v %v", frozen, err)
	}
	changed := request
	changed.TargetEnvironment, changed.IdempotencyKey = "changed-stage", "changed"
	if newer, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, changed); err != nil || newer.SourceRevisionHash == op.SourceRevisionHash {
		t.Fatalf("new root omitted changed policy/binding values: %+v %v", newer, err)
	}
	wrong := lease
	wrong.Token = uuid.NewString()
	if _, err := s.ProjectEnvironmentCloneWorkPoliciesForLease(ctx, wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong lease read private policies: %v", err)
	}
	// Returned arrays must not mutate the persisted capture.
	captures[0].Definitions.Policies = append(captures[0].Definitions.Policies, state.ProjectEnvironmentCloneWorkPolicy{Name: "caller"})
	if frozen, err := s.ProjectEnvironmentCloneWorkPoliciesForLease(ctx, lease); err != nil || reflect.DeepEqual(frozen, captures) {
		t.Fatalf("caller changed durable catalogue: %v", err)
	}
	other, err := s.CreateAccount(ctx, "other-policy-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update app_work_policies set account_id=$2 where app_id=$1", app.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	invalid := request
	invalid.TargetEnvironment, invalid.IdempotencyKey = "invalid-owner", "invalid-owner"
	if _, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, invalid); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong policy owner was captured or silently omitted: %v", err)
	}
	if _, err := s.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, a.ID, p.ID, invalid.IdempotencyKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("bad ownership left a partial operation: %v", err)
	}
	if _, err := s.ProjectEnvironmentCloneWorkPoliciesForLease(ctx, lease); err != nil {
		t.Fatalf("committed worker depended on current source ownership: %v", err)
	}
	if _, err := pool.Exec(ctx, "update app_work_policies set account_id=$2 where app_id=$1", app.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	var snapshot []byte
	if err := pool.QueryRow(ctx, "select snapshot from project_environment_clone_workloads where operation_id=$1 and app_id=$2", op.ID, app.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update project_environment_clone_workloads set snapshot=jsonb_set(snapshot::jsonb,'{policies,work,policies,0,debounce_ms}','999'::jsonb)::json where operation_id=$1 and app_id=$2", op.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneWorkPoliciesForLease(ctx, lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("tampered private catalogue was consumed: %v", err)
	}
	if _, err := pool.Exec(ctx, "update project_environment_clone_workloads set snapshot=$3 where operation_id=$1 and app_id=$2", op.ID, app.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	// Complete topology removal cannot downgrade a committed capture to a
	// fresh live lookup, even when production still has the omitted policies.
	if _, err := pool.Exec(ctx, "update project_environment_clone_workloads set snapshot=(snapshot::jsonb #- '{policies,work}')::json where operation_id=$1 and app_id=$2", op.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneWorkPoliciesForLease(ctx, lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("missing policy catalogue fell back to live source: %v", err)
	}
}
