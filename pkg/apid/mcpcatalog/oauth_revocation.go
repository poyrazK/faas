package mcpcatalog

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Revocation runs independently of monitor enablement, plan and workload state.
// RFC 7009 acknowledgement includes tokens already invalid at the provider.
func (r *ObserverRunner) TickRevocations(ctx context.Context) error {
	store, ok := r.Store.(state.MCPCatalogOAuthRevocationStore)
	if !ok {
		return errors.New("revocation store unavailable")
	}
	read, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	jobs, err := store.ListDueMCPCatalogOAuthRevocations(read, api.MCPCatalogObserverConcurrency)
	cancel()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func() { defer wg.Done(); _ = r.revoke(ctx, store, job) }()
	}
	wg.Wait()
	return nil
}
func (r *ObserverRunner) revoke(ctx context.Context, store state.MCPCatalogOAuthRevocationStore, job state.MCPCatalogObserverJob) error {
	claim, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	job, err := store.ChangeMCPCatalogOAuthRevocation(claim, job, "claim")
	cancel()
	if err != nil {
		return err
	}
	if job.Retirement == nil || job.Retirement.State != "pending" {
		return nil
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(api.MCPCatalogObserverMaxTimeoutSeconds)*time.Second)
	defer cancel()
	reason := "credential_unavailable"
	rev := job.Snapshot.OAuthLifecycle.Revocation
	cfg := rev.Config
	if cfg != nil && cfg.Validate() == nil && r.Identities != nil && r.Client != nil {
		envelope, openErr := secretbox.OpenMulti(r.Identities(work), rev.Ciphertext)
		secret := ""
		secretErr := error(nil)
		if cfg.ResolvedRevocationAuthMethod() != "none" {
			if len(rev.ClientCiphertext) > 0 {
				values, e := secretbox.OpenMulti(r.Identities(work), rev.ClientCiphertext)
				secretErr = e
				secret = values[strings.TrimPrefix(cfg.ClientSecretRef, api.SecretRefPrefix)]
				if !oauthTokenValue(secret) {
					secretErr = errors.New("credential unavailable")
				}
			} else {
				secret, _, secretErr = r.oauthSecret(work, job, cfg.ClientSecretRef)
			}
		}
		if openErr == nil && secretErr == nil && (oauthTokenValue(envelope["REFRESH_TOKEN"]) || oauthTokenValue(envelope["ACCESS_TOKEN"])) {
			reason = "provider_unavailable"
			for _, phase := range []struct {
				key, hint, action string
				done              bool
			}{{"REFRESH_TOKEN", "refresh_token", "refresh_acknowledged", rev.RefreshAcknowledged}, {"ACCESS_TOKEN", "access_token", "access_acknowledged", rev.AccessAcknowledged}} {
				if phase.done {
					continue
				}
				if envelope[phase.key] != "" && r.revokeToken(work, cfg, envelope[phase.key], phase.hint, secret) != nil {
					break
				}
				report, stop := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
				job, err = store.ChangeMCPCatalogOAuthRevocation(report, job, phase.action)
				stop()
				if err != nil {
					return err
				}
				if job.Snapshot.OAuthLifecycle.Revocation.State == "provider_acknowledged" {
					return nil
				}
			}
		}
	}
	report, stop := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	defer stop()
	_, err = store.ChangeMCPCatalogOAuthRevocation(report, job, reason)
	return err
}
func (r *ObserverRunner) revokeToken(ctx context.Context, cfg *api.MCPCatalogObserverOAuthConfig, token, hint, secret string) error {
	copy := *cfg
	copy.TokenEndpoint = cfg.RevocationEndpoint
	copy.TokenAuthMethod = cfg.ResolvedRevocationAuthMethod()
	copy.Resource = ""
	req, err := oauthTokenRequest(ctx, &copy, url.Values{"token": {token}, "token_type_hint": {hint}, "client_id": {cfg.ClientID}}, secret)
	if err != nil {
		return errors.New("revocation unavailable")
	}
	client := *r.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return errors.New("revocation unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.New("revocation unavailable")
	}
	return nil
}
