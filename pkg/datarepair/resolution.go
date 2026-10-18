package datarepair

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const ResolutionNotCommitted = "not_committed"

// ApplyResolution proves the old plan cannot commit, including delayed requests.
// A successful resolution instead returns the original atomic apply receipt.
// This metadata never changes the frozen document or its original approval.
type ApplyResolution struct {
	Outcome           string    `json:"outcome"`
	PlanDigest        string    `json:"plan_digest"`
	RepairOperationID string    `json:"repair_operation_id"`
	ResolvedAt        time.Time `json:"resolved_at"`
	ResolvedBy        string    `json:"resolved_by"`
}

func (r ApplyResolution) Valid(p Plan) bool {
	return r.Outcome == ResolutionNotCommitted && digestPattern.MatchString(r.PlanDigest) && r.PlanDigest == p.Digest &&
		validID(r.RepairOperationID) && r.RepairOperationID != p.SourceOperationID && !r.ResolvedAt.IsZero() &&
		r.ResolvedBy != "" && len(r.ResolvedBy) <= api.DataRepairActorMaxBytes && p.AppliedAt == nil && p.RepairOperationID == nil
}

// ResolvedApply validates an authoritative receipt or a durable closure, never
// absence of a receipt. It is also used at remote and hosted trust boundaries.
func (p Plan) ResolvedApply(id, digest, repairID string) bool {
	if p.ID != id || p.Digest != digest || !validID(id) || !validID(repairID) || !digestPattern.MatchString(digest) || !validID(p.SourceOperationID) || p.SourceOperationID == repairID {
		return false
	}
	if p.ApplyResolution != nil {
		return p.ApplyResolution.Valid(p) && p.ApplyResolution.RepairOperationID == repairID
	}
	return p.AppliedAt != nil && !p.AppliedAt.IsZero() && p.RepairOperationID != nil && *p.RepairOperationID == repairID &&
		p.ApprovedBy != nil && *p.ApprovedBy != ""
}

// Resolve atomically returns the committed receipt or closes future apply
// authority. A busy plan, unreachable source, or lost COMMIT response stays
// unresolved; repeating the exact identity recovers the durable source result.
func (e *Engine) Resolve(ctx context.Context, s Scope, id, digest, repairID, actor string) (Plan, error) {
	if err := e.ready(s); err != nil {
		return Plan{}, err
	}
	if !validID(id) || !validID(repairID) || !digestPattern.MatchString(digest) || actor == "" || len(actor) > api.DataRepairActorMaxBytes {
		return Plan{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	p, err := readPlan(ctx, e.Pool, `SELECT gregale_repair.resolve_apply($1::uuid,$2::jsonb,$3,$4::uuid,$5)`, id, scopeJSON(s), digest, repairID, actor)
	if err != nil {
		return Plan{}, err
	}
	if p.Scope != s || !p.ResolvedApply(id, digest, repairID) {
		return Plan{}, ErrUnavailable
	}
	return p, nil
}
