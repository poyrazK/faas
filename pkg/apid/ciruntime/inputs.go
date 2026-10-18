package ciruntime

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func (h Handler) Input(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	proof, _, ok := h.authorize(w, r, ci.RuntimeInput)
	if !ok {
		return
	}
	store, ok := h.Store.(ci.RuntimeInputStore)
	if !ok {
		writeError(w, ci.ErrRuntimeAuthority)
		return
	}
	finish, ok := h.beginTransfer(ctx, w, proof)
	if !ok {
		return
	}
	defer finish()
	artifact, err := store.JobsRuntimeInput(ctx, h.Config, proof, r.PathValue("input"))
	if err != nil {
		writeError(w, err)
		return
	}
	file, err := h.spoolInput(ctx, artifact)
	if err != nil {
		writeError(w, err)
		return
	}
	defer removeSpool(file)
	current, err := store.JobsRuntimeInput(ctx, h.Config, proof, r.PathValue("input"))
	if err != nil || current != artifact {
		writeError(w, ci.ErrRuntimeAuthority)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.Bytes, 10))
	w.Header().Set(ci.RuntimeDigestHeader, artifact.Digest)
	_, _ = io.Copy(w, file)
}

func (h Handler) spoolInput(ctx context.Context, artifact ci.JobsArtifact) (*os.File, error) {
	body, err := h.Blobs.Get(ctx, artifact.Key)
	if err != nil || body == nil {
		return nil, ci.ErrStorageUnavailable
	}
	defer func() { _ = body.Close() }()
	return spool(ctx, body, artifact.Bytes, artifact.Digest)
}
