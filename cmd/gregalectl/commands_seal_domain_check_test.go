package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSealDomainCheckOutboundPostgres pins the check the seal_domains role
// runs before outboundd drops the fleet identity (ADR 20261022111812, step
// 4): it fails while a credential still opens with the fleet identity,
// writes nothing, and passes once the re-seal moved every row. The storage
// check runs the same way for s3-gatewayd.
func TestSealDomainCheckOutboundPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	dsn := pgtestDSN(t, pool)
	store := state.NewPgStore(pool)
	var out bytes.Buffer
	previous := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previous })

	domain, fleet := mustCheckIdentity(t), mustCheckIdentity(t)
	dir := t.TempDir()
	if err := secretbox.WriteHostKeyAtPath(sealdomain.IdentityPath(dir, sealdomain.Outbound), domain); err != nil {
		t.Fatal(err)
	}
	fleetPath := filepath.Join(dir, "fleet.age")
	if err := secretbox.WriteHostKeyAtPath(fleetPath, fleet); err != nil {
		t.Fatal(err)
	}
	probe, err := sealdomain.SealProbe(sealdomain.Outbound, domain.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSealDomainProbe(ctx, string(sealdomain.Outbound), domain.Recipient().String(), probe); err != nil {
		t.Fatal(err)
	}

	account, err := store.CreateAccount(ctx, "seal-check-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	integration, err := outbound.NewIntegration(uuid.NewString(), "https://api.example.com", "unused-token", nil, 10, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.ProviderAuthMode = outbound.ProviderAuthManaged
	integration.CredentialSource = outbound.CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{http.MethodGet}
	integration.AllowedPathPrefixes = []string{"/v1"}
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{AccountID: uuid.MustParse(account.ID), Name: "seal-check", Policy: integration}); err != nil {
		t.Fatal(err)
	}
	// Dual-sealed, as apid writes before the domain is domain-only.
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{domain.Recipient(), fleet.Recipient()}, outbound.ManagedAuthorizationSealNamespace, []byte("Bearer sk-check"), outbound.ManagedAuthorizationMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, integration.ID, sealed); err != nil {
		t.Fatal(err)
	}

	flags := []string{"--domain", "outbound", "--pg-dsn", dsn, "--dir", dir, "--fleet-key", fleetPath, "--host-key", filepath.Join(dir, "host.age"), "--json"}
	check := func() (sealmigrate.MigrationReport, error) {
		out.Reset()
		err := runSealDomainMigrate(append([]string{"--check"}, flags...))
		var report sealmigrate.MigrationReport
		if jerr := json.Unmarshal(out.Bytes(), &report); jerr != nil {
			t.Fatalf("decode %q: %v", out.String(), jerr)
		}
		return report, err
	}

	report, err := check()
	if err == nil || !strings.Contains(err.Error(), "still open with the fleet or host identity") || report.Pending != 1 {
		t.Fatalf("check with a dual-sealed credential = %+v, %v; want it refused with one pending", report, err)
	}
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT authorization_sealed FROM outbound_integration_credentials WHERE integration_id = $1`, integration.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, sealed) {
		t.Fatal("the check rewrote the credential")
	}

	out.Reset()
	if err := runSealDomainMigrate(append([]string{"--readers-verified"}, flags...)); err != nil {
		t.Fatal(err)
	}
	if report, err := check(); err != nil || report.Pending != 0 || report.Current != 1 {
		t.Fatalf("check after the re-seal = %+v, %v; want it passed", report, err)
	}

	// The storage check runs the storage walk: with no S3 credential stored
	// nothing is pending.
	storage := mustCheckIdentity(t)
	if err := secretbox.WriteHostKeyAtPath(sealdomain.IdentityPath(dir, sealdomain.Storage), storage); err != nil {
		t.Fatal(err)
	}
	storageProbe, err := sealdomain.SealProbe(sealdomain.Storage, storage.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSealDomainProbe(ctx, string(sealdomain.Storage), storage.Recipient().String(), storageProbe); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runSealDomainMigrate([]string{"--check", "--domain", "storage", "--pg-dsn", dsn, "--dir", dir, "--fleet-key", fleetPath, "--host-key", filepath.Join(dir, "host.age"), "--json"}); err != nil {
		t.Fatal(err)
	}
	var storageReport sealmigrate.MigrationReport
	if err := json.Unmarshal(out.Bytes(), &storageReport); err != nil || storageReport.Domain != sealdomain.Storage || storageReport.Pending != 0 {
		t.Fatalf("storage check = %s, %v; want the storage domain with nothing pending", out.String(), err)
	}

	if err := runSealDomainMigrate([]string{"--check", "--domain", "build", "--pg-dsn", dsn}); err == nil || !strings.Contains(err.Error(), "only in this release") {
		t.Fatalf("check of a domain that cannot contract yet = %v; want it refused", err)
	}
}

func mustCheckIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// pgtestDSN is a DSN for the database pgtest isolated the test in: a clone
// database or a schema on the search_path.
func pgtestDSN(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	u, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + pool.Config().ConnConfig.Database
	if sp := pool.Config().ConnConfig.RuntimeParams["search_path"]; sp != "" {
		q := u.Query()
		q.Set("search_path", sp)
		u.RawQuery = q.Encode()
	}
	return u.String()
}
