package datarepair

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReplacementExecutor is additive so older executors fail closed for this
// action while remaining compatible with the original plan workflow.
type ReplacementExecutor interface {
	Replace(context.Context, core.Scope, string, string, string) (core.Plan, error)
}

func (h *Handler) replace(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	if !p.CanApply {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "repair_replace_forbidden", "Replacement forbidden", "This identity cannot replace repair plans"))
		return
	}
	in, ok := decode(w, r)
	if !ok {
		return
	}
	if in.Actor != "" || in.OperationID != "" || in.RepairOperationID != "" {
		problem(w, core.ErrInvalid)
		return
	}
	out, err := h.Engine.Replace(r.Context(), p.Scope, r.PathValue("id"), in.Digest, in.PlanID)
	write(w, out, err)
}

func (c *Remote) Replace(ctx context.Context, scope core.Scope, parent, digest, id string) (core.Plan, error) {
	if !validUUID(parent) || !validUUID(id) || parent == id {
		return core.Plan{}, core.ErrInvalid
	}
	p, err := c.call(ctx, scope, http.MethodPost, "/"+url.PathEscape(parent)+"/replace", request{Digest: digest, PlanID: id})
	if err != nil {
		return core.Plan{}, err
	}
	if !replacementMatches(p, parent, digest, id) {
		return core.Plan{}, core.ErrUnavailable
	}
	return p, nil
}

func replacementMatches(p core.Plan, parent, digest, id string) bool {
	return p.ID == id && p.Replacement != nil && p.Replacement.ParentPlanID == parent &&
		p.Replacement.ParentPlanDigest == digest && p.Replacement.Valid(p)
}

func (s *Managed) Replace(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, b, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	var in managedAction
	if !operation.DecodeOperationBody(w, r, &in, api.DataRepairMaxRequestBytes) {
		return
	}
	id := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validUUID(id) || id == op.ID || id == op.DataRepair.SourceOperationID {
		problem(w, core.ErrInvalid)
		return
	}
	if in.Digest != op.DataRepair.PlanDigest {
		problem(w, core.ErrConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	op, p, err := s.replace(ctx, op, b, id, in)
	if err != nil {
		managedError(w, err)
		return
	}
	managedReply(w, r, op, p)
}

func (s *Managed) replace(ctx context.Context, op state.Operation, b Binding, id string, in managedAction) (state.Operation, core.Plan, error) {
	store, repairs, ok := s.store()
	x, supported := b.Executor.(ReplacementExecutor)
	if !ok || !supported {
		return op, core.Plan{}, core.ErrUnavailable
	}
	var err error
	if reserved := op.DataRepair.ReplacementOperationID; reserved != "" {
		if reserved != id || (op.DataRepair.Phase != api.DataRepairReplacing && op.DataRepair.Phase != api.DataRepairReplaced) {
			return op, core.Plan{}, core.ErrConflict
		}
	} else {
		if _, err := store.OperationByID(ctx, op.AccountID, "", id); err == nil {
			return op, core.Plan{}, core.ErrConflict
		} else if !errors.Is(err, state.ErrNotFound) {
			return op, core.Plan{}, err
		}
		op, err = repairs.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, state.DataRepairAdvance{
			ExpectedSequence: in.ExpectedSequence, Phase: api.DataRepairReplacing, ReplacementOperationID: id,
		})
		if err != nil {
			return op, core.Plan{}, err
		}
		s.audit(ctx, op, "replacement_requested")
	}
	// The reservation is durable before the executor call. On any failure it
	// stays fenced, and only this same parent/digest/child identity may resume.
	p, err := x.Replace(ctx, b.Scope, op.DataRepair.PlanID, in.Digest, id)
	if err != nil {
		return op, p, err
	}
	if !replacementMatches(p, op.ID, in.Digest, id) || p.Scope != b.Scope || p.SourceOperationID != op.DataRepair.SourceOperationID {
		return op, core.Plan{}, core.ErrUnavailable
	}
	child, created, err := repairs.CreateDataRepairOperation(ctx, state.DataRepairAdmission{AccountID: op.AccountID, AppID: op.AppID, ID: id, Binding: b.Identity,
		Plan: api.OperationDataRepair{SourceOperationID: p.SourceOperationID, PlanID: p.ID, PlanDigest: p.Digest,
			Candidates: p.Candidates, Conflicts: p.Conflicts, Unavailable: p.Unavailable, PlanExpiresAt: p.ExpiresAt,
			ReplacesPlanID: op.ID, ReplacesPlanDigest: in.Digest}})
	if err == nil && created {
		s.audit(ctx, op, "replaced")
		s.audit(ctx, child, "prepared")
	}
	return child, p, err
}
