package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMainSidecarSecretReloadMem(t *testing.T) {
	mainSidecarSecretReloadSuite(t, state.NewMemStore())
}

func TestMainSidecarSecretReloadPG(t *testing.T) {
	store, _ := pgStore(t)
	mainSidecarSecretReloadSuite(t, store)
}

func mainSidecarSecretReloadSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, app, _, _ := bindingPromotionFixture(t, store)
	nodeID := bindingAdoptionNode(t, store, ctx)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "default",
		OverrideEnvSecrets: json.RawMessage(`{"SHARED":"secret:SHARED","MAIN_ONLY":"secret:MAIN_ONLY"}`),
		Sidecars:           json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"SHARED":"secret:SHARED","SIDE_ONLY":"secret:SIDE_ONLY"}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"SHARED", "MAIN_ONLY", "SIDE_ONLY", "UNAUTHORIZED"} {
		if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "default", key, "kid", "1111111111111111", []byte("sealed")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, deployment.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSidecarSecretReloadSignal(ctx, deployment.ID, "worker", "SIGUSR1"); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.CreateInstance(ctx, app.ID, deployment.ID, "running", 512, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	read := func() []state.AppSecretRuntimeReloadTarget {
		t.Helper()
		rows, err := store.ListAppSecretRuntimeReloadTargets(ctx, acct.ID, app.ID, "default")
		if err != nil || len(rows) != 4 {
			t.Fatalf("main/sidecar roster: %+v %v", rows, err)
		}
		for _, row := range rows {
			if row.InstanceID != runtime.ID || row.ReloadSupport != "enabled" || row.Key == "UNAUTHORIZED" || row.WorkloadName == "" && row.Key == "SIDE_ONLY" || row.WorkloadName == "worker" && row.Key == "MAIN_ONLY" {
				t.Fatalf("reload grant/support: %+v", row)
			}
		}
		return rows
	}
	read()
	for _, workload := range []string{"", "worker"} {
		result := state.AppSecretRuntimeReloadResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, WorkloadName: workload,
			Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
			AttemptedAt: time.Now().UTC(), Candidates: []state.AppSecretDeliveryCandidate{{Scope: "default", Key: "SHARED", Version: 1}}}
		if workload == "" {
			result.Signal, result.ErrorCode = state.SecretReloadSignalFailed, "signal_failed"
		}
		if n, err := store.RecordAppSecretRuntimeReload(ctx, result); err != nil || n != 1 {
			t.Fatalf("%q reload: %d %v", workload, n, err)
		}
		if workload == "worker" {
			result.Signal = state.SecretReloadSignalNotAttempted
			if n, err := store.RecordAppSecretRuntimeReload(ctx, result); err != nil || n != 1 {
				t.Fatalf("startup delivery: %d %v", n, err)
			}
			for _, row := range read() {
				if row.Key == "SHARED" && row.WorkloadName == "worker" && (row.Signal != state.SecretReloadSignalNotAttempted || row.ApplicationAckVersion != 0) {
					t.Fatalf("startup delivery claimed application adoption: %+v", row)
				}
			}
		}
		status, code := state.SecretApplicationReloadAckApplied, ""
		if workload == "" {
			status, code = state.SecretApplicationReloadAckFailed, "application_reload_failed"
		}
		if n, err := store.RecordAppSecretRuntimeReloadAck(ctx, state.AppSecretRuntimeReloadAckResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, WorkloadName: workload,
			Revision: result.Revision, Status: status, ErrorCode: code, AttemptedAt: time.Now().UTC(), Candidates: result.Candidates}); err != nil || n != 1 {
			t.Fatalf("%q ack: %d %v", workload, n, err)
		}
	}
	for _, row := range read() {
		if row.Key != "SHARED" {
			if row.Reported || row.ApplicationAckVersion != 0 {
				t.Fatalf("receipt leaked to another key: %+v", row)
			}
			continue
		}
		if !row.Reported || row.ApplicationAckVersion != 1 || row.WorkloadName == "" && (row.Signal != state.SecretReloadSignalFailed || row.ApplicationAck != state.SecretApplicationReloadAckFailed) || row.WorkloadName == "worker" && row.ApplicationAck != state.SecretApplicationReloadAckApplied {
			t.Fatalf("receipts mixed between main and sidecar: %+v", row)
		}
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "default", "SHARED", "kid", "2222222222222222", []byte("new-sealed")); err != nil {
		t.Fatal(err)
	}
	for _, workload := range []string{"", "worker"} {
		_, err := store.RecordAppSecretRuntimeReloadAck(ctx, state.AppSecretRuntimeReloadAckResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, WorkloadName: workload,
			Revision: strings.Repeat("a", 64), Status: state.SecretApplicationReloadAckApplied, Candidates: []state.AppSecretDeliveryCandidate{{Scope: "default", Key: "SHARED", Version: 1}}})
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("accepted stale %q receipt after rotation: %v", workload, err)
		}
	}
	// Deletion uses the same workload roster. Both opted-in consumers must
	// remain separate enabled revocation targets, even for a shared key.
	revocation, err := store.DeleteAppSecretInScopeWithRevocation(ctx, acct.ID, app.ID, "default", "SHARED")
	if err != nil || len(revocation.Targets) != 2 {
		t.Fatalf("revocation targets: %+v %v", revocation, err)
	}
	for _, target := range revocation.Targets {
		if target.ReloadSupport != "enabled" {
			t.Fatalf("main/sidecar revocation support: %+v", target)
		}
	}
}
