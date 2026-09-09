package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeConfigRollback_HotRevision(t *testing.T) {
	e := newObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")

	first := e.doAdmin(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": false, "reason": "establish rollback baseline",
	}, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("initial config update status = %d, want 200: %s", first.Code, first.Body.String())
	}
	second := e.doAdmin(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": true, "reason": "enable placement for test",
		"expected_version": int64(1),
	}, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second config update status = %d, want 200: %s", second.Code, second.Body.String())
	}

	rollback := e.doAdmin(t, http.MethodPost, "/v1/admin/config/data_placement_enabled/rollback", map[string]any{
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
	first := e.doAdmin(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": false, "reason": "establish rollback baseline",
	}, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("initial config update status = %d, want 200: %s", first.Code, first.Body.String())
	}
	second := e.doAdmin(t, http.MethodPatch, "/v1/admin/config/data_placement_enabled", map[string]any{
		"value": true, "reason": "enable placement for stale test",
		"expected_version": int64(1),
	}, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second config update status = %d, want 200: %s", second.Code, second.Body.String())
	}

	rollback := e.doAdmin(t, http.MethodPost, "/v1/admin/config/data_placement_enabled/rollback", map[string]any{
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

func TestRuntimeConfigPatch_PropagatesTraceToAuditEvent(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	e := newObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	response := e.doAdmin(t, http.MethodPatch, "/v1/admin/config/hsts_enabled", map[string]any{
		"value": false, "reason": "incident mitigation",
	}, map[string]string{"X-Trace-Id": traceID})
	if response.Code != http.StatusOK {
		t.Fatalf("runtime config patch status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("X-Trace-Id"); got != traceID {
		t.Fatalf("response trace id = %q, want %q", got, traceID)
	}
	events, err := e.store.ListEvents(t.Context(), "", 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, event := range events {
		if event.Kind != "operator.runtime_config_changed" {
			continue
		}
		if event.TraceID == nil || *event.TraceID != traceID {
			t.Fatalf("runtime config audit trace id = %v, want %q", event.TraceID, traceID)
		}
		return
	}
	t.Fatal("operator.runtime_config_changed audit event not found")
}
