package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectReleaseSecretStagingCLI(t *testing.T) {
	for _, scenario := range []string{"accepted", "missing_version", "wrong_request"} {
		t.Run(scenario, func(t *testing.T) {
			reqID := strings.Repeat("a", 32)
			id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/projects/project/release-secrets" {
					t.Error("unexpected staging path")
					w.WriteHeader(404)
					return
				}
				var request struct {
					RequestID string                       `json:"request_id"`
					Workloads map[string]map[string]string `json:"workloads"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.RequestID != reqID || request.Workloads["api"]["TOKEN"] != "private-cli-value" {
					t.Error("staging input mismatch")
				}
				returnedID := reqID
				if scenario == "wrong_request" {
					returnedID = strings.Repeat("c", 32)
				}
				versions := map[string]map[string]string{"api": {"TOKEN": id}}
				if scenario == "missing_version" {
					versions["api"] = map[string]string{}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"request_id": returnedID, "versions": versions})
			}))
			defer server.Close()
			var stderr bytes.Buffer
			original := osStderr
			osStderr = &stderr
			defer func() { osStderr = original }()
			raw, hash, err := stageProjectReleaseSecretConfig(t.Context(), NewClient(server.URL, "test"), "project", reqID, map[string]map[string]string{"api": {"TOKEN": "private-cli-value"}}, "")
			if scenario == "accepted" {
				if err != nil || len(hash) != 64 || !bytes.Contains(raw, []byte(id)) {
					t.Fatal("staging acknowledgement was not bound to configuration")
				}
			} else if err == nil {
				t.Fatal("invalid staging acknowledgement accepted")
			}
			if bytes.Contains(raw, []byte("private-cli-value")) || strings.Contains(stderr.String(), "private-cli-value") {
				t.Fatal("secret plaintext leaked outside staging request")
			}
		})
	}
}
