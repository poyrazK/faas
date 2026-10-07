package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDeploysRetry_JSONReceipt(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "inferred-stage"
		if explicit {
			name = "explicit-stage"
		}
		t.Run(name, func(t *testing.T) {
			stage := json.RawMessage(`{"current":"source_download","retry_requested_stage":"snapshot_prepare","retry_restart_reason":"Retained checkpoints unavailable; rebuilding source."}`)
			want := api.DeploymentResponse{ID: retryTestNewID, Status: "pending", StageState: stage}
			var requested api.RetryDeploymentRequest
			var retryCalls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/"+retryTestID+"/stages":
					writeJSONTest(w, json.RawMessage(`{"current":"","history":[{"name":"snapshot_prepare","status":"failed"}]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/deployments/"+retryTestID+"/retry":
					retryCalls++
					if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
						t.Errorf("retry body: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					writeJSONTest(w, want)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			resetJSONOut(t)
			jsonOutput = true
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			defer func() { osStdout = oldOut }()
			args := []string{retryTestID}
			if explicit {
				args = append(args, "--from=snapshot_prepare")
			}
			if code := cmdDeploysRetry(args); code != 0 {
				t.Fatalf("retry exit=%d", code)
			}
			var got api.DeploymentResponse
			decoder := json.NewDecoder(&stdout)
			if err := decoder.Decode(&got); err != nil {
				t.Fatalf("retry did not emit JSON: %v", err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("retry emitted output after its JSON receipt: %v", err)
			}
			var gotStage, wantStage map[string]string
			if err := json.Unmarshal(got.StageState, &gotStage); err != nil {
				t.Fatalf("retry metadata: %v", err)
			}
			if err := json.Unmarshal(stage, &wantStage); err != nil {
				t.Fatal(err)
			}
			if got.ID != want.ID || got.Status != want.Status || !reflect.DeepEqual(gotStage, wantStage) {
				t.Fatalf("receipt lost retry identity or restart metadata: %+v", got)
			}
			if retryCalls != 1 || requested.FromStage != "snapshot_prepare" {
				t.Fatalf("retry calls=%d requested=%+v", retryCalls, requested)
			}
		})
	}
}
