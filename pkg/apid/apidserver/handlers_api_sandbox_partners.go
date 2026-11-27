package apidserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) apiSandboxOwnerResponse(ctx context.Context, app state.App, op state.APISandboxOperation) (api.APISandboxResponse, error) {
	receipt, err := s.apiSandboxResponse(ctx, app, op)
	if err != nil || !app.Manifest.APISandboxPartner {
		return receipt, err
	}
	store, ok := s.store.(state.APISandboxPartnerStore)
	if !ok {
		return api.APISandboxResponse{}, state.ErrNotFound
	}
	partner, err := store.GetAPISandboxPartner(ctx, app.ID)
	if err != nil {
		return api.APISandboxResponse{}, err
	}
	response := partner.Response()
	receipt.Partner = &response
	return receipt, nil
}

func (s *server) loadAPISandboxPartner(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APISandboxPartnerStore, bool) {
	app, ok := s.loadAPISandbox(w, r, acct)
	if !ok {
		return state.App{}, nil, false
	}
	if !app.Manifest.APISandboxPartner {
		s.notFound(w, "no such sandbox partner")
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.APISandboxPartnerStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("partner sandboxes are unavailable"))
		return state.App{}, nil, false
	}
	return app, store, true
}

func (s *server) issueAPISandboxPartnerInvite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, store, ok := s.loadAPISandboxPartner(w, r, acct)
	if ok {
		s.writeAPISandboxPartnerInvite(w, r, acct, app, store)
	}
}

func (s *server) writeAPISandboxPartnerInvite(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.APISandboxPartnerStore) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	now := time.Now().UTC()
	if !state.APISandboxAcceptsRequests(app, now) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeNotFound, "Sandbox ended", "create a fresh partner copy"))
		return
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate a partner invitation"))
		return
	}
	token := api.APISandboxPartnerInvitePrefix + base64.RawURLEncoding.EncodeToString(nonce[:])
	hash := sha256.Sum256([]byte(token))
	partner, err := store.SetAPISandboxPartnerInvite(r.Context(), app.ID, hash[:], now)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Cannot replace this invite", "revoke existing partner access before issuing a new link"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not issue the partner invitation"))
		return
	}
	s.audit.Emit(r.Context(), "api_sandbox.partner_invited", &acct.ID, map[string]any{"app_id": app.ID})
	writeJSON(w, http.StatusCreated, api.APISandboxPartnerInviteResponse{Partner: partner.Response(), Token: token})
}

func (s *server) revokeAPISandboxPartner(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.loadAPISandboxPartner(w, r, acct)
	if !ok {
		return
	}
	if err := store.RevokeAPISandboxPartner(r.Context(), app.ID, time.Now().UTC()); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not revoke partner access"))
		return
	}
	s.audit.Emit(r.Context(), "api_sandbox.partner_revoked", &acct.ID, map[string]any{"app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) redeemAPISandboxPartnerInvite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := apiSandboxPathID(r, "sandbox")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	encoded, ok := strings.CutPrefix(token, api.APISandboxPartnerInvitePrefix)
	if id == "" || !ok || len(encoded) != base64.RawURLEncoding.EncodedLen(32) {
		partnerInviteUnavailable(w)
		return
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(encoded); err != nil {
		partnerInviteUnavailable(w)
		return
	}
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, apiSandboxSlug(id))
	store, ok := s.store.(state.APISandboxPartnerStore)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read the sandbox"))
		return
	}
	if err != nil || !ok || !app.Manifest.APISandboxPartner || !state.APISandboxAcceptsRequests(app, time.Now()) {
		partnerInviteUnavailable(w)
		return
	}
	hash := sha256.Sum256([]byte(token))
	partner, err := store.GetAPISandboxPartner(ctx, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read partner access"))
		return
	}
	if err != nil || !partner.MatchesInvite(hash[:]) {
		partnerInviteUnavailable(w)
		return
	}
	receipt, err := s.getAPISandboxReceipt(ctx, app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not check sandbox readiness"))
		return
	}
	if receipt.Status != state.APISandboxLive {
		detail := "this invite can be accepted once sandbox verification finishes"
		if receipt.Status == state.APISandboxFailed {
			detail = "sandbox setup failed. Ask the owner for a new copy"
		}
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox is not ready", detail))
		return
	}
	if app.Manifest.APISandboxRun {
		runs, ok := s.store.(state.APISandboxRunStore)
		if !ok {
			partnerInviteUnavailable(w)
			return
		}
		run, err := runs.GetAPISandboxRun(ctx, id)
		if err != nil || run.AppID != app.ID || run.Mode != api.APISandboxRunModePartner {
			partnerInviteUnavailable(w)
			return
		}
		if run.Status != api.APISandboxRunAwaitingPartner || run.SandboxDeploymentID != receipt.DeploymentID {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Partner run unavailable", "accept the invite once the run is waiting for partner requests"))
			return
		}
		receipt.PartnerStepCount = run.PartnerStepCount
	}
	s.completeAPISandboxPartnerRedemption(w, r, store, app, receipt, hash[:])
}

func (s *server) completeAPISandboxPartnerRedemption(w http.ResponseWriter, r *http.Request, store state.APISandboxPartnerStore, app state.App, receipt api.APISandboxResponse, hash []byte) {
	ctx := r.Context()
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not check partner access"))
		return
	}
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	consumer, err := s.apiSandboxConsumer(ctx, app, api.APISandboxPartnerConsumerRef, "API sandbox partner")
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not prepare partner access"))
		return
	}
	token, prefix, keyHash, err := api.GenerateConsumerKey()
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate a sandbox key"))
		return
	}
	key, err := store.RedeemAPISandboxPartnerInvite(ctx, state.APISandboxPartnerRedemption{
		App: app, ConsumerID: consumer.ID, KeyName: "partner-" + uuid.NewString(), Prefix: prefix,
		InviteHash: hash, KeyHash: keyHash, At: time.Now().UTC(),
		Quota: state.ConsumerKeyQuota{PerApp: acct.Plan.ConsumerKeysPerApp(), PerAccount: acct.Plan.ConsumerKeysPerAccount()},
	})
	var exceeded *state.ConsumerKeyQuotaError
	switch {
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrConflict):
		partnerInviteUnavailable(w)
	case errors.As(err, &exceeded):
		api.WriteProblem(w, api.ErrConsumerKeyQuota(acct.Plan, exceeded.Scope, exceeded.Limit, exceeded.Observed))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not redeem the invitation"))
	default:
		s.audit.Emit(ctx, "api_sandbox.partner_redeemed", &acct.ID, map[string]any{"app_id": app.ID, "key_id": key.ID})
		writeJSON(w, http.StatusOK, api.APISandboxInviteResponse{Sandbox: receipt, Token: token})
	}
}

func partnerInviteUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
		"Partner invite unavailable", "ask the sandbox owner for a new one-time link"))
}
