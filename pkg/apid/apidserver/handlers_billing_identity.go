package apidserver

import (
	"context"
	"errors"
	"fmt"
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
// request is a partial update; omitted fields are preserved atomically.
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
	patch, changed, prob := normalizeAccountBillingPatch(req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writer, ok := s.store.(state.AccountBillingInfoPatchStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("billing information updates are unavailable"))
		return
	}
	updated, err := writer.PatchAccountBillingInfo(r.Context(), acct.ID, patch)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "account not found"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update billing information"))
		return
	}
	s.syncCustomerBillingInfo(r.Context(), updated)
	s.audit.Emit(r.Context(), "account.billing_info_updated", &updated.ID, map[string]any{
		"changed_fields": changed,
	})
	writeJSON(w, http.StatusOK, s.accountResponse(r.Context(), updated, r))
}

// Local state remains authoritative on a provider outage. Resolve the active
// provider's binding before syncing; the legacy cache may name another provider
// or be empty after a partially completed checkout.
func (s *server) syncCustomerBillingInfo(ctx context.Context, acct state.Account) {
	syncer, ok := s.billingProvider.(billing.CustomerBillingInfoProvider)
	if !ok {
		return
	}
	billingAcct, err := s.accountForActiveBillingProvider(ctx, acct)
	if err == nil && billingAcct.ProviderCustomerID != "" {
		err = syncer.SyncCustomerBillingInfo(ctx, billingAcct)
	}
	if err != nil {
		s.log.Warn("billing customer identity sync failed", "account", acct.ID, "err", err)
	}
}

func normalizeAccountBillingPatch(req api.UpdateAccountBillingInfoRequest) (state.AccountBillingInfoPatch, []string, *api.Problem) {
	var patch state.AccountBillingInfoPatch
	changed := make([]string, 0, 3)
	for _, field := range []struct {
		name   string
		input  *string
		output **string
		limit  int
	}{
		{"business_name", req.BusinessName, &patch.BusinessName, maxBillingBusinessNameBytes},
		{"billing_address", req.BillingAddress, &patch.BillingAddress, maxBillingAddressBytes},
		{"tax_id", req.TaxID, &patch.TaxID, maxBillingTaxIDBytes},
	} {
		if field.input == nil {
			continue
		}
		value, ok := normalizeBillingField(*field.input, field.limit)
		if !ok {
			return patch, nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", fmt.Sprintf("%s must be valid UTF-8 and at most %d bytes", field.name, field.limit))
		}
		*field.output = &value
		changed = append(changed, field.name)
	}
	return patch, changed, nil
}

func normalizeBillingField(raw string, maxBytes int) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", true
	}
	return value, utf8.ValidString(value) && len(value) <= maxBytes
}
