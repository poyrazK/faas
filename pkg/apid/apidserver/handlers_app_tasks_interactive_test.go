// adr: 1037 — interactive app-task admission is separately gated, returns a
// one-time attach credential, and stores only its digest.

package apidserver_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestInteractiveAppTaskRequiresItsOwnGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.EnableAppTaskAPIForTest(&e)
	apidserver.SeedAppTaskDeployment(t, e, "shell-gate")
	rec := e.Do(t, http.MethodPost, "/v1/apps/shell-gate/tasks", api.CreateAppTaskRequest{Interactive: true, TTY: true}, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("interactive with gate off = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
}

func TestInteractiveAppTaskReturnsOneTimeAttachCredential(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.EnableAppTaskAPIForTest(&e)
	(*e.S()).WithInteractiveAppTasksEnabled(true)
	apidserver.SeedAppTaskDeployment(t, e, "shell")

	rec := e.Do(t, http.MethodPost, "/v1/apps/shell/tasks", api.CreateAppTaskRequest{Interactive: true, TTY: true}, nil)
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

	record, err := (*e.Store()).AppTaskAttachByTask(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("AppTaskAttachByTask: %v", err)
	}
	digest := sha256.Sum256([]byte(created.Attach.Token))
	if !record.TTY || !bytes.Equal(record.TokenSHA256, digest[:]) {
		t.Fatalf("stored attach record = %+v", record)
	}

	read := e.Do(t, http.MethodGet, "/v1/apps/shell/tasks/"+created.ID, nil, nil)
	if read.Code != http.StatusOK || strings.Contains(read.Body.String(), created.Attach.Token) {
		t.Fatalf("GET task leaked the attach token or failed: %d %s", read.Code, read.Body.String())
	}
}

func TestInteractiveAppTaskRejectedByExclusiveOperations(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.EnableAppTaskAPIForTest(&e)
	(*e.S()).WithInteractiveAppTasksEnabled(true)
	apidserver.SeedAppTaskDeployment(t, e, "shell-ops")
	rec := e.Do(t, http.MethodPost, "/v1/apps/shell-ops/operations/tasks", api.ExclusiveAppTaskOperationRequest{
		Policy: "migrations", Key: json.RawMessage(`"k"`), Task: api.CreateAppTaskRequest{Interactive: true},
	}, nil)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("exclusive interactive task = %d, want a 4xx rejection; body=%s", rec.Code, rec.Body.String())
	}
}
