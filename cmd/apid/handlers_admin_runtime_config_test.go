package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeConfigRollback_HotRevision(t *testing.T) {
	e := newObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")

	first := e.do(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": false, "reason": "establish rollback baseline",
	}, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("initial config update status = %d, want 200: %s", first.Code, first.Body.String())
	}
	second := e.do(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": true, "reason": "enable placement for test",
		"expected_version": int64(1),
	}, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second config update status = %d, want 200: %s", second.Code, second.Body.String())
	}

	rollback := e.do(t, http.MethodPost, "/v1/admin/config/data_placement_enabled/rollback", map[string]any{
		"version":          1,
		"reason":           "restore the known-safe placement setting",
		"expected_version": int64(2),
	}, nil)
	if rollback.Code != http.StatusOK {
		t.Fatalf("rollback status = %d, want 200: %s", rollback.Code, rollback.Body.String())
	}
	var response runtimeConfigEntryResponse
	if err := json.Unmarshal(rollback.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode rollback response: %v", err)
	}
	if response.Version != 3 || response.Status != string(state.RuntimeConfigApplied) || string(response.EffectiveValue) != "false" {
		t.Fatalf("rollback response = %#v, want applied v3 with false effective value", response)
	}

	row, err := e.store.GetRuntimeConfig(t.Context(), runtimeConfigDataPlacement, state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		t.Fatalf("GetRuntimeConfig: %v", err)
	}
	if row.Version != 3 || row.Status != state.RuntimeConfigApplied || string(row.DesiredValue) != "false" || string(row.EffectiveValue) != "false" {
		t.Fatalf("stored rollback row = %#v, want applied v3 with false value", row)
	}
	revision, err := e.store.GetRuntimeConfigRevision(t.Context(), runtimeConfigDataPlacement, state.RuntimeConfigScopeGlobal, "", 3)
	if err != nil {
		t.Fatalf("GetRuntimeConfigRevision: %v", err)
	}
	if string(revision.OldValue) != "true" || string(revision.NewValue) != "false" {
		t.Fatalf("rollback revision = %#v, want true -> false", revision)
	}
}

func TestRuntimeConfigRollback_RejectsStaleExpectedVersion(t *testing.T) {
	e := newObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	first := e.do(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": false, "reason": "establish rollback baseline",
	}, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("initial config update status = %d, want 200: %s", first.Code, first.Body.String())
	}
	second := e.do(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": true, "reason": "enable placement for stale test",
		"expected_version": int64(1),
	}, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second config update status = %d, want 200: %s", second.Code, second.Body.String())
	}

	rollback := e.do(t, http.MethodPost, "/v1/admin/config/data_placement_enabled/rollback", map[string]any{
		"version":          1,
		"reason":           "stale operator request",
		"expected_version": int64(1),
	}, nil)
	if rollback.Code != http.StatusConflict {
		t.Fatalf("stale rollback status = %d, want 409: %s", rollback.Code, rollback.Body.String())
	}
	row, err := e.store.GetRuntimeConfig(t.Context(), runtimeConfigDataPlacement, state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		t.Fatalf("GetRuntimeConfig: %v", err)
	}
	if row.Version != 2 || string(row.DesiredValue) != "true" {
		t.Fatalf("stale rollback changed config = %#v, want unchanged v2 true", row)
	}
}

func TestRuntimeConfigListIncludesDaemonAcknowledgements(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	if _, err := e.store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{
		Key: runtimeConfigGatewayStreaming, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot,
	}); err != nil {
		t.Fatalf("seed runtime config: %v", err)
	}
	if err := e.store.AcknowledgeRuntimeConfig(t.Context(), state.RuntimeConfigAck{
		Key: runtimeConfigGatewayStreaming, Scope: state.RuntimeConfigScopeGlobal,
		Consumer: "gatewayd-internal", NodeID: "node-a", Version: 1,
		Status: state.RuntimeConfigAckApplied, EffectiveValue: json.RawMessage(`true`),
	}); err != nil {
		t.Fatalf("seed runtime config acknowledgement: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/admin/config", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list runtime config status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response runtimeConfigListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode runtime config list: %v", err)
	}
	for _, item := range response.Items {
		if item.Key != runtimeConfigGatewayStreaming {
			continue
		}
		if len(item.Acks) != 1 || item.Acks[0].Consumer != "gatewayd-internal" || item.Acks[0].NodeID != "node-a" || item.Acks[0].Status != string(state.RuntimeConfigAckApplied) {
			t.Fatalf("gateway streaming acknowledgements = %#v", item.Acks)
		}
		return
	}
	t.Fatalf("runtime config list did not include %q", runtimeConfigGatewayStreaming)
}
