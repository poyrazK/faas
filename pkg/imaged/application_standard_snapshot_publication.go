package imaged

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

// The notification carries a reference, never capture authority or full ACKs.
// Historical catalog validation does not issue authority to restore the cache.
func (h *Handler) verifyStandardSnapshotWritten(ctx context.Context, app state.App, dep state.Deployment, p snapshotWrittenPayload) error {
	if p.ApplicationStandardCaptureToken == "" {
		return h.checkUnreferencedStandardSnapshot(ctx, app, dep, p)
	}
	s, ok := h.store.(state.ApplicationStandardSnapshotCaptureStore)
	if !ok {
		return fmt.Errorf("imaged: managed snapshot catalog: %w", runtimeadmission.ErrUnavailable)
	}
	r, err := s.GetApplicationStandardSnapshotCapture(ctx, standardSnapshotScopeUUID(app.AccountID), standardSnapshotScopeUUID(app.ID), standardSnapshotScopeUUID(dep.ID), p.ApplicationStandardCaptureToken)
	if err != nil {
		return fmt.Errorf("imaged: managed snapshot catalog: %w", err)
	}
	if r.Acknowledgment == nil || r.Acknowledgment.Check(r.Grant, time.Unix(0, r.Acknowledgment.CompletedAtUnixNano)) != nil {
		return fmt.Errorf("imaged: unpublished managed snapshot: %w", runtimeadmission.ErrInvalid)
	}
	g, a := r.Grant, r.Acknowledgment
	tier := state.SnapshotTierInit
	if g.Mode == "warm" {
		tier = state.SnapshotTierWarm
	}
	actualTier := p.Tier
	if actualTier == "" {
		actualTier = state.SnapshotTierInit
	}
	if g.Mode != "warm" && g.Mode != "park" || p.ApplicationStandardCaptureToken != g.Token || g.Parent.Binding.InstanceID != standardSnapshotScopeUUID(p.SourceInstanceID) || g.Parent.Binding.NodeID != standardSnapshotScopeUUID(p.NodeID) || g.SourceStartedAtUnixNano != p.SourceStartedAt.UnixNano() || g.MemoryKey != p.StorageKey || g.VMStateKey != state.SnapshotVMStateKey(state.Snapshot{StorageKey: p.StorageKey}) || g.PrivateDriveKey != state.SnapshotDriveKey(state.Snapshot{StorageKey: p.StorageKey}) || g.FCVersion != p.FCVersion || actualTier != tier || a.Capture.Memory.Bytes != p.MemBytes || a.Capture.VMState.Bytes != p.VMStateBytes || p.StoredBytes < 0 {
		return fmt.Errorf("imaged: managed snapshot notification mismatch: %w", runtimeadmission.ErrInvalid)
	}
	return nil
}

func (h *Handler) checkUnreferencedStandardSnapshot(ctx context.Context, app state.App, dep state.Deployment, p snapshotWrittenPayload) error {
	token, ok := state.SnapshotCaptureToken(p.StorageKey)
	if !ok {
		return nil // Legacy captures keep their existing publication fences.
	}
	s, ok := h.store.(state.ApplicationStandardSnapshotCaptureStore)
	if !ok {
		return nil
	}
	_, err := s.GetApplicationStandardSnapshotCapture(ctx, standardSnapshotScopeUUID(app.AccountID), standardSnapshotScopeUUID(app.ID), standardSnapshotScopeUUID(dep.ID), token)
	if errors.Is(err, state.ErrNotFound) {
		return nil // A legacy capture does not have managed catalog history.
	}
	if err != nil {
		return fmt.Errorf("imaged: managed snapshot catalog: %w", err)
	}
	return fmt.Errorf("imaged: managed snapshot missing catalog reference: %w", runtimeadmission.ErrInvalid)
}

func standardSnapshotScopeUUID(s string) string {
	u, err := uuid.Parse(s)
	if err != nil {
		return s
	}
	return u.String()
}
