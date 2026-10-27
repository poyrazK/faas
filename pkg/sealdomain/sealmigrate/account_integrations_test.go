package sealmigrate_test

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestMigrateAccountIntegrations re-seals each integration credential kind
// under its own namespace; a Linear connection holds either credential
// kind, and a value under another namespace is not re-sealed.
func TestMigrateAccountIntegrations(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	blob := func(namespace, value string) []byte {
		b, err := secretbox.SealBytesTo([]*age.X25519Recipient{fleet.Recipient()}, namespace, []byte(value), 0)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	f := &accountValues{rows: map[string]map[string]state.AccountSealedValue{}}
	for _, v := range []state.AccountSealedValue{
		{Kind: state.AccountSealedGitLabOAuth, ID: "g1", Sealed: blob(api.GitLabOAuthSecretNamespace, `{"access":"a"}`)},
		{Kind: state.AccountSealedSlackInstallation, ID: "s1", Sealed: blob(state.IssueSlackTokenNamespace, "xoxb-install")},
		{Kind: state.AccountSealedSlackDestination, ID: "d1", Sealed: blob(state.IssueSlackTokenNamespace, "xoxb-destination")},
		{Kind: state.AccountSealedSlackInteraction, ID: "i1", Sealed: blob(state.IssueSlackSigningSecretNamespace, "0123456789abcdef0123456789abcdef")},
		{Kind: state.AccountSealedSlackOAuthFlow, ID: "f1", Sealed: blob(api.IssueSlackOAuthFlowNamespace, `{"app":"a"}`)},
		{Kind: state.AccountSealedLinearConnection, ID: "l1", Sealed: blob(api.IssueTrackerLinearCredentialNamespace, `{"token":"lin_api"}`)},
		{Kind: state.AccountSealedLinearConnection, ID: "l2", Sealed: blob(api.IssueTrackerLinearOAuthNamespace, `{"client":"c"}`)},
		{Kind: state.AccountSealedLinearConnection, ID: "l3", Sealed: blob("other", `{}`)},
		{Kind: state.AccountSealedLinearOAuthFlow, ID: "o1", Sealed: blob(api.IssueTrackerLinearOAuthFlowNamespace, `{"hash":"h"}`)},
	} {
		f.put(v)
	}
	report, err := sealmigrate.MigrateAccount(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Account, Target: domain.Recipient().String(), Scanned: 9, Resealed: 8, Failed: 1}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	for kind, rows := range f.rows {
		for id, v := range rows {
			if id == "l3" {
				continue
			}
			if _, _, err := secretbox.OpenBytes(fleet, v.Sealed); err == nil {
				t.Errorf("%s %s still opens with the fleet identity", kind, id)
			}
			if _, _, err := secretbox.OpenBytes(domain, v.Sealed); err != nil {
				t.Errorf("%s %s does not open with the domain identity: %v", kind, id, err)
			}
		}
	}
	if ns, _, err := secretbox.OpenBytes(domain, f.rows[state.AccountSealedLinearConnection]["l2"].Sealed); err != nil || ns != api.IssueTrackerLinearOAuthNamespace {
		t.Fatalf("Linear OAuth credential kept namespace %q, %v", ns, err)
	}
}
