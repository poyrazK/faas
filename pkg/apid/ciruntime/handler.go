// Package ciruntime serves attempt-scoped CI source/output transfers. No grant
// issuance or compute admission route exists. apid mounts it behind a separate
// default-off transport gate, HTTPS validation and task-bound authority.
package ciruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

type Handler struct {
	Config ci.Config
	Store  ci.RuntimeStore
	Blobs  ci.JobsBlobStore
}

func (h Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/internal/ci/runtime/{run}/control", h.Control)
	mux.HandleFunc("GET /v1/internal/ci/runtime/{run}/source", h.Source)
	mux.HandleFunc("PUT /v1/internal/ci/runtime/{run}/objects", h.Upload)
	mux.HandleFunc("PUT /v1/internal/ci/runtime/{run}/result", h.Seal)
	mux.HandleFunc("PUT /v1/internal/ci/runtime/{run}/progress", h.Progress)
	mux.HandleFunc("GET /v1/internal/ci/runtime/{run}/inputs/{input}", h.Input)
	return mux
}

func (h Handler) authorize(w http.ResponseWriter, r *http.Request, action string) (ci.RuntimeProof, ci.RuntimeView, bool) {
	proof := ci.RuntimeProof{RunID: r.PathValue("run"), InstanceID: r.Header.Get(ci.RuntimeInstanceHeader), Capability: r.Header.Get(ci.RuntimeCapabilityHeader)}
	if h.Store == nil || h.Blobs == nil || len(r.Header.Values(ci.RuntimeInstanceHeader)) != 1 || len(r.Header.Values(ci.RuntimeCapabilityHeader)) != 1 ||
		r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || !ci.ValidRuntimeProof(proof) {
		writeError(w, ci.ErrRuntimeAuthority)
		return proof, ci.RuntimeView{}, false
	}
	view, err := h.Store.AuthorizeJobsRuntime(r.Context(), h.Config, proof, action)
	if err != nil {
		writeError(w, err)
		return proof, view, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return proof, view, true
}

func (h Handler) Control(w http.ResponseWriter, r *http.Request) {
	_, view, ok := h.authorize(w, r, ci.RuntimeControl)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

func (h Handler) Source(w http.ResponseWriter, r *http.Request) {
	proof, view, ok := h.authorize(w, r, ci.RuntimeSource)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	finish, ok := h.beginTransfer(ctx, w, proof)
	if !ok {
		return
	}
	defer finish()
	body, err := h.Blobs.Get(ctx, view.Manifest.Source.Key)
	if err != nil || body == nil {
		writeError(w, errors.New("CI source unavailable"))
		return
	}
	defer func() { _ = body.Close() }()
	file, err := spool(ctx, body, view.Manifest.Source.Bytes, view.Manifest.Source.Digest)
	if err != nil {
		writeError(w, err)
		return
	}
	defer removeSpool(file)
	if _, err := h.Store.AuthorizeJobsRuntime(ctx, h.Config, proof, ci.RuntimeSource); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set(ci.RuntimeDigestHeader, view.Manifest.Source.Digest)
	_, _ = io.Copy(w, file)
}

func (h Handler) Upload(w http.ResponseWriter, r *http.Request) {
	proof, _, ok := h.authorize(w, r, ci.RuntimeUpload)
	if !ok {
		return
	}
	object := ci.RuntimeObject{Name: r.Header.Get(ci.RuntimeObjectHeader), Digest: r.Header.Get(ci.RuntimeDigestHeader), Bytes: r.ContentLength}
	if len(r.Header.Values(ci.RuntimeObjectHeader)) != 1 || len(r.Header.Values(ci.RuntimeDigestHeader)) != 1 || r.Header.Get("Content-Type") != "application/octet-stream" ||
		object.Bytes < 0 || object.Bytes > api.CIArtifactMaxBytes || (object.Name == "logs.bin" && object.Bytes > api.CILogMaxBytesPerAttempt) {
		writeError(w, ci.ErrBackendProtocol)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	finish, ok := h.beginTransfer(ctx, w, proof)
	if !ok {
		return
	}
	defer finish()
	file, err := spool(ctx, http.MaxBytesReader(w, r.Body, object.Bytes+1), object.Bytes, object.Digest)
	if err != nil {
		writeError(w, err)
		return
	}
	defer removeSpool(file)
	if err := h.retain(ctx, proof, object, file); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Handler) retain(ctx context.Context, proof ci.RuntimeProof, object ci.RuntimeObject, file *os.File) error {
	key, retained, err := h.Store.ReserveJobsRuntimeObject(ctx, proof, object)
	if err != nil || retained {
		return err
	}
	if err := h.Blobs.Put(ctx, key, file); err != nil {
		return errors.New("CI output storage unavailable")
	}
	return h.Store.CommitJobsRuntimeObject(ctx, proof, object)
}

func (h Handler) beginTransfer(ctx context.Context, w http.ResponseWriter, proof ci.RuntimeProof) (func(), bool) {
	token, err := h.Store.BeginJobsRuntimeTransfer(ctx, proof)
	if err != nil {
		writeError(w, err)
		return func() {}, false
	}
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(time.Duration(api.CIJobsTransferSeconds) * time.Second)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	finish := func() {
		_ = controller.SetReadDeadline(time.Time{})
		_ = controller.SetWriteDeadline(time.Time{})
		releaseCtx, release := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.CIJobsTransferSeconds)*time.Second)
		defer release()
		_ = h.Store.EndJobsRuntimeTransfer(releaseCtx, proof, token)
	}
	return finish, true
}

func (h Handler) Seal(w http.ResponseWriter, r *http.Request) {
	proof, view, ok := h.authorize(w, r, ci.RuntimeUpload)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	if !view.Sealed {
		finish, ok := h.beginTransfer(ctx, w, proof)
		if !ok {
			return
		}
		defer finish()
	} else {
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(time.Duration(api.CIJobsTransferSeconds) * time.Second))
		defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	}
	if r.Header.Get("Content-Type") != "application/json" {
		writeError(w, ci.ErrBackendProtocol)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.CIJobsResultManifestMaxBytes))
	if err != nil {
		writeError(w, ci.ErrLimit)
		return
	}
	if err := h.Store.SealJobsRuntime(ctx, proof, raw); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func spool(ctx context.Context, body io.Reader, size int64, digest string) (*os.File, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if err != nil || len(raw) != sha256.Size || digest != "sha256:"+hex.EncodeToString(raw) {
		return nil, ci.ErrBackendProtocol
	}
	file, err := os.CreateTemp("", "gregale-ci-transfer-*")
	if err != nil {
		return nil, errors.New("CI transfer unavailable")
	}
	valid := false
	defer func() {
		if !valid {
			removeSpool(file)
		}
	}()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(contextReader{ctx: ctx, reader: body}, size+1))
	if err != nil || count != size || hex.EncodeToString(hash.Sum(nil)) != hex.EncodeToString(raw) {
		return nil, ci.ErrBackendProtocol
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.New("CI transfer unavailable")
	}
	valid = true
	return file, nil
}

func removeSpool(file *os.File) { _ = file.Close(); _ = os.Remove(file.Name()) }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "ci_runtime_unavailable"
	switch {
	case errors.Is(err, ci.ErrRuntimeAuthority), errors.Is(err, ci.ErrNotFound), errors.Is(err, ci.ErrArtifactInput):
		status, code = http.StatusNotFound, "ci_runtime_unavailable"
	case errors.Is(err, ci.ErrConflict):
		status, code = http.StatusConflict, "ci_runtime_conflict"
	case errors.Is(err, ci.ErrLeaseBusy):
		status, code = http.StatusTooManyRequests, "ci_runtime_busy"
	case errors.Is(err, ci.ErrLimit):
		status, code = http.StatusRequestEntityTooLarge, "ci_runtime_limit"
	case errors.Is(err, ci.ErrBackendProtocol):
		status, code = http.StatusBadRequest, "ci_runtime_invalid"
	}
	api.WriteProblem(w, api.NewProblem(status, code, "CI runtime request unavailable", "The CI transfer was refused or could not be completed"))
}
