package main

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// During a domain key rotation a reader stages the new identity as
// <domain>.age and the old one as <domain>.age.previous. verify --publish
// moves the probe to the new identity only for a node that proves it held
// the old one: its previous identity names the probe's recipient and opens
// it. A node with an unrelated previous identity, or none, leaves it.
func TestSealDomainProbeMovesToTheNewIdentityOnRotationPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	newID := func() *age.X25519Identity {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	old, current, stranger := newID(), newID(), newID()
	blob, err := sealdomain.SealProbe(sealdomain.Delivery, old.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSealDomainProbe(ctx, string(sealdomain.Delivery), old.Recipient().String(), blob); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	previousPath := sealdomain.IdentityPath(dir, sealdomain.Delivery) + ".previous"
	rotate := func() bool {
		t.Helper()
		probe, err := store.LoadSealDomainProbe(ctx, string(sealdomain.Delivery))
		if err != nil {
			t.Fatal(err)
		}
		moved, err := rotateSealDomainProbe(ctx, store, sealdomain.Delivery, dir, current, probe)
		if err != nil {
			t.Fatal(err)
		}
		return moved
	}

	if rotate() {
		t.Fatal("a node without a previous identity moved the probe")
	}
	if err := secretbox.WriteHostKeyAtPath(previousPath, stranger); err != nil {
		t.Fatal(err)
	}
	if rotate() {
		t.Fatal("a node whose previous identity is not the probe's recipient moved the probe")
	}
	if err := secretbox.WriteHostKeyAtPath(previousPath, old); err != nil {
		t.Fatal(err)
	}
	if !rotate() {
		t.Fatal("the probe did not move to the new identity")
	}
	probe, err := store.LoadSealDomainProbe(ctx, string(sealdomain.Delivery))
	if err != nil {
		t.Fatal(err)
	}
	if probe.Recipient != current.Recipient().String() || sealdomain.OpenProbe(sealdomain.Delivery, []*age.X25519Identity{current}, probe.SealedBlob) != nil {
		t.Fatalf("probe after rotation = recipient %s; want it sealed to the new identity %s", probe.Recipient, current.Recipient())
	}
	if rotate() {
		t.Fatal("a probe already on the new identity moved again")
	}
}
