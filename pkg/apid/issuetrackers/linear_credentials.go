package issuetrackers

import (
	"context"
	"encoding/json"
	"errors"
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
	Scope LinearCredentialScope `json:"scope"`
	Token string                `json:"token"`
}

const linearCredentialNamespace = "issue_tracker_linear_api_key_v1"

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
func SealLinearCredential(ctx context.Context, verifier LinearConnectionVerifier, recipient *age.X25519Recipient, accountID, appID, teamID, token string) (LinearCredentialScope, []byte, error) {
	if verifier == nil || recipient == nil || !validLinearToken(token) {
		return LinearCredentialScope{}, nil, errors.New("Linear credential configuration is invalid")
	}
	for _, value := range []string{accountID, appID, teamID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return LinearCredentialScope{}, nil, errors.New("Linear connection scope is invalid")
		}
	}
	identity, err := verifier.VerifyLinearConnection(ctx, token, teamID)
	scope := LinearCredentialScope{AccountID: accountID, AppID: appID, Identity: identity}
	if err != nil || !validLinearScope(scope) || identity.TeamID != teamID {
		return LinearCredentialScope{}, nil, errors.New("Linear connection could not be verified")
	}
	plaintext, err := json.Marshal(linearCredentialEnvelope{Scope: scope, Token: token})
	if err != nil {
		return LinearCredentialScope{}, nil, errors.New("Linear credential could not be encoded")
	}
	sealed, err := secretbox.SealBytes(recipient, linearCredentialNamespace, plaintext, 0)
	if err != nil {
		return LinearCredentialScope{}, nil, errors.New("Linear credential could not be sealed")
	}
	return scope, sealed, nil
}

// OpenLinearCredential rejects ciphertext copied to another account, app, team
// or provider identity, including when both records use the same sealing key.
func OpenLinearCredential(identities []*age.X25519Identity, expected LinearCredentialScope, sealed []byte) (string, error) {
	namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	var envelope linearCredentialEnvelope
	if err != nil || namespace != linearCredentialNamespace || !validLinearScope(expected) || json.Unmarshal(plaintext, &envelope) != nil || envelope.Scope != expected || !validLinearToken(envelope.Token) {
		return "", errors.New("Linear credential does not match the connection")
	}
	return envelope.Token, nil
}

func validLinearToken(token string) bool {
	return len(token) > 0 && len(token) <= api.IssueTrackerCredentialMaxBytes && strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
