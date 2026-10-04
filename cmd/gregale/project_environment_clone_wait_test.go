// adr: 567
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cloneWaitFixture() api.ProjectEnvironmentCloneOperationResponse {
	return api.ProjectEnvironmentCloneOperationResponse{OperationID: "op-1", ProjectSlug: "shop", SourceEnvironment: "production", TargetEnvironment: "stage",
		SourceRevisionHash: strings.Repeat("a", 64), Status: "pending", Revision: 1}
}

func TestEnvCreateFullWaitReturnsOneReceipt(t *testing.T) {
	resetJSONOut(t)
	oldOut, oldErr, oldInterval := osStdout, osStderr, projectEnvironmentClonePollInterval
	var out, errOut bytes.Buffer
	osStdout, osStderr, jsonOutput, projectEnvironmentClonePollInterval = &out, &errOut, true, time.Millisecond
	t.Cleanup(func() {
		osStdout, osStderr, jsonOutput, projectEnvironmentClonePollInterval = oldOut, oldErr, false, oldInterval
	})
	op, calls := cloneWaitFixture(), 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.URL.Path != "/v1/projects/shop/environment-clones" {
				t.Errorf("partial route: %s", r.URL.Path)
			}
			var req api.CreateProjectEnvironmentRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Full {
				t.Errorf("request = %+v, %v", req, err)
			}
			w.WriteHeader(http.StatusAccepted)
			writeJSONTest(w, api.ProjectEnvironmentResponse{Slug: "stage", CloneOperation: &op})
			return
		}
		calls++
		current := op
		current.Revision = int64(calls + 1)
		current.Status = "copying"
		if calls == 2 {
			current.Status = "ready"
		}
		writeJSONTest(w, current)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if code := envCreate([]string{"--full", "--wait", "stage", "--from", "production", "--project", "shop", "--timeout", "1"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	var receipt projectEnvironmentCloneWaitReceipt
	d := json.NewDecoder(&out)
	if err := d.Decode(&receipt); err != nil || !receipt.Succeeded || receipt.Clone.Status != "ready" || calls != 2 {
		t.Fatalf("receipt = %+v, calls = %d, error = %v", receipt, calls, err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF || errOut.Len() != 0 {
		t.Fatalf("extra output = %v, stderr = %s", err, errOut.String())
	}
}

func TestProjectEnvironmentCloneWaitTimeoutFailureAndIdentity(t *testing.T) {
	oldInterval := projectEnvironmentClonePollInterval
	projectEnvironmentClonePollInterval = time.Millisecond
	t.Cleanup(func() { projectEnvironmentClonePollInterval = oldInterval })
	for _, vector := range []string{"timeout", "failed", "compensated", "identity", "revision", "unknown", "cancelled"} {
		t.Run(vector, func(t *testing.T) {
			initial := cloneWaitFixture()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := initial
				switch vector {
				case "failed", "compensated":
					current.Status = vector
				case "identity":
					current.TargetEnvironment = "other"
				case "revision":
					current.Revision = 0
				case "unknown":
					current.Status = "unexpected"
				}
				writeJSONTest(w, current)
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if vector == "cancelled" {
				cancel()
			}
			op, timedOut, err := waitForProjectEnvironmentClone(ctx, NewClient(srv.URL, "test"), initial, 20*time.Millisecond)
			wantError := vector == "identity" || vector == "revision" || vector == "unknown" || vector == "cancelled"
			if (err != nil) != wantError || timedOut != (vector == "timeout") || (vector == "failed" || vector == "compensated") && op.Status != vector {
				t.Fatalf("status = %s, timeout = %t, error = %v", op.Status, timedOut, err)
			}
		})
	}
}
