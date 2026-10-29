package sealmigrate_test

import (
	"context"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPostgresMigrateAccountIntegrations runs every integration, product
// email and managed realtime kind's list and re-seal statements against the
// real schema, and
// re-seals a real GitLab OAuth connection to the account domain alone.
func TestPostgresMigrateAccountIntegrations(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet, domain := mustIdentity(t), mustIdentity(t)
	for _, kind := range []string{state.AccountSealedGitLabOAuth, state.AccountSealedSlackInstallation, state.AccountSealedSlackInteraction,
		state.AccountSealedSlackDestination, state.AccountSealedSlackOAuthFlow, state.AccountSealedLinearConnection, state.AccountSealedLinearOAuthFlow,
		state.AccountSealedProductEmailGrant, state.AccountSealedProductEmailPause, state.AccountSealedProductEmailRecipient, state.AccountSealedProductEmailUnsubscribe,
		state.AccountSealedRealtimeCallback, state.AccountSealedRealtimeAuth, state.AccountSealedRealtimeAuthPrevious} {
		if _, err := store.ListAccountSealedValues(ctx, kind, "", 10); err != nil {
			t.Fatalf("list %s: %v", kind, err)
		}
		if replaced, err := store.ResealAccountValue(ctx, state.AccountSealedValue{Kind: kind, ID: uuid.NewString(), Sealed: []byte("old")}, []byte("new"), ""); err != nil || replaced {
			t.Fatalf("reseal a missing %s = %v, %v; want nothing replaced", kind, replaced, err)
		}
	}
	for kind, id := range map[string]string{
		state.AccountSealedRealtimePushDevice:   uuid.NewString() + ":" + strings.Repeat("a", 64) + ":phone",
		state.AccountSealedRealtimePushProvider: uuid.NewString() + ":apns",
	} {
		if _, err := store.ListAccountSealedValues(ctx, kind, "", 10); err != nil {
			t.Fatalf("list %s: %v", kind, err)
		}
		if replaced, err := store.ResealAccountValue(ctx, state.AccountSealedValue{Kind: kind, ID: id, Sealed: []byte("old")}, []byte("new"), ""); err != nil || replaced {
			t.Fatalf("reseal a missing %s = %v, %v; want nothing replaced", kind, replaced, err)
		}
	}

	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	account, err := store.CreateAccount(ctx, "account-integrations-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{fleet.Recipient()}, api.GitLabOAuthSecretNamespace, []byte(`{"access":"a","refresh":"r"}`), api.GitLabOAuthSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutGitLabOAuthConnection(ctx, state.GitLabOAuthConnection{AccountID: account.ID, GitLabUserID: 42, Username: "dev-" + suffix,
		CredentialsSealed: sealed, ExpiresAt: time.Now().Add(time.Hour).UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := sealmigrate.MigrateAccount(ctx, store, domain, []*age.X25519Identity{fleet}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GitLabOAuthConnection(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, got.CredentialsSealed); err == nil {
		t.Fatal("the fleet identity still opens the GitLab credentials")
	}
	if ns, _, err := secretbox.OpenBytes(domain, got.CredentialsSealed); err != nil || ns != api.GitLabOAuthSecretNamespace {
		t.Fatalf("GitLab credentials domain open = %q, %v", ns, err)
	}
}
