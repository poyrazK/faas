package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// liveSignRows is an in-memory liveSignStore.
type liveSignRows struct {
	keys, logins map[string]livekeys.Sealed
	// rewrite names a row rewritten between the backfill's read and write.
	rewrite string
}

func liveSignPageOf(rows map[string]livekeys.Sealed, after string, limit int) []livekeys.Sealed {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := []livekeys.Sealed{}
	for _, id := range ids[:min(limit, len(ids))] {
		out = append(out, rows[id])
	}
	return out
}

func (s *liveSignRows) sign(rows map[string]livekeys.Sealed, row livekeys.Sealed, sig []byte) bool {
	if row.ID == s.rewrite {
		current := rows[row.ID]
		current.Blob = []byte("rewritten")
		rows[row.ID] = current
	}
	current := rows[row.ID]
	if !bytes.Equal(current.Blob, row.Blob) || current.Recipient != row.Recipient || !bytes.Equal(current.Signature, row.Signature) {
		return false
	}
	current.Signature = sig
	rows[row.ID] = current
	return true
}

func (s *liveSignRows) SealedKeysAfter(_ context.Context, after string, limit int) ([]livekeys.Sealed, error) {
	return liveSignPageOf(s.keys, after, limit), nil
}

func (s *liveSignRows) SignKey(_ context.Context, row livekeys.Sealed, sig []byte) (bool, error) {
	return s.sign(s.keys, row, sig), nil
}

func (s *liveSignRows) SealedLoginsAfter(_ context.Context, after string, limit int) ([]livekeys.Sealed, error) {
	return liveSignPageOf(s.logins, after, limit), nil
}

func (s *liveSignRows) SignLogin(_ context.Context, row livekeys.Sealed, sig []byte) (bool, error) {
	return s.sign(s.logins, row, sig), nil
}

// The backfill signs every unsigned key and login as it stands, each for the
// row it is stored on, leaves signed rows alone and counts rows rewritten
// while it ran (ADR 20261028161502).
func TestSignExistingLiveSignsEachRowForItself(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := &liveSignRows{keys: map[string]livekeys.Sealed{}, logins: map[string]livekeys.Sealed{}}
	for i := range liveSignPage + 1 {
		kid := fmt.Sprintf("k%015d", i)
		store.keys[kid] = livekeys.Sealed{ID: kid, DatabaseID: "db-1", Blob: []byte("key-" + kid), Recipient: "age1r"}
	}
	store.keys["k-forged"] = livekeys.Sealed{ID: "k-forged", DatabaseID: "db-1", Blob: []byte("x"), Recipient: "age1r", Signature: []byte("forged")}
	store.logins["db-1"] = livekeys.Sealed{ID: "db-1", DatabaseID: "db-1", Blob: []byte("login"), Recipient: "age1r"}
	store.logins["db-2"] = livekeys.Sealed{ID: "db-2", DatabaseID: "db-2", Blob: []byte("login-2"), Recipient: "age1r"}
	store.rewrite = "db-2"

	report, err := signExistingLive(t.Context(), store, livekeys.NewSigner(private), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := liveSignReport{Keys: liveSignCounts{Signed: liveSignPage + 1, Invalid: 1}, Logins: liveSignCounts{Signed: 1, Changed: 1}}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	verify := livekeys.NewVerifier([]ed25519.PublicKey{public}, true)
	for kid, row := range store.keys {
		if kid == "k-forged" {
			if string(row.Signature) != "forged" {
				t.Fatal("a row signed by an untrusted key was signed again")
			}
			continue
		}
		if err := verify.Check(livekeys.SignatureKindKey, row.DatabaseID, kid, row.Recipient, row.Blob, row.Signature); err != nil {
			t.Fatalf("key %s: %v", kid, err)
		}
	}
	login := store.logins["db-1"]
	if err := verify.Check(livekeys.SignatureKindLogin, "db-1", "", login.Recipient, login.Blob, login.Signature); err != nil {
		t.Fatalf("login: %v", err)
	}
	if len(store.logins["db-2"].Signature) != 0 {
		t.Fatal("a login rewritten during the backfill was signed")
	}
	again, err := signExistingLive(t.Context(), store, livekeys.NewSigner(private), nil)
	if err != nil || again.Keys.AlreadySigned != liveSignPage+1 || again.Keys.Signed != 0 {
		t.Fatalf("rerun = %+v, %v; want every key already signed", again, err)
	}
}

// After a signing-key rotation the backfill signs again, with the new key,
// each row a trusted previous key signed, and leaves a row neither key
// signed for review (ADR 20261028161502, item 5).
func TestSignExistingLiveResignsAfterRotation(t *testing.T) {
	oldPublic, oldPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newPublic, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := livekeys.NewSigner(oldPrivate)
	row := func(kid string) livekeys.Sealed {
		r := livekeys.Sealed{ID: kid, DatabaseID: "db-1", Blob: []byte("key-" + kid), Recipient: "age1r"}
		r.Signature = old.Sign(livekeys.SignatureKindKey, r.DatabaseID, kid, r.Recipient, r.Blob)
		return r
	}
	store := &liveSignRows{keys: map[string]livekeys.Sealed{"k1": row("k1"), "k2": row("k2")}, logins: map[string]livekeys.Sealed{}}
	forged := row("k3")
	forged.Signature = bytes.Repeat([]byte{7}, 64)
	store.keys["k3"] = forged

	trusted := livekeys.NewVerifier([]ed25519.PublicKey{oldPublic}, true)
	report, err := signExistingLive(t.Context(), store, livekeys.NewSigner(newPrivate), trusted)
	if err != nil {
		t.Fatal(err)
	}
	if want := (liveSignCounts{Resigned: 2, Invalid: 1}); report.Keys != want {
		t.Fatalf("keys = %+v, want %+v", report.Keys, want)
	}
	verify := livekeys.NewVerifier([]ed25519.PublicKey{newPublic}, true)
	for _, kid := range []string{"k1", "k2"} {
		r := store.keys[kid]
		if err := verify.Check(livekeys.SignatureKindKey, r.DatabaseID, kid, r.Recipient, r.Blob, r.Signature); err != nil {
			t.Fatalf("%s after the rotation: %v", kid, err)
		}
	}
	if !bytes.Equal(store.keys["k3"].Signature, forged.Signature) {
		t.Fatal("a row no trusted key signed was signed again")
	}
}
