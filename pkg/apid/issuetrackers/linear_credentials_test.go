package issuetrackers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type linearVerifierFunc func(context.Context, string, string) (issuetracker.LinearIdentity, error)

func (f linearVerifierFunc) VerifyLinearConnection(c context.Context, token, team string) (issuetracker.LinearIdentity, error) {
	return f(c, token, team)
}

func TestLinearCredentialScopeAndRotation(t *testing.T) {
	old, _ := age.GenerateX25519Identity()
	current, _ := age.GenerateX25519Identity()
	identity := issuetracker.LinearIdentity{WorkspaceID: "11111111-1111-4111-8111-111111111111", TeamID: "22222222-2222-4222-8222-222222222222", ViewerID: "33333333-3333-4333-8333-333333333333"}
	calls := 0
	verify := linearVerifierFunc(func(_ context.Context, token, team string) (issuetracker.LinearIdentity, error) {
		calls++
		if token != "lin_api_secret" || team != identity.TeamID {
			t.Fatal("wrong verification inputs")
		}
		return identity, nil
	})
	account, app := "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"
	scope, sealed, err := SealLinearCredential(context.Background(), verify, old.Recipient(), account, app, identity.TeamID, "lin_api_secret")
	if err != nil || bytes.Contains(sealed, []byte("lin_api_secret")) {
		t.Fatal("seal failed or plaintext persisted", err)
	}
	token, err := OpenLinearCredential([]*age.X25519Identity{current, old}, scope, sealed)
	if err != nil || token != "lin_api_secret" {
		t.Fatal("rotation failed", err)
	}
	for _, change := range []func(*LinearCredentialScope){
		func(s *LinearCredentialScope) { s.AccountID = app }, func(s *LinearCredentialScope) { s.AppID = account },
		func(s *LinearCredentialScope) { s.Identity.TeamID = account }, func(s *LinearCredentialScope) { s.Identity.WorkspaceID = app }, func(s *LinearCredentialScope) { s.Identity.ViewerID = app },
	} {
		changed := scope
		change(&changed)
		if _, err := OpenLinearCredential([]*age.X25519Identity{old}, changed, sealed); err == nil {
			t.Fatal("cross-scope ciphertext accepted")
		}
	}
	if _, err := OpenLinearCredential([]*age.X25519Identity{current}, scope, sealed); err == nil {
		t.Fatal("wrong key accepted")
	}
	wrong, _ := secretbox.SealBytes(old.Recipient(), "other", []byte(`{}`), 0)
	if _, err := OpenLinearCredential([]*age.X25519Identity{old}, scope, wrong); err == nil {
		t.Fatal("wrong namespace accepted")
	}
	_, _, err = SealLinearCredential(context.Background(), verify, old.Recipient(), "invalid", app, identity.TeamID, "lin_api_secret")
	if err == nil || calls != 1 {
		t.Fatal("invalid scope dispatched")
	}
	_, _, err = SealLinearCredential(context.Background(), linearVerifierFunc(func(context.Context, string, string) (issuetracker.LinearIdentity, error) {
		return identity, errors.New("lin_api_secret")
	}), old.Recipient(), account, app, identity.TeamID, "lin_api_secret")
	if err == nil || strings.Contains(err.Error(), "lin_api_secret") {
		t.Fatal("verification error leaked")
	}
}

func TestLinearSigningSecretIsSealedWithScopeAndPreservesTokenAccess(t *testing.T) {
	key, _ := age.GenerateX25519Identity()
	identity := issuetracker.LinearIdentity{WorkspaceID: "11111111-1111-4111-8111-111111111111", TeamID: "22222222-2222-4222-8222-222222222222", ViewerID: "33333333-3333-4333-8333-333333333333"}
	verifier := linearVerifierFunc(func(context.Context, string, string) (issuetracker.LinearIdentity, error) { return identity, nil })
	secret := "linear_signing_secret_1234567890abcdef"
	scope, sealed, err := SealLinearCredential(t.Context(), verifier, key.Recipient(), "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555", identity.TeamID, "lin_api_secret", secret)
	if err != nil || bytes.Contains(sealed, []byte(secret)) {
		t.Fatal("secret not sealed", err)
	}
	got, err := OpenLinearWebhookSecret([]*age.X25519Identity{key}, scope, sealed)
	if err != nil || got != secret {
		t.Fatal(err)
	}
	token, err := OpenLinearCredential([]*age.X25519Identity{key}, scope, sealed)
	if err != nil || token != "lin_api_secret" {
		t.Fatal("token compatibility lost", err)
	}
	scope.AppID = scope.AccountID
	if _, err := OpenLinearWebhookSecret([]*age.X25519Identity{key}, scope, sealed); err == nil {
		t.Fatal("cross-app callback key accepted")
	}
}
