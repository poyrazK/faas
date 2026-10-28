package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/consumercontrols"
	"github.com/onebox-faas/faas/pkg/state"
)

// Consumer control-plane handlers (ADR-120). Consumer identities are scoped
// to one app and keys are shown in plaintext only from the create response.

func (s *server) consumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool {
	if acct.Plan.ConsumerKeysPerApp() > 0 {
		return true
	}
	api.WriteProblem(w, api.ErrConsumerKeysNotAllowed(acct.Plan))
	return false
}

func (s *server) consumerApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	return s.loadApp(w, r, acct, r.PathValue("slug"))
}

func consumerResponse(c state.APIConsumer) api.APIConsumerResponse {
	return api.APIConsumerResponse{
		ID: c.ID, AppID: c.AppID, ExternalRef: c.ExternalRef, Name: c.Name,
		Status: string(c.Status), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		RevokedAt: c.RevokedAt, ManagedByPlatformTenant: c.PlatformTenantManaged,
	}
}

func consumerKeyResponse(k state.ConsumerKey, plaintext string) api.ConsumerKeyResponse {
	return api.ConsumerKeyResponse{
		ID: k.ID, ConsumerID: k.ConsumerID, Name: k.Name, Prefix: k.Prefix,
		Scopes: append([]string(nil), k.Scopes...), CreatedAt: k.CreatedAt,
		ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt,
		Key: plaintext,
	}
}

func (s *server) listAPIConsumers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	rows, err := s.store.ListAPIConsumersForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list API consumers"))
		return
	}
	out := make([]api.APIConsumerResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, consumerResponse(row))
	}
	writeJSON(w, http.StatusOK, api.APIConsumerListResponse{Consumers: out})
}

func (s *server) createAPIConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.ExternalRef = strings.TrimSpace(req.ExternalRef)
	req.Name = strings.TrimSpace(req.Name)
	if req.ExternalRef == "" || len(req.ExternalRef) > 256 || req.Name == "" || len(req.Name) > 128 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid consumer", "external_ref must be 1-256 characters and name must be 1-128 characters"))
		return
	}
	if req.TrialDays != 0 && !s.apiMonetizationFor(acct.ID) {
		// Trials are API monetization: until it is released the field is
		// answered like any unknown one (ADR-20261018090210).
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", `json: unknown field "trial_days"`))
		return
	}
	if req.TrialDays != 0 && !consumercontrols.ValidTrialDays(req.TrialDays) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid consumer", fmt.Sprintf("trial_days must be 1 to %d", state.MaxAPIConsumerTrialDays)))
		return
	}
	c, err := s.store.CreateAPIConsumer(r.Context(), acct.ID, app.ID, req.ExternalRef, req.Name)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Consumer already exists", "external_ref is already registered for this app"))
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not create API consumer"))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer.created", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": c.ID, "external_ref": c.ExternalRef,
	})
	if req.TrialDays != 0 {
		if _, err := s.consumerControls.StartTrial(r.Context(), acct, c, req.TrialDays); err != nil {
			api.WriteProblem(w, api.ErrInternal("the consumer was created but its trial was not; set it with PUT .../trial"))
			return
		}
	}
	writeJSON(w, http.StatusCreated, consumerResponse(c))
}

func (s *server) getAPIConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	writeJSON(w, http.StatusOK, consumerResponse(c))
}

func (s *server) revokeAPIConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	c, err = s.store.RevokeAPIConsumer(r.Context(), acct.ID, c.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not revoke API consumer"))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer.revoked", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": c.ID,
	})
	writeJSON(w, http.StatusOK, consumerResponse(c))
}

func (s *server) listConsumerKeys(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	rows, err := s.store.ListConsumerKeysForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list consumer keys"))
		return
	}
	out := make([]api.ConsumerKeyResponse, 0)
	for _, row := range rows {
		if row.ConsumerID == c.ID {
			out = append(out, consumerKeyResponse(row, ""))
		}
	}
	writeJSON(w, http.StatusOK, api.ConsumerKeyListResponse{Keys: out})
}

func (s *server) countConsumerKeys(ctx context.Context, acctID, appID string) (int, error) {
	rows, err := s.store.ListConsumerKeysForApp(ctx, acctID, appID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		if row.RevokedAt == nil {
			count++
		}
	}
	return count, nil
}

// countAccountConsumerKeys counts the unrevoked keys that share app's
// per-account allowance, the keys of its billing subject's apps (see
// consumerKeyScopeApps).
func (s *server) countAccountConsumerKeys(ctx context.Context, app state.App) (int, error) {
	apps, err := s.consumerKeyScopeApps(ctx, app)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, scoped := range apps {
		n, err := s.countConsumerKeys(ctx, scoped.AccountID, scoped.ID)
		if err != nil {
			return 0, err
		}
		count += n
	}
	return count, nil
}

// consumerKeyScopeApps returns the apps whose keys share app's per-account
// key allowance, those of its billing subject (ADR-202610171904): every app
// of its organization, whoever created it, once the organization adopted
// billing, and otherwise its creator's apps outside adopted organizations,
// personal and unadopted company apps alike. A company app's rows keep its
// creator's account_id, so counting by that account alone would spend an
// adopted organization's allowance on its creator's personal one.
func (s *server) consumerKeyScopeApps(ctx context.Context, app state.App) ([]state.App, error) {
	owner, err := state.AppResourceAccount(ctx, s.store, app)
	if err != nil {
		return nil, err
	}
	if owner.ResourceOrgID != "" {
		lister, ok := s.store.(state.OrgAppLister)
		if !ok {
			return nil, errors.New("store cannot list organization apps")
		}
		return lister.ListAppsByOrg(ctx, owner.ResourceOrgID)
	}
	apps, err := s.store.ListApps(ctx, app.AccountID)
	if err != nil {
		return nil, err
	}
	adopted, scoped := map[string]bool{}, apps[:0]
	for _, candidate := range apps {
		if candidate.AccountID != app.AccountID {
			continue
		}
		if candidate.OrgID != "" {
			orgAdopted, seen := adopted[candidate.OrgID]
			if !seen {
				org, err := s.store.OrgByID(ctx, candidate.OrgID)
				if err != nil {
					return nil, err
				}
				orgAdopted = !org.Personal && org.ResourceOwnershipEnabled
				adopted[candidate.OrgID] = orgAdopted
			}
			if orgAdopted {
				continue
			}
		}
		scoped = append(scoped, candidate)
	}
	return scoped, nil
}

// consumerKeyScopesProblem trims scopes in place and rejects unknown or
// repeated ones. The billing scope only reaches API monetization's
// self-service routes, so it is not offered until monetization is released
// (ADR-20261018090210).
func (s *server) consumerKeyScopesProblem(accountID string, scopes []string) *api.Problem {
	billing, allowed := s.apiMonetizationFor(accountID), "scopes must contain only read, write, or admin"
	if billing {
		allowed = "scopes must contain only read, write, admin, or billing"
	}
	seen := make(map[string]struct{}, len(scopes))
	for i, scope := range scopes {
		scopes[i] = strings.TrimSpace(scope)
		if scopes[i] != "read" && scopes[i] != "write" && scopes[i] != "admin" && (!billing || scopes[i] != api.ConsumerScopeBilling) {
			return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid consumer key scope", allowed)
		}
		if _, ok := seen[scopes[i]]; ok {
			return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid consumer key scope", "scopes must not contain duplicates")
		}
		seen[scopes[i]] = struct{}{}
	}
	return nil
}

func (s *server) createConsumerKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	if !c.Active() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Consumer is revoked", "keys cannot be issued for a revoked consumer"))
		return
	}
	var req api.CreateConsumerKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 64 || len(req.Scopes) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid consumer key", "name must be 1-64 characters and scopes must not be empty"))
		return
	}
	if problem := s.consumerKeyScopesProblem(acct.ID, req.Scopes); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid expiry", "expires_at must be in the future"))
		return
	}
	k, plaintext, problem := s.issueConsumerKey(r.Context(), acct, c, strings.TrimSpace(req.Name), req.Scopes, req.ExpiresAt)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, consumerKeyResponse(k, plaintext))
}

// issueConsumerKey creates a key for an active consumer within the plan's
// per-app and per-account key quotas and returns its plaintext once. The
// owner's key route and consumer sign-up (ADR-20261021143000) share it.
func (s *server) issueConsumerKey(ctx context.Context, acct state.Account, c state.APIConsumer, name string, scopes []string,
	expiresAt *time.Time) (state.ConsumerKey, string, *api.Problem) {
	appCount, err := s.countConsumerKeys(ctx, acct.ID, c.AppID)
	if err != nil {
		return state.ConsumerKey{}, "", api.ErrInternal("could not count consumer keys")
	}
	if appCount >= acct.Plan.ConsumerKeysPerApp() {
		return state.ConsumerKey{}, "", api.ErrConsumerKeyQuota(acct.Plan, api.PlanQuotaScopeApp, acct.Plan.ConsumerKeysPerApp(), appCount)
	}
	app, err := s.store.AppByID(ctx, c.AppID)
	if err != nil {
		return state.ConsumerKey{}, "", api.ErrInternal("could not count account consumer keys")
	}
	accountCount, err := s.countAccountConsumerKeys(ctx, app)
	if err != nil {
		return state.ConsumerKey{}, "", api.ErrInternal("could not count account consumer keys")
	}
	if accountCount >= acct.Plan.ConsumerKeysPerAccount() {
		return state.ConsumerKey{}, "", api.ErrConsumerKeyQuota(acct.Plan, api.PlanQuotaScopeAccount, acct.Plan.ConsumerKeysPerAccount(), accountCount)
	}
	plaintext, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		return state.ConsumerKey{}, "", api.ErrInternal("could not generate consumer key")
	}
	k, err := s.store.CreateConsumerKeyForConsumer(ctx, acct.ID, c.ID, name, prefix, hash, scopes, expiresAt)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			return state.ConsumerKey{}, "", api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Consumer key already exists", "a key with this name or generated prefix already exists for the app")
		}
		return state.ConsumerKey{}, "", api.ErrInternal("could not create consumer key")
	}
	s.audit.Emit(ctx, "consumer_key.created", &acct.ID, map[string]any{
		"app_id": c.AppID, "consumer_id": c.ID, "key_id": k.ID, "name": k.Name,
		"scopes": k.Scopes,
	})
	return k, plaintext, nil
}

func (s *server) revokeConsumerKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	k, err := s.store.GetConsumerKeyByID(r.Context(), acct.ID, r.PathValue("key_id"))
	if err != nil || k.AppID != app.ID || k.ConsumerID != c.ID {
		s.notFound(w, "no such consumer key")
		return
	}
	k, err = s.store.RevokeConsumerKey(r.Context(), acct.ID, k.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not revoke consumer key"))
		return
	}
	s.audit.Emit(r.Context(), "consumer_key.revoked", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": c.ID, "key_id": k.ID,
	})
	writeJSON(w, http.StatusOK, consumerKeyResponse(k, ""))
}
