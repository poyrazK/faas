package ciruntime

import (
	"context"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

type artifactStore interface {
	Artifact(context.Context, ci.Config, string, string, string, string) (ci.JobsArtifact, error)
}

// Download requires an account ID supplied by apid's authenticated admin/MFA
// middleware. It serves only frozen, verified terminal artifact references.
func (h Handler) Download(w http.ResponseWriter, r *http.Request, account string) {
	store, ok := h.Store.(artifactStore)
	if !ok || h.Blobs == nil || r.URL.RawQuery != "" {
		writeError(w, ci.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(time.Duration(api.CIJobsTransferSeconds) * time.Second))
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	args := []string{account, r.PathValue("execution"), r.PathValue("job"), r.PathValue("artifact")}
	artifact, err := store.Artifact(ctx, h.Config, args[0], args[1], args[2], args[3])
	if err != nil {
		writeError(w, err)
		return
	}
	body, err := h.Blobs.Get(ctx, artifact.Key)
	if err != nil {
		writeError(w, err)
		return
	}
	defer func() { _ = body.Close() }()
	file, err := spool(ctx, body, artifact.Bytes, artifact.Digest)
	if err != nil {
		writeError(w, err)
		return
	}
	defer removeSpool(file)
	if _, err := store.Artifact(ctx, h.Config, args[0], args[1], args[2], args[3]); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(artifact.Path)}))
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.Bytes, 10))
	w.Header().Set(ci.RuntimeDigestHeader, artifact.Digest)
	_, _ = io.Copy(w, file)
}
