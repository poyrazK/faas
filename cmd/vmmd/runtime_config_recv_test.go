// adr: 045
// adr: 590
// issue: 1278
package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/secretbox"

	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeConfigStoreStub struct {
	snapshot state.RuntimeAppEnvSnapshot
	err      error
}

func (s runtimeConfigStoreStub) RuntimeAppEnvForDeployment(context.Context, string, string, string) (state.RuntimeAppEnvSnapshot, error) {
	return s.snapshot, s.err
}

type runtimeSecretsStoreStub struct {
	runtimeConfigStoreStub
	deployment           state.Deployment
	secretRows           []state.AppSecret
	sidecarReloadSignals map[string]string
	reloadResults        []state.AppSecretRuntimeReloadResult
	ackResults           []state.AppSecretRuntimeReloadAckResult
}

func (s runtimeSecretsStoreStub) RuntimeAppValuesForDeployment(_ context.Context, accountID, appID, id string) (state.RuntimeAppValuesSnapshot, error) {
	if id != s.deployment.ID || appID != s.deployment.AppID {
		return state.RuntimeAppValuesSnapshot{}, state.ErrNotFound
	}
	scope := s.deployment.Scope
	if scope == "" {
		scope = "default"
	}
	return state.RuntimeAppValuesSnapshot{RuntimeAppEnvSnapshot: state.RuntimeAppEnvSnapshot{
		AccountID: accountID, AppID: appID, DeploymentID: id, Scope: scope, EnvironmentID: "environment-" + scope,
	}, Secrets: s.secretRows, SecretGrants: state.RuntimeAppSecretGrants{
		OverrideEnvSecrets: s.deployment.OverrideEnvSecrets, Sidecars: s.deployment.Sidecars,
		ReloadSignal: s.deployment.SecretReloadSignal, SidecarReloadSignals: s.sidecarReloadSignals,
	}}, nil
}

func (s *runtimeSecretsStoreStub) RecordAppSecretRuntimeReload(_ context.Context, result state.AppSecretRuntimeReloadResult) (int, error) {
	s.reloadResults = append(s.reloadResults, result)
	return len(result.Candidates), nil
}

func (s *runtimeSecretsStoreStub) RecordAppSecretRuntimeReloadAck(_ context.Context, result state.AppSecretRuntimeReloadAckResult) (int, error) {
	s.ackResults = append(s.ackResults, result)
	return len(result.Candidates), nil
}

func runtimeSecretTestRevision(t *testing.T, store runtimeSecretsStoreStub, workloadName string) string {
	t.Helper()
	selected, err := selectRuntimeSecretRowsForWorkload(t.Context(), store, store.deployment.ID, store.deployment.AppID, "acct-1", workloadName)
	if err != nil {
		t.Fatal(err)
	}
	return selected.Revision
}

func TestRuntimeSecretReloadStatusIsVersionFenced(t *testing.T) {
	const revisionKey = "DATABASE_URL"
	store := &runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "prod", Sidecars: json.RawMessage(`[]`),
	}, secretRows: []state.AppSecret{{
		AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: revisionKey, DeliveryVersion: 3,
	}}}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", "dep-1", "app-1", "acct-1")
	receiver := &runtimeConfigReceiver{ctx: context.Background(), mgr: manager, store: store}
	revision := runtimeSecretTestRevision(t, *store, "")
	request := runtimeConfigRequest{Kind: "secret_reload_status", Revision: revision, Projection: "updated", Signal: "sent"}
	response := sendRuntimeConfigTestRequest(t, receiver, request)
	if !response.Accepted || response.Error != "" || len(store.reloadResults) != 1 {
		t.Fatalf("report response = %+v, records = %d", response, len(store.reloadResults))
	}
	result := store.reloadResults[0]
	if result.Candidates[0].Key != revisionKey || result.Candidates[0].Version != 3 || result.InstanceID != "instance-1" {
		t.Fatalf("recorded runtime reload = %+v", result)
	}

	store.secretRows[0].DeliveryVersion++
	response = sendRuntimeConfigTestRequest(t, receiver, request)
	if response.Accepted || response.Error != "secret_reload_stale" || len(store.reloadResults) != 1 {
		t.Fatalf("stale report response = %+v, records = %d", response, len(store.reloadResults))
	}
}

func TestRuntimeSecretApplicationAckIsVersionFenced(t *testing.T) {
	store := &runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "prod", Sidecars: json.RawMessage(`[]`),
	}, secretRows: []state.AppSecret{{
		AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "DATABASE_URL", DeliveryVersion: 3,
	}}}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", "dep-1", "app-1", "acct-1")
	receiver := &runtimeConfigReceiver{ctx: context.Background(), mgr: manager, store: store}
	revision := runtimeSecretTestRevision(t, *store, "")
	request := runtimeConfigRequest{Kind: "secret_reload_ack", Revision: revision, ApplicationAck: "applied", Generation: strings.Repeat("a", 32)}
	response := sendRuntimeConfigTestRequest(t, receiver, request)
	if !response.Accepted || response.Error != "" || len(store.ackResults) != 1 {
		t.Fatalf("ack response = %+v, records = %d", response, len(store.ackResults))
	}
	result := store.ackResults[0]
	if result.Candidates[0].Version != 3 || result.InstanceID != "instance-1" || result.Status != state.SecretApplicationReloadAckApplied || result.Generation != request.Generation {
		t.Fatalf("recorded application ack = %+v", result)
	}

	store.secretRows[0].DeliveryVersion++
	response = sendRuntimeConfigTestRequest(t, receiver, request)
	if response.Accepted || response.Error != "secret_reload_stale" || len(store.ackResults) != 1 {
		t.Fatalf("stale app ack response = %+v, records = %d", response, len(store.ackResults))
	}
}

func sendRuntimeConfigTestRequest(t *testing.T, receiver *runtimeConfigReceiver, request runtimeConfigRequest) runtimeConfigResponse {
	return sendRuntimeConfigTestRequestForInstance(t, receiver, "instance-1", request)
}

func sendRuntimeConfigTestRequestForInstance(t *testing.T, receiver *runtimeConfigReceiver, instance string, request runtimeConfigRequest) runtimeConfigResponse {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := receiver.handleGuestStream(instance, server)
		_ = server.Close()
		done <- err
	}()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRuntimeConfigFrame(client, body); err != nil {
		t.Fatal(err)
	}
	frame, err := readRuntimeConfigFrameLimit(client, runtimeConfigMaxFrame)
	_ = client.Close()
	if err != nil {
		t.Fatal(err)
	}
	hostErr := <-done
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		t.Fatal(err)
	}
	if hostErr != nil && response.Error == "" {
		t.Fatal(hostErr)
	}
	return response
}

func TestLoadRuntimeSecretsHonorsDeploymentScopeAndAllowlist(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(key, value string) []byte {
		t.Helper()
		ciphertext, sealErr := secretbox.Seal(identity.Recipient(), secretbox.Envelope{key: value})
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		return ciphertext
	}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	manager.SetHostIdentity(identity)
	store := runtimeSecretsStoreStub{
		deployment: state.Deployment{
			ID: "dep-1", AppID: "app-1", Scope: "prod",
			OverrideEnvSecrets: json.RawMessage(`{"DB_URL":"secret:DB_URL"}`),
			Sidecars:           json.RawMessage(`[]`),
		},
		secretRows: []state.AppSecret{
			{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "DB_URL", Ciphertext: seal("DB_URL", "postgres://new"), DeliveryVersion: 5},
			{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "UNUSED", Ciphertext: seal("UNUSED", "do-not-send"), DeliveryVersion: 12},
		},
	}
	response, err := loadRuntimeSecrets(context.Background(), store, manager, "dep-1", "app-1", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Secrets == nil || len(*response.Secrets) != 1 || (*response.Secrets)["DB_URL"] != "postgres://new" {
		t.Fatalf("secrets response = %#v, want only the allowed DB_URL", response.Secrets)
	}
	firstRevision := response.Revision
	unchanged, err := loadRuntimeSecretsIfChanged(context.Background(), store, manager, "dep-1", "app-1", "acct-1", firstRevision)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.Unchanged || unchanged.Secrets != nil || unchanged.Revision != firstRevision {
		t.Fatalf("unchanged response = %+v, want revision-only response", unchanged)
	}
	store.secretRows[1].DeliveryVersion++
	response, err = loadRuntimeSecrets(context.Background(), store, manager, "dep-1", "app-1", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Revision != firstRevision {
		t.Fatalf("unselected secret changed revision: %q -> %q", firstRevision, response.Revision)
	}
	store.secretRows[0].DeliveryVersion++
	response, err = loadRuntimeSecrets(context.Background(), store, manager, "dep-1", "app-1", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Revision == firstRevision {
		t.Fatal("selected secret version did not change revision")
	}
}

func TestLoadRuntimeSecretsMainWithSidecarsHasNoImplicitGrants(t *testing.T) {
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	store := runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "default", Sidecars: json.RawMessage(`[{"name":"metrics"}]`),
	}}
	store.secretRows = []state.AppSecret{{AccountID: "acct-1", AppID: "app-1", Scope: "default", Key: "SIDE_TOKEN", Ciphertext: []byte("must-not-unseal"), DeliveryVersion: 1}}
	for _, grants := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`null`)} {
		store.deployment.OverrideEnvSecrets = grants
		response, err := loadRuntimeSecrets(context.Background(), store, manager, "dep-1", "app-1", "acct-1")
		if err != nil || response.Secrets == nil || len(*response.Secrets) != 0 {
			t.Fatalf("main without grants: %+v %v", response, err)
		}
	}
}

func TestLoadRuntimeSecretsForWorkloadUsesOnlySidecarGrant(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(key, value string) []byte {
		t.Helper()
		ciphertext, sealErr := secretbox.Seal(identity.Recipient(), secretbox.Envelope{key: value})
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		return ciphertext
	}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	manager.SetHostIdentity(identity)
	store := runtimeSecretsStoreStub{
		deployment: state.Deployment{
			ID: "dep-1", AppID: "app-1", Scope: "prod",
			OverrideEnvSecrets: json.RawMessage(`{"MAIN_TOKEN":"secret:MAIN_TOKEN"}`),
			Sidecars:           json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"SIDE_TOKEN":"secret:SIDE_TOKEN"}},{"name":"other","type":"sidecar","env_secrets":{"OTHER_TOKEN":"secret:OTHER_TOKEN"}}]`),
		},
		secretRows: []state.AppSecret{
			{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "SIDE_TOKEN", Ciphertext: seal("SIDE_TOKEN", "sidecar-value"), DeliveryVersion: 7},
			{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "OTHER_TOKEN", Ciphertext: seal("OTHER_TOKEN", "other-value"), DeliveryVersion: 3},
			{AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "MAIN_TOKEN", Ciphertext: seal("MAIN_TOKEN", "main-value"), DeliveryVersion: 9},
		},
	}

	response, err := loadRuntimeSecretsForWorkloadIfChanged(context.Background(), store, manager, "dep-1", "app-1", "acct-1", "worker", "")
	if err != nil {
		t.Fatal(err)
	}
	if response.Secrets == nil || len(*response.Secrets) != 1 || (*response.Secrets)["SIDE_TOKEN"] != "sidecar-value" {
		t.Fatalf("worker secrets = %#v, want only its SIDE_TOKEN grant", response.Secrets)
	}
	if _, err := loadRuntimeSecretsForWorkloadIfChanged(context.Background(), store, manager, "dep-1", "app-1", "acct-1", "other", ""); err != nil {
		t.Fatalf("other authorized sidecar should load its grant: %v", err)
	}
	if _, err := loadRuntimeSecretsForWorkloadIfChanged(context.Background(), store, manager, "dep-1", "app-1", "acct-1", "unknown", ""); err == nil {
		t.Fatal("undeclared workload was allowed to request secrets")
	}
	main, err := loadRuntimeSecretsForWorkloadIfChanged(context.Background(), store, manager, "dep-1", "app-1", "acct-1", "", "")
	if err != nil || main.Secrets == nil || len(*main.Secrets) != 1 || (*main.Secrets)["MAIN_TOKEN"] != "main-value" {
		t.Fatalf("main secrets = %+v %v, want only MAIN_TOKEN", main, err)
	}
}

func TestRuntimeSidecarSecretDeletionProducesEmptyProjection(t *testing.T) {
	store := runtimeSecretsStoreStub{
		deployment: state.Deployment{
			ID: "dep-1", AppID: "app-1", Scope: "prod",
			Sidecars: json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"TOKEN":"secret:TOKEN"}}]`),
		},
		secretRows: []state.AppSecret{{
			AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "TOKEN",
			Ciphertext: []byte("sealed-before-delete"), DeliveryVersion: 1,
		}},
		sidecarReloadSignals: map[string]string{"worker": "SIGHUP"},
	}
	before, err := selectRuntimeSecretRowsForWorkload(context.Background(), store, "dep-1", "app-1", "acct-1", "worker")
	if err != nil {
		t.Fatalf("select before delete: %v", err)
	}

	store.secretRows = nil
	after, err := selectRuntimeSecretRowsForWorkload(context.Background(), store, "dep-1", "app-1", "acct-1", "worker")
	if err != nil {
		t.Fatalf("select after delete: %v", err)
	}
	if len(after.Rows) != 0 || len(after.Entries) != 0 {
		t.Fatalf("deleted secret remained in projection: rows=%+v entries=%+v", after.Rows, after.Entries)
	}
	if after.Revision == before.Revision {
		t.Fatalf("revision did not change after deletion: %q", after.Revision)
	}

	store.secretRows = []state.AppSecret{{
		AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "TOKEN",
		Ciphertext: []byte("sealed-after-recreate"), DeliveryVersion: 1,
	}}
	recreated, err := selectRuntimeSecretRowsForWorkload(context.Background(), store, "dep-1", "app-1", "acct-1", "worker")
	if err != nil {
		t.Fatalf("select after recreate: %v", err)
	}
	if recreated.Revision == before.Revision || recreated.Revision == after.Revision {
		t.Fatalf("delete-and-recreate revision was not fenced: before=%q deleted=%q recreated=%q", before.Revision, after.Revision, recreated.Revision)
	}
}

func TestRuntimeSidecarSecretDeletionRequiresReloadOptIn(t *testing.T) {
	store := runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "prod",
		Sidecars: json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"TOKEN":"secret:TOKEN"}}]`),
	}}
	_, err := selectRuntimeSecretRowsForWorkload(context.Background(), store, "dep-1", "app-1", "acct-1", "worker")
	if err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("missing key without reload opt-in error = %v, want a missing TOKEN error", err)
	}
}

func TestRuntimeMainSecretDeletionProducesEmptyProjectionWhenOptedIn(t *testing.T) {
	store := runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "prod",
		OverrideEnvSecrets: json.RawMessage(`{"TOKEN":"secret:TOKEN"}`),
		SecretReloadSignal: "SIGHUP",
		Sidecars:           json.RawMessage(`[]`),
	}}
	selection, err := selectRuntimeSecretRowsForWorkload(context.Background(), store, "dep-1", "app-1", "acct-1", "")
	if err != nil {
		t.Fatalf("select revoked main workload projection: %v", err)
	}
	if len(selection.Entries) != 0 {
		t.Fatalf("main workload projection retained revoked key: %+v", selection.Entries)
	}
}

func TestRuntimeSecretReloadStatusAndAckKeepSidecarIdentity(t *testing.T) {
	store := &runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "prod",
		Sidecars: json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"TOKEN":"secret:TOKEN"}}]`),
	}, secretRows: []state.AppSecret{{
		AccountID: "acct-1", AppID: "app-1", Scope: "prod", Key: "TOKEN", DeliveryVersion: 4,
	}}}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", "dep-1", "app-1", "acct-1")
	receiver := &runtimeConfigReceiver{ctx: context.Background(), mgr: manager, store: store}
	revision := runtimeSecretTestRevision(t, *store, "worker")
	status := sendRuntimeConfigTestRequest(t, receiver, runtimeConfigRequest{
		Kind: "secret_reload_status", WorkloadName: "worker", Revision: revision, Projection: "updated", Signal: "sent",
	})
	if !status.Accepted || len(store.reloadResults) != 1 || store.reloadResults[0].WorkloadName != "worker" {
		t.Fatalf("sidecar status = %+v, recorded=%+v", status, store.reloadResults)
	}
	ack := sendRuntimeConfigTestRequest(t, receiver, runtimeConfigRequest{
		Kind: "secret_reload_ack", WorkloadName: "worker", Revision: revision, ApplicationAck: "applied",
	})
	if !ack.Accepted || len(store.ackResults) != 1 || store.ackResults[0].WorkloadName != "worker" {
		t.Fatalf("sidecar ack = %+v, recorded=%+v", ack, store.ackResults)
	}
}

func TestLoadRuntimeSecretsRepresentsEmptyPayload(t *testing.T) {
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	store := runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "default", Sidecars: json.RawMessage(`[]`),
	}}
	response, err := loadRuntimeSecrets(context.Background(), store, manager, "dep-1", "app-1", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Secrets == nil || len(*response.Secrets) != 0 {
		t.Fatalf("empty secret response = %#v, want a present empty map", response.Secrets)
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["secrets"]) != "{}" {
		t.Fatalf("empty secrets wire payload = %s, want {}", wire["secrets"])
	}
}

func TestLoadRuntimeSecretsFailsLoudForMissingAllowlistedKey(t *testing.T) {
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	store := runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "default",
		OverrideEnvSecrets: json.RawMessage(`{"DB_URL":"secret:DB_URL"}`),
		Sidecars:           json.RawMessage(`[]`),
	}}
	_, err := loadRuntimeSecrets(context.Background(), store, manager, "dep-1", "app-1", "acct-1")
	if err == nil || !strings.Contains(err.Error(), "DB_URL") {
		t.Fatalf("loadRuntimeSecrets error = %v, want missing DB_URL", err)
	}
}
