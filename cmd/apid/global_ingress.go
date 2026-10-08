package main

// adr: 658
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/globalingress"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type GlobalIngressPublisherConfig struct {
	ManifestPath string `toml:"manifest_path"`
}

func (s *server) configureGlobalIngressPublisher(cfg GlobalIngressPublisherConfig) error {
	if cfg.ManifestPath == "" {
		return nil
	}
	if !filepath.IsAbs(cfg.ManifestPath) || filepath.Clean(cfg.ManifestPath) != cfg.ManifestPath || s.globalQuotaAuthority == nil || (s.controlPlaneCell.ID != "" || s.controlPlaneCell.Region != "") {
		return runtimequota.ErrTrust
	}
	if _, ok := s.globalDirectory.(globaldirectory.IngressStore); !ok {
		return runtimequota.ErrTrust
	}
	if err := s.globalQuotaAuthority.Directories.CheckSigner(s.globalQuotaAuthority.Signer, runtimequota.Directory); err != nil {
		return err
	}
	if err := globalingress.CheckPublishDirectory(cfg.ManifestPath); err != nil {
		return err
	}
	s.globalIngressManifest = cfg.ManifestPath
	return nil
}
func (s *server) runGlobalIngressPublisher(ctx context.Context) {
	if s.globalIngressManifest == "" || s.globalQuotaAuthority == nil {
		return
	}
	store, ok := s.globalDirectory.(globaldirectory.IngressStore)
	if !ok {
		return
	}
	p := globaldirectory.IngressPublisher{Store: store, Signer: s.globalQuotaAuthority.Signer, Trust: s.globalQuotaAuthority.Directories, Verifier: s.globalVerifier, Export: func(raw []byte) error { return globalingress.WriteManifest(s.globalIngressManifest, raw) }, OnError: func() { s.log.Warn("global ingress publication incomplete; lease remains bounded") }}
	_ = p.Run(ctx)
}

// Internal daemon protocol: a purpose-bound directory signature authenticates
// the request; no customer token is sent or retained. The original delegated
// receipt/consent and current tenant are read again before observing admission.
func (s *server) regionalIngressStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Authorization") != "" || s.regionalQuotaAPIBase == "" {
		api.WriteProblem(w, api.ErrValidation("use a signed directory observation"))
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.GlobalIngressProbeMaxBytes))
	e, decodeErr := runtimequota.DecodeServingProbe(raw)
	p, verifyErr := s.regionalQuotaTrust.VerifyServingProbe(e)
	now := time.Now().UTC()
	if err != nil || decodeErr != nil || verifyErr != nil || p.IssuedAtMicros > now.UnixMicro() || p.ExpiresAtMicros <= now.UnixMicro() {
		api.WriteProblem(w, api.ErrValidation("send a fresh bounded directory observation"))
		return
	}
	b := p.Grant.Binding
	if b.CellID != s.controlPlaneCell.ID || b.Region != s.controlPlaneCell.Region || b.APIBase != s.regionalQuotaAPIBase || p.Origin != api.RegionalAppOrigin(b.RegionalAppSlug, s.domain) {
		s.notFound(w, "no such regional observation")
		return
	}
	status, err := s.observeRegionalIngress(r.Context(), p)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("regional serving observation unavailable"))
		return
	}
	body, err := json.Marshal(status)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("regional serving observation unavailable"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
func (s *server) observeRegionalIngress(ctx context.Context, p runtimequota.ServingProbe) (runtimequota.ServingStatus, error) {
	status := runtimequota.ServingStatus{Probe: p, Code: "unavailable"}
	b := p.Grant.Binding
	journal, ok := s.store.(state.RegionalRuntimeQuotaStore)
	if !ok {
		return status, runtimequota.ErrTrust
	}
	consents, ok := s.store.(state.RegionalRuntimeConsentStore)
	if !ok {
		return status, runtimequota.ErrTrust
	}
	receipt, err := journal.RegionalRuntimeQuota(ctx, b.RegionalAppID, p.Grant.AllocationID)
	if err != nil {
		return status, err
	}
	head, err := journal.RegionalRuntimeQuotaHead(ctx, b.RegionalAppID)
	if err != nil {
		return status, err
	}
	consent, err := consents.RegionalRuntimeConsent(ctx, b.RegionalAccountID, b.CellID, b.RegionalAppID, p.ConsentID)
	if err != nil {
		return status, err
	}
	now := time.Now().UTC()
	if receipt.Grant != p.Grant || head.Grant != p.Grant || receipt.Retirement != nil || receipt.ConsentID != p.ConsentID || !consent.Consent.Matches(p.Grant) || consent.RevokedAt != nil || consent.Consent.ExpiresAtMicros <= now.UnixMicro() {
		return status, state.ErrRegionalRuntimeConsent
	}
	app, err := s.store.AppByID(ctx, b.RegionalAppID)
	if err != nil {
		return status, err
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return status, err
	}
	if quotaLocalUUID(app.AccountID) != b.RegionalAccountID || app.OrgID != "" && !s.personalRegionalOrg(ctx, app.OrgID, app.AccountID) || app.Slug != b.RegionalAppSlug || app.ComputeRegion != b.Region || !acct.MayDeploy() || !acct.EmailVerified() || acct.Plan != p.Grant.Policy.Plan || api.NormalizeAppVisibility(app.Visibility) != api.AppVisibilityPublic || app.Status != state.AppActive {
		return status, state.ErrConflict
	}
	err = state.CheckRegionalRuntimeQuotaAdmission(ctx, journal, b.RegionalAppID)
	if errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
		status.Code = "admission_frozen"
	} else if err != nil {
		return status, err
	} else {
		// A matching accepted head must never bypass the central native admission
		// guard. Positive publication requires its future all-path qualification,
		// including shared authorization/billing; a receipt is insufficient.
		return status, state.ErrRegionalRuntimeQuotaFrozen
	}
	now = time.Now().UTC()
	status.ObservedAtMicros = now.UnixMicro()
	status.ExpiresAtMicros = min(p.ExpiresAtMicros, consent.Consent.ExpiresAtMicros)
	if status.Validate(p) != nil {
		return status, runtimequota.ErrInvalid
	}
	return status, nil
}

func (s *server) personalRegionalOrg(ctx context.Context, orgID, accountID string) bool {
	org, err := s.store.OrgByID(ctx, orgID)
	return err == nil && org.Personal && org.PersonalOwnerAccountID != nil && quotaLocalUUID(*org.PersonalOwnerAccountID) == quotaLocalUUID(accountID) && org.Status == state.OrgStatusActive && !org.DeletedPending
}
