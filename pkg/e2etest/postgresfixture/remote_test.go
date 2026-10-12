package postgresfixture

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRemoteControlAuthenticatesAndPreservesErrors(t *testing.T) {
	local := &Provider{major: 16, materials: make(map[string]mp.CredentialMaterial)}
	endpoint, token := serveProvider(t, local)
	config := mp.BackendConfig{Settings: map[string]string{"control_url": endpoint}, SecretEnv: map[string]string{"control-token": "TOKEN"}}
	p, err := NewRemote(config, func(string) string { return token })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.(mp.ResourceDiscoverer).Discover(t.Context(), mp.ResourceDiscoveryRequest{}); !errors.Is(err, mp.ErrNotFound) {
		t.Fatalf("discovery error = %v", err)
	}
	created, err := p.Provision(t.Context(), mp.ProvisionRequest{ResourceID: "owned", Spec: mp.Spec{PostgresMajor: 16}})
	if err != nil || created.ProviderResourceID != "fixture-owned" {
		t.Fatal("remote provision failed")
	}
	if _, err := p.Inspect(t.Context(), created.ProviderResourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRemote(config, func(string) string { return "wrong" }); !errors.Is(err, mp.ErrUnavailable) {
		t.Fatal("control API accepted an invalid token")
	}
	if _, err := p.Update(t.Context(), mp.UpdateRequest{}); !errors.Is(err, mp.ErrUnsupported) {
		t.Fatal("fixture advertised unsupported mutation")
	}
	for _, endpoint := range []string{"https://127.0.0.1:1", "http://example.test:1", "http://169.254.169.254:80", "http://user:password@127.0.0.1:1", "http://127.0.0.1:1?token=secret", "http://127.0.0.1:1/path"} {
		config.Settings["control_url"] = endpoint
		if _, err := NewRemote(config, func(string) string { return token }); !errors.Is(err, mp.ErrInvalid) {
			t.Fatalf("unsafe fixture endpoint accepted: %s", endpoint)
		}
	}
}

func TestSQLFixtureSerialAuthorityAndRemoteRevocation(t *testing.T) {
	catalog := pgtest.OpenMigrated(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fixture := New(t, catalog, identity, []byte(strings.Repeat("h", 32)), "", 0)
	store := state.NewPgStore(catalog)
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@sql-fixture.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "fixture-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	database, bindings := fixture.Create(t, account.ID, app.ID)
	t.Cleanup(func() {
		for _, b := range bindings {
			if _, err := fixture.Bindings.Delete(context.Background(), account.ID, b.ID); err != nil {
				t.Error("binding cleanup failed")
			}
		}
		if _, err := fixture.Service.Delete(context.Background(), account.ID, database.ID); err != nil {
			t.Error("database cleanup failed")
		}
	})
	connect := func(access mp.CredentialAccess) string {
		t.Helper()
		value, err := fixture.Observer.URI(t.Context(), bindings[access])
		if err != nil {
			t.Fatal("credential observation failed")
		}
		return value
	}
	migration, err := mp.ConnectCredentialSQL(t.Context(), connect(mp.CredentialMigration))
	if err != nil {
		t.Fatal("migration login failed")
	}
	defer func() { _ = migration.Close(context.Background()) }()
	if _, err := migration.Exec(t.Context(), `CREATE TABLE serial_fixture(id SERIAL PRIMARY KEY, body text NOT NULL)`); err != nil {
		t.Fatal("migration DDL failed")
	}
	writerURI := connect(mp.CredentialReadWrite)
	writer, err := mp.ConnectCredentialSQL(t.Context(), writerURI)
	if err != nil {
		t.Fatal("writer login failed")
	}
	defer func() { _ = writer.Close(context.Background()) }()
	var id int
	if err := writer.QueryRow(t.Context(), `INSERT INTO serial_fixture(body) VALUES('retained') RETURNING id`).Scan(&id); err != nil || id != 1 {
		t.Fatal("writer lacks SERIAL authority")
	}
	reader, err := mp.ConnectCredentialSQL(t.Context(), connect(mp.CredentialReadOnly))
	if err != nil {
		t.Fatal("reader login failed")
	}
	defer func() { _ = reader.Close(context.Background()) }()
	if _, err := reader.Exec(t.Context(), `INSERT INTO serial_fixture(body) VALUES('forbidden')`); err == nil {
		t.Fatal("reader could write")
	}
	if _, err := reader.Exec(t.Context(), `SELECT nextval('serial_fixture_id_seq')`); err == nil {
		t.Fatal("reader could advance sequence")
	}
	remote, err := NewRemote(fixture.Config.Backends[0], func(string) string { return fixture.remoteToken })
	if err != nil {
		t.Fatal(err)
	}
	request := mp.CredentialRequest{IdentityKey: "replacement", Access: mp.CredentialReadWrite}
	material, err := remote.IssueCredentials(t.Context(), request)
	if err != nil {
		t.Fatal("remote replacement issue failed")
	}
	defer func() {
		if err := remote.RevokeCredentials(context.Background(), request); err != nil {
			t.Error("replacement cleanup failed")
		}
	}()
	parsed, _ := url.Parse(writerURI)
	parsed.User = url.UserPassword(material.Username, material.Password)
	replacement, err := mp.ConnectCredentialSQL(t.Context(), parsed.String())
	if err != nil {
		t.Fatal("replacement login failed")
	}
	defer func() { _ = replacement.Close(context.Background()) }()
	if err := replacement.QueryRow(t.Context(), `INSERT INTO serial_fixture(body) VALUES('replacement') RETURNING id`).Scan(&id); err != nil || id != 2 {
		t.Fatal("replacement lacks existing sequence authority")
	}
	old, _ := url.Parse(writerURI)
	fixture.Provider.mu.Lock()
	oldIdentity := ""
	for identity, material := range fixture.Provider.materials {
		if material.Username == old.User.Username() {
			oldIdentity = identity
			break
		}
	}
	fixture.Provider.mu.Unlock()
	if oldIdentity == "" {
		t.Fatal("missing old fixture identity")
	}
	if err := remote.RevokeCredentials(t.Context(), mp.CredentialRequest{IdentityKey: oldIdentity}); err != nil {
		t.Fatal("remote revoke failed")
	}
	if writer.Ping(t.Context()) == nil || fixture.VerifyRevoked(t.Context(), writerURI) != nil {
		t.Fatal("revoked held session or fresh authentication survived")
	}
	var rows int
	if err := replacement.QueryRow(t.Context(), `SELECT count(*) FROM serial_fixture`).Scan(&rows); err != nil || rows != 2 {
		t.Fatal("retirement lost SQL data")
	}
}
