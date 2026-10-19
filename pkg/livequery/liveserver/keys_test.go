package liveserver

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// sealedKey seals a new live secret the way apid stores it.
func sealedKey(t *testing.T, to *age.X25519Recipient, namespace string) (livekeys.SealedKey, seal.Secret) {
	t.Helper()
	text, err := seal.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := seal.ParseSecret(text)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := secretbox.SealBytes(to, namespace, []byte(text), 256)
	if err != nil {
		t.Fatal(err)
	}
	return livekeys.SealedKey{KID: secret.KID, Sealed: blob, Recipient: to.String()}, secret
}

func TestOpenKeys(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Unix(1_800_000_000, 0)
	active, activeSecret := sealedKey(t, identity.Recipient(), livekeys.SealNamespace)
	retiring, retiringSecret := sealedKey(t, identity.Recipient(), livekeys.SealNamespace)
	retiring.RetiresAt = now.Add(time.Hour)
	foreign, _ := sealedKey(t, stranger.Recipient(), livekeys.SealNamespace)
	wrongNamespace, _ := sealedKey(t, identity.Recipient(), "secrets")
	swapped, _ := sealedKey(t, identity.Recipient(), livekeys.SealNamespace)
	swapped.KID = active.KID[:15] + "0" // a row whose secret names another key
	if swapped.KID == active.KID {
		swapped.KID = active.KID[:15] + "1"
	}

	ring, id := OpenKeys("db_1", []livekeys.SealedKey{active, retiring, foreign, wrongNamespace, swapped}, []*age.X25519Identity{identity}, log)
	if ring.Len() != 2 {
		t.Fatalf("ring holds %d keys, want only the two that open", ring.Len())
	}
	for name, secret := range map[string]seal.Secret{"active": activeSecret, "retiring": retiringSecret} {
		token, err := secret.Seal(seal.Query{Database: "db_1", SQL: "SELECT 1"}, now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ring.Open(token, now); err != nil {
			t.Fatalf("%s key: %v", name, err)
		}
	}
	if ring.Opens(retiring.KID, now.Add(2*time.Hour)) {
		t.Fatal("a retired key still opens seals")
	}
	if _, again := OpenKeys("db_1", []livekeys.SealedKey{retiring, active}, []*age.X25519Identity{identity}, log); again != id {
		t.Fatalf("ring identity depends on order: %q vs %q", again, id)
	}
	retiring.RetiresAt = now.Add(2 * time.Hour)
	if _, moved := OpenKeys("db_1", []livekeys.SealedKey{active, retiring}, []*age.X25519Identity{identity}, log); moved == id {
		t.Fatal("ring identity ignores a changed retirement")
	}
}
