//go:build linux

// adr:438
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeSecretProcessStoreStub struct {
	runtimeSecretsStoreStub
	processes []state.AppSecretRuntimeProcess
	retire    bool
	err       error
}

func (s *runtimeSecretProcessStoreStub) BeginAppSecretRuntimeProcess(_ context.Context, p state.AppSecretRuntimeProcess) error {
	s.processes = append(s.processes, p)
	return s.err
}
func (s *runtimeSecretProcessStoreStub) RetireAppSecretRuntimeProcess(_ context.Context, p state.AppSecretRuntimeProcess) error {
	s.retire = true
	s.processes = append(s.processes, p)
	return s.err
}

func TestRuntimeSecretProcessGenerationIdentity(t *testing.T) {
	for _, workload := range []string{"", "worker", "granted"} {
		t.Run(workload, func(t *testing.T) {
			store := &runtimeSecretProcessStoreStub{runtimeSecretsStoreStub: runtimeSecretsStoreStub{deployment: state.Deployment{
				ID: "dep-1", AppID: "app-1", Scope: "prod", SecretReloadSignal: "SIGHUP", Sidecars: json.RawMessage(`[{"name":"worker","type":"sidecar"},{"name":"granted","type":"sidecar","env_secrets":{"TOKEN":"secret:TOKEN"}}]`),
			}, sidecarReloadSignals: map[string]string{"worker": "SIGHUP", "granted": "SIGHUP"}}}
			manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", "dep-1", "app-1", "acct-1")
			receiver := &runtimeConfigReceiver{ctx: context.Background(), mgr: manager, store: store}
			req := runtimeConfigRequest{Kind: "secret_generation_start", WorkloadName: workload, Generation: strings.Repeat("a", 32), PreviousGeneration: strings.Repeat("b", 32)}
			got := sendRuntimeConfigTestRequest(t, receiver, req)
			if !got.Accepted || got.Generation != req.Generation || len(store.processes) != 1 {
				t.Fatalf("registration: %+v", got)
			}
			p := store.processes[0]
			if p.AccountID != "acct-1" || p.AppID != "app-1" || p.InstanceID != "instance-1" || p.WorkloadName != workload || p.PreviousGeneration != req.PreviousGeneration {
				t.Fatalf("crossed identity: %+v", p)
			}
			store.err = state.ErrConflict
			got = sendRuntimeConfigTestRequest(t, receiver, req)
			if got.Accepted || got.Error != "secret_generation_stale" {
				t.Fatalf("stale generation: %+v", got)
			}
			store.err = nil
			req.Kind, req.PreviousGeneration = "secret_generation_retire", ""
			got = sendRuntimeConfigTestRequest(t, receiver, req)
			if !got.Accepted || !store.retire {
				t.Fatalf("retirement: %+v", got)
			}
		})
	}
}

func TestRuntimeSecretProcessGenerationClosedRequest(t *testing.T) {
	valid := runtimeConfigRequest{Kind: "secret_generation_start", Generation: strings.Repeat("a", 32)}
	for _, change := range []func(*runtimeConfigRequest){
		func(r *runtimeConfigRequest) { r.Generation = "short" }, func(r *runtimeConfigRequest) { r.Generation = strings.Repeat("A", 32) },
		func(r *runtimeConfigRequest) { r.Scope = "default" }, func(r *runtimeConfigRequest) { r.Revision = strings.Repeat("a", 64) },
		func(r *runtimeConfigRequest) { r.WorkloadName = "../other" }, func(r *runtimeConfigRequest) { r.ApplicationAck = "applied" },
		func(r *runtimeConfigRequest) {
			r.Kind = "secret_generation_retire"
			r.PreviousGeneration = r.Generation
		},
	} {
		req := valid
		change(&req)
		receiver := &runtimeConfigReceiver{ctx: context.Background()}
		got := sendRuntimeConfigTestRequest(t, receiver, req)
		if got.Error != "invalid_request" || got.Accepted {
			t.Fatalf("accepted malformed request: %+v %+v", req, got)
		}
	}
}

func TestRuntimeSecretProcessGenerationRequiresReloadGrant(t *testing.T) {
	for _, workload := range []string{"", "worker", "unknown"} {
		t.Run(workload, func(t *testing.T) {
			store := &runtimeSecretProcessStoreStub{runtimeSecretsStoreStub: runtimeSecretsStoreStub{deployment: state.Deployment{ID: "dep-1", AppID: "app-1", Sidecars: json.RawMessage(`[{"name":"worker","type":"sidecar"}]`)}}}
			manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", "dep-1", "app-1", "acct-1")
			receiver := &runtimeConfigReceiver{ctx: context.Background(), mgr: manager, store: store}
			got := sendRuntimeConfigTestRequest(t, receiver, runtimeConfigRequest{Kind: "secret_generation_start", WorkloadName: workload, Generation: strings.Repeat("a", 32)})
			if got.Accepted || len(store.processes) != 0 {
				t.Fatal("registered ungranted workload")
			}
		})
	}
}
