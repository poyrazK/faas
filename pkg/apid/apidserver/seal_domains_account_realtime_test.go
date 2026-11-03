package apidserver_test

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestRealtimeHost_SealsInTheAccountDomain checks the keys apid gives
// managed realtime in domain-only mode (ADR 20261022111812): a credential
// sealed with them opens only with the domain identity, and the realtime
// reader still opens one sealed before the domain.
func TestRealtimeHost_SealsInTheAccountDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	withAccountDomain(t, fleet, true, domain, sealdomain.WriteDomain)
	recipients, identities := apidserver.RealtimeHostKeys()
	to, err := recipients()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytesTo(to, api.RealtimeAuthTokenNamespace, []byte("rt-token"), api.RealtimeCallbackAuthTokenMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, sealed); err == nil {
		t.Fatal("the fleet identity opens a realtime credential sealed in domain mode")
	}
	old, err := secretbox.SealBytesTo([]*age.X25519Recipient{fleet.Recipient()}, api.RealtimeAuthTokenNamespace, []byte("rt-old"), api.RealtimeCallbackAuthTokenMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	for value, blob := range map[string][]byte{"rt-token": sealed, "rt-old": old} {
		if _, plain, err := secretbox.OpenBytesMulti(identities(context.Background()), blob); err != nil || string(plain) != value {
			t.Fatalf("realtime reader open of %q = %q, %v", value, plain, err)
		}
	}
}
