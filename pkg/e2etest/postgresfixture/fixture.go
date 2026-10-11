// Package postgresfixture supplies disposable provider management backed by
// real PostgreSQL roles. It is test infrastructure, never a production driver.
package postgresfixture

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/state"
)

type Fixture struct {
	Catalog  *mp.PostgresStore
	Service  *mp.Service
	Bindings *mp.BindingService
	Observer credentialdelivery.Observer
	Data     *pgxpool.Pool
	Provider *Provider
	Major    int
	Enabled  bool
}

func roleAbsent(err error) bool {
	var pgerr *pgconn.PgError
	return errors.As(err, &pgerr) && pgerr.Code == "42704"
}

// New uses a separate, empty customer database. host/port optionally address a
// TLS passthrough proxy reachable by guests; they never redirect catalog SQL.
func New(t *testing.T, catalog *pgxpool.Pool, identity *age.X25519Identity, hmac []byte, host string, port uint16) *Fixture {
	t.Helper()
	data := pgtest.OpenDatabase(t)
	if data == nil {
		return nil
	}
	ctx := t.Context()
	var ssl string
	var version int
	if err := data.QueryRow(ctx, "SHOW ssl").Scan(&ssl); err != nil || ssl != "on" {
		t.Fatal("PostgreSQL fixture requires native TLS")
	}
	if err := data.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil {
		t.Fatal("read fixture SQL version")
	}
	major := version / 10000
	config := data.Config().ConnConfig
	if host == "" {
		host = config.Host
	}
	if port == 0 {
		port = config.Port
	}
	provider := &Provider{pool: data, owner: "gpo_" + strings.ReplaceAll(uuid.NewString(), "-", ""), host: host, port: port, major: major, materials: make(map[string]mp.CredentialMaterial)}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, role := range append(provider.roles, provider.owner) {
			if _, err := data.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); err != nil && !roleAbsent(err) {
				t.Error("SQL fixture ownership cleanup failed")
			}
			if _, err := data.Exec(cleanup, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("SQL fixture role cleanup failed")
			}
		}
	})
	for _, statement := range []string{
		"CREATE ROLE " + pgx.Identifier{provider.owner}.Sanitize() + " NOLOGIN NOINHERIT",
		"REVOKE ALL ON DATABASE " + pgx.Identifier{config.Database}.Sanitize() + " FROM PUBLIC",
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
		"ALTER SCHEMA public OWNER TO " + pgx.Identifier{provider.owner}.Sanitize(),
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{config.Database}.Sanitize() + " TO " + pgx.Identifier{provider.owner}.Sanitize(),
	} {
		if _, err := data.Exec(ctx, statement); err != nil {
			t.Fatal("prepare isolated SQL authority")
		}
	}
	registry, err := mp.NewRegistry(mp.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "native-sql"}, Backends: []mp.BackendConfig{{ID: "native-sql", Driver: "sqlfixture", Region: "us-east-1", Namespace: "disposable"}}}, func(string) string { return "" }, map[string]mp.Factory{"sqlfixture": func(mp.BackendConfig, func(string) string) (mp.Provider, error) { return provider, nil }})
	if err != nil {
		t.Fatal(err)
	}
	store, err := mp.NewPostgresStore(catalog)
	if err != nil {
		t.Fatal(err)
	}
	secrets := state.NewPgStore(catalog)
	sink, err := credentialdelivery.New(secrets, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return hmac })
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{Catalog: store, Data: data, Provider: provider, Major: major, Enabled: true, Observer: credentialdelivery.Observer{Store: secrets, Identity: identity, HMACKey: hmac, PostgresMajor: major}}
	f.Service, err = mp.NewService(registry, store, mp.ServiceOptions{ProvisioningEnabled: func() bool { return f.Enabled }})
	if err != nil {
		t.Fatal(err)
	}
	f.Bindings, err = mp.NewBindingService(registry, store, store, sink, mp.BindingServiceOptions{ProvisioningEnabled: func() bool { return f.Enabled }})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *Fixture) Create(t *testing.T, account, app string) (mp.Database, map[mp.CredentialAccess]mp.Binding) {
	t.Helper()
	database, err := f.Service.Create(t.Context(), mp.CreateRequest{AccountID: account, Name: "native-" + uuid.NewString()[:8], Spec: mp.Spec{Region: "us-east-1", PostgresMajor: f.Major, Class: mp.ClassDevelopment, Availability: mp.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	bindings := make(map[mp.CredentialAccess]mp.Binding)
	for _, item := range []struct {
		access mp.CredentialAccess
		key    string
	}{{mp.CredentialReadWrite, "DATABASE_URL"}, {mp.CredentialReadOnly, "READ_DATABASE_URL"}, {mp.CredentialMigration, "MIGRATION_DATABASE_URL"}} {
		binding, err := f.Bindings.Create(t.Context(), mp.CreateBindingRequest{AccountID: account, AppID: app, DatabaseID: database.ID, Scope: "default", EnvironmentKey: item.key, Access: item.access})
		if err != nil {
			t.Fatal(err)
		}
		bindings[item.access] = binding
	}
	return database, bindings
}

type Provider struct {
	mu                    sync.Mutex
	pool                  *pgxpool.Pool
	owner, host, resource string
	port                  uint16
	major                 int
	spec                  mp.Spec
	materials             map[string]mp.CredentialMaterial
	roles                 []string
}

func (p *Provider) Capabilities() mp.Capabilities {
	return mp.Capabilities{PostgresMajors: []int{p.major}, ServiceClasses: []mp.ServiceClass{mp.ClassDevelopment}, Availability: []mp.Availability{mp.AvailabilitySingleZone}, CredentialAccess: []mp.CredentialAccess{mp.CredentialReadWrite, mp.CredentialReadOnly, mp.CredentialMigration}, ScaleToZero: true, PooledConnections: true, MaxStorageBytes: 1 << 30, UsageMeters: []mp.Meter{mp.MeterComputeUnitSeconds}}
}
func (p *Provider) observed() mp.ObservedDatabase {
	return mp.ObservedDatabase{ProviderResourceID: p.resource, DataResourceID: p.resource + "/dataset", Status: mp.ProviderStatusReady, Spec: p.spec}
}
func (p *Provider) Provision(_ context.Context, r mp.ProvisionRequest) (mp.ObservedDatabase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resource == "" {
		p.resource = "fixture-" + r.ResourceID
		p.spec = r.Spec
	}
	return p.observed(), nil
}
func (p *Provider) Inspect(context.Context, string) (mp.ObservedDatabase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.observed(), nil
}
func (p *Provider) Discover(context.Context, mp.ResourceDiscoveryRequest) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resource == "" {
		return "", mp.ErrNotFound
	}
	return p.resource, nil
}
func (*Provider) Update(context.Context, mp.UpdateRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (*Provider) Restore(context.Context, mp.RestoreRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (p *Provider) Delete(context.Context, mp.DeleteRequest) (mp.DeleteResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.materials) != 0 {
		return mp.DeleteResult{}, mp.ErrConflict
	}
	return mp.DeleteResult{Done: true}, nil
}
func (*Provider) Usage(_ context.Context, _ string, w mp.UsageWindow) (mp.Usage, error) {
	return mp.Usage{Window: w}, nil
}

func (p *Provider) IssueCredentials(ctx context.Context, r mp.CredentialRequest) (mp.CredentialMaterial, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if material, ok := p.materials[r.IdentityKey]; ok {
		return material, nil
	}
	role := "gpr_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := uuid.NewString()
	quoted := pgx.Identifier{role}.Sanitize()
	database := p.pool.Config().ConnConfig.Database
	p.roles = append(p.roles, role)
	if _, err := p.pool.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN NOINHERIT PASSWORD '"+password+"'"); err != nil {
		return mp.CredentialMaterial{}, mp.ErrUnavailable
	}
	statements := []string{"GRANT CONNECT ON DATABASE " + pgx.Identifier{database}.Sanitize() + " TO " + quoted}
	if r.Access == mp.CredentialMigration {
		statements = append(statements, "GRANT "+pgx.Identifier{p.owner}.Sanitize()+" TO "+quoted, "ALTER ROLE "+quoted+" IN DATABASE "+pgx.Identifier{database}.Sanitize()+" SET role TO '"+p.owner+"'")
	} else {
		grants := "SELECT"
		if r.Access == mp.CredentialReadWrite {
			grants = "SELECT, INSERT, UPDATE, DELETE"
		} else if r.Access != mp.CredentialReadOnly {
			return mp.CredentialMaterial{}, mp.ErrUnsupported
		}
		statements = append(statements, "GRANT USAGE ON SCHEMA public TO "+quoted, "GRANT "+grants+" ON ALL TABLES IN SCHEMA public TO "+quoted, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{p.owner}.Sanitize()+" IN SCHEMA public GRANT "+grants+" ON TABLES TO "+quoted)
	}
	for _, statement := range statements {
		if _, err := p.pool.Exec(ctx, statement); err != nil {
			return mp.CredentialMaterial{}, mp.ErrUnavailable
		}
	}
	material := mp.CredentialMaterial{ProviderIdentityID: role, Username: role, Password: password, Database: database, TLSMode: "require", Endpoints: []mp.Endpoint{{Role: mp.EndpointDirect, Host: p.host, Port: p.port}, {Role: mp.EndpointPooled, Host: p.host, Port: p.port}}}
	p.materials[r.IdentityKey] = material
	return material, nil
}

func (p *Provider) RevokeCredentials(ctx context.Context, r mp.CredentialRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	material, ok := p.materials[r.IdentityKey]
	if !ok {
		return nil
	}
	if _, err := p.pool.Exec(ctx, "ALTER ROLE "+pgx.Identifier{material.Username}.Sanitize()+" NOLOGIN"); err != nil {
		return mp.ErrUnavailable
	}
	if _, err := p.pool.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()", material.Username); err != nil {
		return mp.ErrUnavailable
	}
	delete(p.materials, r.IdentityKey)
	return nil
}

// VerifyRevoked requires a healthy administrator connection before treating a
// failed fresh login as proof. Outage must never be mistaken for revocation.
func (f *Fixture) VerifyRevoked(ctx context.Context, uri string) error {
	if f.Data.Ping(ctx) != nil {
		return mp.ErrUnavailable
	}
	config, err := pgx.ParseConfig(uri)
	if err != nil || config.TLSConfig == nil {
		return mp.ErrConflict
	}
	config.TLSConfig.MinVersion = tls.VersionTLS12
	config.Fallbacks = nil
	clear(config.RuntimeParams)
	conn, err := pgx.ConnectConfig(ctx, config)
	if err == nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return mp.ErrConflict
	}
	var auth *pgconn.PgError
	if !errors.As(err, &auth) || (auth.Code != "28000" && auth.Code != "28P01") {
		return mp.ErrUnavailable
	}
	return nil
}
