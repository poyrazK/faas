package secretbox

import (
	"bytes"
	"strings"
	"testing"

	"filippo.io/age"
)

// TestSealBytesTo_EveryRecipientOpens pins the dual-seal step of a seal
// domain (ADR 20261022111812): a blob sealed to a domain and the fleet
// recipient opens with either identity alone and with neither other key.
func TestSealBytesTo_EveryRecipientOpens(t *testing.T) {
	domain := mustGenHostKey(t, "domain.age")
	fleet := mustGenHostKey(t, "fleet.age")
	outsider := mustGenHostKey(t, "outsider.age")
	blob, err := SealBytesTo([]*age.X25519Recipient{domain.Recipient(), fleet.Recipient()}, "outbound_authorization", []byte("Bearer sk-test"), 64)
	if err != nil {
		t.Fatalf("SealBytesTo: %v", err)
	}
	for _, tc := range []struct {
		name string
		id   *age.X25519Identity
		ok   bool
	}{
		{"domain", domain, true},
		{"fleet", fleet, true},
		{"outsider", outsider, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns, plain, err := OpenBytes(tc.id, blob)
			if !tc.ok {
				if err == nil {
					t.Fatal("an identity that is not a recipient opened the blob")
				}
				return
			}
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			if ns != "outbound_authorization" || !bytes.Equal(plain, []byte("Bearer sk-test")) {
				t.Fatalf("round trip = (%q, %q)", ns, plain)
			}
		})
	}
}

func TestSealBytesTo_WritesDuplicateRecipientOnce(t *testing.T) {
	id := mustGenHostKey(t, "domain.age")
	once, err := SealBytesTo([]*age.X25519Recipient{id.Recipient()}, "ns", []byte("v"), 0)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := SealBytesTo([]*age.X25519Recipient{id.Recipient(), id.Recipient()}, "ns", []byte("v"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Count(string(twice), "-> X25519"), strings.Count(string(once), "-> X25519"); got != want || want != 1 {
		t.Fatalf("X25519 stanzas = %d, want %d (one per distinct recipient)", got, want)
	}
}

func TestSealBytesTo_RejectsBadInput(t *testing.T) {
	id := mustGenHostKey(t, "domain.age")
	for _, tc := range []struct {
		name       string
		recipients []*age.X25519Recipient
		plaintext  []byte
		max        int
	}{
		{"no recipients", nil, []byte("v"), 0},
		{"nil recipient", []*age.X25519Recipient{id.Recipient(), nil}, []byte("v"), 0},
		{"over cap", []*age.X25519Recipient{id.Recipient()}, bytes.Repeat([]byte("a"), 9), 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SealBytesTo(tc.recipients, "ns", tc.plaintext, tc.max); err == nil {
				t.Fatal("SealBytesTo accepted bad input")
			}
		})
	}
}

func TestSealTo_EveryRecipientOpens(t *testing.T) {
	domain := mustGenHostKey(t, "domain.age")
	fleet := mustGenHostKey(t, "fleet.age")
	outsider := mustGenHostKey(t, "outsider.age")
	blob, err := SealTo([]*age.X25519Recipient{domain.Recipient(), fleet.Recipient(), fleet.Recipient()}, Envelope{"GITHUB_TOKEN": "ghs_test"})
	if err != nil {
		t.Fatalf("SealTo: %v", err)
	}
	if n := strings.Count(string(blob), "-> X25519"); n != 2 {
		t.Fatalf("X25519 stanzas = %d, want 2 (one per distinct recipient)", n)
	}
	for _, id := range []*age.X25519Identity{domain, fleet} {
		env, err := Open(id, blob)
		if err != nil || env["GITHUB_TOKEN"] != "ghs_test" {
			t.Fatalf("Open = %v, %v", env, err)
		}
	}
	if _, err := Open(outsider, blob); err == nil {
		t.Fatal("an identity that is not a recipient opened the envelope")
	}
}

func TestSealTo_RejectsBadInput(t *testing.T) {
	id := mustGenHostKey(t, "domain.age")
	for _, tc := range []struct {
		name       string
		recipients []*age.X25519Recipient
		env        Envelope
	}{
		{"no recipients", nil, Envelope{"KEY": "v"}},
		{"nil recipient", []*age.X25519Recipient{id.Recipient(), nil}, Envelope{"KEY": "v"}},
		{"invalid envelope", []*age.X25519Recipient{id.Recipient()}, Envelope{"": "v"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SealTo(tc.recipients, tc.env); err == nil {
				t.Fatal("SealTo accepted bad input")
			}
		})
	}
}
