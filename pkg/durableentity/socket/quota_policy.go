package socket

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

var ErrPolicyMismatch = errors.New("durable quota policy differs from node configuration")

type QuotaEntityCapUpdater interface {
	RaiseQuotaEntityLimit(context.Context, durableentity.ID, int64) error
}
type QuotaPolicyRequest struct {
	OperationID               string       `json:"operation_id"`
	ExpectedRevision          uint64       `json:"expected_revision"`
	ExpectedMigrationID       string       `json:"expected_migration_id"`
	ExpectedMigrationRevision uint64       `json:"expected_migration_revision"`
	MigrationID               string       `json:"migration_id"`
	Limits                    TenantLimits `json:"limits"`
}
type QuotaPolicyChange struct {
	Revision uint64             `json:"revision"`
	Actor    string             `json:"actor"`
	At       time.Time          `json:"at"`
	From     TenantLimits       `json:"from"`
	Request  QuotaPolicyRequest `json:"request"`
}
type QuotaPolicyStatus struct {
	Revision    uint64              `json:"revision"`
	Limits      TenantLimits        `json:"limits"`
	NodeMatches bool                `json:"node_matches"`
	Changes     []QuotaPolicyChange `json:"changes"`
}
type QuotaPolicyPreview struct {
	Revision               uint64       `json:"revision"`
	Current                TenantLimits `json:"current"`
	Proposed               TenantLimits `json:"proposed"`
	CurrentReservedBytes   int64        `json:"current_reserved_bytes"`
	ProjectedReservedBytes int64        `json:"projected_reserved_bytes"`
	AdditionalBytes        int64        `json:"additional_bytes"`
	ByteShortfall          int64        `json:"byte_shortfall"`
	NodeMatches            bool         `json:"node_matches"`
	ReadyToApply           bool         `json:"ready_to_apply"`
}

func increaseLimits(from, to TenantLimits) bool {
	return to.valid() && to.Connections >= from.Connections && to.GrantsPerMinute >= from.GrantsPerMinute && to.InvocationsPerMinute >= from.InvocationsPerMinute && to.RetainedBytes >= from.RetainedBytes && to.EntityBytes >= from.EntityBytes
}
func validQuotaPolicy(r quotaRecord) bool {
	if r.Schema == 1 {
		return r.PolicyRevision == 0 && len(r.PolicyChanges) == 0
	}
	if r.PolicyRevision == 0 || r.PolicyRevision > 32 || uint64(len(r.PolicyChanges)) != r.PolicyRevision || r.Migration == nil || r.Migration.Limits != r.Limits {
		return false
	}
	seen := map[string]bool{}
	for i, c := range r.PolicyChanges {
		if c.Revision != uint64(i+1) || c.Request.ExpectedRevision != uint64(i) || !grantIdentity(c.Actor) || c.At.IsZero() || !c.From.valid() || !validPolicyRequest(c.Request) || !increaseLimits(c.From, c.Request.Limits) || seen[c.Request.OperationID] {
			return false
		}
		if i > 0 && c.From != r.PolicyChanges[i-1].Request.Limits {
			return false
		}
		seen[c.Request.OperationID] = true
	}
	return r.PolicyChanges[len(r.PolicyChanges)-1].Request.Limits == r.Limits
}
func validPolicyRequest(p QuotaPolicyRequest) bool {
	return sessionUUID(p.OperationID) && sessionUUID(p.MigrationID) && sessionUUID(p.ExpectedMigrationID) && p.ExpectedMigrationRevision > 0 && p.ExpectedRevision < 32 && p.Limits.valid()
}
func (q *Quotas) PolicyStatus(ctx context.Context, id durableentity.ID) (QuotaPolicyStatus, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return QuotaPolicyStatus{}, err
	}
	r, _, err := q.read(ctx, scopeKey(scope))
	if err != nil {
		return QuotaPolicyStatus{}, err
	}
	return QuotaPolicyStatus{r.PolicyRevision, r.Limits, r.Limits == q.Limits, r.PolicyChanges}, nil
}
func policyPreview(r quotaRecord, p QuotaPolicyRequest, node TenantLimits) (QuotaPolicyPreview, error) {
	preview := QuotaPolicyPreview{Revision: r.PolicyRevision, Current: r.Limits, Proposed: p.Limits, NodeMatches: node == p.Limits, CurrentReservedBytes: retainedBytes(r)}
	if !validPolicyRequest(p) || !increaseLimits(r.Limits, p.Limits) || r.Schema >= 2 && r.Limits == p.Limits {
		return preview, durableentity.ErrInvalid
	}
	for _, c := range r.PolicyChanges {
		if c.Request.MigrationID == p.MigrationID || c.Request.ExpectedMigrationID == p.MigrationID {
			return preview, durableentity.ErrInvalid
		}
	}
	preview.ProjectedReservedBytes = grantBytes(r) + recoveryBytes(r)
	for _, e := range r.Entities {
		preview.ProjectedReservedBytes += max(e.Bytes, p.Limits.EntityBytes)
	}
	preview.AdditionalBytes = preview.ProjectedReservedBytes - preview.CurrentReservedBytes
	preview.ByteShortfall = max(0, preview.ProjectedReservedBytes-p.Limits.RetainedBytes)
	preview.ReadyToApply = !registryCleanupActive(r) && preview.ByteShortfall == 0 && preview.NodeMatches && !pendingRetirements(r) && r.PolicyRevision == p.ExpectedRevision && r.Migration != nil && r.Migration.Phase != "complete" && r.Migration.ID == p.ExpectedMigrationID && r.Migration.Revision == p.ExpectedMigrationRevision && p.MigrationID != r.Migration.ID && len(r.Connections) == 0
	return preview, nil
}
func (q *Quotas) PreviewPolicy(ctx context.Context, id durableentity.ID, p QuotaPolicyRequest) (QuotaPolicyPreview, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return QuotaPolicyPreview{}, err
	}
	r, _, err := q.read(ctx, scopeKey(scope))
	if err != nil {
		return QuotaPolicyPreview{}, err
	}
	prune(&r, q.now())
	return policyPreview(r, p, q.Limits)
}

// ApplyPolicy commits the policy, durable audit receipt and replacement migration
// together. Schema 2 rejects legacy writers instead of letting them drop policy
// fields. Only definite CAS conflicts retry; unknown outcomes require inspection.
func (q *Quotas) ApplyPolicy(ctx context.Context, id durableentity.ID, p QuotaPolicyRequest, actor string) (*QuotaPolicyChange, error) {
	scope, err := scopeFor(id)
	if err != nil || !validPolicyRequest(p) || !grantIdentity(actor) {
		return nil, durableentity.ErrInvalid
	}
	for attempt := 0; attempt < 8; attempt++ {
		r, version, err := q.read(ctx, scopeKey(scope))
		if err != nil {
			return nil, err
		}
		for _, c := range r.PolicyChanges {
			if c.Request.OperationID == p.OperationID {
				if c.Request != p {
					return nil, durableentity.ErrRequestConflict
				}
				return &c, nil
			}
		}
		if registryCleanupActive(r) {
			return nil, ErrRegistryCleanupPending
		}
		prune(&r, q.now())
		preview, err := policyPreview(r, p, q.Limits)
		if err != nil {
			return nil, err
		}
		if r.PolicyRevision != p.ExpectedRevision || r.Migration == nil || r.Migration.ID != p.ExpectedMigrationID || r.Migration.Revision != p.ExpectedMigrationRevision {
			return nil, durableentity.ErrConflict
		}
		if !preview.NodeMatches {
			return nil, ErrPolicyMismatch
		}
		if !preview.ReadyToApply {
			return nil, durableentity.ErrInventoryPending
		}
		change := QuotaPolicyChange{Revision: r.PolicyRevision + 1, Actor: actor, At: q.now(), From: r.Limits, Request: p}
		r.Schema = max(r.Schema, 2)
		r.PolicyRevision = change.Revision
		r.PolicyChanges = append(r.PolicyChanges, change)
		r.Limits = p.Limits
		r.Migration = &QuotaMigration{ID: p.MigrationID, Revision: 1, Phase: "backfill", NotBefore: q.now().Add(api.MaxDurableEntityLease + api.DurableEntityInvokeTimeout), Limits: p.Limits}
		body, err := q.marshalQuota(ctx, scopeKey(scope), r)
		if err != nil {
			return nil, err
		}
		if len(body) > maxQuotaRecordBytes {
			return nil, durableentity.ErrLimit
		}
		next, err := q.Store.Put(ctx, "tenant-quotas/v1/"+scopeKey(scope), body, version)
		if errors.Is(err, durableentity.ErrConflict) {
			continue
		}
		if err != nil || next == "" {
			return nil, errors.Join(durableentity.ErrUncertain, err)
		}
		return &change, nil
	}
	return nil, durableentity.ErrBusy
}
