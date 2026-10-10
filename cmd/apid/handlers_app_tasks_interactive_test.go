// adr: 958 — interactive app-task admission is separately gated, returns a
// one-time attach credential, and stores only its digest.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestInteractiveAppTaskRequiresItsOwnGate(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableAppTaskAPIForTest(&e)
	seedAppTaskDeployment(t, e, "shell-gate")
	rec := e.do(t, http.MethodPost, "/v1/apps/shell-gate/tasks", api.CreateAppTaskRequest{Interactive: true, TTY: true}, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("interactive with gate off = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
}

func TestInteractiveAppTaskReturnsOneTimeAttachCredential(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableAppTaskAPIForTest(&e)
	e.s.WithInteractiveAppTasksEnabled(true)
	seedAppTaskDeployment(t, e, "shell")

	rec := e.do(t, http.MethodPost, "/v1/apps/shell/tasks", api.CreateAppTaskRequest{Interactive: true, TTY: true}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST interactive task = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	var created api.AppTaskResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Interactive || created.Attach == nil || len(created.Attach.Token) < 40 ||
		created.Attach.Path != api.AppTaskAttachPath("shell", created.ID) || created.Attach.Subprotocol != api.AppTaskAttachSubprotocol {
		t.Fatalf("create response = %+v", created)
	}
	if strings.Join(created.Command, " ") != api.AppTaskInteractiveDefaultCommand || created.TimeoutSeconds != api.AppTaskInteractiveDefaultTimeoutSeconds {
		t.Fatalf("interactive defaults = %+v", created)
	}

	record, err := e.store.AppTaskAttachByTask(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("AppTaskAttachByTask: %v", err)
	}
	digest := sha256.Sum256([]byte(created.Attach.Token))
	if !record.TTY || !bytes.Equal(record.TokenSHA256, digest[:]) {
		t.Fatalf("stored attach record = %+v", record)
	}

	read := e.do(t, http.MethodGet, "/v1/apps/shell/tasks/"+created.ID, nil, nil)
	if read.Code != http.StatusOK || strings.Contains(read.Body.String(), created.Attach.Token) {
		t.Fatalf("GET task leaked the attach token or failed: %d %s", read.Code, read.Body.String())
	}
}

func TestInteractiveAppTaskRejectedByExclusiveOperations(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableAppTaskAPIForTest(&e)
	e.s.WithInteractiveAppTasksEnabled(true)
	seedAppTaskDeployment(t, e, "shell-ops")
	rec := e.do(t, http.MethodPost, "/v1/apps/shell-ops/operations/tasks", api.ExclusiveAppTaskOperationRequest{
		Policy: "migrations", Key: json.RawMessage(`"k"`), Task: api.CreateAppTaskRequest{Interactive: true},
	}, nil)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("exclusive interactive task = %d, want a 4xx rejection; body=%s", rec.Code, rec.Body.String())
	}
}

func TestInteractiveAppTaskGateEnv(t *testing.T) {
	getenv := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if interactiveAppTasksEnabledFromEnv(getenv(map[string]string{})) {
		t.Fatal("empty environment enabled interactive app tasks")
	}
	if !interactiveAppTasksEnabledFromEnv(getenv(map[string]string{"FAAS_INTERACTIVE_APP_TASKS": "1"})) {
		t.Fatal("FAAS_INTERACTIVE_APP_TASKS=1 did not enable interactive app tasks")
	}
}
