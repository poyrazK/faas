package issuetrackers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

const linearOAuthNamespace = "issue_tracker_linear_oauth_v1"
const linearOAuthFlowNamespace = "issue_tracker_linear_oauth_flow_v1"

type LinearOAuthCredential struct {
	Scope         LinearCredentialScope
	ClientID      string
	Tokens        issuetracker.LinearOAuthTokens
	WebhookSecret string
}

func SealLinearOAuthCredential(recipient *age.X25519Recipient, credential LinearOAuthCredential) ([]byte, error) {
	if recipient == nil || !validLinearScope(credential.Scope) || !validLinearToken(credential.ClientID) || !validLinearToken(credential.Tokens.AccessToken) || !validLinearToken(credential.Tokens.RefreshToken) || credential.Tokens.ExpiresAt.IsZero() || (credential.WebhookSecret != "" && !issuetracker.ValidLinearWebhookSecret(credential.WebhookSecret)) {
		return nil, errors.New("invalid scoped Linear OAuth credential")
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	return secretbox.SealBytes(recipient, linearOAuthNamespace, raw, 0)
}
func OpenLinearOAuthCredential(ids []*age.X25519Identity, expected LinearCredentialScope, sealed []byte) (LinearOAuthCredential, error) {
	namespace, raw, err := secretbox.OpenBytesMulti(ids, sealed)
	var c LinearOAuthCredential
	if err != nil || namespace != linearOAuthNamespace || json.Unmarshal(raw, &c) != nil || c.Scope != expected || !validLinearScope(expected) || !validLinearToken(c.ClientID) || !validLinearToken(c.Tokens.AccessToken) || !validLinearToken(c.Tokens.RefreshToken) || c.Tokens.ExpiresAt.IsZero() || (c.WebhookSecret != "" && !issuetracker.ValidLinearWebhookSecret(c.WebhookSecret)) {
		return c, errors.New("linear OAuth credential does not match the connection")
	}
	return c, nil
}

type LinearOAuthFlowSecret struct {
	AppID, AccountID, Hash, IssueID, ClientID, RedirectURI string
	ExpectedRevision                                       int64
	Verifier                                               string
	Tokens                                                 issuetracker.LinearOAuthTokens
	Workspace                                              issuetracker.LinearOAuthWorkspace
}

func NewLinearOAuthProof() (nonce, verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	nonce = hex.EncodeToString(b)
	if _, err = rand.Read(b); err != nil {
		return
	}
	verifier = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}
func LinearOAuthStateHash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}
func SealLinearOAuthFlow(recipient *age.X25519Recipient, c LinearOAuthFlowSecret) ([]byte, error) {
	if recipient == nil {
		return nil, errors.New("OAuth sealing unavailable")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return secretbox.SealBytes(recipient, linearOAuthFlowNamespace, raw, 0)
}
func OpenLinearOAuthFlow(ids []*age.X25519Identity, row state.LinearOAuthFlow) (LinearOAuthFlowSecret, error) {
	ns, raw, err := secretbox.OpenBytesMulti(ids, row.Sealed)
	var c LinearOAuthFlowSecret
	if err != nil || ns != linearOAuthFlowNamespace || json.Unmarshal(raw, &c) != nil || c.AppID != row.AppID || c.AccountID != row.AccountID || c.Hash != row.Hash || c.IssueID != row.IssueID || c.ExpectedRevision != row.ExpectedRevision {
		return c, errors.New("linear OAuth flow scope changed")
	}
	return c, nil
}
func oauthTokenCurrent(c LinearOAuthCredential) bool { return c.Tokens.ExpiresAt.After(time.Now()) }
