package apidserver_test

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// production 2026-11-17: runtime credentials were sealed to secretd's runtime
// domain while apid had neither that identity nor secretd's verifier, and
// every database switch retried verification for 30 minutes. A switch is now
// refused up front unless one of the two can verify what the sink seals.
func TestCutoverCredentialsAreVerifiableOnlyWithTheSealingIdentityOrSecretd(t *testing.T) {
	sealing, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		held     *age.X25519Identity
		verifier bool
		want     bool
	}{
		{"apid holds the sealing identity", sealing, false, true},
		{"sealed to a key apid does not hold", other, false, false},
		{"secretd verifies", other, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			sink, err := apidserver.NewAppSecretCredentialSink(state.NewMemStore(),
				func() *age.X25519Recipient { return sealing.Recipient() }, func() []byte { return []byte("test-key") })
			if err != nil {
				t.Fatal(err)
			}
			held := c.held
			*sink.Identities() = func() []*age.X25519Identity { return []*age.X25519Identity{held} }
			if c.verifier {
				*sink.CutoverProbe() = func(context.Context, managedpostgres.Cutover, managedpostgres.CutoverCredential, managedpostgres.Database) error {
					return nil
				}
			}
			if got := sink.CanVerifyCutoverCredentials(); got != c.want {
				t.Fatalf("CanVerifyCutoverCredentials = %v, want %v", got, c.want)
			}
		})
	}
}
