package secretbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"filippo.io/age"
)

// adr: 20261023173624 — runtime secret envelopes are bound to their account.

func TestSealBoundRoundTrip(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{AccountID: "0b6a6a1e-6f3c-4c8e-9d2a-5d1f7a3e9c10"}
	blob, err := SealOneBound(id.Recipient(), binding, "DATABASE_URL", "postgres://x", 0)
	if err != nil {
		t.Fatal(err)
	}
	env, got, err := OpenMultiBound([]*age.X25519Identity{id}, blob)
	if err != nil {
		t.Fatal(err)
	}
	if got != binding || env["DATABASE_URL"] != "postgres://x" || len(env) != 1 {
		t.Fatalf("OpenMultiBound = %v, %+v", env, got)
	}
	if err := got.Check(binding.AccountID); err != nil {
		t.Fatalf("Check(own account) = %v", err)
	}
	for _, other := range []string{"", "c2f1d0a4-0000-4000-8000-000000000001"} {
		if err := got.Check(other); !errors.Is(err, ErrBindingMismatch) {
			t.Errorf("Check(%q) = %v, want ErrBindingMismatch", other, err)
		}
	}
	// Generic readers still open bound envelopes; they only drop the binding.
	plain, err := OpenMulti([]*age.X25519Identity{id}, blob)
	if err != nil || plain["DATABASE_URL"] != "postgres://x" {
		t.Fatalf("OpenMulti(bound) = %v, %v", plain, err)
	}
}

func TestUnboundEnvelopesStayCompatible(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	// A zero binding writes the original format, which every reader opens.
	blob, err := SealOneBound(id.Recipient(), Binding{}, "API_KEY", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	if raw := decrypt(t, id, blob); !bytes.Equal(raw, []byte(`{"API_KEY":"k"}`)) {
		t.Fatalf("zero binding plaintext = %s", raw)
	}
	legacy, err := SealOne(id.Recipient(), "API_KEY", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	env, binding, err := OpenMultiBound([]*age.X25519Identity{id}, legacy)
	if err != nil || !binding.IsZero() || env["API_KEY"] != "k" {
		t.Fatalf("OpenMultiBound(legacy) = %v, %+v, %v", env, binding, err)
	}
	if err := binding.Check("any-account"); err != nil {
		t.Fatalf("unbound Check = %v", err)
	}
}

// The release before binding decodes plaintext straight into an Envelope.
// A bound envelope must fail that decode, so the previous release refuses
// it rather than handing the binding fields to a workload as values.
func TestPreBindingReaderRefusesBoundEnvelope(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := SealOneBound(id.Recipient(), Binding{AccountID: "acct-1"}, "API_KEY", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	var previous Envelope
	if err := json.Unmarshal(decrypt(t, id, blob), &previous); err == nil {
		t.Fatalf("pre-binding decode accepted a bound envelope: %v", previous)
	}
}

func TestRequireRefusesUnboundEnvelopes(t *testing.T) {
	owner := "0b6a6a1e-6f3c-4c8e-9d2a-5d1f7a3e9c10"
	for _, tc := range []struct {
		name    string
		binding Binding
		account string
		want    error
	}{
		{"bound to the account", Binding{AccountID: owner}, owner, nil},
		{"bound to another account", Binding{AccountID: owner}, "c2f1d0a4-0000-4000-8000-000000000001", ErrBindingMismatch},
		{"bound, no account", Binding{AccountID: owner}, "", ErrBindingMismatch},
		{"unbound", Binding{}, owner, ErrUnbound},
		{"unbound, no account", Binding{}, "", ErrUnbound},
	} {
		if err := tc.binding.Require(tc.account); !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: Require(%q) = %v, want %v", tc.name, tc.account, err, tc.want)
		}
	}
}

func TestBoundEnvelopeRejectsMalformedPlaintext(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for name, plaintext := range map[string]string{
		"unknown version": `{"v":2,"binding":{"account_id":"acct-1"},"values":{"A":"1"}}`,
		"missing account": `{"v":1,"binding":{"account_id":""},"values":{"A":"1"}}`,
		"bad account":     `{"v":1,"binding":{"account_id":"a b"},"values":{"A":"1"}}`,
		"unknown field":   `{"v":1,"binding":{"account_id":"acct-1"},"values":{"A":"1"},"extra":1}`,
		"bad key":         `{"v":1,"binding":{"account_id":"acct-1"},"values":{"lower":"1"}}`,
	} {
		blob, err := sealPlaintext([]*age.X25519Recipient{id.Recipient()}, []byte(plaintext))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := OpenMultiBound([]*age.X25519Identity{id}, blob); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	if _, err := SealOneBound(id.Recipient(), Binding{AccountID: "bad account"}, "A", "1", 0); err == nil {
		t.Error("sealed with an invalid account")
	}
}

func decrypt(t *testing.T, id *age.X25519Identity, blob []byte) []byte {
	t.Helper()
	r, err := age.Decrypt(bytes.NewReader(blob), id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
