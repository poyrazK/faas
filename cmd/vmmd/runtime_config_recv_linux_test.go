// adr: 045
// issue: 1278
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/secretbox"

	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeConfigStoreStub struct {
	rows []state.AppEnv
}

func (s runtimeConfigStoreStub) ListAppEnv(context.Context, string, string) ([]state.AppEnv, error) {
	return s.rows, nil
}

type runtimeSecretsStoreStub struct {
	runtimeConfigStoreStub
	deployment    state.Deployment
	secretRows    []state.AppSecret
	reloadResults []state.AppSecretRuntimeReloadResult
	ackResults    []state.AppSecretRuntimeReloadAckResult
}

func (s runtimeSecretsStoreStub) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	if id != s.deployment.ID {
		return state.Deployment{}, state.ErrNotFound
	}
	return s.deployment, nil
}

func (s runtimeSecretsStoreStub) ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error) {
	return s.secretRows, nil
}

func (s *runtimeSecretsStoreStub) RecordAppSecretRuntimeReload(_ context.Context, result state.AppSecretRuntimeReloadResult) (int, error) {
	s.reloadResults = append(s.reloadResults, result)
	return len(result.Candidates), nil
}

func (s *runtimeSecretsStoreStub) RecordAppSecretRuntimeReloadAck(_ context.Context, result state.AppSecretRuntimeReloadAckResult) (int, error) {
	s.ackResults = append(s.ackResults, result)
	return len(result.Candidates), nil
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
	revision := runtimeSecretRevision("prod", store.secretRows)
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
	revision := runtimeSecretRevision("prod", store.secretRows)
	request := runtimeConfigRequest{Kind: "secret_reload_ack", Revision: revision, ApplicationAck: "applied"}
	response := sendRuntimeConfigTestRequest(t, receiver, request)
	if !response.Accepted || response.Error != "" || len(store.ackResults) != 1 {
		t.Fatalf("ack response = %+v, records = %d", response, len(store.ackResults))
	}
	result := store.ackResults[0]
	if result.Candidates[0].Version != 3 || result.InstanceID != "instance-1" || result.Status != state.SecretApplicationReloadAckApplied {
		t.Fatalf("recorded application ack = %+v", result)
	}

	store.secretRows[0].DeliveryVersion++
	response = sendRuntimeConfigTestRequest(t, receiver, request)
	if response.Accepted || response.Error != "secret_reload_stale" || len(store.ackResults) != 1 {
		t.Fatalf("stale app ack response = %+v, records = %d", response, len(store.ackResults))
	}
}

func sendRuntimeConfigTestRequest(t *testing.T, receiver *runtimeConfigReceiver, request runtimeConfigRequest) runtimeConfigResponse {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := receiver.handleGuestStream("instance-1", server)
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
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestLoadRuntimeConfigReturnsDefaultScopeAndRevision(t *testing.T) {
	updated := time.Date(2026, time.September, 18, 20, 0, 0, 123, time.UTC)
	response, err := loadRuntimeConfig(context.Background(), runtimeConfigStoreStub{rows: []state.AppEnv{
		{Scope: "default", Key: "FEATURE_FLAG", Value: "on", UpdatedAt: updated},
		{Scope: "staging", Key: "FEATURE_FLAG", Value: "off", UpdatedAt: updated.Add(time.Hour)},
	}}, "acct-1", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Env["FEATURE_FLAG"] != "on" {
		t.Fatalf("env = %+v, want default scope value", response.Env)
	}
	if response.Revision != updated.Format(time.RFC3339Nano) {
		t.Fatalf("revision = %q, want %q", response.Revision, updated.Format(time.RFC3339Nano))
	}
}

func TestLoadRuntimeConfigReturnsEmptyMapWithoutRows(t *testing.T) {
	response, err := loadRuntimeConfig(context.Background(), runtimeConfigStoreStub{}, "acct-1", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Env == nil || len(response.Env) != 0 {
		t.Fatalf("env = %#v, want an empty map", response.Env)
	}
	if response.Revision != "" {
		t.Fatalf("revision = %q, want empty", response.Revision)
	}
}

func TestRuntimeConfigCacheExpiresAndCopies(t *testing.T) {
	cache := newRuntimeConfigCache()
	loaded := time.Date(2026, time.September, 18, 20, 0, 0, 0, time.UTC)
	cache.put("acct-1", "app-1", runtimeConfigResponse{Env: map[string]string{"FEATURE_X": "on"}, Revision: "r1"}, loaded)

	got, ok := cache.get("acct-1", "app-1", loaded.Add(time.Second))
	if !ok || got.Env["FEATURE_X"] != "on" {
		t.Fatalf("cache get = (%+v, %t)", got, ok)
	}
	got.Env["FEATURE_X"] = "mutated"
	again, ok := cache.get("acct-1", "app-1", loaded.Add(2*time.Second))
	if !ok || again.Env["FEATURE_X"] != "on" {
		t.Fatalf("cache returned aliased response = (%+v, %t)", again, ok)
	}
	if _, ok := cache.get("acct-1", "app-1", loaded.Add(runtimeConfigCacheTTL)); ok {
		t.Fatal("expired cache entry remained available")
	}
}

func TestRuntimeConfigCacheInvalidatesByIdentity(t *testing.T) {
	cache := newRuntimeConfigCache()
	now := time.Now()
	cache.put("acct-1", "app-1", runtimeConfigResponse{Revision: "r1"}, now)
	cache.put("acct-2", "app-1", runtimeConfigResponse{Revision: "r2"}, now)
	cache.put("acct-1", "app-2", runtimeConfigResponse{Revision: "r3"}, now)

	cache.invalidate("acct-1", "app-1")
	if _, ok := cache.get("acct-1", "app-1", now); ok {
		t.Fatal("account-specific invalidation did not remove entry")
	}
	if _, ok := cache.get("acct-2", "app-1", now); !ok {
		t.Fatal("account-specific invalidation removed another account")
	}
	cache.invalidate("", "app-1")
	if _, ok := cache.get("acct-2", "app-1", now); ok {
		t.Fatal("app-wide invalidation did not remove entry")
	}
	if _, ok := cache.get("acct-1", "app-2", now); !ok {
		t.Fatal("app-wide invalidation removed another app")
	}
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

func TestLoadRuntimeSecretsRejectsSidecarDeployment(t *testing.T) {
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	store := runtimeSecretsStoreStub{deployment: state.Deployment{
		ID: "dep-1", AppID: "app-1", Scope: "default", Sidecars: json.RawMessage(`[{"name":"metrics"}]`),
	}}
	_, err := loadRuntimeSecrets(context.Background(), store, manager, "dep-1", "app-1", "acct-1")
	if err == nil || !errors.Is(err, errRuntimeSecretSidecarsUnsupported) {
		t.Fatalf("loadRuntimeSecrets error = %v, want sidecars unsupported", err)
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
