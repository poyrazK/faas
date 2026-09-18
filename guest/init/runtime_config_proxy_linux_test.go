// adr: 045
// issue: 1278
package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetadataEnvHandlerReturnsLiveConfig(t *testing.T) {
	previous := dialRuntimeConfigHost
	t.Cleanup(func() { dialRuntimeConfigHost = previous })
	dialRuntimeConfigHost = func() (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			body, err := readRuntimeConfigFrame(server)
			if err != nil {
				return
			}
			var req runtimeConfigRequest
			if err := json.Unmarshal(body, &req); err != nil || req.Scope != "default" {
				return
			}
			_ = writeRuntimeConfigFrame(server, []byte(`{"env":{"FEATURE_FLAG":"on"},"revision":"2026-09-18T20:00:00Z"}`))
		}()
		return client, nil
	}

	req := httptest.NewRequest(http.MethodGet, metadataEnvEndpoint, nil)
	rec := httptest.NewRecorder()
	metadataEnvHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got runtimeConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Env["FEATURE_FLAG"] != "on" || got.Revision == "" {
		t.Fatalf("response = %+v, want live env and revision", got)
	}
}

func TestMetadataEnvHandlerRejectsNonDefaultScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, metadataEnvEndpoint+"?scope=staging", nil)
	rec := httptest.NewRecorder()
	metadataEnvHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestStampRuntimeConfigEnvIsPlatformOwned(t *testing.T) {
	got := StampRuntimeConfigEnv([]string{"FAAS_METADATA_ENV_ENDPOINT=https://attacker"})
	if got[len(got)-1] != "FAAS_METADATA_ENV_ENDPOINT="+metadataEnvEndpoint {
		t.Fatalf("endpoint stamp = %q", got[len(got)-1])
	}
}
