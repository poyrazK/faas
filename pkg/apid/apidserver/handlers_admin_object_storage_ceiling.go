package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// postObsObjectStorageCeiling serves POST
// /v1/admin/ops/accounts/{id}/object-storage-ceiling?confirm=true&gib=N&reason=R:
// support grants a paid billing subject, an account or a resource-owning org,
// an object-storage ceiling of N GiB in place of its plan's, or with gib=plan
// returns it to the plan's (ADR-20261101214152). The subject's spend cap still
// tightens the ceiling, so a customer who wants more than the cap pays for
// raises the cap as well.
func (s *server) postObsObjectStorageCeiling(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	grants, ok := s.store.(state.ObjectStorageCeilingGrantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("object storage ceiling grants are not available"))
		return
	}
	ownerID, grantBytes, reason, prob := parseObjectStorageCeilingRequest(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	plan, prob := s.objectStorageBillingSubjectPlan(r.Context(), ownerID)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if grantBytes != nil && !plan.IsPaid() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Free plan storage is not raised",
			"a Free subject's storage is never billed, so its 1 GiB ceiling stays; the customer upgrades first"))
		return
	}
	previous, err := grants.ObjectStorageCeilingGrant(r.Context(), ownerID)
	if err == nil && grantBytes == nil {
		err = grants.ClearObjectStorageCeilingGrant(r.Context(), ownerID)
	} else if err == nil {
		err = grants.SetObjectStorageCeilingGrant(r.Context(), state.ObjectStorageCeilingGrant{
			OwnerID: ownerID, MaxBytes: *grantBytes, Reason: reason, GrantedBy: acct.ID, GrantedAt: time.Now().UTC(),
		})
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not apply the object storage ceiling"))
		return
	}
	s.emitObjectStorageCeilingAudit(r.Context(), acct.ID, ownerID, previous, grantBytes, reason)
	view, err := s.objectStorageCeilingView(r.Context(), ownerID, plan, grantBytes)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the object storage ceiling"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// parseObjectStorageCeilingRequest reads the subject, the ceiling in whole GiB
// (nil for gib=plan) and the audit reason.
func parseObjectStorageCeilingRequest(r *http.Request) (string, *int64, string, *api.Problem) {
	q := r.URL.Query()
	if q.Get("confirm") != "true" {
		return "", nil, "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirm required", "?confirm=true is required for an object storage ceiling change")
	}
	ownerID := r.PathValue("id")
	if _, err := uuid.Parse(ownerID); err != nil {
		return "", nil, "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad account id", "expected UUID")
	}
	reason := q.Get("reason")
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		return "", nil, "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid reason", "reason must match [a-z0-9_]{1,64}")
	}
	if q.Get("gib") == "plan" {
		return ownerID, nil, reason, nil
	}
	gib, err := strconv.ParseInt(q.Get("gib"), 10, 64)
	if err != nil || gib <= 0 || gib > api.MaxObjectStoragePolicyValue>>30 {
		return "", nil, "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid gib", "gib must be a positive whole number of GiB, or plan to return to the plan's ceiling")
	}
	bytes := gib << 30
	return ownerID, &bytes, reason, nil
}

// objectStorageBillingSubjectPlan returns the plan of the account or
// resource-owning org that owns object storage under id.
func (s *server) objectStorageBillingSubjectPlan(ctx context.Context, id string) (api.Plan, *api.Problem) {
	account, err := s.store.AccountByID(ctx, id)
	if err == nil {
		return account.Plan, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return "", api.ErrCapacity("could not read the account")
	}
	org, err := s.store.OrgByID(ctx, id)
	if err == nil && !org.Personal && org.ResourceOwnershipEnabled {
		return org.Plan, nil
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return "", api.ErrCapacity("could not read the organization")
	}
	return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "no account or resource-owning organization has this id")
}

// objectStorageCeilingView reports the plan's ceiling, the grant and the
// ceiling admission now enforces with the subject's spend cap.
func (s *server) objectStorageCeilingView(ctx context.Context, ownerID string, plan api.Plan, grantBytes *int64) (api.ObsObjectStorageCeilingResponse, error) {
	var capCents *int64
	cents, capped, err := s.store.GetAccountOverageCapCents(ctx, ownerID)
	if err != nil {
		return api.ObsObjectStorageCeilingResponse{}, err
	}
	if capped {
		capCents = &cents
	}
	limits, _ := api.LimitsFor(plan)
	return api.ObsObjectStorageCeilingResponse{
		OwnerID: ownerID, Plan: plan, PlanCeilingBytes: limits.ObjectStorage.MaxAccountBytes, GrantBytes: grantBytes,
		CeilingBytes: api.ObjectStorageLimitsFor(plan, grantBytes, capCents).MaxAccountBytes,
	}, nil
}

func (s *server) emitObjectStorageCeilingAudit(ctx context.Context, actor, ownerID string, previous *state.ObjectStorageCeilingGrant, grantBytes *int64, reason string) {
	if s.audit == nil {
		return
	}
	var previousBytes any
	if previous != nil {
		previousBytes = previous.MaxBytes
	}
	var maxBytes any
	if grantBytes != nil {
		maxBytes = *grantBytes
	}
	subject := ownerID
	s.audit.Emit(ctx, "operator.action.object_storage_ceiling", &subject, map[string]any{
		"actor":              actor,
		"target_account_id":  ownerID,
		"max_bytes":          maxBytes,
		"previous_max_bytes": previousBytes,
		"reason":             reason,
	})
}
