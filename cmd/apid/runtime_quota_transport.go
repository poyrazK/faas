package main

// adr: 644
import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type RuntimeQuotaTransportConfig struct {
	DirectoryIssuerID string `toml:"directory_issuer_id"`
	PrivateKeyPath    string `toml:"private_key_path"`
	TrustBundlePath   string `toml:"trust_bundle_path"`
	RegionalAPIBase   string `toml:"regional_api_base"`
}

func (s *server) configureRuntimeQuotaTransport(ctx context.Context, cfg RuntimeQuotaTransportConfig, pool *pgxpool.Pool) error {
	if cfg == (RuntimeQuotaTransportConfig{}) {
		return nil
	}
	if s.globalDirectory != nil {
		return s.configureDirectoryQuotaAuthority(ctx, cfg)
	}
	if pool == nil || cfg.DirectoryIssuerID != "" || cfg.PrivateKeyPath != "" || !api.ValidGlobalDirectoryAPIBase(cfg.RegionalAPIBase) || cfg.TrustBundlePath == "" {
		return fmt.Errorf("apid: regional quota transport requires a bound database, canonical HTTPS origin and trust bundle: %w", runtimequota.ErrTrust)
	}
	cell, err := (&sqlc.Queries{}).ReadControlPlaneCell(ctx, pool)
	if err != nil || cell.CellID != s.controlPlaneCell.ID || cell.ComputeRegion != s.controlPlaneCell.Region {
		return fmt.Errorf("apid: quota receiver cell differs from its database: %w", runtimequota.ErrBinding)
	}
	anchors, err := runtimequota.LoadTrustAnchors(cfg.TrustBundlePath)
	if err != nil {
		return err
	}
	directoryTrusted := false
	for _, anchor := range anchors {
		directoryTrusted = directoryTrusted || anchor.Role == runtimequota.Directory
	}
	if !directoryTrusted {
		return runtimequota.ErrTrust
	}
	trust, err := runtimequota.NewTrust(anchors)
	if err != nil {
		return err
	}
	s.regionalQuotaTrust, s.regionalQuotaAPIBase = trust, cfg.RegionalAPIBase
	return nil
}

func (s *server) configureDirectoryQuotaAuthority(ctx context.Context, cfg RuntimeQuotaTransportConfig) error {
	store, ok := s.globalDirectory.(globaldirectory.RuntimeQuotaStore)
	issuerStore, issuerOK := s.globalDirectory.(globaldirectory.RuntimeQuotaIssuerStore)
	if !ok || !issuerOK || cfg.RegionalAPIBase != "" || s.controlPlaneCell.ID != "" || s.controlPlaneCell.Region != "" {
		return runtimequota.ErrTrust
	}
	keys, err := runtimequota.LoadAuthorityKeys(runtimequota.KeyConfig{PrivateKeyPath: cfg.PrivateKeyPath, TrustBundlePath: cfg.TrustBundlePath}, runtimequota.Directory, cfg.DirectoryIssuerID)
	if err != nil {
		return err
	}
	if err := issuerStore.BindRuntimeQuotaIssuer(ctx, keys.Signer.IssuerID()); err != nil {
		return fmt.Errorf("apid: immutable directory quota issuer: %w", err)
	}
	s.globalQuotaAuthority = &globaldirectory.RuntimeQuotaAuthority{Store: store, Signer: keys.Signer, Directories: keys.Trust, Cells: keys.Trust}
	return nil
}

func (s *server) deliverGlobalRuntimeQuota(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.runtimeQuotaStore(w, r); !ok {
		return
	}
	if s.globalQuotaAuthority == nil {
		api.WriteProblem(w, api.ErrCapacity("global runtime quota transport is unavailable"))
		return
	}
	var req struct {
		RegionalAPIKey string `json:"regional_api_key"`
		ConsentID      string `json:"runtime_consent_id,omitempty"`
	}
	if !decodeGlobalDirectoryRequest(w, r, &req) {
		return
	}
	if region := r.PathValue("region"); region == "" || !api.ValidComputeRegion(region) {
		api.WriteProblem(w, api.ErrValidation("use a canonical selected region"))
		return
	}
	if req.ConsentID != "" && (!globaldirectory.ValidID(req.ConsentID) || req.RegionalAPIKey != "") {
		api.WriteProblem(w, api.ErrValidation("supply either original runtime consent or a request-only regional key"))
		return
	}
	receipt, err := s.deliverRuntimeQuotaRequest(r, acct, req.RegionalAPIKey, req.ConsentID)
	if err != nil {
		s.runtimeQuotaTransportError(w, r, acct, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *server) deliverRuntimeQuotaRequest(r *http.Request, acct state.Account, key, consentID string) (runtimequota.DeliveryReceipt, error) {
	if consentID != "" {
		return s.globalQuotaAuthority.DeliverWithConsent(r.Context(), s.globalVerifier, globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("allocation_id"), r.PathValue("region"), consentID)
	}
	return s.globalQuotaAuthority.Deliver(r.Context(), s.globalVerifier, globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("allocation_id"), r.PathValue("region"), key)
}

func (s *server) acceptCellRuntimeQuota(w http.ResponseWriter, r *http.Request, acct state.Account) {
	delivery, ok := s.readQuotaDelivery(w, r)
	if !ok {
		return
	}
	app, _ := r.Context().Value(cellDeploymentAppContextKey{}).(state.App)
	b := delivery.Binding
	if delivery.ConsentID != "" {
		api.WriteProblem(w, api.ErrValidation("use the delegated quota delivery route for runtime consent"))
		return
	}
	// Personal binding proofs omit the organization UUID. The middleware
	// resolves personal ownership; the journal rechecks it under local locks.
	if b.RegionalAccountID != quotaLocalUUID(acct.ID) || b.RegionalAccountID != quotaLocalUUID(app.AccountID) || b.RegionalOrgID != "" || b.RegionalAppID != quotaLocalUUID(app.ID) || b.RegionalAppSlug != app.Slug || app.ComputeRegion != b.Region {
		s.notFound(w, "no such regional quota binding")
		return
	}
	authority, ok := s.cellQuotaAuthority(w, delivery.Binding)
	if !ok {
		return
	}
	grant, err := authority.Directories.VerifyGrant(delivery.Authority, delivery.Binding)
	if err != nil || grant.AllocationID != r.PathValue("allocation_id") {
		api.WriteProblem(w, api.ErrValidation("quota authority differs from request path"))
		return
	}
	receipt, err := authority.AcceptGrant(r.Context(), delivery.Authority)
	s.writeQuotaDeliveryReceipt(w, r, receipt, err, false)
}

// The memory store uses compact UUIDs; signed wire identities are canonical.
// Local authorization still resolves the original row before this conversion.
func quotaLocalUUID(raw string) string {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return ""
	}
	return id.String()
}

// Retirement only narrows a signed original allocation. It requires pinned
// directory authority instead of a mutable app/key that may have been collected.
func (s *server) retireCellRuntimeQuota(w http.ResponseWriter, r *http.Request) {
	delivery, ok := s.readQuotaDelivery(w, r)
	if !ok {
		return
	}
	if delivery.ConsentID != "" {
		api.WriteProblem(w, api.ErrValidation("retirement requires only original signed directory authority"))
		return
	}
	authority, ok := s.cellQuotaAuthority(w, delivery.Binding)
	if !ok {
		return
	}
	retirement, err := authority.Directories.VerifyRetirement(delivery.Authority, delivery.Binding)
	if err != nil || retirement.Grant.AllocationID != r.PathValue("allocation_id") {
		api.WriteProblem(w, api.ErrValidation("quota retirement differs from request path"))
		return
	}
	receipt, err := authority.AcceptRetirement(r.Context(), delivery.Authority)
	s.writeQuotaDeliveryReceipt(w, r, receipt, err, true)
}

func (s *server) readQuotaDelivery(w http.ResponseWriter, r *http.Request) (runtimequota.Delivery, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.regionalQuotaAPIBase == "" {
		api.WriteProblem(w, api.ErrCapacity("regional runtime quota receiver is unavailable"))
		return runtimequota.Delivery{}, false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.RegionalRuntimeQuotaDeliveryMaxBytes))
	d, decodeErr := runtimequota.DecodeDelivery(raw)
	if err != nil || decodeErr != nil || !globaldirectory.ValidID(r.PathValue("allocation_id")) {
		api.WriteProblem(w, api.ErrValidation("send a bounded canonical quota delivery"))
		return runtimequota.Delivery{}, false
	}
	if d.Binding.CellID != s.controlPlaneCell.ID || d.Binding.Region != s.controlPlaneCell.Region || d.Binding.APIBase != s.regionalQuotaAPIBase || d.Binding.CellID != r.PathValue("cell_id") || d.Binding.RegionalAppID != r.PathValue("app_id") {
		s.notFound(w, "no such regional quota binding")
		return runtimequota.Delivery{}, false
	}
	return d, true
}

func (s *server) cellQuotaAuthority(w http.ResponseWriter, binding runtimequota.Binding) (state.RegionalRuntimeQuotaAuthority, bool) {
	store, ok := s.store.(state.RegionalRuntimeQuotaStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("regional runtime quota journal is unavailable"))
	}
	return state.RegionalRuntimeQuotaAuthority{Store: store, Directories: s.regionalQuotaTrust, Binding: binding}, ok
}

func (s *server) writeQuotaDeliveryReceipt(w http.ResponseWriter, r *http.Request, receipt state.RegionalRuntimeQuotaReceipt, err error, retired bool) {
	if err != nil {
		s.runtimeQuotaTransportError(w, r, state.Account{}, err)
		return
	}
	if receipt.Grant.AllocationID != r.PathValue("allocation_id") {
		api.WriteProblem(w, api.ErrValidation("quota allocation differs from request path"))
		return
	}
	out := runtimequota.DeliveryReceipt{Retirement: receipt.RetirementEnvelope}
	if !retired {
		out.Grant = receipt.GrantEnvelope
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) runtimeQuotaTransportError(w http.ResponseWriter, r *http.Request, acct state.Account, err error) {
	switch {
	case errors.Is(err, globaldirectory.ErrRuntimeConsentUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeGlobalAppTargetVerificationFailed, "Directory runtime consent unavailable", "The original directory consent must be active and match the allocation; renewal requires a new consent UUID."))
	case errors.Is(err, state.ErrRegionalRuntimeConsent):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Runtime consent unavailable", "The original regional runtime delivery consent is missing, expired, revoked or differs from the signed allocation."))
	case errors.Is(err, runtimequota.ErrInvalid), errors.Is(err, runtimequota.ErrBinding), errors.Is(err, runtimequota.ErrTrust):
		api.WriteProblem(w, api.ErrValidation("quota authority or binding could not be verified"))
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrRegionalRuntimeQuotaOrder), errors.Is(err, globaldirectory.ErrRuntimeQuotaRetired):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Quota identity conflicts", "The allocation is retired or conflicts with the retained regional stream."))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such regional quota binding")
	default:
		s.globalRuntimeQuotaError(w, r, acct, err)
	}
}
