package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	probesql "github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
	"github.com/onebox-faas/faas/pkg/state"
)

const durableQualificationEnv = "FAAS_MANAGED_POSTGRES_QUALIFY_DURABLE"

type durableQualificationConfig struct {
	pool     *pgxpool.Config
	identity *age.X25519Identity
	hmac     []byte
	options  managedpostgres.DurableLifecycleOptions
}

// Preflight runs before the first provider mutation. A database-level marker
// proves the operator selected an explicitly disposable catalog. Session GUCs
// (including PGOPTIONS) cannot substitute for the stored database setting.
func loadDurableQualification(ctx context.Context, getenv func(string) string, spec managedpostgres.Spec, timeout time.Duration) (durableQualificationConfig, error) {
	var c durableQualificationConfig
	if !isLifecycleQualificationEnabled(getenv) || spec.Validate() != nil || timeout <= 0 {
		return c, managedpostgres.ErrInvalid
	}
	runID := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_RUN_ID"))
	accountID := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_ACCOUNT_ID"))
	appID := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_APP_ID"))
	for _, raw := range []string{runID, accountID, appID} {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || id.String() != raw {
			return c, managedpostgres.ErrInvalid
		}
	}
	c.options = managedpostgres.DurableLifecycleOptions{RunID: runID, MigrationEnvironmentKey: "MIGRATION_DATABASE_URL", LifecycleQualificationOptions: managedpostgres.LifecycleQualificationOptions{
		AccountID: accountID, AppID: appID, DatabaseName: qualificationDatabaseName(runID), Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, Spec: spec, Timeout: timeout,
	}}
	identity, err := readQualificationSecret(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_AGE_IDENTITY_FILE"))
	if err != nil {
		return c, err
	}
	identities, err := age.ParseIdentities(strings.NewReader(string(identity)))
	if err != nil || len(identities) != 1 {
		return c, managedpostgres.ErrInvalid
	}
	var identityOK bool
	c.identity, identityOK = identities[0].(*age.X25519Identity)
	if !identityOK {
		return c, managedpostgres.ErrInvalid
	}
	c.hmac, err = readQualificationSecret(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_HMAC_KEY_FILE"))
	if err != nil || len(c.hmac) < 32 {
		return c, managedpostgres.ErrInvalid
	}
	dsn := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_CATALOG_URL"))
	if dsn == "" {
		return c, managedpostgres.ErrInvalid
	}
	c.pool, err = pgxpool.ParseConfig(dsn)
	if err != nil {
		return c, managedpostgres.ErrInvalid
	}
	c.pool.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, c.pool.Copy())
	if err != nil {
		return c, managedpostgres.ErrUnavailable
	}
	defer pool.Close()
	if err := c.checkCatalog(ctx, pool); err != nil {
		return c, err
	}
	store := state.NewPgStore(pool)
	account, err := store.AccountByID(ctx, accountID)
	limits, supported := api.ManagedPostgresLimitsFor(account.Plan)
	if err != nil || account.ID != accountID || account.Status != state.AccountActive || !supported || limits.DatabasesMax < 1 {
		return c, managedpostgres.ErrInvalid
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil || app.AccountID != accountID || app.Status == "deleted" {
		return c, managedpostgres.ErrInvalid
	}
	for _, key := range []string{c.options.EnvironmentKey, c.options.MigrationEnvironmentKey} {
		if _, err := store.GetAppSecretInScope(ctx, accountID, appID, c.options.Scope, key); !errors.Is(err, state.ErrNotFound) {
			return c, managedpostgres.ErrConflict
		}
	}
	catalog, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		return c, managedpostgres.ErrInvalid
	}
	rows, err := catalog.List(ctx, accountID)
	if err != nil {
		return c, managedpostgres.ErrUnavailable
	}
	for _, row := range rows {
		if row.ID == c.options.DatabaseID() || row.Name == c.options.DatabaseName {
			return c, managedpostgres.ErrConflict
		}
	}
	if _, err := catalog.Get(ctx, accountID, c.options.DatabaseID()); !errors.Is(err, managedpostgres.ErrNotFound) {
		return c, managedpostgres.ErrConflict
	}
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialMigration} {
		if _, err := catalog.GetBinding(ctx, accountID, c.options.BindingID(access)); !errors.Is(err, managedpostgres.ErrNotFound) {
			return c, managedpostgres.ErrConflict
		}
	}
	return c, nil
}

func (c durableQualificationConfig) checkCatalog(ctx context.Context, pool *pgxpool.Pool) error {
	marked, err := probesql.New().IsDisposableQualificationCatalog(ctx, pool, "faas.qualification_run="+c.options.RunID)
	if err != nil || !marked {
		return managedpostgres.ErrConflict
	}
	return nil
}

func readQualificationSecret(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0111 != 0 || info.Size() < 1 || info.Size() > 4096 {
		return nil, managedpostgres.ErrInvalid
	}
	f, err := os.Open(path) //nolint:forbidigo // Operator-owned private regular file; Lstat and SameFile reject substitution.
	if err != nil {
		return nil, managedpostgres.ErrUnavailable
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode().Perm()&0077 != 0 || opened.Mode().Perm()&0111 != 0 || opened.Size() < 1 || opened.Size() > 4096 {
		return nil, managedpostgres.ErrConflict
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return nil, managedpostgres.ErrInvalid
	}
	return data, nil
}

func (c durableQualificationConfig) factory(getenv func(string) string) managedpostgres.DurableLifecycleFactory {
	return func(ctx context.Context, stage managedpostgres.DurableQualificationStage) (session managedpostgres.DurableLifecycleSession, resultErr error) {
		pool, err := pgxpool.NewWithConfig(ctx, c.pool.Copy())
		if err != nil {
			return session, managedpostgres.ErrUnavailable
		}
		defer func() {
			if resultErr != nil {
				pool.Close()
			}
		}()
		if err := c.checkCatalog(ctx, pool); err != nil {
			return session, err
		}
		fault := &atomic.Bool{}
		registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": func(config managedpostgres.BackendConfig, env func(string) string) (managedpostgres.Provider, error) {
			provider, err := neon.New(config, env)
			if err != nil {
				return nil, managedpostgres.ErrInvalid
			}
			p, ok := provider.(*neon.Provider)
			if !ok {
				return nil, managedpostgres.ErrInvalid
			}
			return &qualificationNeon{Provider: p, stage: stage, observed: fault, disconnect: pool.Close}, nil
		}})
		if err != nil || registry == nil {
			return session, managedpostgres.ErrInvalid
		}
		catalog, err := managedpostgres.NewPostgresStore(pool)
		if err != nil {
			return session, err
		}
		store := state.NewPgStore(pool)
		sink, err := credentialdelivery.New(store, func() *age.X25519Recipient { return c.identity.Recipient() }, func() []byte { return c.hmac })
		if err != nil {
			return session, err
		}
		enabled := func() bool { return !stage.Cleanup }
		allowed := func(_ context.Context, account string) bool { return account == c.options.AccountID }
		service, err := managedpostgres.NewService(registry, catalog, managedpostgres.ServiceOptions{NewID: c.options.DatabaseID, ProvisioningEnabled: enabled, ProvisioningAllowed: allowed})
		if err != nil {
			return session, err
		}
		bindings, err := managedpostgres.NewBindingService(registry, catalog, catalog, &qualificationFaultSink{CredentialSink: sink, stage: stage, observed: fault, disconnect: pool.Close}, managedpostgres.BindingServiceOptions{
			NewID: func() string { return c.options.BindingID(stage.Access) }, ProvisioningEnabled: enabled, ProvisioningAllowed: allowed,
		})
		if err != nil {
			return session, err
		}
		observer := credentialdelivery.Observer{Store: store, Identity: c.identity, HMACKey: c.hmac, PostgresMajor: c.options.Spec.PostgresMajor, RunID: c.options.RunID}
		return managedpostgres.DurableLifecycleSession{Catalog: catalog, Service: service, Bindings: bindings, FaultObserved: fault.Load, VerifyCredential: observer.Credential, VerifyDeleted: observer.Deleted, VerifyWorkload: observer.Workload, Close: pool.Close}, nil
	}
}

// Embedding the concrete adapter preserves optional provider contracts (such as
// final usage). Faults happen after a successful upstream effect, once/session.
type qualificationNeon struct {
	*neon.Provider
	stage      managedpostgres.DurableQualificationStage
	observed   *atomic.Bool
	disconnect func()
}

func (p *qualificationNeon) Provision(ctx context.Context, r managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	o, err := p.Provider.Provision(ctx, r)
	if err == nil && p.stage.Fault == managedpostgres.QualificationProvisionAck && p.observed.CompareAndSwap(false, true) {
		p.disconnect()
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	return o, err
}

func (p *qualificationNeon) IssueCredentials(ctx context.Context, r managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	m, err := p.Provider.IssueCredentials(ctx, r)
	if err == nil && p.stage.Fault == managedpostgres.QualificationCredentialAck && p.observed.CompareAndSwap(false, true) {
		p.disconnect()
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrUnavailable
	}
	return m, err
}

type qualificationFaultSink struct {
	managedpostgres.CredentialSink
	stage      managedpostgres.DurableQualificationStage
	observed   *atomic.Bool
	disconnect func()
}

func (s *qualificationFaultSink) Put(ctx context.Context, b managedpostgres.Binding, m managedpostgres.CredentialMaterial) (string, error) {
	ref, err := s.CredentialSink.Put(ctx, b, m)
	if err == nil && s.stage.Fault == managedpostgres.QualificationSecretAck && s.observed.CompareAndSwap(false, true) {
		s.disconnect()
		return "", managedpostgres.ErrUnavailable
	}
	return ref, err
}

func durableQualificationEnabled(getenv func(string) string) bool {
	return strings.EqualFold(strings.TrimSpace(getenv(durableQualificationEnv)), "true")
}
