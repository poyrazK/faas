package rekey

import (
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261023173624 — re-sealing keeps a runtime secret's account binding;
// dropping it would hand the envelope to any account again.
func TestResealKeepsTheAccountBinding(t *testing.T) {
	previous, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	binding := secretbox.Binding{AccountID: "acct-owner"}
	for name, sealed := range map[string]func() ([]byte, error){
		"bound": func() ([]byte, error) {
			return secretbox.SealOneBound(previous.Recipient(), binding, "DB_URL", "postgres://owner", 0)
		},
		"unbound": func() ([]byte, error) {
			return secretbox.SealOne(previous.Recipient(), "DB_URL", "postgres://owner", 0)
		},
	} {
		blob, err := sealed()
		if err != nil {
			t.Fatal(err)
		}
		r := &Replayer{active: current.Recipient(), identities: []*age.X25519Identity{current, previous}, currentKid: current.Recipient().String()}
		out, err := r.resealEnvelope(state.SecretRekeyEnvelope{Key: "DB_URL", Kid: previous.Recipient().String(), Ciphertext: blob})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		env, got, err := secretbox.OpenMultiBound([]*age.X25519Identity{current}, out.Ciphertext)
		if err != nil || env["DB_URL"] != "postgres://owner" {
			t.Fatalf("%s: re-sealed envelope = %v, %v", name, env, err)
		}
		want := secretbox.Binding{}
		if name == "bound" {
			want = binding
		}
		if got != want {
			t.Errorf("%s: binding after re-seal = %+v, want %+v", name, got, want)
		}
	}
}
