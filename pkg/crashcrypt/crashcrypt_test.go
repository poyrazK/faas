package crashcrypt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestBackendStoresOnlyCiphertext(t *testing.T) {
	ctx := context.Background()
	inner, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identity, sealed, err := NewKey(fleet.Recipient(), "cap-1")
	if err != nil {
		t.Fatal(err)
	}
	be := Backend{Inner: inner, Identity: identity}
	secret := append(make([]byte, 1<<20), []byte("customer-session-token-4242")...)
	if err := be.Put(ctx, "snap/d/warm/captures/cap-1/mem", bytes.NewReader(secret)); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Get(ctx, "snap/d/warm/captures/cap-1/mem"); !storage.IsNotFound(err) {
		t.Fatalf("plaintext object stored (err=%v)", err)
	}
	rc, err := inner.Get(ctx, "snap/d/warm/captures/cap-1/mem"+Suffix)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(rc)
	_ = rc.Close()
	if bytes.Contains(stored, []byte("customer-session-token")) || len(stored) > 64<<10 {
		t.Fatalf("stored twin leaks plaintext or is uncompressed (%d bytes)", len(stored))
	}

	// Another node opens the sealed key with the fleet identity and reads it back.
	opened, err := OpenKey([]*age.X25519Identity{fleet}, "cap-1", sealed)
	if err != nil {
		t.Fatal(err)
	}
	rc, err = Backend{Inner: inner, Identity: opened}.Get(ctx, "snap/d/warm/captures/cap-1/mem")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, secret) {
		t.Fatal("round trip differs")
	}
	if _, err := OpenKey([]*age.X25519Identity{fleet}, "cap-2", sealed); err == nil {
		t.Fatal("sealed key opened for another capture")
	}
	if _, err := be.Get(ctx, "snap/missing"); !storage.IsNotFound(err) {
		t.Fatalf("missing object err = %v, want not found", err)
	}
	if err := be.Delete(ctx, "snap/d/warm/captures/cap-1/mem"); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Get(ctx, "snap/d/warm/captures/cap-1/mem"+Suffix); !storage.IsNotFound(err) {
		t.Fatal("delete left the encrypted twin")
	}
	var _ = errors.New
}
