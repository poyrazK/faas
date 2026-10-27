package issuetrackers

import (
	"context"
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type LinearOAuthService struct {
	Store    state.LinearOAuthStore
	Provider issuetracker.LinearOAuthProvider
	Verifier LinearConnectionVerifier
	Config   issuetracker.LinearOAuthConfig
	// Recipients seal OAuth credentials and consent flows: the account seal
	// domain and the fleet recipient while the domain rolls out (ADR
	// 20261022111812). Identities open them, the domain first.
	Recipients []*age.X25519Recipient
	Identities []*age.X25519Identity
}

func (s LinearOAuthService) Start(ctx context.Context, app, account, issue string, revision int64) (string, string, error) {
	if !s.Config.Enabled() || len(s.Recipients) == 0 {
		return "", "", api.ErrCapacity("Linear OAuth is not configured")
	}
	nonce, verifier, challenge, err := NewLinearOAuthProof()
	if err != nil {
		return "", "", err
	}
	hash := LinearOAuthStateHash(nonce)
	secret := LinearOAuthFlowSecret{AppID: app, AccountID: account, IssueID: issue, Hash: hash, ExpectedRevision: revision, Verifier: verifier, ClientID: s.Config.ClientID, RedirectURI: s.Config.RedirectURI}
	sealed, err := SealLinearOAuthFlowTo(s.Recipients, secret)
	if err != nil {
		return "", "", err
	}
	err = s.Store.StartLinearOAuthFlow(ctx, state.LinearOAuthFlow{AppID: app, AccountID: account, IssueID: issue, Hash: hash, ExpectedRevision: revision, Sealed: sealed, ExpiresAt: time.Now().Add(api.IssueTrackerLinearOAuthFlowTTL)})
	return nonce, s.Config.Authorize(nonce, challenge), err
}
func (s LinearOAuthService) Exchange(ctx context.Context, nonce, account, code string) error {
	if !s.Config.Enabled() || !validLinearToken(code) {
		return api.ErrValidation("restart Linear authorization")
	}
	row, err := s.Store.ClaimLinearOAuthFlow(ctx, LinearOAuthStateHash(nonce), account, "authorizing", "exchanging")
	if err != nil {
		return api.ErrValidation("Linear authorization expired or was already used; restart it")
	}
	secret, err := OpenLinearOAuthFlow(s.Identities, row)
	if err != nil || secret.ClientID != s.Config.ClientID || secret.RedirectURI != s.Config.RedirectURI {
		return api.ErrValidation("restart Linear authorization")
	}
	tokens, err := s.Provider.ExchangeLinearOAuth(ctx, s.Config, code, secret.Verifier)
	if err != nil {
		return api.ErrCapacity("Linear authorization exchange failed; restart consent")
	}
	workspace, err := s.Provider.ListLinearOAuthTeams(ctx, tokens.AccessToken)
	if err != nil || len(workspace.Teams) == 0 {
		return api.ErrCapacity("Linear workspace or accessible teams could not be confirmed; restart consent")
	}
	secret.Tokens, secret.Workspace = tokens, workspace
	secret.Verifier = ""
	sealed, err := SealLinearOAuthFlowTo(s.Recipients, secret)
	if err != nil {
		return err
	}
	return s.Store.SaveLinearOAuthFlow(ctx, row, sealed)
}
func (s LinearOAuthService) Pending(ctx context.Context, nonce, account string) (state.LinearOAuthFlow, LinearOAuthFlowSecret, error) {
	row, err := s.Store.GetLinearOAuthFlow(ctx, LinearOAuthStateHash(nonce), account)
	if err != nil || row.Phase != "choosing" {
		return row, LinearOAuthFlowSecret{}, api.ErrValidation("Linear authorization expired or incomplete; restart it")
	}
	secret, err := OpenLinearOAuthFlow(s.Identities, row)
	if err != nil || secret.ClientID != s.Config.ClientID || secret.RedirectURI != s.Config.RedirectURI || !secret.Tokens.ExpiresAt.After(time.Now()) {
		return row, secret, api.ErrValidation("Linear authorization expired; restart it")
	}
	return row, secret, nil
}
func (s LinearOAuthService) Finish(ctx context.Context, nonce, app, account, issue, team, webhook string) (api.IssueTrackerLinearConnection, error) {
	row, secret, err := s.Pending(ctx, nonce, account)
	if err != nil {
		return api.IssueTrackerLinearConnection{}, err
	}
	if row.AppID != app || row.IssueID != issue {
		return api.IssueTrackerLinearConnection{}, api.ErrValidation("Linear authorization scope changed")
	}
	found := false
	for _, t := range secret.Workspace.Teams {
		if t.ID == team {
			found = true
		}
	}
	if !found {
		return api.IssueTrackerLinearConnection{}, api.ErrValidation("select an authorized Linear team")
	}
	identity, err := s.Verifier.VerifyLinearConnection(ctx, issuetracker.OAuthAuthorization(secret.Tokens.AccessToken), team)
	if err != nil || identity.WorkspaceID != secret.Workspace.WorkspaceID || identity.ViewerID != secret.Workspace.ViewerID || identity.TeamID != team {
		return api.IssueTrackerLinearConnection{}, api.ErrValidation("Linear workspace or selected team changed; restart consent")
	}
	scope := LinearCredentialScope{AccountID: account, AppID: app, Identity: identity}
	sealed, err := SealLinearOAuthCredentialTo(s.Recipients, LinearOAuthCredential{Scope: scope, ClientID: s.Config.ClientID, Tokens: secret.Tokens, WebhookSecret: webhook})
	if err != nil {
		return api.IssueTrackerLinearConnection{}, api.ErrValidation("check the Linear signing secret and restart consent")
	}
	if _, err = s.Store.ClaimLinearOAuthFlow(ctx, row.Hash, account, "choosing", "consuming"); err != nil {
		return api.IssueTrackerLinearConnection{}, api.ErrValidation("Linear authorization already used; reload the connection")
	}
	out, err := s.Store.ConfigureIssueTrackerLinearConnection(ctx, state.IssueTrackerLinearRecord{AppID: app, AccountID: account, SealedCredential: sealed, Connection: api.IssueTrackerLinearConnection{Connected: true, AuthMethod: "oauth", OAuthStatus: "ready", AccessTokenExpiresAt: &secret.Tokens.ExpiresAt, WebhookEnabled: webhook != "", WorkspaceID: identity.WorkspaceID, ViewerID: identity.ViewerID, TeamID: team}}, row.ExpectedRevision)
	if err == nil {
		_ = s.Store.DeleteLinearOAuthFlow(ctx, row.Hash, account)
	}
	return out.Connection, err
}
func (s LinearOAuthService) Resolve(ctx context.Context, r state.IssueTrackerLinearRecord) (string, state.IssueTrackerLinearRecord, error) {
	scope := linearOAuthRecordScope(r)
	if r.Connection.AuthMethod != "oauth" {
		t, e := OpenLinearCredential(s.Identities, scope, r.SealedCredential)
		if e != nil {
			return "", r, api.ErrCapacity("Linear credentials unavailable")
		}
		return t, r, nil
	}
	if !s.Config.Enabled() || r.Connection.OAuthStatus == "reconnect_required" || r.Connection.OAuthStatus == "revoking" {
		return "", r, LinearConnectionRequired()
	}
	c, err := OpenLinearOAuthCredential(s.Identities, scope, r.SealedCredential)
	if err != nil || c.ClientID != s.Config.ClientID {
		_, _ = s.Store.SetLinearOAuthStatus(ctx, r, "reconnect_required")
		return "", r, LinearConnectionRequired()
	}
	if r.Connection.OAuthStatus == "ready" && c.Tokens.ExpiresAt.After(time.Now().Add(api.IssueTrackerLinearOAuthRefreshSkew)) {
		return issuetracker.OAuthAuthorization(c.Tokens.AccessToken), r, nil
	}
	if !r.RefreshStartedAt.IsZero() && time.Since(r.RefreshStartedAt) >= api.IssueTrackerLinearOAuthRefreshGrace {
		_, _ = s.Store.SetLinearOAuthStatus(ctx, r, "reconnect_required")
		return "", r, LinearConnectionRequired()
	}
	claimed, err := s.Store.ClaimLinearOAuthRefresh(ctx, r)
	if err != nil {
		return "", r, api.ErrCapacity("Linear credential refresh is busy; retry shortly")
	}
	// Keep the prior refresh token until its replacement is durably sealed. Linear
	// permits replay of an uncertain rotation for 30 minutes, bounded above.
	tokens, refreshErr := s.Provider.RefreshLinearOAuth(ctx, s.Config, c.Tokens.RefreshToken)
	commit, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.IssueTrackerLinearOAuthCommitTimeout)
	defer cancel()
	if refreshErr != nil {
		status := "refreshing"
		if errors.Is(refreshErr, issuetracker.ErrLinearOAuthRejected) {
			status = "reconnect_required"
		}
		_, _ = s.Store.FinishLinearOAuthRefresh(commit, claimed, nil, nil, status)
		if status == "refreshing" {
			return "", r, api.ErrCapacity("Linear refresh outcome is unconfirmed; retry within the recovery window")
		}
		return "", r, LinearConnectionRequired()
	}
	c.Tokens = tokens
	sealed, err := SealLinearOAuthCredentialTo(s.Recipients, c)
	if err != nil {
		return "", r, api.ErrCapacity("Linear refreshed credential could not be sealed; retry shortly")
	}
	out, err := s.Store.FinishLinearOAuthRefresh(commit, claimed, sealed, &tokens.ExpiresAt, "ready")
	if err != nil {
		return "", r, Changed()
	}
	return issuetracker.OAuthAuthorization(tokens.AccessToken), out, nil
}
func linearOAuthRecordScope(r state.IssueTrackerLinearRecord) LinearCredentialScope {
	return LinearCredentialScope{AccountID: r.AccountID, AppID: r.AppID, Identity: issuetracker.LinearIdentity{WorkspaceID: r.Connection.WorkspaceID, TeamID: r.Connection.TeamID, ViewerID: r.Connection.ViewerID}}
}
func (s LinearOAuthService) Revoke(ctx context.Context, r state.IssueTrackerLinearRecord) error {
	if r.Connection.AuthMethod != "oauth" {
		return api.ErrValidation("this connection is not OAuth")
	}
	c, err := OpenLinearOAuthCredential(s.Identities, linearOAuthRecordScope(r), r.SealedCredential)
	if err != nil {
		return LinearConnectionRequired()
	}
	r, err = s.Store.SetLinearOAuthStatus(ctx, r, "revoking")
	if err != nil {
		return api.ErrCapacity("Linear refresh is in flight; retry revocation shortly")
	}
	err = s.Provider.RevokeLinearOAuth(ctx, c.Tokens.RefreshToken)
	if err != nil {
		return api.ErrCapacity("Linear revocation is unconfirmed; retry or revoke access in Linear, then disconnect locally")
	}

	_, err = s.Store.ConfigureIssueTrackerLinearConnection(ctx, state.IssueTrackerLinearRecord{AppID: r.AppID, AccountID: r.AccountID}, r.Connection.Revision)
	return err
}

func (s LinearOAuthService) VerifiedToken(ctx context.Context, r state.IssueTrackerLinearRecord) (string, error) {
	token, current, err := s.Resolve(ctx, r)
	if err != nil {
		return "", err
	}
	r = current
	expected := linearOAuthRecordScope(r).Identity
	identity, err := s.Verifier.VerifyLinearConnection(ctx, token, expected.TeamID)
	if err != nil || identity != expected {
		if r.Connection.AuthMethod == "oauth" {
			var rejected *issuetracker.ProviderUnauthorized
			if errors.As(err, &rejected) || (err == nil && identity != expected) {
				_, _ = s.Store.SetLinearOAuthStatus(ctx, r, "reconnect_required")
			} else {
				return "", api.ErrCapacity("Linear identity temporarily unavailable")
			}
		}
		return "", LinearConnectionRequired()
	}
	return token, nil
}
