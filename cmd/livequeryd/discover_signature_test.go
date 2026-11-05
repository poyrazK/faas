package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// A discovered login must carry apid's signature for its own database once
// livequeryd verifies signatures: a login a database writer stored would
// point that database's ID at a connection of their choosing (ADR
// 20261028161502).
func TestDiscoveredDatabaseChecksTheLoginSignature(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const db = "3f1c0b6e-9a2b-44d1-8c7e-0123456789ab"
	ids := []*age.X25519Identity{identity}
	row := livekeys.LiveDatabase{DatabaseID: db, BackendID: "neon-a", Slot: "gregale_live_3f1c",
		Sealed: sealLogin(t, identity, livekeys.LoginSealNamespaceFor(db), "postgresql://live:pw@ep-a.neon.tech/neondb"), Recipient: identity.Recipient().String()}
	signed := row
	signed.Signature = livekeys.NewSigner(private).Sign(livekeys.SignatureKindLogin, db, "", row.Recipient, row.Sealed)
	moved := row
	moved.Signature = livekeys.NewSigner(private).Sign(livekeys.SignatureKindLogin, "0a1b2c3d-0000-4000-8000-000000000001", "", row.Recipient, row.Sealed)

	expand := discovery{verify: livekeys.NewVerifier([]ed25519.PublicKey{public}, false)}
	contract := discovery{verify: livekeys.NewVerifier([]ed25519.PublicKey{public}, true)}
	for name, tc := range map[string]struct {
		row   livekeys.LiveDatabase
		d     discovery
		serve bool
	}{
		"signed":                {signed, contract, true},
		"unsigned before":       {row, expand, true},
		"unsigned after":        {row, contract, false},
		"signed for another db": {moved, expand, false},
	} {
		if _, err := discoveredDatabase(tc.row, ids, tc.d); (err == nil) != tc.serve {
			t.Errorf("%s: discovered err = %v, want served=%v", name, err, tc.serve)
		}
	}
}
