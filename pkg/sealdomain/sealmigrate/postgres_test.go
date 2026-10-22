package sealmigrate_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/state"
)

func openStore(t *testing.T) *state.PgStore {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool)
}

func TestPostgresSealDomainProbe(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	domain := mustIdentity(t)
	if _, err := store.LoadSealDomainProbe(ctx, "outbound"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing probe error = %v, want ErrNotFound", err)
	}
	blob, err := sealdomain.SealProbe(sealdomain.Outbound, domain.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSealDomainProbe(ctx, "outbound", domain.Recipient().String(), blob); err != nil {
		t.Fatal(err)
	}
	probe, err := store.LoadSealDomainProbe(ctx, "outbound")
	if err != nil {
		t.Fatal(err)
	}
	if probe.Recipient != domain.Recipient().String() {
		t.Fatalf("probe recipient = %s", probe.Recipient)
	}
	if err := sealdomain.OpenProbe(sealdomain.Outbound, []*age.X25519Identity{domain}, probe.SealedBlob); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSealDomainProbe(ctx, "Outbound!", domain.Recipient().String(), blob); err == nil {
		t.Fatal("the domain CHECK accepted an invalid name")
	}
}

func TestPostgresGithubWebhookSecretSealing(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet := mustIdentity(t)
	recipients := []*age.X25519Recipient{fleet.Recipient()}
	base := rand.Int64N(1<<40) + 1<<41
	legacyID, sealedID := base, base+1

	// A row written by the previous release: plaintext only.
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, legacyID, state.GithubWebhookSecret{Value: []byte("legacy-secret-0123456789")}, "test"); err != nil {
		t.Fatal(err)
	}
	// A row written by this release: both columns.
	secret := []byte("current-secret-0123456789")
	sealed, err := sealdomain.SealGithubWebhookSecret(recipients, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertGithubWebhookSecret(ctx, sealedID, state.GithubWebhookSecret{Value: secret, Sealed: sealed}, "test"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetGithubWebhookSecret(ctx, sealedID)
	if err != nil || !bytes.Equal(got.Sealed, sealed) || !bytes.Equal(got.Value, secret) {
		t.Fatalf("GetGithubWebhookSecret = %+v, %v", got, err)
	}

	unsealed, err := store.ListUnsealedGithubWebhookSecrets(ctx, base-1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInstallation(unsealed, legacyID) || containsInstallation(unsealed, sealedID) {
		t.Fatalf("unsealed rows = %+v; want the legacy row and not the sealed one", unsealed)
	}
	if updated, err := store.SealGithubWebhookSecret(ctx, legacyID, []byte("rotated-meanwhile-012345"), sealed); err != nil || updated {
		t.Fatalf("sealing against a changed plaintext = %v, %v; want no write", updated, err)
	}

	report, err := sealmigrate.SealGithubWebhookSecrets(ctx, store, recipients)
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed < 1 {
		t.Fatalf("backfill report = %+v", report)
	}
	legacy, err := store.GetGithubWebhookSecret(ctx, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{fleet}, legacy.Sealed)
	if err != nil || string(opened) != "legacy-secret-0123456789" {
		t.Fatalf("backfilled copy opens to %q, %v", opened, err)
	}
}

func containsInstallation(rows []state.GithubWebhookSecretRow, id int64) bool {
	for _, r := range rows {
		if r.InstallationID == id {
			return true
		}
	}
	return false
}
