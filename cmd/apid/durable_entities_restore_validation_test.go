// adr: 853
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func TestApplicationValidatedRestoreRevalidatesAndSkipsReplay(t *testing.T) {
	e, app, dispatch, _ := entityAPIFixture(t, false)
	request := entityRequest("validated-source")
	hook := entityOutboxHook(t, e, app)
	seedAPIOutbox(t, e, app, request, hook.ID)
	id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, request)
	if problem != nil {
		t.Fatal(problem)
	}
	exported, err := e.s.durableEntities.ExportState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(exported)
	var dto api.DurableEntityStateExport
	if json.Unmarshal(body, &dto) != nil {
		t.Fatal("export DTO")
	}
	restore := api.DurableEntityRestoreRequest{Namespace: request.Namespace, Key: request.Key, RequestID: "validated-restore", ExpectedVersion: 1, Export: dto}
	path := "/v1/apps/" + app.Slug + "/entities/restore"
	if rec := e.do(t, http.MethodPost, path+"/validate", restore, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatal(rec.Code, rec.Body.String())
	}
	e.s.durableEntityRestoreValidationEnabled = true
	var reject atomic.Bool
	dispatch.guest = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.DurableEntityRestoreValidationPath {
			t.Error("normal business path invoked", r.URL.Path)
		}
		payload, err := io.ReadAll(r.Body)
		var envelope durableentity.RestoreValidationRequest
		if err != nil || json.Unmarshal(payload, &envelope) != nil || envelope.Validate() != nil || envelope.Event != "validate_restore" {
			t.Error("invalid validation envelope")
		}
		writeJSON(w, http.StatusOK, map[string]any{"protocol_version": 1, "valid": !reject.Load()})
	})
	if rec := e.do(t, http.MethodPost, path, restore, nil); rec.Code != http.StatusUnprocessableEntity || dispatch.calls.Load() != 0 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec := e.do(t, http.MethodPost, path+"/validate", restore, nil)
	var verdict api.DurableEntityRestoreValidationResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &verdict) != nil || !verdict.Valid || verdict.DeploymentID == "" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	restore.ValidationDeploymentID = verdict.DeploymentID
	for _, replay := range []bool{false, true} {
		rec = e.do(t, http.MethodPost, path, restore, nil)
		var out api.DurableEntityRestoreResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Replayed != replay || out.Version != 2 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	if dispatch.calls.Load() != 2 {
		t.Fatal("replay invoked validator", dispatch.calls.Load())
	}
	restore.RequestID = "wrong-deployment"
	restore.ExpectedVersion = 2
	restore.ValidationDeploymentID = uuid.NewString()
	if rec := e.do(t, http.MethodPost, path, restore, nil); rec.Code != http.StatusConflict || dispatch.calls.Load() != 2 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	restore.RequestID = "rejected"
	restore.ValidationDeploymentID = verdict.DeploymentID
	reject.Store(true)
	if rec := e.do(t, http.MethodPost, path, restore, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal(rec.Code, rec.Body.String())
	}
	current, err := e.s.durableEntities.Read(t.Context(), id)
	if err != nil || current.Version != 2 {
		t.Fatal("rejection committed", current, err)
	}
}
