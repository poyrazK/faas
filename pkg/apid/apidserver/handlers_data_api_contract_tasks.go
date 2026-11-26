// adr: 20261027123814 — Data API contract tasks run without the app-task capability.
package apidserver

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dataapi"
	"github.com/onebox-faas/faas/pkg/state"
)

// createDataAPIContractTask admits one fixed schema inspection. The route
// belongs to the Data API capability, so owner tooling works for an account
// the general app-task API is not released to.
func (s *server) createDataAPIContractTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var request api.CreateDataAPIContractTaskRequest
	if err := decodeJSONSized(r, &request, api.AppTaskRequestMaxBytes); err != nil || request.Kind.Command() == nil {
		api.WriteProblem(w, api.ErrValidation(`kind must be one of "types", "validators" or "schema"`))
		return
	}
	task := request.Kind.Request()
	if r.URL.Query().Get("output") == "artifact" {
		task = request.Kind.ArtifactRequest()
	}
	resolved, problem := task.Resolve()
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	row, problem := s.admitAppTask(r, acct, app, resolved)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusAccepted, appTaskResponse(row))
}

// getDataAPIContractTask reads one contract inspection; any other app task
// is not found here.
func (s *server) getDataAPIContractTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "no such Data API contract task")
		return
	}
	row, err := s.store.AppTaskByID(r.Context(), acct.ID, app.ID, id)
	if errors.Is(err, state.ErrNotFound) || err == nil && api.DataAPIContractTaskKindOf(row.Command, row.CommandShell) == "" {
		s.notFound(w, "no such Data API contract task")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load Data API contract task"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("output") == "artifact" {
		s.downloadDataAPIContractArtifact(w, r, app, row)
		return
	}
	writeJSON(w, http.StatusOK, appTaskResponse(row))
}

func (s *server) downloadDataAPIContractArtifact(w http.ResponseWriter, r *http.Request, app state.App, task state.AppTask) {
	if !api.IsDataAPIContractArtifactTask(task.Command, task.CommandShell) {
		s.notFound(w, "no such Data API contract artifact")
		return
	}
	if !s.currentDataAPITask(r, app, task) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Data API contract artifact unavailable", "The task is incomplete, failed, or belongs to an earlier deployment."))
		return
	}
	kind := api.DataAPIContractTaskKindOf(task.Command, task.CommandShell)
	filename := "database.types.ts"
	if kind == api.DataAPIContractSchema {
		if _, err := dataapi.Parse([]byte(task.StdoutTail)); err != nil {
			api.WriteProblem(w, api.ErrInternal("Data API task returned an invalid schema snapshot"))
			return
		}
		filename = "database.schema.json"
	} else {
		if dashboardDataAPITypeFingerprint(task.StdoutTail) == "" {
			api.WriteProblem(w, api.ErrInternal("Data API task returned an invalid TypeScript contract"))
			return
		}
		if kind == api.DataAPIContractValidators {
			filename = "database.validators.ts"
		}
	}
	writeDataAPIContractDownload(w, "application/octet-stream", filename, task)
}

func writeDataAPIContractDownload(w http.ResponseWriter, contentType, filename string, task state.AppTask) {
	content := []byte(task.StdoutTail)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set(api.DataAPIContractArtifactDeploymentHeader, task.DeploymentID)
	w.Header().Set(api.DataAPIContractArtifactSHA256Header, fmt.Sprintf("%x", sha256.Sum256(content)))
	_, _ = w.Write(content)
}
