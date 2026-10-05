//go:build linux

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMainAndSidecarSecretReceiptsRemainIndependent(t *testing.T) {
	store := &runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "prod", SecretReloadSignal: "SIGHUP",
		OverrideEnvSecrets: json.RawMessage(`{"SHARED":"secret:SHARED","MAIN_TOKEN":"secret:MAIN_TOKEN"}`),
		Sidecars:           json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"SHARED":"secret:SHARED","SIDE_TOKEN":"secret:SIDE_TOKEN"}}]`),
	}, secretRows: []state.AppSecret{
		{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "SHARED", DeliveryVersion: 1},
		{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "MAIN_TOKEN", DeliveryVersion: 2},
		{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "SIDE_TOKEN", DeliveryVersion: 3},
	}}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", "dep-1", "app-1", "acct-1")
	receiver := &runtimeConfigReceiver{ctx: context.Background(), mgr: manager, store: store}
	main, err := selectRuntimeSecretRowsForWorkload(context.Background(), store, "dep-1", "app-1", "acct-1", "")
	if err != nil {
		t.Fatal(err)
	}
	sidecar, err := selectRuntimeSecretRowsForWorkload(context.Background(), store, "dep-1", "app-1", "acct-1", "worker")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ name, revision, privateKey string }{{"", main.Revision, "MAIN_TOKEN"}, {"worker", sidecar.Revision, "SIDE_TOKEN"}} {
		for _, kind := range []string{"secret_reload_status", "secret_reload_ack"} {
			request := runtimeConfigRequest{Kind: kind, WorkloadName: target.name, Revision: target.revision}
			if kind == "secret_reload_status" {
				request.Projection, request.Signal, request.ErrorCode = "updated", "failed", "signal_failed"
			} else {
				request.ApplicationAck, request.ApplicationAckErrorCode = "failed", "application_reload_failed"
			}
			response := sendRuntimeConfigTestRequest(t, receiver, request)
			if !response.Accepted {
				t.Fatalf("%q %s failed receipt rejected: %+v", target.name, kind, response)
			}
		}
		reload, ack := store.reloadResults[len(store.reloadResults)-1], store.ackResults[len(store.ackResults)-1]
		if reload.WorkloadName != target.name || ack.WorkloadName != target.name || len(reload.Candidates) != 2 || len(ack.Candidates) != 2 {
			t.Fatalf("workload identity/grants mixed: %+v %+v", reload, ack)
		}
		for _, candidate := range ack.Candidates {
			if candidate.Key != "SHARED" && candidate.Key != target.privateKey {
				t.Fatalf("receipt crossed grant boundary: %+v", ack)
			}
		}
	}
	// Main-only rotation invalidates main's revision without invalidating the
	// sidecar's independent receipt for the shared, unchanged key.
	store.secretRows[1].DeliveryVersion++
	for _, target := range []struct {
		name, revision string
		accepted       bool
	}{{"", main.Revision, false}, {"worker", sidecar.Revision, true}} {
		response := sendRuntimeConfigTestRequest(t, receiver, runtimeConfigRequest{Kind: "secret_reload_ack", WorkloadName: target.name, Revision: target.revision, ApplicationAck: "applied"})
		if response.Accepted != target.accepted || !target.accepted && response.Error != "secret_reload_stale" {
			t.Fatalf("rotation receipt %q: %+v", target.name, response)
		}
	}
}

func TestMainSecretAllowlistWithSidecarsFailsClosed(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{}`)} {
		deployment := state.Deployment{Sidecars: raw, OverrideEnvSecrets: json.RawMessage(`{"TOKEN":"secret:TOKEN"}`)}
		if _, err := runtimeSecretWorkloadAllowlist(deployment, ""); err == nil {
			t.Fatalf("malformed roster %s accepted", raw)
		}
	}
	deployment := state.Deployment{Sidecars: json.RawMessage(`[{"name":"worker"}]`), OverrideEnvSecrets: json.RawMessage(`{"TOKEN":"literal"}`)}
	if _, err := runtimeSecretWorkloadAllowlist(deployment, ""); err == nil {
		t.Fatal("invalid main secret grant accepted")
	}
}
