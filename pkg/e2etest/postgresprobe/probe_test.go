package postgresprobe

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProbeCommandAdapterRejectsShellSource(t *testing.T) {
	for _, argv := range [][]string{nil, {"sh"}, {"sh", "-c", "/postgres-probe migrate"}, {"/bin/sh", "-lc", "/postgres-probe migrate; echo leaked"}, {"/postgres-probe", "check-runtime", "extra"}, {"/postgres-probe", "unknown"}} {
		if _, err := Mode(argv); err == nil {
			t.Fatal("accepted unsupported probe command")
		}
	}
	for _, item := range []struct {
		argv []string
		mode string
	}{
		{[]string{"/postgres-probe"}, "serve"},
		{[]string{"/postgres-probe", "check-runtime"}, "check-runtime"},
		{[]string{"/bin/sh", "-lc", "/postgres-probe migrate"}, "migrate"},
	} {
		if mode, err := Mode(item.argv); err != nil || mode != item.mode {
			t.Fatal("valid fixture command rejected")
		}
	}
}

func TestProbeRejectsMigrationSecretsInServingProcess(t *testing.T) {
	config := Config{RunID: uuid.NewString(), RuntimeURI: "private-runtime", ReaderURI: "private-reader", MigrationURI: "private-migration", PostgresMajor: 16}
	for _, path := range []string{"/healthz", "/probe"} {
		response := httptest.NewRecorder()
		config.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private-") {
			t.Fatalf("migration leakage was not rejected: status=%d", response.Code)
		}
	}
}

func TestProbeSQLDataPrivilegesRotationAndFailure(t *testing.T) {
	catalog := pgtest.OpenMigrated(t)
	if catalog == nil {
		return
	}
	ctx := t.Context()
	store := state.NewPgStore(catalog)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@native.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "probe-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "go124", RAMMB: 256, CPUMillicores: 250})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	proxy := postgresfixture.OpenProxy(t, catalog, "127.0.0.1", 0)
	fixture := postgresfixture.New(t, catalog, identity, []byte(strings.Repeat("h", 32)), proxy.Host, proxy.Port)
	if fixture == nil {
		return
	}
	database, bindings := fixture.Create(t, account.ID, app.ID)
	getURI := func(access mp.CredentialAccess) string {
		value, err := fixture.Observer.URI(ctx, bindings[access])
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	config := Config{RunID: uuid.NewString(), RuntimeURI: getURI(mp.CredentialReadWrite), ReaderURI: getURI(mp.CredentialReadOnly), MigrationURI: getURI(mp.CredentialMigration), PostgresMajor: fixture.Major}
	if err := config.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	migrationURI := config.MigrationURI
	config.MigrationURI = ""
	if err := fixture.VerifyRevoked(ctx, config.RuntimeURI); err == nil {
		t.Fatal("live credential incorrectly reported revoked")
	}
	refused, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead, err := url.Parse(config.RuntimeURI)
	if err != nil {
		t.Fatal("invalid fixture URI")
	}
	dead.Host = refused.Addr().String()
	_ = refused.Close()
	if err := fixture.VerifyRevoked(ctx, dead.String()); !errors.Is(err, mp.ErrUnavailable) {
		t.Fatal("SQL outage incorrectly reported as revocation")
	}
	for _, invalid := range []Config{
		{RunID: config.RunID, RuntimeURI: config.ReaderURI, ReaderURI: config.ReaderURI, PostgresMajor: fixture.Major},
		{RunID: config.RunID, RuntimeURI: config.RuntimeURI, ReaderURI: config.RuntimeURI, PostgresMajor: fixture.Major},
	} {
		if _, err := invalid.Check(ctx); err == nil {
			t.Fatal("probe accepted incorrect SQL authority")
		}
	}
	first, err := config.Check(ctx)
	if err != nil || first.Counter != 1 {
		t.Fatalf("initial SQL probe: counter=%d error=%v", first.Counter, err)
	}
	config.MigrationURI = migrationURI
	if err := config.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	config.MigrationURI = ""
	second, err := config.Check(ctx)
	if err != nil || second.Counter != 2 || first.Marker != second.Marker {
		t.Fatal("release retry reset SQL data")
	}
	oldURI := config.RuntimeURI
	rotated, err := fixture.Bindings.Rotate(ctx, account.ID, bindings[mp.CredentialReadWrite].ID)
	if err != nil {
		t.Fatal(err)
	}
	bindings[mp.CredentialReadWrite] = rotated
	config.RuntimeURI = getURI(mp.CredentialReadWrite)
	third, err := config.Check(ctx)
	if err != nil || third.Counter != 3 || third.RuntimeProof == first.RuntimeProof || third.ReaderProof != first.ReaderProof {
		t.Fatal("rotated SQL probe failed to preserve data/credential identity")
	}
	// This SQL-only test supplies the retirement receipt; the native test must
	// obtain it from the real scheduler after draining guest instances.
	if err := store.FinalizeManagedPostgresBindingRotationsForApp(ctx, app.ID, rotated.RotationWakeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Bindings.ReconcileRotationCleanup(ctx, account.ID, rotated.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.VerifyRevoked(ctx, oldURI); err != nil {
		t.Fatal("old credential still authenticates")
	}
	response := httptest.NewRecorder()
	config.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/probe", nil))
	var result Result
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Counter != 4 {
		t.Fatal("HTTP SQL workload failed")
	}
	if strings.Contains(response.Body.String(), "postgres://") {
		t.Fatal("probe leaked URI")
	}
	if _, err := fixture.Data.Exec(ctx, "TRUNCATE public.gregale_durable_qualification_probe"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Check(ctx); err == nil {
		t.Fatal("probe repaired or accepted missing data")
	}
	fixture.Enabled = false
	for _, binding := range bindings {
		uri, err := fixture.Observer.URI(ctx, binding)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Bindings.Delete(ctx, account.ID, binding.ID); err != nil {
			t.Fatal(err)
		}
		if err := fixture.Observer.Deleted(ctx, binding); err != nil {
			t.Fatal(err)
		}
		if err := fixture.VerifyRevoked(ctx, uri); err != nil {
			t.Fatal("deleted binding still authenticates")
		}
	}
	if _, err := fixture.Service.Delete(context.Background(), account.ID, database.ID); err != nil {
		t.Fatal(err)
	}
}
