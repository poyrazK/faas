package ciruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func (h Handler) Progress(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.CIJobsProgressSeconds)*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	proof, _, ok := h.authorize(w, r, ci.RuntimeProgress)
	if !ok {
		return
	}
	store, ok := h.Store.(ci.RuntimeProgressStore)
	if !ok {
		writeError(w, ci.ErrRuntimeAuthority)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		writeError(w, ci.ErrBackendProtocol)
		return
	}
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(time.Duration(api.CIJobsProgressSeconds) * time.Second)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	defer func() { _ = controller.SetReadDeadline(time.Time{}); _ = controller.SetWriteDeadline(time.Time{}) }()
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.CIJobsProgressRequestMaxBytes))
	if err != nil {
		writeError(w, ci.ErrLimit)
		return
	}
	update, err := ci.DecodeProgressUpdate(raw)
	if err != nil {
		writeError(w, err)
		return
	}
	receipt, err := store.AppendJobsRuntimeProgress(ctx, h.Config, proof, update)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(receipt)
}
