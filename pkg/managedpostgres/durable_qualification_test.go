// adr: 731 — durable catalog and encrypted credential restart qualification.
package managedpostgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/state"
)

// SQL is real (including password authentication, ACLs and encrypted app
// secrets); only provider management is simulated. A provider instance is
// discarded at every restart; its externally committed effects survive.
type durableSQLAuthority struct {
	t                             *testing.T
	pool                          *pgxpool.Pool
	owner                         string
	resource                      string
	spec                          mp.Spec
	materials                     map[string]mp.CredentialMaterial
	retired                       []mp.CredentialMaterial
	roles                         []string
	creates, instances, workloads int
	deleted                       bool
	deleteErr                     error
}

type durableSQLProvider struct {
	a          *durableSQLAuthority
	stage      mp.DurableQualificationStage
	fault      *bool
	disconnect func()
}

func (p *durableSQLProvider) Capabilities() mp.Capabilities {
	return mp.Capabilities{PostgresMajors: []int{16}, ServiceClasses: []mp.ServiceClass{mp.ClassDevelopment}, Availability: []mp.Availability{mp.AvailabilitySingleZone}, CredentialAccess: []mp.CredentialAccess{mp.CredentialReadWrite, mp.CredentialMigration}, PooledConnections: true, ScaleToZero: true, MaxStorageBytes: 1 << 30, UsageMeters: []mp.Meter{mp.MeterComputeUnitSeconds}}
}

func (p *durableSQLProvider) Provision(_ context.Context, r mp.ProvisionRequest) (mp.ObservedDatabase, error) {
	if p.a.resource == "" {
		p.a.resource = "project-" + r.ResourceID
		p.a.creates++
		p.a.spec = r.Spec
	}
	if p.stage.Fault == mp.QualificationProvisionAck && !*p.fault {
		*p.fault = true
		p.disconnect()
		return mp.ObservedDatabase{}, mp.ErrUnavailable
	}
	return p.observed(), nil
}

func (p *durableSQLProvider) observed() mp.ObservedDatabase {
	return mp.ObservedDatabase{ProviderResourceID: p.a.resource, DataResourceID: p.a.resource + "/branch", Status: mp.ProviderStatusReady, Spec: p.a.spec}
}

func (p *durableSQLProvider) Inspect(context.Context, string) (mp.ObservedDatabase, error) {
	return p.observed(), nil
}
func (p *durableSQLProvider) Discover(context.Context, mp.ResourceDiscoveryRequest) (string, error) {
	if p.a.resource == "" {
		return "", mp.ErrNotFound
	}
	return p.a.resource, nil
}
func (*durableSQLProvider) Update(context.Context, mp.UpdateRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (*durableSQLProvider) Restore(context.Context, mp.RestoreRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (p *durableSQLProvider) Delete(context.Context, mp.DeleteRequest) (mp.DeleteResult, error) {
	if p.a.deleteErr != nil {
		return mp.DeleteResult{}, p.a.deleteErr
	}
	p.a.deleted = true
	return mp.DeleteResult{Done: true}, nil
}
func (*durableSQLProvider) Usage(_ context.Context, _ string, w mp.UsageWindow) (mp.Usage, error) {
	return mp.Usage{Window: w}, nil
}

func (p *durableSQLProvider) IssueCredentials(ctx context.Context, r mp.CredentialRequest) (mp.CredentialMaterial, error) {
	m, ok := p.a.materials[r.IdentityKey]
	if !ok {
		role := "dq_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		password := uuid.NewString()
		quoted := pgx.Identifier{role}.Sanitize()
		p.a.exec(ctx, "CREATE ROLE "+quoted+" LOGIN NOINHERIT PASSWORD '"+password+"'") // Test-only DDL; password is generated UUID.
		p.a.roles = append(p.a.roles, role)
		database := p.a.pool.Config().ConnConfig.Database
		p.a.exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{database}.Sanitize()+" TO "+quoted)
		if r.Access == mp.CredentialMigration {
			p.a.exec(ctx, "GRANT "+pgx.Identifier{p.a.owner}.Sanitize()+" TO "+quoted)
			p.a.exec(ctx, "ALTER ROLE "+quoted+" IN DATABASE "+pgx.Identifier{database}.Sanitize()+" SET role TO '"+p.a.owner+"'")
		} else {
			p.a.exec(ctx, "GRANT USAGE ON SCHEMA public TO "+quoted)
			p.a.exec(ctx, "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "+quoted)
			p.a.exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{p.a.owner}.Sanitize()+" IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "+quoted)
		}
		config := p.a.pool.Config().ConnConfig
		m = mp.CredentialMaterial{ProviderIdentityID: role, Username: role, Password: password, Database: database, TLSMode: "require", Endpoints: []mp.Endpoint{{Role: mp.EndpointPooled, Host: config.Host, Port: config.Port}, {Role: mp.EndpointDirect, Host: config.Host, Port: config.Port}}}
		p.a.materials[r.IdentityKey] = m
	}
	if p.stage.Fault == mp.QualificationCredentialAck && !*p.fault {
		*p.fault = true
		p.disconnect()
		return mp.CredentialMaterial{}, mp.ErrUnavailable
	}
	return m, nil
}

func (p *durableSQLProvider) RevokeCredentials(ctx context.Context, r mp.CredentialRequest) error {
	if m, ok := p.a.materials[r.IdentityKey]; ok {
		p.a.exec(ctx, "ALTER ROLE "+pgx.Identifier{m.Username}.Sanitize()+" NOLOGIN")
		p.a.retired = append(p.a.retired, m)
		delete(p.a.materials, r.IdentityKey)
	}
	return nil
}

func (a *durableSQLAuthority) exec(ctx context.Context, sql string) {
	a.t.Helper()
	if _, err := a.pool.Exec(ctx, sql); err != nil {
		a.t.Fatalf("fixture DDL: %v", err)
	}
}

type durableFaultSink struct {
	mp.CredentialSink
	stage      mp.DurableQualificationStage
	observed   *bool
	disconnect func()
}

func (s *durableFaultSink) Put(ctx context.Context, b mp.Binding, m mp.CredentialMaterial) (string, error) {
	ref, err := s.CredentialSink.Put(ctx, b, m)
	if err == nil && s.stage.Fault == mp.QualificationSecretAck && !*s.observed {
		*s.observed = true
		s.disconnect()
		return "", mp.ErrUnavailable
	}
	return ref, err
}

func durableFixture(t *testing.T) (mp.DurableLifecycleOptions, mp.DurableLifecycleFactory, *durableSQLAuthority, *pgxpool.Pool) {
	t.Helper()
	catalogPool := pgtest.OpenMigrated(t)
	dataPool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	store := state.NewPgStore(catalogPool)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@durable.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "durable-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Runtime: "go124", RAMMB: 256, CPUMillicores: 250})
	if err != nil {
		t.Fatal(err)
	}
	options := mp.DurableLifecycleOptions{RunID: uuid.NewString(), MigrationEnvironmentKey: "MIGRATION_DATABASE_URL", LifecycleQualificationOptions: mp.LifecycleQualificationOptions{
		AccountID: account.ID, AppID: app.ID, DatabaseName: "durable-test", Scope: "default", EnvironmentKey: "DATABASE_URL", Access: mp.CredentialReadWrite,
		Spec: mp.Spec{Region: "us-east-1", PostgresMajor: 16, Class: mp.ClassDevelopment, Availability: mp.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30}, Timeout: 30 * time.Second,
	}}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	hmac := []byte(strings.Repeat("h", 32))
	a := &durableSQLAuthority{t: t, pool: dataPool, owner: "dqo_" + strings.ReplaceAll(uuid.NewString(), "-", ""), materials: make(map[string]mp.CredentialMaterial)}
	a.exec(ctx, "CREATE ROLE "+pgx.Identifier{a.owner}.Sanitize()+" NOLOGIN NOINHERIT")
	a.exec(ctx, "REVOKE ALL ON DATABASE "+pgx.Identifier{dataPool.Config().ConnConfig.Database}.Sanitize()+" FROM PUBLIC")
	a.exec(ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC")
	a.exec(ctx, "ALTER SCHEMA public OWNER TO "+pgx.Identifier{a.owner}.Sanitize())
	a.exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{dataPool.Config().ConnConfig.Database}.Sanitize()+" TO "+pgx.Identifier{a.owner}.Sanitize())
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, role := range append(a.roles, a.owner) {
			if _, err := dataPool.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("drop owned: %v", err)
			}
			if _, err := dataPool.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("drop role: %v", err)
			}
		}
	})
	now := time.Now().UTC()
	factory := func(ctx context.Context, stage mp.DurableQualificationStage) (mp.DurableLifecycleSession, error) {
		a.instances++
		// Move across retry cooldowns without bypassing catalog leases or waits.
		now = now.Add(2 * time.Minute)
		at := now
		pool, err := pgxpool.NewWithConfig(ctx, catalogPool.Config().Copy())
		if err != nil {
			return mp.DurableLifecycleSession{}, err
		}
		failed := true
		defer func() {
			if failed {
				pool.Close()
			}
		}()
		catalog, err := mp.NewPostgresStore(pool)
		if err != nil {
			return mp.DurableLifecycleSession{}, err
		}
		observed := false
		provider := &durableSQLProvider{a: a, stage: stage, fault: &observed, disconnect: pool.Close}
		registry, err := mp.NewRegistry(mp.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "sql"}, Backends: []mp.BackendConfig{{ID: "sql", Driver: "sqlfixture", Region: "us-east-1", Namespace: "disposable"}}}, func(string) string { return "" }, map[string]mp.Factory{"sqlfixture": func(mp.BackendConfig, func(string) string) (mp.Provider, error) { return provider, nil }})
		if err != nil {
			return mp.DurableLifecycleSession{}, err
		}
		secretStore := state.NewPgStore(pool)
		sink, err := credentialdelivery.New(secretStore, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return hmac })
		if err != nil {
			return mp.DurableLifecycleSession{}, err
		}
		enabled := func() bool { return !stage.Cleanup }
		service, err := mp.NewService(registry, catalog, mp.ServiceOptions{Now: func() time.Time { return at }, NewID: options.DatabaseID, ProvisioningEnabled: enabled})
		if err != nil {
			return mp.DurableLifecycleSession{}, err
		}
		bindings, err := mp.NewBindingService(registry, catalog, catalog, &durableFaultSink{CredentialSink: sink, stage: stage, observed: &observed, disconnect: pool.Close}, mp.BindingServiceOptions{Now: func() time.Time { return at }, NewID: func() string { return options.BindingID(stage.Access) }, ProvisioningEnabled: enabled})
		if err != nil {
			return mp.DurableLifecycleSession{}, err
		}
		observer := credentialdelivery.Observer{Store: secretStore, Identity: identity, HMACKey: hmac, PostgresMajor: 16, RunID: options.RunID}
		failed = false
		return mp.DurableLifecycleSession{Catalog: catalog, Service: service, Bindings: bindings, FaultObserved: func() bool { return observed }, VerifyCredential: observer.Credential, VerifyDeleted: observer.Deleted, VerifyWorkload: func(ctx context.Context, w, m mp.Binding, prepare bool) error {
			a.workloads++
			return observer.Workload(ctx, w, m, prepare)
		}, Close: pool.Close}, nil
	}
	return options, factory, a, catalogPool
}

func TestDurableQualificationPostgresRestartAndSQL(t *testing.T) {
	options, factory, authority, _ := durableFixture(t)
	report, err := mp.QualifyDurableLifecycle(context.Background(), factory, options)
	if err != nil {
		t.Fatalf("durable lifecycle: %v checks=%+v", err, report.Checks)
	}
	if err := mp.ValidateLifecycleQualificationReport(report); err != nil {
		t.Fatalf("durable evidence rejected: %v checks=%+v", err, report.Checks)
	}
	for _, check := range report.Checks {
		if !check.Passed {
			t.Errorf("failed check %+v", check)
		}
	}
	if authority.creates != 1 || authority.instances < 10 || authority.workloads != 2 || len(authority.roles) != 3 || !authority.deleted || len(authority.materials) != 0 {
		t.Fatalf("unexpected recovery/cleanup counts: creates=%d instances=%d workloads=%d roles=%d deleted=%v live_credentials=%d", authority.creates, authority.instances, authority.workloads, len(authority.roles), authority.deleted, len(authority.materials))
	}
	if len(authority.retired) != 3 {
		t.Fatal("cleanup did not revoke all credential generations")
	}
	for _, material := range authority.retired {
		uri, err := credentialdelivery.ConnectionURL(mp.CredentialReadWrite, material)
		if err != nil {
			t.Fatal(err)
		}
		probe, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		connection, err := mp.ConnectCredentialSQL(probe, uri)
		cancel()
		if err == nil {
			_ = connection.Close(context.Background())
			t.Fatal("revoked SQL role still authenticates")
		}
	}
	if err := authority.pool.Ping(context.Background()); err != nil {
		t.Fatal("server unavailability cannot prove revocation")
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{options.AccountID, options.AppID, options.DatabaseID(), authority.resource, "postgres://", "AGE-SECRET-KEY"} {
		if strings.Contains(string(data), sensitive) {
			t.Fatal("report leaked private identity or credential")
		}
	}
}

func TestDurableQualificationRejectsMissingCommittedFault(t *testing.T) {
	options, factory, authority, _ := durableFixture(t)
	wrapped := func(ctx context.Context, stage mp.DurableQualificationStage) (mp.DurableLifecycleSession, error) {
		s, err := factory(ctx, stage)
		if stage.Fault != "" {
			s.FaultObserved = func() bool { return false }
		}
		return s, err
	}
	report, err := mp.QualifyDurableLifecycle(context.Background(), wrapped, options)
	if !errors.Is(err, mp.ErrQualificationFailed) || !authority.deleted {
		t.Fatalf("fault not proven: err=%v deleted=%v checks=%+v", err, authority.deleted, report.Checks)
	}
}

func TestDurableQualificationDoesNotAdoptForeignName(t *testing.T) {
	options, factory, authority, _ := durableFixture(t)
	s, err := factory(context.Background(), mp.DurableQualificationStage{Access: mp.CredentialReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	foreignID := uuid.NewString()
	now := time.Now().UTC()
	_, _, err = s.Catalog.Reserve(context.Background(), mp.Database{ID: foreignID, AccountID: options.AccountID, Name: options.DatabaseName, Spec: options.Spec, BackendID: "sql", BackendFingerprint: strings.Repeat("a", 64), State: mp.StateProvisioning, DesiredGeneration: 1, CreatedAt: now, UpdatedAt: now}, 3)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, err = mp.QualifyDurableLifecycle(context.Background(), factory, options)
	if !errors.Is(err, mp.ErrQualificationFailed) || authority.deleted || authority.creates != 0 {
		t.Fatalf("adopted/deleted preexisting fixture: err=%v deleted=%v", err, authority.deleted)
	}
}

func TestDurableQualificationDetectsDataLossAndStillCleansUp(t *testing.T) {
	options, factory, authority, _ := durableFixture(t)
	wrapped := func(ctx context.Context, stage mp.DurableQualificationStage) (mp.DurableLifecycleSession, error) {
		s, err := factory(ctx, stage)
		if err != nil {
			return s, err
		}
		verify := s.VerifyWorkload
		s.VerifyWorkload = func(ctx context.Context, w, m mp.Binding, prepare bool) error {
			if !prepare {
				authority.exec(ctx, "TRUNCATE public.gregale_durable_qualification_probe")
			}
			return verify(ctx, w, m, prepare)
		}
		return s, nil
	}
	report, err := mp.QualifyDurableLifecycle(context.Background(), wrapped, options)
	if !errors.Is(err, mp.ErrQualificationFailed) || !authority.deleted || mp.ValidateLifecycleQualificationReport(report) == nil {
		t.Fatalf("data loss accepted or cleanup skipped: err=%v deleted=%v checks=%+v", err, authority.deleted, report.Checks)
	}
}

func TestDurableQualificationCleanupFailureBlocksEvidence(t *testing.T) {
	options, factory, authority, _ := durableFixture(t)
	authority.deleteErr = mp.ErrUnavailable
	report, err := mp.QualifyDurableLifecycle(context.Background(), factory, options)
	if !errors.Is(err, mp.ErrQualificationFailed) || authority.deleted || mp.ValidateLifecycleQualificationReport(report) == nil {
		t.Fatalf("cleanup failure accepted: err=%v deleted=%v checks=%+v", err, authority.deleted, report.Checks)
	}
}

func TestDurableQualificationRejectsReusedRunTombstone(t *testing.T) {
	options, factory, authority, _ := durableFixture(t)
	if _, err := mp.QualifyDurableLifecycle(context.Background(), factory, options); err != nil {
		t.Fatal(err)
	}
	_, err := mp.QualifyDurableLifecycle(context.Background(), factory, options)
	if !errors.Is(err, mp.ErrQualificationFailed) || authority.creates != 1 || len(authority.retired) != 3 {
		t.Fatal("reused run adopted or compensated old resources")
	}
}
