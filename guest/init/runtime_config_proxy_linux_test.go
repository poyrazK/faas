// adr: 045
// issue: 1278
package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestMetadataSecretReloadAckHandlerSendsOnlyClosedMetadata(t *testing.T) {
	previous := dialRuntimeConfigHost
	t.Cleanup(func() { dialRuntimeConfigHost = previous })
	revision := strings.Repeat("b", 64)
	dialRuntimeConfigHost = func() (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			body, err := readRuntimeConfigFrame(server)
			if err != nil {
				return
			}
			var request runtimeConfigRequest
			if json.Unmarshal(body, &request) != nil || request.Kind != "secret_reload_ack" || request.Revision != revision ||
				request.ApplicationAck != "applied" || request.ApplicationAckErrorCode != "" {
				return
			}
			_ = writeRuntimeConfigFrame(server, []byte(`{"accepted":true,"revision":"`+revision+`"}`))
		}()
		return client, nil
	}
	req := httptest.NewRequest(http.MethodPost, metadataSecretReloadAckEndpoint,
		strings.NewReader(`{"revision":"`+revision+`","status":"applied"}`))
	rec := httptest.NewRecorder()
	metadataSecretReloadAckHandler(rec, req)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"accepted":true`) {
		t.Fatalf("ack response = %d %s, want accepted", rec.Code, rec.Body.String())
	}
}

func TestMetadataSecretReloadAckHandlerRejectsInvalidRequest(t *testing.T) {
	for _, body := range []string{
		`{"revision":"short","status":"applied"}`,
		`{"revision":"` + strings.Repeat("a", 64) + `","status":"applied","value":"secret"}`,
		`{"revision":"` + strings.Repeat("a", 64) + `","status":"failed","error":"database password"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, metadataSecretReloadAckEndpoint, strings.NewReader(body))
		rec := httptest.NewRecorder()
		metadataSecretReloadAckHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("request %s returned %d, want 400", body, rec.Code)
		}
	}
}

func TestStampRuntimeConfigEnvIsPlatformOwned(t *testing.T) {
	got := StampRuntimeConfigEnv([]string{"FAAS_METADATA_ENV_ENDPOINT=https://attacker"})
	if got[len(got)-1] != "FAAS_METADATA_ENV_ENDPOINT="+metadataEnvEndpoint {
		t.Fatalf("endpoint stamp = %q", got[len(got)-1])
	}
}
