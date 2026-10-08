package main

// adr: 774
import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) directoryRuntimeConsents(w http.ResponseWriter, r *http.Request) (globaldirectory.RuntimeConsentStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !globaldirectory.ValidSlug(r.PathValue("slug")) || !globaldirectory.ValidID(r.PathValue("consent_id")) {
		api.WriteProblem(w, api.ErrValidation("use an original global slug and canonical consent UUID"))
		return nil, false
	}
	journal, ok := s.globalDirectory.(globaldirectory.RuntimeConsentStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("directory runtime consent is unavailable"))
	}
	return journal, ok
}

func (s *server) putGlobalRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	j, ok := s.directoryRuntimeConsents(w, r)
	if !ok {
		return
	}
	if s.globalQuotaAuthority == nil {
		api.WriteProblem(w, api.ErrCapacity("directory runtime authority is unavailable"))
		return
	}
	var req api.PutGlobalRuntimeConsentRequest
	if !decodeGlobalRuntimeConsentRequest(w, r, &req) {
		return
	}
	if req.Region == "" || !api.ValidComputeRegion(req.Region) || !api.ValidAPIKeyFormat(req.RegionalAPIKey) || req.ExpiresAt.IsZero() || !req.ExpiresAt.Equal(req.ExpiresAt.Truncate(time.Microsecond)) {
		api.WriteProblem(w, api.ErrValidation("send an explicit selected region, microsecond expiry and request-only regional admin key"))
		return
	}
	svc := globaldirectory.RuntimeConsentService{Store: s.globalDirectory, Journal: j, IssuerID: s.globalQuotaAuthority.Signer.IssuerID(), Verifier: s.globalVerifier}
	out, err := svc.Setup(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("consent_id"), req.Region, req.ExpiresAt, req.RegionalAPIKey)
	s.writeGlobalRuntimeConsent(w, r, acct, out, err)
}

func (s *server) getGlobalRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	j, ok := s.directoryRuntimeConsents(w, r)
	if !ok {
		return
	}
	out, err := j.RuntimeConsent(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("consent_id"))
	s.writeGlobalRuntimeConsent(w, r, acct, out, err)
}

func (s *server) revokeGlobalRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	j, ok := s.directoryRuntimeConsents(w, r)
	if !ok {
		return
	}
	var req api.RevokeGlobalRuntimeConsentRequest
	if !decodeGlobalRuntimeConsentRequest(w, r, &req) {
		return
	}
	if !api.ValidAPIKeyFormat(req.RegionalAPIKey) {
		api.WriteProblem(w, api.ErrValidation("supply a request-only admin key for the original regional account"))
		return
	}
	svc := globaldirectory.RuntimeConsentService{Journal: j, Verifier: s.globalVerifier}
	out, err := svc.Revoke(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("consent_id"), req.RegionalAPIKey)
	s.writeGlobalRuntimeConsent(w, r, acct, out, err)
}

func (s *server) writeGlobalRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account, out globaldirectory.RuntimeConsentRecord, err error) {
	var bound *globaldirectory.RuntimeConsentLimitError
	switch {
	case err == nil:
		if r.Method != http.MethodGet {
			s.audit.Emit(r.Context(), "global_runtime_consent.changed", &acct.ID, map[string]any{"consent_id": out.Consent.ID, "global_app_id": out.Consent.GlobalAppID, "status": out.Status})
		}
		writeJSON(w, http.StatusOK, globalRuntimeConsentResponse(out))
	case errors.As(err, &bound):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Runtime consent limit reached", "Revoke an unused consent or wait for expiry.").WithLimit(bound.Limit, bound.Observed).WithDocs("https://gregale.dev/docs/global-app-directory#runtime-delivery-consent"))
	case errors.Is(err, globaldirectory.ErrRuntimeConsentUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeGlobalAppTargetVerificationFailed, "Runtime consent unavailable", "Review the original consent and retry with its original regional admin key; renewal requires a new consent UUID."))
	default:
		s.globalDirectoryError(w, r, acct, err)
	}
}

func globalRuntimeConsentResponse(r globaldirectory.RuntimeConsentRecord) api.GlobalRuntimeConsentResponse {
	c, b := r.Consent, r.Consent.Binding
	status := r.Status
	if status != globaldirectory.RuntimeConsentRevoked && !time.UnixMicro(c.ExpiresAtMicros).After(time.Now()) {
		status = "expired"
	}
	return api.GlobalRuntimeConsentResponse{ID: c.ID, IssuerID: c.IssuerID, GlobalAppID: c.GlobalAppID, OwnerID: c.OwnerID, Slug: r.Slug,
		Binding:   api.GlobalRuntimeConsentBindingResponse{CellID: b.CellID, Region: b.Region, CellGeneration: b.CellGeneration, RegionalAccountID: b.RegionalAccountID, RegionalAppID: b.RegionalAppID, RegionalAppSlug: b.RegionalAppSlug, APIBase: b.APIBase},
		ExpiresAt: time.UnixMicro(c.ExpiresAtMicros), CreatedAt: r.CreatedAt, Status: status, RegionalCreatedAt: r.RegionalCreatedAt, RegionalRevokedAt: r.RegionalRevokedAt}
}

func decodeGlobalRuntimeConsentRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.GlobalDirectoryRequestMaxBytes))
	valid := err == nil && uniqueRuntimeConsentRequest(raw, r.Method == http.MethodPut)
	if valid {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		valid = d.Decode(out) == nil && d.Decode(&struct{}{}) == io.EOF
	}
	if !valid {
		api.WriteProblem(w, api.ErrValidation("send one bounded runtime consent object with each field exactly once"))
	}
	return valid
}

func uniqueRuntimeConsentRequest(raw []byte, setup bool) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err := d.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		if key != "regional_api_key" && (!setup || (key != "region" && key != "expires_at")) {
			return false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	last, err := d.Token()
	return err == nil && last == json.Delim('}') && d.Decode(&struct{}{}) == io.EOF
}
