package issuetrackers

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type LinearConnectionVerifier interface {
	VerifyLinearConnection(context.Context, string, string) (issuetracker.LinearIdentity, error)
}

type LinearCredentialScope struct {
	AccountID string                      `json:"account_id"`
	AppID     string                      `json:"app_id"`
	Identity  issuetracker.LinearIdentity `json:"identity"`
}

type linearCredentialEnvelope struct {
	Scope         LinearCredentialScope `json:"scope"`
	Token         string                `json:"token"`
	WebhookSecret string                `json:"webhook_secret,omitempty"`
}

const linearCredentialNamespace = api.IssueTrackerLinearCredentialNamespace

func validLinearScope(scope LinearCredentialScope) bool {
	for _, value := range []string{scope.AccountID, scope.AppID, scope.Identity.WorkspaceID, scope.Identity.TeamID, scope.Identity.ViewerID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return false
		}
	}
	return true
}

// SealLinearCredential performs a fresh provider verification before returning
// ciphertext. Caller authorization and durable connection storage are separate.
func SealLinearCredential(ctx context.Context, verifier LinearConnectionVerifier, recipient *age.X25519Recipient, accountID, appID, teamID, token string, webhookSecrets ...string) (LinearCredentialScope, []byte, error) {
	return SealLinearCredentialTo(ctx, verifier, []*age.X25519Recipient{recipient}, accountID, appID, teamID, token, webhookSecrets...)
}

// SealLinearCredentialTo is SealLinearCredential for several recipients: the
// account seal domain and the fleet recipient while the domain rolls out
// (ADR 20261022111812).
func SealLinearCredentialTo(ctx context.Context, verifier LinearConnectionVerifier, recipients []*age.X25519Recipient, accountID, appID, teamID, token string, webhookSecrets ...string) (LinearCredentialScope, []byte, error) {
	webhookSecret := ""
	if len(webhookSecrets) > 1 {
		return LinearCredentialScope{}, nil, errors.New("invalid webhook configuration")
	}
	if len(webhookSecrets) == 1 {
		webhookSecret = webhookSecrets[0]
	}
	if webhookSecret != "" && !issuetracker.ValidLinearWebhookSecret(webhookSecret) {
		return LinearCredentialScope{}, nil, errors.New("invalid webhook signing secret")
	}
	if verifier == nil || len(recipients) == 0 || slices.Contains(recipients, nil) || !validLinearToken(token) {
		return LinearCredentialScope{}, nil, errors.New("linear credential configuration is invalid")
	}
	for _, value := range []string{accountID, appID, teamID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return LinearCredentialScope{}, nil, errors.New("linear connection scope is invalid")
		}
	}
	identity, err := verifier.VerifyLinearConnection(ctx, token, teamID)
	scope := LinearCredentialScope{AccountID: accountID, AppID: appID, Identity: identity}
	if err != nil || !validLinearScope(scope) || identity.TeamID != teamID {
		return LinearCredentialScope{}, nil, errors.New("linear connection could not be verified")
	}
	plaintext, err := json.Marshal(linearCredentialEnvelope{Scope: scope, Token: token, WebhookSecret: webhookSecret})
	if err != nil {
		return LinearCredentialScope{}, nil, errors.New("linear credential could not be encoded")
	}
	sealed, err := secretbox.SealBytesTo(recipients, linearCredentialNamespace, plaintext, 0)
	if err != nil {
		return LinearCredentialScope{}, nil, errors.New("linear credential could not be sealed")
	}
	return scope, sealed, nil
}

// OpenLinearCredential rejects ciphertext copied to another account, app, team
// or provider identity, including when both records use the same sealing key.
func OpenLinearCredential(identities []*age.X25519Identity, expected LinearCredentialScope, sealed []byte) (string, error) {
	namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err == nil && namespace == linearOAuthNamespace {
		c, e := OpenLinearOAuthCredential(identities, expected, sealed)
		if e != nil || !oauthTokenCurrent(c) {
			return "", errors.New("linear OAuth access requires refresh or reconnect")
		}
		return issuetracker.OAuthAuthorization(c.Tokens.AccessToken), nil
	}
	var envelope linearCredentialEnvelope
	if err != nil || namespace != linearCredentialNamespace || !validLinearScope(expected) || json.Unmarshal(plaintext, &envelope) != nil || envelope.Scope != expected || !validLinearToken(envelope.Token) {
		return "", errors.New("linear credential does not match the connection")
	}
	return envelope.Token, nil
}

func validLinearToken(token string) bool {
	return len(token) > 0 && len(token) <= api.IssueTrackerCredentialMaxBytes && strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// OpenLinearWebhookSecret keeps the signing secret bound to the verified key's scope.
func OpenLinearWebhookSecret(identities []*age.X25519Identity, expected LinearCredentialScope, sealed []byte) (string, error) {
	namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err == nil && namespace == linearOAuthNamespace {
		c, e := OpenLinearOAuthCredential(identities, expected, sealed)
		if e != nil || !issuetracker.ValidLinearWebhookSecret(c.WebhookSecret) {
			return "", errors.New("linear callback credential does not match the connection")
		}
		return c.WebhookSecret, nil
	}
	var envelope linearCredentialEnvelope
	if err != nil || namespace != linearCredentialNamespace || !validLinearScope(expected) || json.Unmarshal(plaintext, &envelope) != nil || envelope.Scope != expected || !validLinearToken(envelope.Token) || !issuetracker.ValidLinearWebhookSecret(envelope.WebhookSecret) {
		return "", errors.New("linear callback credential does not match the connection")
	}
	return envelope.WebhookSecret, nil
}
