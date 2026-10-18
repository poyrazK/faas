package datarepair

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

// ResolutionExecutor is additive: an older executor cannot prove closure.
type ResolutionExecutor interface {
	Resolve(context.Context, core.Scope, string, string, string, string) (core.Plan, error)
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	if !p.CanApply {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "repair_resolve_forbidden", "Resolution forbidden", "This identity cannot close repair apply authority"))
		return
	}
	in, ok := decode(w, r)
	if !ok {
		return
	}
	if in.OperationID != "" || in.PlanID != "" || (in.Actor != "" && !h.Coordinator) {
		problem(w, core.ErrInvalid)
		return
	}
	actor := p.Actor
	if h.Coordinator && in.Actor != "" {
		actor = in.Actor
	}
	out, err := h.Engine.Resolve(r.Context(), p.Scope, r.PathValue("id"), in.Digest, in.RepairOperationID, actor)
	write(w, out, err)
}

func (c *Remote) Resolve(ctx context.Context, scope core.Scope, id, digest, repairID, actor string) (core.Plan, error) {
	if !validUUID(id) || !validUUID(repairID) {
		return core.Plan{}, core.ErrInvalid
	}
	p, err := c.call(ctx, scope, http.MethodPost, "/"+url.PathEscape(id)+"/resolve", request{Digest: digest, RepairOperationID: repairID, Actor: actor})
	if err != nil {
		return core.Plan{}, err
	}
	if !p.ResolvedApply(id, digest, repairID) {
		return core.Plan{}, core.ErrUnavailable
	}
	return p, nil
}

func (s *Managed) Resolve(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, b, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	var in managedAction
	if !operation.DecodeOperationBody(w, r, &in, api.DataRepairMaxRequestBytes) {
		return
	}
	if in.Digest != op.DataRepair.PlanDigest {
		problem(w, core.ErrConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	actor := acct.ID
	if principal, ok := httpkit.PrincipalFrom(r); ok && principal.Key != nil {
		actor = principal.Key.ID
	}
	op, p, err := s.resolve(ctx, op, b, in.ExpectedSequence, actor)
	if err != nil {
		managedError(w, err)
		return
	}
	managedReply(w, r, op, p)
}

func (s *Managed) resolve(ctx context.Context, op state.Operation, b Binding, sequence int64, actor string) (state.Operation, core.Plan, error) {
	x, supported := b.Executor.(ResolutionExecutor)
	if !supported {
		return op, core.Plan{}, core.ErrUnavailable
	}
	if op.DataRepair.Approval == nil || op.DataRepair.Approval.Decision != api.OperationApprovalApproved {
		return op, core.Plan{}, core.ErrConflict
	}
	switch op.DataRepair.Phase {
	case api.DataRepairResolving, api.DataRepairClosed, api.DataRepairSucceeded:
		// Same plan/digest/operation replay may carry the original sequence.
	case api.DataRepairReplacing, api.DataRepairReplaced:
		if op.DataRepair.ApplyClosedAt == nil {
			return op, core.Plan{}, core.ErrConflict
		}
	default:
		if op.LatestSequence != sequence {
			return op, core.Plan{}, core.ErrConflict
		}
		var err error
		op, err = s.advance(ctx, op, api.DataRepairResolving, nil)
		if err != nil {
			return op, core.Plan{}, err
		}
	}
	// Reservation precedes the source call. An uncertain response keeps apply
	// and replacement blocked; no timeout or negative receipt lookup releases it.
	p, err := x.Resolve(ctx, b.Scope, op.DataRepair.PlanID, op.DataRepair.PlanDigest, op.ID, actor)
	if err != nil {
		return op, p, err
	}
	return s.finishResolution(ctx, op, p)
}

func (s *Managed) finishResolution(ctx context.Context, op state.Operation, p core.Plan) (state.Operation, core.Plan, error) {
	if !matchesPlan(op, p) || !p.ResolvedApply(op.DataRepair.PlanID, op.DataRepair.PlanDigest, op.ID) {
		return op, core.Plan{}, core.ErrUnavailable
	}
	if p.ApplyResolution == nil {
		if !receiptMatches(op, p) {
			return op, p, core.ErrConflict
		}
		if op.DataRepair.Phase == api.DataRepairSucceeded {
			return op, p, nil
		}
		got, err := s.advance(ctx, op, api.DataRepairSucceeded, p.AppliedAt)
		return got, p, err
	}
	at := p.ApplyResolution.ResolvedAt
	if op.DataRepair.ApplyClosedAt != nil && op.DataRepair.ApplyClosedAt.Equal(at) &&
		(op.DataRepair.Phase == api.DataRepairClosed || op.DataRepair.Phase == api.DataRepairReplacing || op.DataRepair.Phase == api.DataRepairReplaced) {
		return op, p, nil
	}
	_, store, ok := s.store()
	if !ok {
		return op, p, core.ErrUnavailable
	}
	got, err := store.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, state.DataRepairAdvance{
		ExpectedSequence: op.LatestSequence, Phase: api.DataRepairClosed, ApplyClosedAt: &at,
	})
	if err == nil {
		s.audit(ctx, got, api.DataRepairClosed)
	}
	return got, p, err
}
