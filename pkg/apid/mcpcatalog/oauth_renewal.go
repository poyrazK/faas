package mcpcatalog

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type oauthRejectedExchange struct{}

func (*oauthRejectedExchange) Error() string { return "token exchange rejected" }

func (r *ObserverRunner) recordOAuthRenewal(ctx context.Context, store state.MCPCatalogOAuthRenewalStore, job state.MCPCatalogObserverJob, id, phase string, status *api.MCPCatalogObserverOAuthStatus) error {
	persist, stop := context.WithTimeout(context.WithoutCancel(ctx), api.MCPPolicyConvergenceReadTimeout)
	defer stop()
	return store.RecordMCPCatalogOAuthRenewal(persist, job, id, phase, status)
}
func (r *ObserverRunner) recoverOAuthRenewal(ctx context.Context, job state.MCPCatalogObserverJob, journal api.MCPCatalogOAuthRenewal) (string, error) {
	if journal.State != "saved" || journal.Result == nil {
		return "", errors.New("reauthorization required")
	}
	result := *journal.Result
	store, ok := r.Store.(state.MCPCatalogObserverStore)
	if !ok {
		return "", errors.New("renewal store unavailable")
	}
	persist, stop := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
	err := store.SaveMCPCatalogObserverOAuth(persist, job, result)
	stop()
	if err != nil {
		return "", err
	}
	if result.State == "reauthorization_required" {
		return "", errors.New("reauthorization required")
	}
	envelope, err := secretbox.OpenMulti(r.Identities(ctx), result.Ciphertext)
	if err != nil || !oauthTokenValue(envelope["ACCESS_TOKEN"]) || !oauthTokenValue(envelope["REFRESH_TOKEN"]) {
		return "", errors.New("credential unavailable")
	}
	if result.ExpiresAt == nil || !result.ExpiresAt.After(time.Now().Add(time.Duration(api.MCPCatalogOAuthRefreshSkewSeconds)*time.Second)) {
		st := *job.Snapshot.ObserverStatus
		st.OAuth = &result
		journal.State, journal.Result = "installed", nil
		st.Renewal = &journal
		job.Snapshot.ObserverStatus = &st
		return r.oauthToken(ctx, job)
	}
	return envelope["ACCESS_TOKEN"], nil
}
