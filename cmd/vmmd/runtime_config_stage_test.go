// adr: 566
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeDeploymentEnvStore struct {
	byDeployment   map[string]state.RuntimeAppEnvSnapshot
	lastDeployment string
}

func (s *runtimeDeploymentEnvStore) RuntimeAppEnvForDeployment(_ context.Context, accountID, appID, deploymentID string) (state.RuntimeAppEnvSnapshot, error) {
	s.lastDeployment = deploymentID
	snapshot, ok := s.byDeployment[deploymentID]
	if !ok || snapshot.AccountID != accountID || snapshot.AppID != appID {
		return state.RuntimeAppEnvSnapshot{}, state.ErrNotFound
	}
	return snapshot, nil
}

func runtimeEnvTestSnapshot(deploymentID, scope, value string) state.RuntimeAppEnvSnapshot {
	return state.RuntimeAppEnvSnapshot{AccountID: "acct-1", AppID: "app-1", DeploymentID: deploymentID, Scope: scope,
		EnvironmentID: "environment-" + scope, Values: []state.AppEnv{{AccountID: "acct-1", AppID: "app-1", Scope: scope, Key: "MODE", Value: value}}}
}

func TestRuntimeEnvSelectsInstanceDeploymentAndRechecksOwnership(t *testing.T) {
	store := &runtimeDeploymentEnvStore{byDeployment: map[string]state.RuntimeAppEnvSnapshot{
		"prod-dep":  runtimeEnvTestSnapshot("prod-dep", "default", "production"),
		"stage-dep": runtimeEnvTestSnapshot("stage-dep", "stage", "staging"),
		"other-dep": runtimeEnvTestSnapshot("other-dep", "other", "sibling"),
	}}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	for instance, deployment := range map[string]string{"prod": "prod-dep", "stage": "stage-dep", "other": "other-dep"} {
		manager.RegisterInstanceForTest(instance, deployment, "app-1", "acct-1")
	}
	receiver := &runtimeConfigReceiver{ctx: t.Context(), mgr: manager, store: store}
	for _, call := range []struct{ instance, deployment, value string }{
		{"prod", "prod-dep", "production"}, {"stage", "stage-dep", "staging"}, {"other", "other-dep", "sibling"}, {"prod", "prod-dep", "production"},
	} {
		response := sendRuntimeConfigTestRequestForInstance(t, receiver, call.instance, runtimeConfigRequest{Kind: "env", Scope: "default"})
		if response.Error != "" || response.Env["MODE"] != call.value || store.lastDeployment != call.deployment {
			t.Fatalf("%s read another deployment's values: %+v lookup=%s", call.instance, response, store.lastDeployment)
		}
	}
	store.byDeployment["stage-dep"] = runtimeEnvTestSnapshot("stage-dep", "stage", "edited")
	response := sendRuntimeConfigTestRequestForInstance(t, receiver, "stage", runtimeConfigRequest{Kind: "env"})
	if response.Error != "" || response.Env["MODE"] != "edited" {
		t.Fatalf("stage edit not visible: %+v", response)
	}
	delete(store.byDeployment, "stage-dep")
	for _, call := range []struct{ instance, scope, code string }{
		{"stage", "", "config_unavailable"}, {"stage", "production", "unsupported_scope"}, {"missing", "", "instance_not_found"},
	} {
		response = sendRuntimeConfigTestRequestForInstance(t, receiver, call.instance, runtimeConfigRequest{Kind: "env", Scope: call.scope})
		if response.Error != call.code || len(response.Env) != 0 {
			t.Fatalf("unowned read: %+v, want %s", response, call.code)
		}
	}
}

func TestRuntimeEnvRejectsMismatchedSnapshotAndValues(t *testing.T) {
	for _, fault := range []struct {
		name   string
		change func(*state.RuntimeAppEnvSnapshot)
	}{
		{"account", func(s *state.RuntimeAppEnvSnapshot) { s.AccountID = "other" }},
		{"app", func(s *state.RuntimeAppEnvSnapshot) { s.AppID = "other" }},
		{"deployment", func(s *state.RuntimeAppEnvSnapshot) { s.DeploymentID = "other" }},
		{"scope", func(s *state.RuntimeAppEnvSnapshot) { s.Scope = "../production" }},
		{"row_account", func(s *state.RuntimeAppEnvSnapshot) { s.Values[0].AccountID = "other" }},
		{"row_app", func(s *state.RuntimeAppEnvSnapshot) { s.Values[0].AppID = "other" }},
		{"row_scope", func(s *state.RuntimeAppEnvSnapshot) { s.Values[0].Scope = "default" }},
		{"invalid_key", func(s *state.RuntimeAppEnvSnapshot) { s.Values[0].Key = "not a key" }},
		{"duplicate_key", func(s *state.RuntimeAppEnvSnapshot) { s.Values = append(s.Values, s.Values[0]) }},
	} {
		t.Run(fault.name, func(t *testing.T) {
			snapshot := runtimeEnvTestSnapshot("stage-dep", "stage", "private")
			fault.change(&snapshot)
			response, err := loadRuntimeConfig(t.Context(), runtimeConfigStoreStub{snapshot: snapshot}, "stage-dep", "app-1", "acct-1")
			if err == nil || len(response.Env) != 0 {
				t.Fatalf("mismatched store result leaked: %+v %v", response, err)
			}
		})
	}
	if _, err := loadRuntimeConfig(t.Context(), runtimeConfigStoreStub{err: state.ErrNotFound}, "stage-dep", "app-1", "acct-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("store failure: %v", err)
	}
}

func TestRuntimeEnvRevisionDetectsDeletionAndScopeLifetime(t *testing.T) {
	snapshot := runtimeEnvTestSnapshot("stage-dep", "stage", "private")
	snapshot.Values[0].UpdatedAt = time.Now()
	snapshot.Values = append(snapshot.Values, state.AppEnv{AccountID: "acct-1", AppID: "app-1", Scope: "stage", Key: "OLDER", Value: "deleted", UpdatedAt: time.Now().Add(-time.Hour)})
	read := func() runtimeConfigResponse {
		t.Helper()
		response, err := loadRuntimeConfig(t.Context(), runtimeConfigStoreStub{snapshot: snapshot}, snapshot.DeploymentID, "app-1", "acct-1")
		if err != nil || len(response.Revision) != 64 || response.Env == nil {
			t.Fatalf("read: %+v %v", response, err)
		}
		return response
	}
	original := read()
	snapshot.Values[0], snapshot.Values[1] = snapshot.Values[1], snapshot.Values[0]
	if read().Revision != original.Revision {
		t.Fatal("row order changed revision")
	}
	snapshot.Values = snapshot.Values[1:]
	withoutOlder := read()
	if withoutOlder.Revision == original.Revision {
		t.Fatal("deleting an older key did not change revision")
	}
	snapshot.EnvironmentID = "replacement-environment"
	if read().Revision == withoutOlder.Revision {
		t.Fatal("environment lifetime did not change revision")
	}
	snapshot.Values = nil
	if len(read().Env) != 0 {
		t.Fatal("empty scope did not produce an empty projection")
	}
}
