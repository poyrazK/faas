package apidserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func (s *server) WithExecutionFileStorage(backend storage.StorageBackend) *server {
	s.executionFileStorage = backend
	return s
}
func (s *server) executionFilesService() runfiles.Service {
	st, _ := s.store.(state.ExecutionFileStore)
	recipients, _ := accountRecipients(hostSecretRecipient())
	return runfiles.Service{Store: st, Storage: s.executionFileStorage, Recipients: recipients, Identities: accountIdentities()}
}
func (s *server) executionFileAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (runfiles.Access, bool) {
	access, p := executionAccessForRequest(r)
	if p != nil {
		writeExecutionAccessError(w, p)
		return runfiles.Access{}, false
	}
	return runfiles.Access{Account: acct, Principal: access.principal, Wide: access.broad}, true
}
func writeRunFileError(w http.ResponseWriter, err error) {
	var p *api.Problem
	if errors.As(err, &p) {
		api.WriteProblem(w, p)
	} else if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Run file not found", "no such Run file"))
	} else {
		api.WriteProblem(w, runfiles.Problem(err))
	}
}
func (s *server) prepareExecutionFile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionFileAccess(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateExecutionFileRequest
	if decodeJSONSized(r, &req, api.ExecutionContractSchemaMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid Run file metadata"))
		return
	}
	out, err := s.executionFilesService().Prepare(r.Context(), a, req)
	if err != nil {
		writeRunFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) uploadExecutionFile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.executionV2Enabled || !requireExecutionEntitlement(w, acct) {
		if !s.executionV2Enabled {
			api.WriteProblem(w, api.ErrCapacity("new Run file uploads are disabled"))
		}
		return
	}
	a, ok := s.executionFileAccess(w, r, acct)
	if !ok {
		return
	}
	ctx, cancel := runfiles.UploadContext(r.Context(), w)
	defer cancel()
	out, err := s.executionFilesService().Upload(ctx, a, r.PathValue("id"), r.ContentLength, r.Header.Get("X-Content-SHA256"), r.Body)
	if err != nil {
		writeRunFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) getExecutionFile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionFileAccess(w, r, acct)
	if !ok {
		return
	}
	f, err := s.executionFilesService().Get(r.Context(), a, r.PathValue("id"))
	if err != nil {
		writeRunFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runfiles.Response(f))
}
func (s *server) downloadExecutionFile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionFileAccess(w, r, acct)
	if !ok {
		return
	}
	ctx, cancel := runfiles.DownloadContext(r.Context(), w)
	defer cancel()
	f, reader, err := s.executionFilesService().Open(ctx, a, r.PathValue("id"))
	if err != nil {
		writeRunFileError(w, err)
		return
	}
	defer reader.Close() //nolint:errcheck
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(f.SizeBytes, 10))
	w.Header().Set("X-Content-SHA256", f.SHA256)
	if _, err = io.CopyBuffer(w, reader, make([]byte, api.ExecutionFileStreamChunkBytes)); err != nil {
		panic(http.ErrAbortHandler)
	}
	if err := http.NewResponseController(w).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		panic(http.ErrAbortHandler)
	}
}
func (s *server) deleteExecutionFile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionFileAccess(w, r, acct)
	if !ok {
		return
	}
	st, _ := s.store.(state.ExecutionFileStore)
	if st == nil {
		writeRunFileError(w, state.ErrNotFound)
		return
	}
	if err := st.RevokeExecutionFile(r.Context(), acct.ID, r.PathValue("id"), a.Principal, a.Wide, time.Now().UTC()); err != nil {
		writeRunFileError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cleanup_scheduled"})
}
func (s *server) runExecutionFileGC(ctx context.Context) {
	ticker := time.NewTicker(api.ExecutionFileGCInterval)
	defer ticker.Stop()
	for {
		_, _ = s.executionFilesService().Collect(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
