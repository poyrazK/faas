package issueslack

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const oauthFlowNamespace = api.IssueSlackOAuthFlowNamespace

type OAuthService struct {
	Store  state.IssueSlackOAuthStore
	Client *Client
	Config OAuthConfig
	// Recipients seal workspace tokens, signing secrets and consent flows:
	// the account seal domain and the fleet recipient while the domain rolls
	// out (ADR 20261022111812). Identities open them, the domain first.
	Recipients []*age.X25519Recipient
	Identities []*age.X25519Identity
}
type oauthFlowSecret struct {
	AppID, AccountID, Hash, ConfigHash                       string
	Revision                                                 int64
	WorkspaceID, WorkspaceName, SlackAppID, BotUserID, Token string
}

func OAuthStateHash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}
func ValidOAuthNonce(nonce string) bool {
	if len(nonce) != 64 {
		return false
	}
	raw, err := hex.DecodeString(nonce)
	return err == nil && len(raw) == 32 && nonce == hex.EncodeToString(raw)
}
func (s OAuthService) configHash() string {
	b, _ := json.Marshal(s.Config)
	return OAuthStateHash(string(b))
}
func (s OAuthService) seal(c oauthFlowSecret) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return secretbox.SealBytesTo(s.Recipients, oauthFlowNamespace, b, api.IssueSlackOAuthSealedMaxBytes)
}
func (s OAuthService) open(f state.IssueSlackOAuthFlow) (oauthFlowSecret, error) {
	var c oauthFlowSecret
	ns, raw, err := secretbox.OpenBytesMulti(s.Identities, f.Sealed)
	if err != nil || ns != oauthFlowNamespace || json.Unmarshal(raw, &c) != nil || c.AppID != f.AppID || c.AccountID != f.AccountID || c.Hash != f.Hash || c.Revision != f.ExpectedRevision || c.ConfigHash != s.configHash() {
		return c, state.ErrInvalidArgument
	}
	return c, nil
}
func (s OAuthService) Start(ctx context.Context, app, owner string, revision int64) (string, string, error) {
	if !s.Config.Enabled() || len(s.Recipients) == 0 {
		return "", "", api.ErrCapacity("Slack OAuth is not configured")
	}
	raw := make([]byte, api.IssueSlackLinkNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	nonce := hex.EncodeToString(raw)
	c := oauthFlowSecret{AppID: app, AccountID: owner, Hash: OAuthStateHash(nonce), Revision: revision, ConfigHash: s.configHash()}
	sealed, err := s.seal(c)
	if err != nil {
		return "", "", err
	}
	err = s.Store.StartIssueSlackOAuth(ctx, state.IssueSlackOAuthFlow{AppID: app, AccountID: owner, Hash: c.Hash, ExpectedRevision: revision, Sealed: sealed, ExpiresAt: time.Now().Add(api.IssueSlackOAuthFlowTTL)})
	return nonce, s.Config.Authorize(nonce), err
}
func (s OAuthService) Exchange(ctx context.Context, nonce, owner, code string) error {
	if !s.Config.Enabled() || !ValidOAuthNonce(nonce) || len(code) == 0 || len(code) > api.IssueSlackOAuthCodeMaxBytes {
		return state.ErrInvalidArgument
	}
	f, err := s.Store.ClaimIssueSlackOAuth(ctx, OAuthStateHash(nonce), owner)
	if err != nil {
		return err
	}
	c, err := s.open(f)
	if err != nil {
		return err
	}
	t, err := s.Client.ExchangeOAuth(ctx, s.Config, code)
	if err != nil {
		return api.ErrCapacity("Slack authorization failed; restart consent")
	}
	c.Token = t.Token
	c.WorkspaceID = t.WorkspaceID
	c.WorkspaceName = t.WorkspaceName
	c.SlackAppID = t.SlackAppID
	c.BotUserID = t.BotUserID
	sealed, err := s.seal(c)
	if err != nil {
		return err
	}
	return s.Store.SaveIssueSlackOAuth(ctx, f, sealed)
}
func (s OAuthService) Pending(ctx context.Context, nonce, owner string) (state.IssueSlackOAuthFlow, state.IssueSlackInstallation, error) {
	if !s.Config.Enabled() || !ValidOAuthNonce(nonce) {
		return state.IssueSlackOAuthFlow{}, state.IssueSlackInstallation{}, state.ErrInvalidArgument
	}
	f, err := s.Store.GetIssueSlackOAuth(ctx, OAuthStateHash(nonce), owner)
	if err != nil || f.Phase != "choosing" {
		return f, state.IssueSlackInstallation{}, state.ErrInvalidArgument
	}
	c, err := s.open(f)
	if err != nil || !ValidBotToken(c.Token) {
		return f, state.IssueSlackInstallation{}, state.ErrInvalidArgument
	}
	return f, state.IssueSlackInstallation{AppID: f.AppID, AccountID: owner, Revision: f.ExpectedRevision, WorkspaceID: c.WorkspaceID, WorkspaceName: c.WorkspaceName, SlackAppID: c.SlackAppID, BotUserID: c.BotUserID}, nil
}
func (s OAuthService) Finish(ctx context.Context, nonce, app, owner string) error {
	f, in, err := s.Pending(ctx, nonce, owner)
	if err != nil {
		return err
	}
	if f.AppID != app {
		return state.ErrInvalidArgument
	}
	c, err := s.open(f)
	if err != nil {
		return err
	}
	if err = s.Client.VerifyOAuth(ctx, OAuthTokens{Token: c.Token, WorkspaceID: c.WorkspaceID, BotUserID: c.BotUserID}); err != nil {
		return api.ErrCapacity("Slack workspace verification failed; restart consent")
	}
	in.TokenSealed, err = secretbox.SealBytesTo(s.Recipients, state.IssueSlackTokenNamespace, []byte(c.Token), api.IssueSlackTokenMaxBytes)
	if err != nil {
		return err
	}
	signing, err := secretbox.SealBytesTo(s.Recipients, state.IssueSlackSigningSecretNamespace, []byte(s.Config.SigningSecret), api.IssueSlackSigningSecretBytes)
	if err != nil {
		return err
	}
	return s.Store.FinishIssueSlackOAuth(ctx, f, in, signing)
}
func (s OAuthService) Channels(ctx context.Context, app, owner string) ([]OAuthChannel, error) {
	i, err := s.Store.IssueSlackInstallationCredential(ctx, app, owner)
	if err != nil {
		return nil, err
	}
	if i.Status != "connected" {
		return nil, nil
	}
	token, err := OpenToken(s.Identities, i.TokenSealed)
	if err != nil {
		return nil, err
	}
	return s.Client.OAuthChannels(ctx, token)
}
func (s OAuthService) SetDestination(ctx context.Context, app, owner string, in state.PutIssueSlackDestination, revision int64) error {
	i, err := s.Store.IssueSlackInstallationCredential(ctx, app, owner)
	if err != nil {
		return err
	}
	if i.Status != "connected" || i.Revision != revision {
		return api.ErrValidation("reload the Slack installation")
	}
	token, err := OpenToken(s.Identities, i.TokenSealed)
	if err != nil {
		return err
	}
	if in.Enabled {
		if err = s.Client.VerifyOAuthChannel(ctx, token, in.ChannelID); err != nil {
			return api.ErrValidation("choose an active channel the Slack bot has joined")
		}
	}
	return s.Store.SetIssueSlackOAuthDestination(ctx, app, owner, in, revision)
}
