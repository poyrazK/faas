package issuetrackers

import (
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestLinearOAuthCredentialsAreScopedSealedAndExpireWithoutLosingWebhookVerification(t *testing.T) {
	key, _ := age.GenerateX25519Identity()
	scope := LinearCredentialScope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Identity: issuetracker.LinearIdentity{WorkspaceID: uuid.NewString(), TeamID: uuid.NewString(), ViewerID: uuid.NewString()}}
	c := LinearOAuthCredential{Scope: scope, ClientID: "client", Tokens: issuetracker.LinearOAuthTokens{AccessToken: "access_secret", RefreshToken: "refresh_secret", ExpiresAt: time.Now().Add(time.Hour)}, WebhookSecret: "signing_secret_12345678901234567890"}
	sealed, err := SealLinearOAuthCredential(key.Recipient(), c)
	if err != nil || strings.Contains(string(sealed), "secret") {
		t.Fatal("unsealed credential", err)
	}
	token, err := OpenLinearCredential([]*age.X25519Identity{key}, scope, sealed)
	if err != nil || token != "Bearer access_secret" {
		t.Fatal(token, err)
	}
	for _, kind := range []string{"account", "app", "team", "viewer", "workspace"} {
		changed := scope
		switch kind {
		case "account":
			changed.AccountID = uuid.NewString()
		case "app":
			changed.AppID = uuid.NewString()
		case "team":
			changed.Identity.TeamID = uuid.NewString()
		case "viewer":
			changed.Identity.ViewerID = uuid.NewString()
		case "workspace":
			changed.Identity.WorkspaceID = uuid.NewString()
		}
		if _, err := OpenLinearOAuthCredential([]*age.X25519Identity{key}, changed, sealed); err == nil {
			t.Fatal("cross-scope OAuth credential", kind)
		}
	}
	c.Tokens.ExpiresAt = time.Now().Add(-time.Minute)
	sealed, _ = SealLinearOAuthCredential(key.Recipient(), c)
	if _, err = OpenLinearCredential([]*age.X25519Identity{key}, scope, sealed); err == nil {
		t.Fatal("expired access token reused")
	}
	if secret, err := OpenLinearWebhookSecret([]*age.X25519Identity{key}, scope, sealed); err != nil || secret != c.WebhookSecret {
		t.Fatal("access expiry broke separate signature verification", err)
	}
	nonce, verifier, challenge, err := NewLinearOAuthProof()
	if err != nil || len(nonce) != 64 || len(verifier) != 64 || challenge == verifier {
		t.Fatal("invalid PKCE proof")
	}
	hash := LinearOAuthStateHash(nonce)
	flow := state.LinearOAuthFlow{Hash: hash, AppID: scope.AppID, AccountID: scope.AccountID, IssueID: uuid.NewString(), ExpectedRevision: 4}
	sealed, _ = SealLinearOAuthFlow(key.Recipient(), LinearOAuthFlowSecret{Hash: hash, AppID: flow.AppID, AccountID: flow.AccountID, IssueID: flow.IssueID, ExpectedRevision: 4, Verifier: verifier})
	flow.Sealed = sealed
	if _, err = OpenLinearOAuthFlow([]*age.X25519Identity{key}, flow); err != nil {
		t.Fatal(err)
	}
	flow.ExpectedRevision++
	flow.Sealed = sealed
	if _, err = OpenLinearOAuthFlow([]*age.X25519Identity{key}, flow); err == nil {
		t.Fatal("pending OAuth credentials escaped their configuration revision")
	}
}
