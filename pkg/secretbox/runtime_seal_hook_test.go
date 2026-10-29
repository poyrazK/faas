package secretbox

import (
	"bytes"
	"errors"
	"testing"

	"filippo.io/age"
)

// adr: 20261028161502 — every runtime envelope SealBound returns has passed
// the writer's signing hook.
func TestRuntimeSealHookSeesEveryRuntimeEnvelope(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var seen [][]byte
	SetRuntimeSealHook(func(blob []byte) error {
		seen = append(seen, bytes.Clone(blob))
		return nil
	})
	t.Cleanup(func() { SetRuntimeSealHook(nil) })

	bound, err := SealOneBound(id.Recipient(), Binding{AccountID: "0b6a6a1e-6f3c-4c8e-9d2a-5d1f7a3e9c10"}, "KEY", "v", 0)
	if err != nil {
		t.Fatal(err)
	}
	unbound, err := SealBound([]*age.X25519Recipient{id.Recipient()}, Binding{}, Envelope{"KEY": "v"})
	if err != nil {
		t.Fatal(err)
	}
	// SealOne is not a runtime envelope writer path, so the hook stays out.
	if _, err := SealOne(id.Recipient(), "KEY", "v", 0); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !bytes.Equal(seen[0], bound) || !bytes.Equal(seen[1], unbound) {
		t.Fatalf("hook saw %d envelopes, want the bound and the unbound runtime envelope", len(seen))
	}

	refused := errors.New("signature store down")
	SetRuntimeSealHook(func([]byte) error { return refused })
	if blob, err := SealOneBound(id.Recipient(), Binding{}, "KEY", "v", 0); !errors.Is(err, refused) || blob != nil {
		t.Fatalf("SealOneBound with a failing hook = %v, %v; want no envelope", blob, err)
	}
}
