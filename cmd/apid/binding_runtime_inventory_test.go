package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingRuntimeInventoryPassedProbeLeavesOldServingInstanceStale(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, deployment := seedAppTaskDeployment(t, e, "binding-runtime")
	bindings, binding := readyVerificationBinding(t, e, app)
	ctx := context.Background()
	old, err := e.store.CreateInstance(ctx, app.ID, deployment.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	e.store.BackdateForTest(old.ID, time.Now().Add(-time.Hour))
	rotated, _, err := bindings.BeginBindingRotation(ctx, e.acct.ID, binding.ID, uuid.NewString(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	finishVerificationBinding(t, bindings, rotated)
	if err := e.store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, MaxOutputBytes: 4096})
	completeVerificationTask(t, e, beginVerificationTask(t, e, task.ID), passedPostgresVerification)
	read := func() api.AppBindingInventory {
		return decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil))
	}
	got := read()
	if got.Bindings[0].VerificationStatus != "passed" || got.RuntimeFreshness == nil || len(got.RuntimeFreshness.Deployments) != 1 {
		t.Fatalf("inventory=%+v", got)
	}
	runtime := got.RuntimeFreshness.Deployments[0]
	if runtime.Status != "stale" || runtime.Serving.Stale != 1 || runtime.Serving.Current != 0 || runtime.Resident.Stale != 1 {
		t.Fatalf("passing probe hid old runtime: %+v", runtime)
	}
	next, err := e.store.CreateInstance(ctx, app.ID, deployment.ID, "cold_booting", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	runtime = read().RuntimeFreshness.Deployments[0]
	if runtime.Status != "stale" || runtime.Starting != 1 || runtime.Serving.Current != 0 {
		t.Fatalf("starting replacement hid stale serving instance: %+v", runtime)
	}
	if err := e.store.UpdateInstanceState(ctx, old.ID, "draining"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateInstanceState(ctx, next.ID, "running"); err != nil {
		t.Fatal(err)
	}
	runtime = read().RuntimeFreshness.Deployments[0]
	if runtime.Status != "stale" || runtime.Serving.Stale != 0 || runtime.Serving.Current != 1 || runtime.Resident.Stale != 1 {
		t.Fatalf("draining stale resident disappeared: %+v", runtime)
	}
	if err := e.store.UpdateInstanceState(ctx, old.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	runtime = read().RuntimeFreshness.Deployments[0]
	if runtime.Status != "current" || runtime.Serving.Current != 1 || runtime.Resident.Stale != 0 {
		t.Fatalf("replacement not reflected: %+v", runtime)
	}
}

type bindingRuntimeReadStore struct {
	*inventoryReadStore
	runtimeErr, refreshErr     error
	refreshRows                []state.BindingRefreshInventory
	runtimeCalls, refreshCalls int
	requestedIDs               []string
}

func (s *bindingRuntimeReadStore) ReadBindingRuntimeInventory(ctx context.Context, account, app, scope string) (state.BindingRuntimeInventory, error) {
	s.runtimeCalls++
	if s.runtimeErr != nil {
		return state.BindingRuntimeInventory{}, s.runtimeErr
	}
	return s.MemStore.ReadBindingRuntimeInventory(ctx, account, app, scope)
}

func (s *bindingRuntimeReadStore) ListBindingRefreshInventory(_ context.Context, _, _ string, ids []string) ([]state.BindingRefreshInventory, error) {
	s.refreshCalls++
	s.requestedIDs = append([]string(nil), ids...)
	return s.refreshRows, s.refreshErr
}

func TestBindingRuntimeInventoryPartialReadsAndBatchedRefresh(t *testing.T) {
	for _, failure := range []string{"", "runtime", "refresh"} {
		t.Run(failure, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			app := createApp(t, e, "binding-refresh")
			bindings, binding := readyVerificationBinding(t, e, app)
			rotated, _, err := bindings.BeginBindingRotation(context.Background(), e.acct.ID, binding.ID, uuid.NewString(), time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			finishVerificationBinding(t, bindings, rotated)
			wakeID := rotated.RotationWakeID
			now := time.Now().UTC()
			reads := &bindingRuntimeReadStore{inventoryReadStore: &inventoryReadStore{MemStore: e.store, objects: []state.ObjectStorageBindingInventory{
				{BucketName: "assets", Scope: "default", Prefix: "GREGALE_S3_ASSETS", State: "active", RotationPending: true, RotationWakeID: wakeID},
			}}, refreshRows: []state.BindingRefreshInventory{{WakeID: wakeID, Status: "retrying", Attempts: 2, FailureReason: "telemetry_missing", RequestedAt: now}}}
			if failure == "runtime" {
				reads.runtimeErr = errors.New("PRIVATE_ERROR password")
			}
			if failure == "refresh" {
				reads.refreshErr = errors.New("PRIVATE_ERROR password")
			}
			e.s.store = reads
			response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil)
			got := decodeBindingInventory(t, response)
			if reads.runtimeCalls != 1 || reads.refreshCalls != 1 || !reflect.DeepEqual(reads.requestedIDs, []string{wakeID}) || len(got.Bindings) != 2 {
				t.Fatalf("not batched: %+v reads=%+v", got, reads)
			}
			if got.Complete != (failure == "") || got.HasErrors() != (failure != "") || (got.RuntimeFreshness != nil) != (failure != "runtime") {
				t.Fatalf("partial inventory=%+v", got)
			}
			for _, item := range got.Bindings {
				if item.Refresh == nil || item.Refresh.WakeID != wakeID {
					t.Fatalf("refresh missing: %+v", item)
				}
				if failure == "refresh" && item.Refresh.Status != "unknown" || failure != "refresh" && (item.Refresh.Status != "retrying" || item.Refresh.FailureReason != "telemetry_missing") {
					t.Fatalf("refresh status=%+v", item.Refresh)
				}
			}
			if strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(response.Body.String(), "password") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestBindingRuntimeDeploymentStatusPrecedence(t *testing.T) {
	for _, tc := range []struct {
		row  state.BindingRuntimeDeployment
		want string
	}{
		{state.BindingRuntimeDeployment{}, "inactive"},
		{state.BindingRuntimeDeployment{Resident: state.BindingRuntimeCounts{Current: 1}}, "current"},
		{state.BindingRuntimeDeployment{Resident: state.BindingRuntimeCounts{Current: 1}, Starting: 1}, "updating"},
		{state.BindingRuntimeDeployment{Resident: state.BindingRuntimeCounts{Unknown: 1}, Starting: 1}, "unknown"},
		{state.BindingRuntimeDeployment{Resident: state.BindingRuntimeCounts{Stale: 1, Unknown: 1}, Starting: 1}, "stale"},
	} {
		if got := bindingRuntimeDeploymentStatus(tc.row); got != tc.want {
			t.Fatalf("status=%s want=%s row=%+v", got, tc.want, tc.row)
		}
	}
}

func TestBindingRefreshMissingRecordDoesNotClaimCompletion(t *testing.T) {
	item := api.AppBindingInventoryItem{Type: "postgres", Binding: "DATABASE_URL", Scope: "default"}
	inventory := api.AppBindingInventory{Bindings: []api.AppBindingInventoryItem{item}}
	setBindingRefreshes(&inventory, map[string]string{bindingVerificationKey(item.Type, item.Binding, item.Scope): "wake-1"}, nil, true)
	if inventory.Bindings[0].Refresh.Status != "not_queued" {
		t.Fatalf("missing outbox record=%+v", inventory.Bindings[0].Refresh)
	}
}
