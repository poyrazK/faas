package mcpcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// oauthToken renews only within this role's durable observer lease. Rotated
// tokens are persisted before use; neither provider responses nor credentials
// become log messages, receipts, application environment secrets or API data.
func (r *ObserverRunner) oauthToken(ctx context.Context, job state.MCPCatalogObserverJob) (string, error) {
	cfg := job.Snapshot.Config.Observer.OAuth
	if cfg.Validate() != nil || r.Identities == nil || r.Client == nil {
		return "", errors.New("credential unavailable")
	}
	var refresh, source string
	var err error
	if cfg.Guided {
		source = "guided"
	} else {
		refresh, source, err = r.oauthSecret(ctx, job, cfg.RefreshTokenSecretRef)
		if err != nil {
			return "", err
		}
	}
	clientSecret := ""
	if cfg.ClientSecretRef != "" {
		clientSecret, _, err = r.oauthSecret(ctx, job, cfg.ClientSecretRef)
		if err != nil {
			return "", err
		}
	}
	source = digest([]byte(source))
	identities := r.Identities(ctx)
	if len(identities) == 0 || identities[0] == nil {
		return "", errors.New("credential unavailable")
	}
	envelope := secretbox.Envelope{"REFRESH_TOKEN": refresh}
	previous := job.Snapshot.ObserverStatus.OAuth
	if journal := job.Snapshot.ObserverStatus.Renewal; journal != nil && journal.SourceSHA256 == source && journal.State != "installed" && journal.State != "failed" {
		store, ok := r.Store.(state.MCPCatalogOAuthRenewalStore)
		if !ok {
			return "", errors.New("renewal store unavailable")
		}
		read, stop := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
		current, _, e := store.BeginMCPCatalogOAuthRenewal(read, job, source)
		stop()
		if e != nil {
			return "", e
		}
		return r.recoverOAuthRenewal(ctx, job, current)
	}
	if previous != nil && previous.SourceSHA256 == source {
		if previous.State == "reauthorization_required" {
			return "", errors.New("reauthorization required")
		}
		envelope, err = secretbox.OpenMulti(identities, previous.Ciphertext)
		if err != nil || !oauthTokenValue(envelope["REFRESH_TOKEN"]) || !oauthTokenValue(envelope["ACCESS_TOKEN"]) {
			return "", errors.New("credential unavailable")
		}
		refresh = envelope["REFRESH_TOKEN"]
		if previous.ExpiresAt != nil && previous.ExpiresAt.After(time.Now().Add(time.Duration(api.MCPCatalogOAuthRefreshSkewSeconds+job.Snapshot.Config.Observer.IntervalSeconds)*time.Second)) {
			return envelope["ACCESS_TOKEN"], nil
		}
	}
	if cfg.Guided && (previous == nil || previous.SourceSHA256 != source) {
		return "", errors.New("credential unavailable")
	}
	journalStore, ok := r.Store.(state.MCPCatalogOAuthRenewalStore)
	if !ok {
		return "", errors.New("renewal store unavailable")
	}
	beginCtx, stop := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	journal, fresh, err := journalStore.BeginMCPCatalogOAuthRenewal(beginCtx, job, source)
	stop()
	if err != nil {
		return "", err
	}
	if !fresh {
		return r.recoverOAuthRenewal(ctx, job, journal)
	}
	refreshCtx, cancel := context.WithTimeout(ctx, time.Duration(job.Snapshot.Config.Observer.TimeoutSeconds)*time.Second)
	response, revoked, err := r.refreshOAuth(refreshCtx, cfg, refresh, clientSecret)
	cancel()
	uncertain := err != nil
	if err != nil && !oauthTokenValue(response.AccessToken) && !oauthTokenValue(response.RefreshToken) {
		phase := "reauthorization_required"
		var rejected *oauthRejectedExchange
		if errors.As(err, &rejected) {
			phase = "failed"
		}
		_ = r.recordOAuthRenewal(ctx, journalStore, job, journal.ID, phase, nil)
		return "", errors.New("credential unavailable")
	}

	status := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: source}
	status.GrantedScopes = cfg.RequestedScopes()
	if previous != nil && previous.SourceSHA256 == source {
		status.GrantedScopes = previous.GrantedScopes
	}
	if revoked || uncertain {
		status.State = "reauthorization_required"
		if oauthTokenValue(response.AccessToken) {
			envelope["ACCESS_TOKEN"] = response.AccessToken
		}
		if oauthTokenValue(response.RefreshToken) {
			envelope["REFRESH_TOKEN"] = response.RefreshToken
		}
		if previous != nil && previous.SourceSHA256 == source {
			status.ExpiresAt = previous.ExpiresAt
			status.LastRenewalAt = previous.LastRenewalAt
		}
	} else {
		now := time.Now().UTC()
		expires := now.Add(time.Duration(response.ExpiresIn) * time.Second)
		status.ExpiresAt, status.LastRenewalAt = &expires, &now
		envelope["ACCESS_TOKEN"] = response.AccessToken
		if response.RefreshToken != "" {
			envelope["REFRESH_TOKEN"] = response.RefreshToken
		}
	}
	if !revoked && !uncertain {
		status.GrantedScopes, err = compatibleOAuthScopes(cfg, status.GrantedScopes, response.Scope)
		if err != nil {
			status.State = "reauthorization_required"
		}
	}
	status.Ciphertext, err = secretbox.Seal(identities[0].Recipient(), envelope)
	if err != nil {
		return "", errors.New("credential unavailable")
	}
	if err := r.recordOAuthRenewal(ctx, journalStore, job, journal.ID, "saved", &status); err != nil {
		return "", err
	}
	journal.State, journal.Result = "saved", &status
	return r.recoverOAuthRenewal(ctx, job, journal)
}

func (r *ObserverRunner) oauthSecret(ctx context.Context, job state.MCPCatalogObserverJob, ref string) (string, string, error) {
	key := strings.TrimPrefix(ref, api.SecretRefPrefix)
	readCtx, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	defer cancel()
	secret, err := r.Store.GetAppSecretInScope(readCtx, job.AccountID, job.AppID, api.MCPConnectionScope(job.Snapshot.Config.Scope), key)
	if err != nil || secret == nil || secret.AccountID != job.AccountID || secret.AppID != job.AppID || secret.Key != key || secret.Scope != api.MCPConnectionScope(job.Snapshot.Config.Scope) || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" || secret.ManagedCredentialRef != "" {
		return "", "", errors.New("credential unavailable")
	}
	envelope, err := secretbox.OpenMulti(r.Identities(readCtx), secret.Ciphertext)
	if err != nil || !oauthTokenValue(envelope[key]) {
		return "", "", errors.New("credential unavailable")
	}
	return envelope[key], state.MCPCatalogOAuthSecretSource(*secret), nil
}

func oauthTokenValue(value string) bool {
	return value != "" && len(value) <= api.MCPCatalogOAuthMaxTokenBytes && !strings.ContainsAny(value, "\r\n")
}

type oauthRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	Scope        string `json:"scope"`
}

func (r *ObserverRunner) refreshOAuth(ctx context.Context, cfg *api.MCPCatalogObserverOAuthConfig, refresh, clientSecret string) (oauthRefreshResponse, bool, error) {
	var data oauthRefreshResponse
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {cfg.ClientID}}
	req, err := oauthTokenRequest(ctx, cfg, form, clientSecret)
	if err != nil {
		return data, false, err
	}
	// Force redirect refusal even when callers supply a custom test client.
	client := *r.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return data, false, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MCPCatalogOAuthMaxResponseBytes+1))
	if err != nil || len(body) > api.MCPCatalogOAuthMaxResponseBytes || json.Unmarshal(body, &data) != nil {
		return data, false, errors.New("invalid token response")
	}
	if response.StatusCode == http.StatusBadRequest && data.Error == "invalid_grant" {
		return data, true, nil
	}
	if response.StatusCode != http.StatusOK || data.Error != "" {
		return data, false, &oauthRejectedExchange{}
	}
	if !strings.EqualFold(data.TokenType, "Bearer") || !oauthTokenValue(data.AccessToken) || (data.RefreshToken != "" && !oauthTokenValue(data.RefreshToken)) || data.ExpiresIn <= api.MCPCatalogOAuthRefreshSkewSeconds || data.ExpiresIn > api.MCPCatalogOAuthMaxExpirySeconds {
		return data, false, errors.New("invalid token response")
	}
	return data, false, nil
}
