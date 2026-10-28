package sealmigrate_test

import (
	"context"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail"
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

// TestMigrateAccountProductEmail re-seals each product email value, keeping
// its subscription binding, and refuses an envelope of another shape.
func TestMigrateAccountProductEmail(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	token := strings.Repeat("A", 43)
	value := func(id string) []byte {
		b, err := productemail.Seal(fleet, id, token)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	f := &accountValues{rows: map[string]map[string]state.AccountSealedValue{}}
	f.put(state.AccountSealedValue{Kind: state.AccountSealedProductEmailGrant, ID: "s1", Sealed: value("s1")})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedProductEmailPause, ID: "s1", Sealed: value("evidence-s1")})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedProductEmailRecipient, ID: "d1", Sealed: value("evidence-d1")})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedProductEmailUnsubscribe, ID: "d1", Sealed: value("d1")})
	other, err := secretbox.SealTo([]*age.X25519Recipient{fleet.Recipient()}, secretbox.Envelope{"MFA_SECRET": "JBSWY3DPEHPK3PXP"})
	if err != nil {
		t.Fatal(err)
	}
	f.put(state.AccountSealedValue{Kind: state.AccountSealedProductEmailGrant, ID: "s2", Sealed: other})

	report, err := sealmigrate.MigrateAccount(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Account, Target: domain.Recipient().String(), Scanned: 5, Resealed: 4, Failed: 1}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	grant := f.rows[state.AccountSealedProductEmailGrant]["s1"].Sealed
	if _, err := productemail.Open([]*age.X25519Identity{fleet}, "s1", grant); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed grant")
	}
	if got, err := productemail.Open([]*age.X25519Identity{domain}, "s1", grant); err != nil || got != token {
		t.Fatalf("re-sealed grant open = %q, %v; want its subscription binding kept", got, err)
	}
}

// TestMigrateAccountRealtime re-seals managed realtime credentials; a push
// value's namespace is bound to its row, so a value copied to another row
// is not re-sealed.
func TestMigrateAccountRealtime(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	blob := func(namespace, value string) []byte {
		b, err := secretbox.SealBytesTo([]*age.X25519Recipient{fleet.Recipient()}, namespace, []byte(value), 0)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	principal := strings.Repeat("a", 64)
	device := "ep-1:" + principal + ":phone:1"
	f := &accountValues{rows: map[string]map[string]state.AccountSealedValue{}}
	for _, v := range []state.AccountSealedValue{
		{Kind: state.AccountSealedRealtimeCallback, ID: "e1", Sealed: blob(api.RealtimeCallbackAuthNamespace, "callback-token")},
		{Kind: state.AccountSealedRealtimeAuth, ID: "e1", Sealed: blob(api.RealtimeAuthTokenNamespace, "auth-token")},
		{Kind: state.AccountSealedRealtimeAuthPrevious, ID: "e1", Sealed: blob(api.RealtimeAuthTokenNamespace, "old-token")},
		{Kind: state.AccountSealedRealtimePushDevice, ID: device, Sealed: blob(api.RealtimePushDeviceNamespacePrefix+device, `{"token":"t"}`)},
		{Kind: state.AccountSealedRealtimePushDevice, ID: "ep-1:" + principal + ":tablet", Sealed: blob(api.RealtimePushDeviceNamespacePrefix+device, `{"token":"t"}`)},
		{Kind: state.AccountSealedRealtimePushProvider, ID: "ep-1:apns", Sealed: blob(api.RealtimePushProviderNamespacePrefix+"ep-1:apns", `{"provider":"apns"}`)},
	} {
		f.put(v)
	}
	report, err := sealmigrate.MigrateAccount(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Account, Target: domain.Recipient().String(), Scanned: 6, Resealed: 5, Failed: 1}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	if ns, _, err := secretbox.OpenBytes(domain, f.rows[state.AccountSealedRealtimePushDevice][device].Sealed); err != nil || ns != api.RealtimePushDeviceNamespacePrefix+device {
		t.Fatalf("push device target domain open = %q, %v", ns, err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, f.rows[state.AccountSealedRealtimeAuth]["e1"].Sealed); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed auth token")
	}
}
