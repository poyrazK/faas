package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/egressoverage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Complimentary accounts (ADR-20261111140000): an operator gives an account a
// paid plan without a subscription. meterd neither bills nor holds its usage,
// its spend cap is $0 so it stops at the plan's included amounts, and when the
// optional end date passes apid returns it to Free.

// complimentaryRequest is one parsed operator request. Plan is empty to clear.
type complimentaryRequest struct {
	accountID string
	plan      api.Plan
	reason    string
	expiresAt time.Time
}

// postObsAccountComplimentary serves POST
// /v1/admin/ops/accounts/{id}/complimentary?confirm=true&plan=P&reason=R[&until=RFC3339]:
// plan hobby, pro or scale makes the account complimentary on that plan,
// plan=none returns it to Free.
func (s *server) postObsAccountComplimentary(w http.ResponseWriter, r *http.Request, operator state.Account) {
	if allowed, prob := s.adminAllows(operator); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	store, ok := s.store.(state.ComplimentaryAccountStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("complimentary accounts are not available"))
		return
	}
	req, prob := parseComplimentaryRequest(r, time.Now().UTC())
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	acct, err := s.store.AccountByID(r.Context(), req.accountID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "no account has this id"))
		return
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the account"))
		return
	}
	var view api.ObsComplimentaryResponse
	if req.plan == "" {
		view, prob = s.clearComplimentary(r.Context(), store, acct)
	} else {
		view, prob = s.setComplimentary(r.Context(), store, acct, operator.ID, req)
	}
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	s.emitComplimentaryAudit(r.Context(), operator.ID, acct, req)
	writeJSON(w, http.StatusOK, view)
}

// setComplimentary refuses an account with a subscription (it is already
// billed), then records the row, moves the plan and holds the spend cap at $0.
func (s *server) setComplimentary(ctx context.Context, store state.ComplimentaryAccountStore, acct state.Account, operatorID string, req complimentaryRequest) (api.ObsComplimentaryResponse, *api.Problem) {
	if acct.StripeSubscriptionItem != "" {
		return api.ObsComplimentaryResponse{}, api.NewProblem(http.StatusConflict, api.CodeConflict, "Account has a subscription",
			"a subscribed account is billed by its subscription; cancel it before making the account complimentary")
	}
	row := state.ComplimentaryAccount{AccountID: acct.ID, Plan: req.plan, Reason: req.reason, GrantedBy: operatorID,
		GrantedAt: time.Now().UTC(), ExpiresAt: req.expiresAt}
	if err := store.SetComplimentaryAccount(ctx, row); err != nil {
		return api.ObsComplimentaryResponse{}, api.ErrCapacity("could not record the complimentary account")
	}
	if err := s.setAccountPlan(ctx, acct, req.plan); err != nil {
		return api.ObsComplimentaryResponse{}, api.ErrCapacity("could not change the plan")
	}
	zero := int64(0)
	if err := s.store.UpdateAccountOverageCapCents(ctx, acct.ID, &zero); err != nil {
		return api.ObsComplimentaryResponse{}, api.ErrCapacity("could not hold the spend cap at $0")
	}
	if err := s.endOverageElections(ctx, acct); err != nil {
		return api.ObsComplimentaryResponse{}, api.ErrCapacity("could not turn off overage")
	}
	view := api.ObsComplimentaryResponse{AccountID: acct.ID, Plan: req.plan, Complimentary: true, Reason: req.reason}
	if !req.expiresAt.IsZero() {
		at := req.expiresAt
		view.ExpiresAt = &at
	}
	return view, nil
}

// clearComplimentary returns a complimentary account to Free with no spend
// cap. An account that is not complimentary is left as it is.
func (s *server) clearComplimentary(ctx context.Context, store state.ComplimentaryAccountStore, acct state.Account) (api.ObsComplimentaryResponse, *api.Problem) {
	current, err := store.ComplimentaryAccount(ctx, acct.ID)
	if err != nil {
		return api.ObsComplimentaryResponse{}, api.ErrCapacity("could not read the complimentary account")
	}
	if current == nil {
		return api.ObsComplimentaryResponse{AccountID: acct.ID, Plan: acct.Plan}, nil
	}
	if err := s.endComplimentary(ctx, store, acct); err != nil {
		return api.ObsComplimentaryResponse{}, api.ErrCapacity("could not end the complimentary account")
	}
	return api.ObsComplimentaryResponse{AccountID: acct.ID, Plan: api.PlanFree}, nil
}

// endComplimentary moves the account to Free, clears the $0 spend cap it was
// held to and removes the row, in that order, so a failure leaves the row for
// the next attempt. An account that subscribed meanwhile keeps its plan.
func (s *server) endComplimentary(ctx context.Context, store state.ComplimentaryAccountStore, acct state.Account) error {
	if acct.StripeSubscriptionItem == "" {
		if err := s.setAccountPlan(ctx, acct, api.PlanFree); err != nil {
			return fmt.Errorf("return to free: %w", err)
		}
		if err := s.store.UpdateAccountOverageCapCents(ctx, acct.ID, nil); err != nil {
			return fmt.Errorf("clear spend cap: %w", err)
		}
	}
	return store.ClearComplimentaryAccount(ctx, acct.ID)
}

// parseComplimentaryRequest reads the account, plan (none clears), the audit
// reason and the optional end date, which must be in the future.
func parseComplimentaryRequest(r *http.Request, now time.Time) (complimentaryRequest, *api.Problem) {
	q := r.URL.Query()
	bad := func(title, detail string) (complimentaryRequest, *api.Problem) {
		return complimentaryRequest{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, title, detail)
	}
	if q.Get("confirm") != "true" {
		return bad("confirm required", "?confirm=true is required for a complimentary account change")
	}
	req := complimentaryRequest{accountID: r.PathValue("id"), reason: q.Get("reason")}
	if _, err := uuid.Parse(req.accountID); err != nil {
		return bad("Bad account id", "expected UUID")
	}
	if len(req.reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(req.reason) {
		return bad("invalid reason", "reason must match [a-z0-9_]{1,64}")
	}
	switch plan := api.Plan(q.Get("plan")); {
	case plan == "none":
	case plan.IsPaid():
		req.plan = plan
	default:
		return bad("invalid plan", "plan must be hobby, pro, scale, or none to end the complimentary plan")
	}
	if until := q.Get("until"); until != "" {
		if req.plan == "" {
			return bad("until without a plan", "until applies only when making an account complimentary")
		}
		at, err := time.Parse(time.RFC3339, until)
		if err != nil || !at.After(now) {
			return bad("invalid until", "until must be an RFC 3339 time in the future")
		}
		req.expiresAt = at.UTC()
	}
	return req, nil
}

func (s *server) emitComplimentaryAudit(ctx context.Context, operatorID string, acct state.Account, req complimentaryRequest) {
	if s.audit == nil {
		return
	}
	var expires any
	if !req.expiresAt.IsZero() {
		expires = req.expiresAt
	}
	plan := string(req.plan)
	if plan == "" {
		plan = "none"
	}
	subject := acct.ID
	s.audit.Emit(ctx, "operator.action.complimentary", &subject, map[string]any{
		"actor":             operatorID,
		"target_account_id": acct.ID,
		"from_plan":         string(acct.Plan),
		"plan":              plan,
		"expires_at":        expires,
		"reason":            req.reason,
	})
}

// endOverageElections turns off the overage a complimentary account had
// elected: egress past the allowance and managed PostgreSQL compute past it.
// Neither counts toward the spend cap, so the $0 cap alone would not stop
// them, and they would run unbilled.
func (s *server) endOverageElections(ctx context.Context, acct state.Account) error {
	if elections, ok := s.store.(state.EgressOverageStore); ok {
		if _, err := egressoverage.Set(ctx, elections, acct.ID, acct.Plan, false, time.Now().UTC()); err != nil && !errors.Is(err, egressoverage.ErrNotElectable) {
			return fmt.Errorf("egress overage: %w", err)
		}
	}
	if s.managedPostgres != nil {
		if err := s.managedPostgres.SetOverage(ctx, acct.ResourceOwnerID(), false); err != nil {
			return fmt.Errorf("managed postgres overage: %w", err)
		}
	}
	return nil
}

// complimentaryRefusal refuses a customer change that would let a
// complimentary account spend past its plan's included amounts: raising the
// spend cap or electing overage (ADR-20261111140000). A read failure refuses
// too, so an unbilled account never runs past its allowance.
func (s *server) complimentaryRefusal(ctx context.Context, accountID string) *api.Problem {
	store, ok := s.store.(state.ComplimentaryAccountStore)
	if !ok {
		return nil
	}
	comp, err := store.ComplimentaryAccount(ctx, accountID)
	if err != nil {
		return api.ErrInternal("could not read the account's plan")
	}
	if comp == nil {
		return nil
	}
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Complimentary plan",
		fmt.Sprintf("this account has a complimentary %s plan, which stops at the plan's included amounts; overage and spend cap changes need a subscription", comp.Plan)).
		WithHint("Subscribe to a paid plan with gregale billing upgrade, or contact support.")
}
