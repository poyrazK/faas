//go:build managed_postgres_native && metal

package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Portable half of the native gate: real authenticated routes, production
// loader/sealer/reconciler, durable catalog and restricted SQL roles. No VM
// lifecycle is claimed by this test. The native runner adds deployed guests.
func TestManagedPostgresFixturePublicRotation(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("public rotation qualification requires an explicit available SQL fixture")
	}
	// This explicitly invoked qualification gate must never report a skipped
	// pgtest connection as passing evidence.
	t.Cleanup(func() {
		if t.Skipped() {
			t.Error("public rotation qualification did not execute")
		}
	})
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	pool := pgtest.OpenMigrated(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	hmac := []byte(strings.Repeat("h", 32))
	fixture := postgresfixture.New(t, pool, identity, hmac, "", 0)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@fixture-api.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountEmailVerified(t.Context(), account.ID); err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, hash, "owned-test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "fixture-api-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	database, bindings := fixture.Create(t, account.ID, app.ID)
	t.Cleanup(func() {
		for _, binding := range bindings {
			if _, err := fixture.Bindings.Delete(context.Background(), account.ID, binding.ID); err != nil {
				t.Error("API fixture binding cleanup failed")
			}
		}
		if _, err := fixture.Service.Delete(context.Background(), account.ID, database.ID); err != nil {
			t.Error("API fixture database cleanup failed")
		}
	})
	previousRecipient, previousHMAC, previousIdentities, previousIdentity := setSecretRecipient, hostHMACKey, mfaIdentities, mfaIdentity
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	hostHMACKey = func() []byte { return hmac }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	mfaIdentity = func() *age.X25519Identity { return identity }
	t.Cleanup(func() {
		setSecretRecipient = previousRecipient
		hostHMACKey = previousHMAC
		mfaIdentities = previousIdentities
		mfaIdentity = previousIdentity
	})
	environment := make(map[string]string)
	for _, entry := range fixture.DaemonEnvironment(t) {
		name, value, _ := strings.Cut(entry, "=")
		environment[name] = value
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var httpServer *httptest.Server
	var stop func()
	restart := func() {
		t.Helper()
		if stop != nil {
			stop()
		}
		service, reconciler, bindingService, bindingReconciler, usage, health, err := loadManagedPostgres(pool, func(name string) string { return environment[name] }, logger, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		server := newServerWithDeps(store, logger, "gregale.dev", noopNotifier{}, "", noopMailer{}, nil, nil, nil, 15*time.Minute, "").WithManagedPostgres(service, reconciler, bindingService, bindingReconciler, usage, health)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- bindingReconciler.Run(ctx) }()
		httpServer = httptest.NewServer(server.handler())
		current := httpServer
		stop = func() { current.Close(); cancel(); <-done }
	}
	t.Cleanup(func() {
		if stop != nil {
			stop()
		}
	})
	restart()
	request := func(method, path, credential string, want int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, httpServer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		response, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatal("public fixture API request failed")
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil || response.StatusCode != want {
			t.Fatalf("public %s %s status=%d want=%d", method, path, response.StatusCode, want)
		}
		if strings.Contains(string(data), "postgres://") || strings.Contains(string(data), "postgresql://") {
			t.Fatal("public rotation exposed a connection URL")
		}
		return data
	}
	old := bindings[mp.CredentialReadWrite]
	oldURI, err := fixture.Observer.URI(t.Context(), old)
	if err != nil {
		t.Fatal(err)
	}
	oldSession, err := mp.ConnectCredentialSQL(t.Context(), oldURI)
	if err != nil {
		t.Fatal("old credential did not authenticate")
	}
	defer func() { _ = oldSession.Close(context.Background()) }()
	migrationURI, err := fixture.Observer.URI(t.Context(), bindings[mp.CredentialMigration])
	if err != nil {
		t.Fatal(err)
	}
	migration, err := mp.ConnectCredentialSQL(t.Context(), migrationURI)
	if err != nil {
		t.Fatal("migration login failed")
	}
	defer func() { _ = migration.Close(context.Background()) }()
	schema, err := templates.FS.ReadFile("rest-api-postgres/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(t.Context(), string(schema)); err != nil {
		t.Fatal("shipped schema migration failed")
	}
	if _, err := oldSession.Exec(t.Context(), `INSERT INTO notes(body) VALUES('before-rotation')`); err != nil {
		t.Fatal("initial runtime write failed")
	}
	path := "/v1/postgres/bindings/" + old.ID
	request(http.MethodPost, path+"/rotate", "", http.StatusUnauthorized)
	var response api.ManagedPostgresBinding
	if json.Unmarshal(request(http.MethodPost, path+"/rotate", key, http.StatusOK), &response) != nil || response.CredentialGeneration != old.CredentialGeneration+1 {
		t.Fatal("public rotation did not advance credential generation")
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		if json.Unmarshal(request(http.MethodGet, path, key, http.StatusOK), &response) != nil {
			t.Fatal("invalid public binding response")
		}
		if !response.RotationPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("production binding reconciler did not finish public rotation")
		}
		time.Sleep(100 * time.Millisecond)
	}
	current, err := fixture.Catalog.GetBinding(t.Context(), account.ID, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	bindings[mp.CredentialReadWrite] = current
	if oldSession.Ping(t.Context()) == nil || fixture.VerifyRevoked(t.Context(), oldURI) != nil {
		t.Fatal("public rotation did not revoke held and fresh old logins")
	}
	if fixture.Observer.Credential(t.Context(), current) != nil {
		t.Fatal("public rotation delivered an unusable credential")
	}
	replacementURI, err := fixture.Observer.URI(t.Context(), current)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := mp.ConnectCredentialSQL(t.Context(), replacementURI)
	if err != nil {
		t.Fatal("replacement login failed")
	}
	defer func() { _ = replacement.Close(context.Background()) }()
	if _, err := replacement.Exec(t.Context(), `INSERT INTO notes(body) VALUES('after-rotation')`); err != nil {
		t.Fatal("replacement runtime lost writer or sequence authority")
	}
	var rows, preserved int
	if err := fixture.Data.QueryRow(t.Context(), `SELECT count(*),count(DISTINCT body) FILTER (WHERE body IN ('before-rotation','after-rotation')) FROM notes`).Scan(&rows, &preserved); err != nil || rows != 2 || preserved != 2 {
		t.Fatal("public rotation lost or duplicated SQL data")
	}
	restart()
	if json.Unmarshal(request(http.MethodGet, path, key, http.StatusOK), &response) != nil || response.RotationPending || response.CredentialGeneration != current.CredentialGeneration {
		t.Fatal("completed public rotation did not survive API restart")
	}
}
