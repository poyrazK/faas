package main

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	maxBillingBusinessNameBytes = 256
	maxBillingAddressBytes      = 1000
	maxBillingTaxIDBytes        = 128
)

// updateAccountBillingInfo implements PATCH /v1/account/billing. The
// request is a partial update, but the store receives a complete normalized
// snapshot so clearing a field is explicit and idempotent.
func (s *server) updateAccountBillingInfo(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateAccountBillingInfoRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.BusinessName == nil && req.BillingAddress == nil && req.TaxID == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "at least one billing field is required"))
		return
	}
	businessName, address, taxID := acct.BusinessName, acct.BillingAddress, acct.TaxID
	changed := make([]string, 0, 3)
	if req.BusinessName != nil {
		var ok bool
		businessName, ok = normalizeBillingField(*req.BusinessName, maxBillingBusinessNameBytes)
		if !ok {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "business_name must be valid UTF-8 and at most 256 bytes"))
			return
		}
		changed = append(changed, "business_name")
	}
	if req.BillingAddress != nil {
		var ok bool
		address, ok = normalizeBillingField(*req.BillingAddress, maxBillingAddressBytes)
		if !ok {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "billing_address must be valid UTF-8 and at most 1000 bytes"))
			return
		}
		changed = append(changed, "billing_address")
	}
	if req.TaxID != nil {
		var ok bool
		taxID, ok = normalizeBillingField(*req.TaxID, maxBillingTaxIDBytes)
		if !ok {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "tax_id must be valid UTF-8 and at most 128 bytes"))
			return
		}
		changed = append(changed, "tax_id")
	}
	updated, err := s.store.UpdateAccountBillingInfo(r.Context(), acct.ID, businessName, address, taxID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "account not found"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update billing information"))
		return
	}
	// Providers may implement the optional sync surface. Local state remains
	// the source of truth when a provider is temporarily unavailable; the
	// next update or billing bootstrap retries the same complete snapshot.
	if syncer, ok := s.billingProvider.(billing.CustomerBillingInfoProvider); ok && updated.ProviderCustomerID != "" {
		if err := syncer.SyncCustomerBillingInfo(r.Context(), updated); err != nil {
			s.log.Warn("billing customer identity sync failed", "account", updated.ID, "err", err)
		}
	}
	s.audit.Emit(r.Context(), "account.billing_info_updated", &updated.ID, map[string]any{
		"changed_fields": changed,
	})
	writeJSON(w, http.StatusOK, s.accountResponse(r.Context(), updated, r))
}

func normalizeBillingField(raw string, maxBytes int) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", true
	}
	return value, utf8.ValidString(value) && len(value) <= maxBytes
}
