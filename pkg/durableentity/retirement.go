package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type RetirementRequest struct {
	OperationID string `json:"operation_id"`
	Version     uint64 `json:"expected_version"`
	Revision    string `json:"expected_recovery_revision"`
}
type RetirementStatus struct {
	Request       RetirementRequest `json:"request"`
	Actor         string            `json:"actor"`
	At            time.Time         `json:"at"`
	Revision      uint64            `json:"revision"`
	Phase         string            `json:"phase"`
	NotBefore     time.Time         `json:"not_before"`
	RetainedBytes int64             `json:"retained_bytes"`
}
type retirement struct {
	RetirementStatus
	Cursor string             `json:"cursor,omitempty"`
	Proof  *inventoryProgress `json:"proof,omitempty"`
}
type RetirementPreview struct {
	Version                uint64            `json:"version"`
	RecoveryRevision       string            `json:"recovery_revision"`
	RetainedBytes          int64             `json:"retained_bytes"`
	Receipts               uint64            `json:"receipts"`
	ProjectedRecoveryBytes int64             `json:"projected_recovery_bytes"`
	LiveOwner              bool              `json:"live_owner"`
	Alarm                  bool              `json:"alarm"`
	PendingOutbox          int               `json:"pending_outbox"`
	Ready                  bool              `json:"ready"`
	Retirement             *RetirementStatus `json:"retirement,omitempty"`
}

func validRetirementRequest(p RetirementRequest) bool {
	parsed, err := uuid.Parse(p.OperationID)
	return err == nil && parsed.Version() == 4 && parsed.String() == p.OperationID && validDigest(p.Revision)
}
func validRetirement(base manifest) bool {
	r := base.Retirement
	if r == nil {
		return base.Schema != 7
	}
	if base.Schema != 7 || base.OwnerID != "" || base.AlarmDelivery != nil || base.OutboxDelivery != nil || base.StorageUsage == nil || !validRetirementRequest(r.Request) || r.Request.Version != base.Version || !validIdentity(r.Actor) || r.At.IsZero() || r.NotBefore.IsZero() || r.Revision == 0 || len(r.Cursor) > api.MaxObjectS3ListCursorBytes || r.RetainedBytes < 0 || r.RetainedBytes > 64<<20 {
		return false
	}
	if r.Phase != "proof" && r.Phase != "cleanup" && r.Phase != "verify" && r.Phase != "complete" {
		return false
	}
	if r.Phase == "complete" && (r.Cursor != "" || r.Proof != nil || r.RetainedBytes != base.StorageUsage.TotalBytes()) {
		return false
	}
	if r.Phase != "complete" && r.RetainedBytes != 0 {
		return false
	}
	return r.Proof == nil || r.Phase == "proof" && r.Proof.Version == base.Version && r.Proof.SnapshotKey == base.SnapshotKey && r.Proof.SnapshotHash == base.SnapshotHash && validInventoryProgress(base, *r.Proof)
}
func (m *Manager) PreviewRetirement(ctx context.Context, id ID) (RetirementPreview, error) {
	base, _, err := m.readManifest(ctx, id)
	if err != nil {
		return RetirementPreview{}, err
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return RetirementPreview{}, err
	}
	out := RetirementPreview{Version: base.Version, RecoveryRevision: recoveryRevision(base), LiveOwner: base.OwnerID != "" && m.now().Before(base.ExpiresAt), Alarm: state.AlarmAt != nil, PendingOutbox: len(state.Outbox)}
	projectedFits := true
	if base.StorageUsage != nil {
		out.RetainedBytes, out.Receipts = base.StorageUsage.TotalBytes(), base.StorageUsage.ReceiptCount
		out.ProjectedRecoveryBytes = out.RetainedBytes
		if base.Version != 0 {
			state.Data = json.RawMessage(`{}`)
			body, err := json.Marshal(state)
			if err != nil {
				return RetirementPreview{}, err
			}
			out.ProjectedRecoveryBytes += int64(len(body)) - base.StorageUsage.SnapshotBytes
			projectedFits = len(body) <= api.MaxDurableEntitySnapshotBytes && (base.StorageLimitBytes == 0 || out.ProjectedRecoveryBytes <= base.StorageLimitBytes)
		}
	}
	out.Ready = projectedFits && base.Retirement == nil && !out.LiveOwner && !out.Alarm && out.PendingOutbox == 0 && base.AlarmDelivery == nil && base.OutboxDelivery == nil && base.StorageUsage != nil && out.RetainedBytes <= 64<<20
	if base.Retirement != nil {
		status := base.Retirement.RetirementStatus
		out.Retirement = &status
	}
	return out, nil
}
func (m *Manager) RetirementStatus(ctx context.Context, id ID) (*RetirementStatus, error) {
	base, _, err := m.readManifest(ctx, id)
	if err != nil {
		return nil, err
	}
	if base.Retirement == nil {
		return nil, nil
	}
	status := base.Retirement.RetirementStatus
	return &status, nil
}

// StartRetirement permanently fences this identity. A fresh recovery snapshot
// scrubs application state but retains all committed request receipts. There is
// no tombstone deletion, restore or identity reuse operation.
func (m *Manager) StartRetirement(ctx context.Context, id ID, request RetirementRequest, actor string) (*RetirementStatus, error) {
	if !validRetirementRequest(request) || !validIdentity(actor) {
		return nil, ErrInvalid
	}
	if _, ok := m.store.(CleanupStore); !ok {
		return nil, ErrUnsupported
	}
	base, etag, err := m.readManifest(ctx, id)
	if err != nil {
		return nil, err
	}
	if base.Retirement != nil {
		if base.Retirement.Request != request {
			return nil, ErrRequestConflict
		}
		status := base.Retirement.RetirementStatus
		return &status, nil
	}
	if base.Version != request.Version || recoveryRevision(base) != request.Revision {
		return nil, ErrConflict
	}
	if base.OwnerID != "" && m.now().Before(base.ExpiresAt) {
		return nil, ErrBusy
	}
	if base.StorageUsage == nil {
		return nil, ErrInventoryPending
	}
	if base.StorageUsage.TotalBytes() > 64<<20 {
		return nil, ErrLimit
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return nil, err
	}
	if state.AlarmAt != nil || len(state.Outbox) != 0 || base.AlarmDelivery != nil || base.OutboxDelivery != nil {
		return nil, ErrBusy
	}
	if base.Epoch == math.MaxUint64 || base.Generation == math.MaxUint64 {
		return nil, ErrLimit
	}
	base.Epoch++
	base.Generation++
	base.OwnerID, base.Token, base.ExpiresAt = "", "", time.Time{}
	if base.Version != 0 {
		state.Data = json.RawMessage(`{}`)
		body, err := json.Marshal(state)
		if err != nil {
			return nil, err
		}
		if len(body) > api.MaxDurableEntitySnapshotBytes {
			return nil, exceeded("snapshot_bytes", api.MaxDurableEntitySnapshotBytes, len(body))
		}
		projected := base.StorageUsage.TotalBytes() - base.StorageUsage.SnapshotBytes + int64(len(body))
		if projected > 64<<20 || base.StorageLimitBytes > 0 && projected > base.StorageLimitBytes {
			return nil, ErrLimit
		}
		key := fmt.Sprintf("%ssnapshots/%d/%s.json", id.prefix(), base.Generation, uuid.NewString())
		version, err := m.store.Put(ctx, key, body, "")
		if err != nil || version == "" {
			return nil, errors.Join(ErrUncertain, err)
		}
		base.SnapshotKey, base.SnapshotHash = key, digest(body)
		usage := *base.StorageUsage
		usage.SnapshotBytes = int64(len(body))
		base.StorageUsage = &usage
	}
	base.Retirement = &retirement{RetirementStatus: RetirementStatus{Request: request, Actor: actor, At: m.now().UTC(), Revision: 1, Phase: "proof", NotBefore: m.now().UTC().Add(api.MaxDurableEntityLease + api.DurableEntityInvokeTimeout)}}
	if err := m.putManifest(ctx, base, etag); err != nil {
		return nil, err
	}
	status := base.Retirement.RetirementStatus
	return &status, nil
}

// StepRetirement authenticates the retained receipt graph in bounded pages,
// removes only proven obsolete immutable objects, then verifies a full listing.
// Delete failures and unknown writes leave the persisted checkpoint unchanged.
func (m *Manager) StepRetirement(ctx context.Context, id ID, operation string, revision uint64) (*RetirementStatus, error) {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return nil, ErrUnsupported
	}
	base, etag, err := m.readManifest(ctx, id)
	if err != nil {
		return nil, err
	}
	r := base.Retirement
	if r == nil || r.Request.OperationID != operation || r.Revision != revision {
		return nil, ErrConflict
	}
	if r.Phase == "complete" {
		status := r.RetirementStatus
		return &status, nil
	}
	if m.now().Before(r.NotBefore) {
		return nil, ErrBusy
	}
	if r.Revision == math.MaxUint64 {
		return nil, ErrLimit
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return nil, err
	}
	if r.Phase == "proof" {
		if r.Proof == nil {
			p, err := m.startInventory(ctx, base, state)
			if err != nil {
				return nil, err
			}
			r.Proof = &p
		}
		if err := m.inventoryJournalPage(ctx, base, r.Proof); err != nil {
			return nil, err
		}
		if r.Proof.LogicalComplete {
			if !sameUsage(r.Proof.Usage, *base.StorageUsage) {
				return nil, ErrCorrupt
			}
			r.Proof = nil
			r.Phase = "cleanup"
		} else {
			r.Proof.Revision = uuid.NewString()
			r.Proof.Checksum = inventoryChecksum(*r.Proof)
		}
	} else {
		page, err := store.ListEntityObjects(ctx, id.prefix(), r.Cursor, api.DurableEntityCleanupPageSize)
		if err != nil {
			return nil, err
		}
		if !validCleanupPage(id, r.Cursor, page) {
			return nil, ErrCorrupt
		}
		seen := map[string]bool{}
		for _, key := range page.Keys {
			if seen[key] {
				return nil, ErrCorrupt
			}
			seen[key] = true
		}
		cache := map[string]journalNode{}
		for _, key := range page.Keys {
			unused, err := m.unusedObject(ctx, base, state, key, cache)
			if err != nil {
				return nil, err
			}
			// Tombstone, checkpoints, indexes and unrecognized keys are retained.
			// Keys at/after the barrier are also retained, including late uploads.
			if !unused {
				continue
			}
			if r.Phase == "verify" {
				r.Phase, r.Cursor = "cleanup", ""
				r.Revision++
				if err := m.putManifest(ctx, base, etag); err != nil {
					return nil, err
				}
				status := r.RetirementStatus
				return &status, nil
			}
			if err := store.DeleteEntityObject(ctx, key); err != nil {
				return nil, errors.Join(ErrUncertain, err)
			}
		}
		r.Cursor = page.NextCursor
		if r.Cursor == "" {
			if r.Phase == "cleanup" {
				r.Phase = "verify"
			} else {
				r.Phase = "complete"
				r.RetainedBytes = base.StorageUsage.TotalBytes()
			}
		}
	}
	r.Revision++
	if err := m.putManifest(ctx, base, etag); err != nil {
		return nil, err
	}
	status := r.RetirementStatus
	return &status, nil
}

// RestartRetirementCleanup discards only an observational cursor. The permanent
// fence, authenticated receipt proof and full quota charge remain intact.
func (m *Manager) RestartRetirementCleanup(ctx context.Context, id ID, operation string, revision uint64) (*RetirementStatus, error) {
	base, etag, err := m.readManifest(ctx, id)
	if err != nil {
		return nil, err
	}
	r := base.Retirement
	if r == nil || r.Request.OperationID != operation || r.Revision != revision || r.Phase != "cleanup" && r.Phase != "verify" {
		return nil, ErrConflict
	}
	if r.Revision == math.MaxUint64 {
		return nil, ErrLimit
	}
	r.Phase, r.Cursor = "cleanup", ""
	r.Revision++
	if err := m.putManifest(ctx, base, etag); err != nil {
		return nil, err
	}
	status := r.RetirementStatus
	return &status, nil
}
