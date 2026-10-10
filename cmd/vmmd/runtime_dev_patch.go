package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	runtimeDevPatchKind        = "dev_patch"
	runtimeDevPatchDisabled    = "dev_patch_disabled"
	runtimeDevPatchUnavailable = "dev_patch_unavailable"
)

type runtimeDevPatchStore interface {
	state.DevSourcePatchStore
	AppByID(ctx context.Context, id string) (state.App, error)
}

// runtimeDevPatch is the wire form of one patch. Archive is a tar.gz of
// regular files relative to ImageDir; JSON encodes it as base64.
type runtimeDevPatch struct {
	Generation int64    `json:"generation"`
	ImageDir   string   `json:"image_dir"`
	Archive    []byte   `json:"archive"`
	Deleted    []string `json:"deleted,omitempty"`
	Digest     string   `json:"digest"`
}

// handleRuntimeDevPatch answers a guest's poll with the newest patch for its
// own app and deployment. The instance identity comes from the vsock
// listener, never from the request. A guest told dev_patch_disabled stops
// polling for the rest of its life.
func (r *runtimeConfigReceiver) handleRuntimeDevPatch(instance string, req runtimeConfigRequest, conn net.Conn) (string, error) {
	store, ok := r.store.(runtimeDevPatchStore)
	if !r.devPatchEnabled || !ok || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchDisabled})
	}
	// guest-init polls from boot, before the wake publishes the instance as
	// live. Not-live must stay retryable: a disabled answer here would end
	// the guest's loop and the snapshot prime would carry that dead loop
	// into every instance restored from it.
	deploymentID, appID, _, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil || deploymentID == "" || appID == "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchUnavailable})
	}
	requestCtx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	app, err := store.AppByID(requestCtx, appID)
	if err != nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchUnavailable})
	}
	if !state.IsDeveloperApp(app) {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchDisabled})
	}
	patch, err := store.LatestDevSourcePatch(requestCtx, appID, deploymentID, req.PatchGeneration)
	if errors.Is(err, state.ErrNotFound) {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Unchanged: true})
	}
	if err != nil || patch.ImageDir != api.DevPatchImageDir || !devPatchDigestMatches(patch) {
		if r.log != nil {
			r.log.Debug("developer live patch unavailable", "instance", instance, "err_kind", "load_failed")
		}
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchUnavailable})
	}
	// Mark before the guest can write anything: from here on this instance
	// must never be snapshotted (ADR-005, ADR-740).
	r.diverged.Mark(instance)
	return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{DevPatch: &runtimeDevPatch{
		Generation: patch.Generation, ImageDir: patch.ImageDir, Archive: patch.Archive,
		Deleted: patch.Deleted, Digest: patch.Digest,
	}})
}

func devPatchDigestMatches(patch state.DevSourcePatch) bool {
	sum := sha256.Sum256(patch.Archive)
	return hex.EncodeToString(sum[:]) == patch.Digest
}
