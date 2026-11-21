package apidserver

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getAgentTaskReceipt(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	if s.agentAPI == nil {
		api.WriteProblem(w, api.ErrCapacity("agent task receipts unavailable"))
		return
	}
	row, p := s.agentAPI.TaskReceipt(r.Context(), caller, r.PathValue("id"))
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, executionResponse(row))
}
func (s *server) downloadAgentTaskArtifact(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	if s.agentAPI == nil {
		api.WriteProblem(w, api.ErrCapacity("agent task receipts unavailable"))
		return
	}
	row, p := s.agentAPI.TaskReceipt(r.Context(), caller, r.PathValue("id"))
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	for _, a := range row.Artifacts {
		if a.Name == r.PathValue("artifact") {
			s.streamRunArtifact(w, r, caller.Account, a, false)
			return
		}
	}
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Artifact not found", "no such output on this task invocation"))
}
func (s *server) streamRunArtifact(w http.ResponseWriter, r *http.Request, account state.Account, a api.ExecutionArtifact, attachment bool) {
	reader := io.NopCloser(bytes.NewReader(a.Content))
	size := int64(a.SizeBytes)
	if a.FileID != "" {
		ctx, cancel := runfiles.DownloadContext(r.Context(), w)
		defer cancel()
		f, opened, err := s.executionFilesService().Open(ctx, runfiles.Access{Account: account, Wide: true}, a.FileID)
		if err != nil {
			writeRunFileError(w, err)
			return
		}
		reader = opened
		size = f.SizeBytes
	}
	defer reader.Close() //nolint:errcheck
	if attachment {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(a.Name)}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-SHA256", a.SHA256)
	if _, err := io.CopyBuffer(w, reader, make([]byte, api.ExecutionFileStreamChunkBytes)); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func (s *server) getAgentPipelineReceipt(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	if s.agentAPI == nil {
		api.WriteProblem(w, api.ErrCapacity("agent pipeline receipts unavailable"))
		return
	}
	row, p := s.agentAPI.PipelineReceipt(r.Context(), caller, r.PathValue("id"))
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, row)
}

func (s *server) getAgentPipelineEvents(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	if s.agentAPI == nil {
		api.WriteProblem(w, api.ErrCapacity("agent pipeline progress unavailable"))
		return
	}
	if _, problem := s.agentAPI.PipelineReceipt(r.Context(), caller, r.PathValue("id")); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.ExecutionPipelineProgressStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("pipeline progress unavailable"))
		return
	}
	after, problem := executionEventCursor(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	page, err := store.ListExecutionPipelineEvents(r.Context(), caller.Account.ID, r.PathValue("id"), nil, after, api.ExecutionPipelineProgressRetained)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	// App callers use the existing app-scoped task receipts for output reads.
	if page.Progress != nil {
		for i := range page.Progress.Steps {
			page.Progress.Steps[i].EventsURL = ""
		}
	}
	for _, e := range page.Events {
		if e.Step != nil {
			e.Step.EventsURL = ""
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}
