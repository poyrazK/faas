package datarepair

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

type ManagedHost interface {
	httpkit.AppLoader
	Store() state.Store
	EmitAudit(context.Context, string, *string, map[string]any)
}

// Binding is installed by the operator. Identity pins both the executor
// generation and its destination; a changed binding cannot resume old work.
type Binding struct {
	Scope              core.Scope
	Identity           string
	Executor           Executor
	ScheduledRehearsal bool
}

func BindingIdentity(scope core.Scope, generation, origin string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s", scope.AccountID, scope.AppID, scope.Environment, scope.PlatformTenantID, generation, origin))))
}

type Managed struct {
	Host     ManagedHost
	Bindings []Binding
}

type ManagedResponse struct {
	Operation   api.OperationResponse `json:"operation"`
	Plan        core.Plan             `json:"plan"`
	StatusURL   string                `json:"status_url"`
	TimelineURL string                `json:"timeline_url"`
	ApprovalURL string                `json:"approval_url"`
	ReviewURL   string                `json:"review_url"`
}

func repairScope(op state.Operation) core.Scope {
	return core.Scope{AccountID: op.AccountID, AppID: op.AppID, Environment: op.Scope, PlatformTenantID: op.PlatformTenantID}
}

func (s *Managed) binding(op state.Operation) (Binding, error) {
	for _, b := range s.Bindings {
		if b.Scope == repairScope(op) && b.Executor != nil && (op.DataRepair == nil || b.Identity == op.DataRepairBinding) {
			return b, nil
		}
	}
	return Binding{}, core.ErrUnavailable
}

func (s *Managed) store() (state.OperationStore, state.DataRepairOperationStore, bool) {
	a, ok := s.Host.Store().(state.OperationStore)
	b, other := s.Host.Store().(state.DataRepairOperationStore)
	return a, b, ok && other
}

func (s *Managed) owned(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Operation, Binding, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.Operation{}, Binding{}, false
	}
	store, _, ok := s.store()
	if !ok {
		problem(w, core.ErrUnavailable)
		return state.Operation{}, Binding{}, false
	}
	op, err := store.OperationByID(r.Context(), acct.ID, "", r.PathValue("id"))
	if err != nil {
		operation.WriteOperationError(w, err)
		return op, Binding{}, false
	}
	if op.AppID != app.ID || op.DataRepair == nil {
		operation.WriteOperationError(w, state.ErrNotFound)
		return op, Binding{}, false
	}
	b, err := s.binding(op)
	if err != nil {
		problem(w, err)
		return op, b, false
	}
	return op, b, true
}

func managedReply(w http.ResponseWriter, r *http.Request, op state.Operation, p core.Plan) {
	base := "/v1/apps/" + r.PathValue("slug") + "/operations/" + op.ID
	httpkit.WriteJSON(w, http.StatusOK, ManagedResponse{Operation: op.OperationResponse, Plan: p, StatusURL: base, TimelineURL: base + "/timeline", ApprovalURL: base + "/approvals/repair", ReviewURL: "/v1/apps/" + r.PathValue("slug") + "/data-repair/review?operation=" + op.ID})
}

type managedCreate struct {
	SourceOperationID string `json:"source_operation_id"`
}
type managedAction struct {
	Digest           string `json:"digest"`
	ExpectedSequence int64  `json:"expected_sequence"`
}

func validUUID(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil && id.String() == raw
}

func (s *Managed) Create(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var in managedCreate
	if !operation.DecodeOperationBody(w, r, &in, api.DataRepairMaxRequestBytes) {
		return
	}
	id := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validUUID(id) || !validUUID(in.SourceOperationID) {
		problem(w, core.ErrInvalid)
		return
	}
	s.create(w, r, acct, app.ID, id, in.SourceOperationID)
}

func (s *Managed) create(w http.ResponseWriter, r *http.Request, acct state.Account, appID, id, source string) {
	store, repairs, ok := s.store()
	if !ok {
		problem(w, core.ErrUnavailable)
		return
	}
	if prior, err := store.OperationByID(r.Context(), acct.ID, "", id); err == nil {
		if prior.AppID != appID || prior.DataRepair == nil || prior.DataRepair.SourceOperationID != source {
			operation.WriteOperationError(w, state.ErrOperationInputConflict)
			return
		}
		b, e := s.binding(prior)
		if e != nil {
			problem(w, e)
			return
		}
		p, e := b.Executor.Get(r.Context(), b.Scope, prior.DataRepair.PlanID)
		if e != nil {
			problem(w, e)
			return
		}
		if !matchesPlan(prior, p) {
			problem(w, core.ErrConflict)
			return
		}
		managedReply(w, r, prior, p)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		operation.WriteOperationError(w, err)
		return
	}
	op, err := store.OperationByID(r.Context(), acct.ID, "", source)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	if op.AppID != appID || op.DataRepair != nil {
		operation.WriteOperationError(w, state.ErrNotFound)
		return
	}
	b, err := s.binding(op)
	if err != nil {
		problem(w, err)
		return
	}
	if !op.State.Terminal() {
		problem(w, core.ErrConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	p, err := b.Executor.Prepare(ctx, b.Scope, source, id)
	if err != nil {
		problem(w, err)
		return
	}
	if p.ID != id || p.SourceOperationID != source || p.Scope != b.Scope || p.Replacement != nil {
		problem(w, core.ErrUnavailable)
		return
	}
	op, created, err := repairs.CreateDataRepairOperation(ctx, state.DataRepairAdmission{AccountID: acct.ID, AppID: appID, ID: id, Binding: b.Identity, Plan: api.OperationDataRepair{SourceOperationID: source, PlanID: p.ID, PlanDigest: p.Digest, Candidates: p.Candidates, Conflicts: p.Conflicts, Unavailable: p.Unavailable, PlanExpiresAt: p.ExpiresAt}})
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	if created {
		s.audit(ctx, op, "prepared")
	}
	managedReply(w, r, op, p)
}

func (s *Managed) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, b, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	p, err := b.Executor.Get(r.Context(), b.Scope, op.DataRepair.PlanID)
	if err != nil {
		problem(w, err)
		return
	}
	if !matchesPlan(op, p) {
		problem(w, core.ErrConflict)
		return
	}
	managedReply(w, r, op, p)
}

func matchesPlan(op state.Operation, p core.Plan) bool {
	return op.DataRepair != nil && p.ID == op.DataRepair.PlanID && p.Digest == op.DataRepair.PlanDigest && p.SourceOperationID == op.DataRepair.SourceOperationID && p.Scope == repairScope(op)
}

func (s *Managed) Action(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, b, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	var in managedAction
	if !operation.DecodeOperationBody(w, r, &in, api.DataRepairMaxRequestBytes) {
		return
	}
	if in.Digest != op.DataRepair.PlanDigest || in.ExpectedSequence != op.LatestSequence {
		problem(w, core.ErrConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	var p core.Plan
	var err error
	switch r.PathValue("action") {
	case "rehearse":
		op, p, err = s.rehearse(ctx, op, b)
	case "cancel-rehearsal":
		op, p, err = s.cancelRehearsal(ctx, op, b)
	case "apply":
		op, p, err = s.apply(ctx, op, b)
	case "reconcile":
		op, p, err = s.reconcile(ctx, op, b)
	default:
		err = core.ErrNotFound
	}
	if err != nil {
		managedError(w, err)
		return
	}
	managedReply(w, r, op, p)
}

func managedError(w http.ResponseWriter, err error) {
	// State errors and executor errors have distinct stable, redacted contracts.
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrOperationExpired) {
		operation.WriteOperationError(w, err)
		return
	}
	problem(w, err)
}

func (s *Managed) advance(ctx context.Context, op state.Operation, phase string, at *time.Time) (state.Operation, error) {
	_, store, ok := s.store()
	if !ok {
		return state.Operation{}, core.ErrUnavailable
	}
	got, err := store.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, state.DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: phase, AppliedAt: at})
	if err == nil {
		s.audit(ctx, got, phase)
	}
	return got, err
}

func (s *Managed) audit(ctx context.Context, op state.Operation, phase string) {
	s.Host.EmitAudit(ctx, "app.data_repair_"+phase, &op.AccountID, map[string]any{"app_id": op.AppID, "operation_id": op.ID, "source_operation_id": op.DataRepair.SourceOperationID, "plan_id": op.DataRepair.PlanID, "plan_digest": op.DataRepair.PlanDigest})
}

func (s *Managed) rehearse(ctx context.Context, op state.Operation, b Binding) (state.Operation, core.Plan, error) {
	if b.ScheduledRehearsal {
		return s.enqueueRehearsal(ctx, op, b)
	}
	op, err := s.advance(ctx, op, api.DataRepairRehearsing, nil)
	if err != nil {
		return op, core.Plan{}, err
	}
	p, err := b.Executor.Rehearse(ctx, b.Scope, op.DataRepair.PlanID, op.DataRepair.PlanDigest)
	if err == nil && (!matchesPlan(op, p) || p.Rehearsal == nil || !p.Rehearsal.Passed || p.Rehearsal.PlanDigest != p.Digest || p.RehearsedAt == nil) {
		err = core.ErrUnavailable
	}
	if err != nil {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = s.advance(clean, op, api.DataRepairPrepared, nil)
		return op, p, err
	}
	op, err = s.advance(ctx, op, api.DataRepairAwaitingApproval, nil)
	return op, p, err
}

func (s *Managed) apply(ctx context.Context, op state.Operation, b Binding) (state.Operation, core.Plan, error) {
	if op.DataRepair.Phase == api.DataRepairSucceeded {
		return s.reconcile(ctx, op, b)
	}
	if op.DataRepair.Approval == nil || op.DataRepair.Approval.Decision != api.OperationApprovalApproved {
		return op, core.Plan{}, core.ErrConflict
	}
	op, err := s.advance(ctx, op, api.DataRepairApplying, nil)
	if err != nil {
		return op, core.Plan{}, err
	}
	// A receipt lookup always precedes approval/apply, including expiry and
	// uncertain-response recovery. A committed receipt never needs reapproval.
	p, err := b.Executor.Get(ctx, b.Scope, op.DataRepair.PlanID)
	if err == nil && !matchesPlan(op, p) {
		err = core.ErrConflict
	}
	if err == nil && p.AppliedAt == nil {
		_, err = b.Executor.Approve(ctx, b.Scope, p.ID, p.Digest, op.DataRepair.Approval.Actor.ID)
		if err == nil {
			p, err = b.Executor.Apply(ctx, b.Scope, p.ID, p.Digest, op.ID, "gregale-managed-repair-v1")
		}
	}
	if err == nil && receiptMatches(op, p) {
		got, saveErr := s.advance(ctx, op, api.DataRepairSucceeded, p.AppliedAt)
		if saveErr == nil {
			return got, p, nil
		}
	}
	// Preserve ambiguity even if the application commit succeeded but the
	// control-plane write failed or the request was disconnected.
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = s.advance(clean, op, api.DataRepairUnknown, nil)
	return op, p, core.ErrOutcomeUnknown
}

func receiptMatches(op state.Operation, p core.Plan) bool {
	return matchesPlan(op, p) && p.AppliedAt != nil && p.RepairOperationID != nil && *p.RepairOperationID == op.ID &&
		op.DataRepair.Approval != nil && p.ApprovedBy != nil && *p.ApprovedBy == op.DataRepair.Approval.Actor.ID
}

func (s *Managed) reconcile(ctx context.Context, op state.Operation, b Binding) (state.Operation, core.Plan, error) {
	p, err := b.Executor.Get(ctx, b.Scope, op.DataRepair.PlanID)
	if err != nil {
		return op, p, err
	}
	if !matchesPlan(op, p) {
		return op, p, core.ErrConflict
	}
	if op.DataRepair.Phase == api.DataRepairResolving && p.ResolvedApply(op.DataRepair.PlanID, op.DataRepair.PlanDigest, op.ID) {
		return s.finishResolution(ctx, op, p)
	}
	if receiptMatches(op, p) {
		if op.DataRepair.Phase == api.DataRepairSucceeded {
			return op, p, nil
		}
		op, err = s.advance(ctx, op, api.DataRepairSucceeded, p.AppliedAt)
		return op, p, err
	}
	if p.AppliedAt != nil {
		return op, p, core.ErrConflict
	}
	if b.ScheduledRehearsal && op.DataRepair.Phase == api.DataRepairRehearsing {
		// Only schedd may release a scheduled claim after native retirement.
		return op, p, nil
	}
	if op.DataRepair.Phase == api.DataRepairApplying && time.Since(op.UpdatedAt) > time.Duration(api.DataRepairTimeoutSeconds)*time.Second {
		op, err = s.advance(ctx, op, api.DataRepairUnknown, nil)
		return op, p, err
	}
	if time.Now().After(p.ExpiresAt) && (op.DataRepair.Phase == api.DataRepairPrepared || op.DataRepair.Phase == api.DataRepairRehearsing || op.DataRepair.Phase == api.DataRepairAwaitingApproval || op.DataRepair.Phase == api.DataRepairApproved) {
		op, err = s.advance(ctx, op, api.DataRepairFailed, nil)
		return op, p, err
	}
	// Rehearsal is read-only on the source. A disconnected coordinator can
	// recover its evidence, or release its claim after the execution bound.
	if op.DataRepair.Phase == api.DataRepairRehearsing {
		if p.RehearsedAt != nil && p.Rehearsal != nil && p.Rehearsal.Passed && p.Rehearsal.PlanDigest == p.Digest {
			op, err = s.advance(ctx, op, api.DataRepairAwaitingApproval, nil)
		} else if time.Since(op.UpdatedAt) > time.Duration(api.DataRepairTimeoutSeconds)*time.Second {
			op, err = s.advance(ctx, op, api.DataRepairPrepared, nil)
		}
	}
	// Absence of a receipt is never proof of rollback. Applying/unknown work
	// stays unresolved; an explicit apply may reuse only this same fixed ID.
	return op, p, err
}
