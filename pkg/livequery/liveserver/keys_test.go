package liveserver

import (
	"crypto/ed25519"
	"crypto/rand"
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

	ring, id := OpenKeys("db_1", []livekeys.SealedKey{active, retiring, foreign, wrongNamespace, swapped}, []*age.X25519Identity{identity}, nil, log)
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
	if _, again := OpenKeys("db_1", []livekeys.SealedKey{retiring, active}, []*age.X25519Identity{identity}, nil, log); again != id {
		t.Fatalf("ring identity depends on order: %q vs %q", again, id)
	}
	retiring.RetiresAt = now.Add(2 * time.Hour)
	if _, moved := OpenKeys("db_1", []livekeys.SealedKey{active, retiring}, []*age.X25519Identity{identity}, nil, log); moved == id {
		t.Fatal("ring identity ignores a changed retirement")
	}
}

// adr: 20261016151947 — a live key sealed to its database's namespace opens
// only for that database. This release also opens the legacy unbound
// namespace apid still writes; a key bound to another database, or a login,
// never opens as this database's key.
func TestOpenKeysDatabaseBoundNamespace(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Unix(1_800_000_000, 0)
	legacy, legacySecret := sealedKey(t, identity.Recipient(), livekeys.SealNamespace)
	bound, boundSecret := sealedKey(t, identity.Recipient(), livekeys.KeySealNamespaceFor("db_1"))
	otherDatabase, _ := sealedKey(t, identity.Recipient(), livekeys.KeySealNamespaceFor("db_2"))
	login, _ := sealedKey(t, identity.Recipient(), livekeys.LoginSealNamespaceFor("db_1"))

	ring, _ := OpenKeys("db_1", []livekeys.SealedKey{legacy, bound, otherDatabase, login}, []*age.X25519Identity{identity}, nil, log)
	if ring.Len() != 2 {
		t.Fatalf("ring holds %d keys, want the legacy and the db_1-bound key only", ring.Len())
	}
	for name, secret := range map[string]seal.Secret{"legacy": legacySecret, "bound": boundSecret} {
		token, err := secret.Seal(seal.Query{Database: "db_1", SQL: "SELECT 1"}, now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ring.Open(token, now); err != nil {
			t.Fatalf("%s key: %v", name, err)
		}
	}
	if ring.Opens(otherDatabase.KID, now) || ring.Opens(login.KID, now) {
		t.Fatal("a key bound to another database, or a login, joined db_1's ring")
	}
	if empty, _ := OpenKeys("", []livekeys.SealedKey{bound}, []*age.X25519Identity{identity}, nil, log); empty.Len() != 0 {
		t.Fatal("a bound key opened for an unnamed database")
	}
}

// A database writer can seal a key of their choosing to livequeryd's public
// recipient. Only keys apid signed for this database join the ring: a
// forged signature never, an unsigned key only before the contract step
// (ADR 20261028161502).
func TestOpenKeysRefusesKeysApidDidNotSign(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, forger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const db = "db_1"
	sign := func(by ed25519.PrivateKey, k livekeys.SealedKey) livekeys.SealedKey {
		k.Signature = livekeys.NewSigner(by).Sign(livekeys.SignatureKindKey, db, k.KID, k.Recipient, k.Sealed)
		return k
	}
	signed, _ := sealedKey(t, identity.Recipient(), livekeys.KeySealNamespaceFor(db))
	signed = sign(private, signed)
	forged, _ := sealedKey(t, identity.Recipient(), livekeys.KeySealNamespaceFor(db))
	forged = sign(forger, forged)
	unsigned, _ := sealedKey(t, identity.Recipient(), livekeys.KeySealNamespaceFor(db))
	// A key apid signed for another database, moved here.
	moved, _ := sealedKey(t, identity.Recipient(), livekeys.KeySealNamespaceFor(db))
	moved.Signature = livekeys.NewSigner(private).Sign(livekeys.SignatureKindKey, "db_2", moved.KID, moved.Recipient, moved.Sealed)
	all := []livekeys.SealedKey{signed, forged, unsigned, moved}

	expand := livekeys.NewVerifier([]ed25519.PublicKey{public}, false)
	if ring, _ := OpenKeys(db, all, []*age.X25519Identity{identity}, expand, log); ring.Len() != 2 {
		t.Fatalf("before the contract step the ring holds %d keys, want the signed and the unsigned one", ring.Len())
	}
	contract := livekeys.NewVerifier([]ed25519.PublicKey{public}, true)
	if ring, _ := OpenKeys(db, all, []*age.X25519Identity{identity}, contract, log); ring.Len() != 1 {
		t.Fatalf("after the contract step the ring holds %d keys, want only the signed one", ring.Len())
	}
}
