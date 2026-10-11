package socket

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

// Permanent bounded receipts both fence admission and charge recovery bytes.
// They are never evicted to make room for another identity.
type QuotaRetirement struct {
	EntityKey     string    `json:"entity_key"`
	OperationID   string    `json:"operation_id"`
	Actor         string    `json:"actor"`
	At            time.Time `json:"at"`
	Phase         string    `json:"phase"`
	RetainedBytes int64     `json:"retained_bytes"`
	ReleasedBytes int64     `json:"released_bytes"`
}
type RetirementReader interface {
	RetirementStatus(context.Context, durableentity.ID) (*durableentity.RetirementStatus, error)
}
type QuotaRetirementPreview struct {
	ReservedBytes int64            `json:"reserved_bytes"`
	Ready         bool             `json:"ready"`
	Retirement    *QuotaRetirement `json:"retirement,omitempty"`
}

func retired(r quotaRecord, id durableentity.ID) bool {
	key := entityKey(id)
	for _, item := range r.Retirements {
		if item.EntityKey == key {
			return true
		}
	}
	return false
}
func pendingRetirements(r quotaRecord) bool {
	for _, item := range r.Retirements {
		if item.Phase != "complete" {
			return true
		}
	}
	return false
}
func recoveryBytes(r quotaRecord) int64 {
	var bytes int64
	for _, item := range r.Retirements {
		if item.Phase == "complete" {
			bytes += item.RetainedBytes
		}
	}
	return bytes
}
func validQuotaRetirements(r quotaRecord) bool {
	if r.Schema < 3 {
		return len(r.Retirements) == 0
	}
	if r.Schema == 3 && len(r.Retirements) == 0 || len(r.Retirements) > retirementCapacity(r) {
		return false
	}
	seen, operations := map[string]bool{}, map[string]bool{}
	for _, item := range r.Retirements {
		if !validScopeKey(item.EntityKey) || !sessionUUID(item.OperationID) || !grantIdentity(item.Actor) || item.At.IsZero() || seen[item.EntityKey] || operations[item.OperationID] || item.RetainedBytes < 0 || item.RetainedBytes > 64<<20 || item.ReleasedBytes < 0 || item.ReleasedBytes > 64<<20 || item.Phase != "held" && item.Phase != "complete" {
			return false
		}
		if item.Phase == "held" && (item.RetainedBytes != 0 || item.ReleasedBytes != 0) {
			return false
		}
		found := false
		for _, e := range r.Entities {
			if e.Key == item.EntityKey {
				found = true
			}
			if e.Key == item.EntityKey && item.Phase == "complete" {
				return false
			}
		}
		if item.Phase == "held" && !found {
			return false
		}
		seen[item.EntityKey], operations[item.OperationID] = true, true
	}
	return true
}
func (q *Quotas) PreviewRetirement(ctx context.Context, id durableentity.ID) (QuotaRetirementPreview, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return QuotaRetirementPreview{}, err
	}
	r, _, err := q.read(ctx, scopeKey(scope))
	if err != nil {
		return QuotaRetirementPreview{}, err
	}
	prune(&r, q.now())
	out := QuotaRetirementPreview{Ready: !registryCleanupHoldsWrites(r) && r.Schema >= 2 && r.Limits == q.Limits && r.Migration != nil && r.Migration.Phase == "complete" && len(r.Connections) == 0 && retirementSpace(r, entityKey(id))}
	for _, e := range r.Entities {
		if e.Key == entityKey(id) {
			out.ReservedBytes = e.Bytes
		}
	}
	out.Ready = out.Ready && out.ReservedBytes > 0
	for _, item := range r.Retirements {
		if item.EntityKey == entityKey(id) {
			copy := item
			out.Retirement = &copy
			out.Ready = false
		}
	}
	return out, nil
}

// TrackRetirement follows the manifest fence. Failure leaves the full original
// reservation charged. Schema 3 rejects older quota writers before reclamation.
func (q *Quotas) TrackRetirement(ctx context.Context, id durableentity.ID, operation string, reader RetirementReader) (*QuotaRetirement, error) {
	if reader == nil || !sessionUUID(operation) {
		return nil, durableentity.ErrInvalid
	}
	scope, err := scopeFor(id)
	if err != nil {
		return nil, err
	}
	var result *QuotaRetirement
	err = q.mutate(ctx, scopeKey(scope), &scope, func(r *quotaRecord, _ time.Time) error {
		for _, item := range r.Retirements {
			if item.EntityKey == entityKey(id) {
				if item.OperationID != operation {
					return durableentity.ErrRequestConflict
				}
				copy := item
				result = &copy
				return nil
			}
			if item.OperationID == operation {
				return durableentity.ErrRequestConflict
			}
		}
		if r.Schema < 2 || r.Migration == nil || r.Migration.Phase != "complete" {
			return durableentity.ErrInventoryPending
		}
		if len(r.Connections) != 0 {
			return durableentity.ErrBusy
		}
		if !retirementSpace(*r, entityKey(id)) {
			return durableentity.ErrLimit
		}
		reserved := false
		for _, e := range r.Entities {
			if e.Key == entityKey(id) {
				reserved = true
			}
		}
		if !reserved {
			return durableentity.ErrInventoryPending
		}
		status, err := reader.RetirementStatus(ctx, id)
		if err != nil {
			return err
		}
		if status == nil || status.Request.OperationID != operation {
			return durableentity.ErrConflict
		}
		item := QuotaRetirement{EntityKey: entityKey(id), OperationID: operation, Actor: status.Actor, At: status.At, Phase: "held"}
		r.Schema = max(r.Schema, 3)
		r.Retirements = append(r.Retirements, item)
		for i := range r.Grants {
			if r.Grants[i].Entity == id {
				r.Grants[i].Revoked = true
			}
		}
		result = &item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ReclaimRetirement replaces a full cap reservation with authenticated recovery
// bytes only after a permanent tombstone and completed cleanup are observed.
// The receipt and removal commit together; retries cannot reclaim twice.
func (q *Quotas) ReclaimRetirement(ctx context.Context, id durableentity.ID, operation string, reader RetirementReader) (*QuotaRetirement, error) {
	if reader == nil || !sessionUUID(operation) {
		return nil, durableentity.ErrInvalid
	}
	scope, err := scopeFor(id)
	if err != nil {
		return nil, err
	}
	var result *QuotaRetirement
	err = q.mutate(ctx, scopeKey(scope), &scope, func(r *quotaRecord, _ time.Time) error {
		for i := range r.Retirements {
			item := &r.Retirements[i]
			if item.EntityKey != entityKey(id) {
				continue
			}
			if item.OperationID != operation {
				return durableentity.ErrRequestConflict
			}
			if item.Phase == "complete" {
				copy := *item
				result = &copy
				return nil
			}
			status, err := reader.RetirementStatus(ctx, id)
			if err != nil {
				return err
			}
			if status == nil || status.Request.OperationID != operation || status.Phase != "complete" {
				return durableentity.ErrInventoryPending
			}
			if status.RetainedBytes < 0 || status.RetainedBytes > r.Limits.EntityBytes {
				return durableentity.ErrInventoryPending
			}
			var reserved int64
			entities := r.Entities[:0]
			for _, e := range r.Entities {
				if e.Key == item.EntityKey {
					reserved = e.Bytes
				} else {
					entities = append(entities, e)
				}
			}
			if reserved < status.RetainedBytes {
				return durableentity.ErrInventoryPending
			}
			r.Entities = entities
			item.Phase, item.RetainedBytes, item.ReleasedBytes = "complete", status.RetainedBytes, reserved-status.RetainedBytes
			copy := *item
			result = &copy
			return nil
		}
		return durableentity.ErrNotFound
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
