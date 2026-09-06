package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func postgresStoreFixture(t *testing.T) (*PostgresStore, *pgxpool.Pool, context.Context, string) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	account, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@postgres-store.test", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	return store, pool, ctx, account.ID
}

func postgresTestDatabase(accountID, name string, at time.Time) Database {
	return Database{
		ID:                 uuid.NewString(),
		AccountID:          accountID,
		Name:               name,
		Spec:               testSpec(),
		BackendID:          "primary-a",
		BackendFingerprint: strings.Repeat("a", 64),
		State:              StateProvisioning,
		DesiredGeneration:  1,
		CreatedAt:          at,
		UpdatedAt:          at,
	}
}

func postgresReadyDatabase(t *testing.T, store *PostgresStore, accountID, name string, at time.Time) Database {
	t.Helper()
	database, created, err := store.Reserve(context.Background(), postgresTestDatabase(accountID, name, at), 10)
	if err != nil || !created {
		t.Fatalf("reserve ready database: created=%v database=%+v err=%v", created, database, err)
	}
	claimed, err := store.Claim(context.Background(), accountID, database.ID, uuid.NewString(), StateProvisioning, at, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("claim ready database: %v", err)
	}
	if err := store.RecordProviderResource(context.Background(), database.ID, claimed.LeaseToken, "provider-"+database.ID, at.Add(time.Second)); err != nil {
		t.Fatalf("record ready database: %v", err)
	}
	database, err = store.FinishProvision(context.Background(), database.ID, claimed.LeaseToken, at.Add(2*time.Second))
	if err != nil {
		t.Fatalf("finish ready database: %v", err)
	}
	return database
}

func TestPostgresStoreReservationIsIdempotentAndQuotaIsAtomic(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)
	firstInput := postgresTestDatabase(accountID, "assets", at)
	first, created, err := store.Reserve(ctx, firstInput, 3)
	if err != nil || !created {
		t.Fatalf("first reservation: created=%v database=%+v err=%v", created, first, err)
	}
	retryInput := postgresTestDatabase(accountID, "assets", at.Add(time.Second))
	retry, created, err := store.Reserve(ctx, retryInput, 3)
	if err != nil || created || retry.ID != first.ID {
		t.Fatalf("idempotent reservation: created=%v database=%+v err=%v", created, retry, err)
	}

	var quotaErrors atomic.Int32
	var unexpected atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, _, reserveErr := store.Reserve(ctx, postgresTestDatabase(accountID, fmt.Sprintf("database-%02d", index), at.Add(time.Duration(index+2)*time.Second)), 3)
			switch {
			case reserveErr == nil:
			case errors.Is(reserveErr, ErrQuotaExceeded):
				quotaErrors.Add(1)
			default:
				unexpected.Add(1)
			}
		}(i)
	}
	wait.Wait()
	items, err := store.List(ctx, accountID)
	if err != nil || len(items) != 3 || quotaErrors.Load() != 10 || unexpected.Load() != 0 {
		t.Fatalf("quota race: items=%d quota_errors=%d unexpected=%d err=%v", len(items), quotaErrors.Load(), unexpected.Load(), err)
	}
	if _, err := store.Get(ctx, uuid.NewString(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant-scoped get = %v, want ErrNotFound", err)
	}
}

func TestPostgresStoreLeaseRecoveryRetryAndTombstone(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	start := time.Date(2026, 9, 5, 21, 0, 0, 0, time.UTC)
	input := postgresTestDatabase(accountID, "orders", start)
	database, created, err := store.Reserve(ctx, input, 3)
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v %v", database, created, err)
	}
	if rows, err := store.Due(ctx, false, 20, start); err != nil || len(rows) != 0 {
		t.Fatalf("disabled provisioning was due: rows=%d err=%v", len(rows), err)
	}
	if rows, err := store.Due(ctx, true, 20, start); err != nil || len(rows) != 1 {
		t.Fatalf("provisioning not due: rows=%d err=%v", len(rows), err)
	}

	first, err := store.Claim(ctx, accountID, database.ID, "first-worker", StateProvisioning, start, start.Add(2*time.Minute))
	if err != nil || first.AttemptCount != 1 {
		t.Fatalf("first claim: %+v %v", first, err)
	}
	if _, err := store.Claim(ctx, accountID, database.ID, "contender", StateProvisioning, start, start.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent claim = %v, want ErrConflict", err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, first.LeaseToken, "provider-orders", start.Add(time.Second)); err != nil {
		t.Fatalf("record provider resource: %v", err)
	}

	afterCrash := start.Add(3 * time.Minute)
	rows, err := store.Due(ctx, true, 20, afterCrash)
	if err != nil || len(rows) != 1 || rows[0].ProviderResourceID != "provider-orders" {
		t.Fatalf("expired lease discovery: %+v %v", rows, err)
	}
	if _, err := store.FinishProvision(ctx, database.ID, first.LeaseToken, afterCrash); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired lease completion = %v, want ErrConflict", err)
	}
	recovered, err := store.Claim(ctx, accountID, database.ID, "recovered-worker", StateProvisioning, afterCrash, afterCrash.Add(2*time.Minute))
	if err != nil || recovered.AttemptCount != 2 || recovered.ProviderResourceID != "provider-orders" {
		t.Fatalf("recovery claim: %+v %v", recovered, err)
	}
	if _, err := store.FinishProvision(ctx, database.ID, first.LeaseToken, afterCrash); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale completion = %v, want ErrConflict", err)
	}
	if err := store.Release(ctx, database.ID, recovered.LeaseToken, StateProvisioning, "unavailable", afterCrash, afterCrash.Add(time.Minute)); err != nil {
		t.Fatalf("release with cooldown: %v", err)
	}
	if err := store.Release(ctx, database.ID, "wrong-worker", StateProvisioning, "provider password leaked", afterCrash, afterCrash.Add(time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe error code = %v, want ErrInvalid", err)
	}
	if rows, err := store.Due(ctx, true, 20, afterCrash.Add(30*time.Second)); err != nil || len(rows) != 0 {
		t.Fatalf("cooldown ignored: rows=%d err=%v", len(rows), err)
	}

	thirdAt := afterCrash.Add(time.Minute)
	third, err := store.Claim(ctx, accountID, database.ID, "third-worker", StateProvisioning, thirdAt, thirdAt.Add(2*time.Minute))
	if err != nil || third.AttemptCount != 3 {
		t.Fatalf("third claim: %+v %v", third, err)
	}
	ready, err := store.FinishProvision(ctx, database.ID, third.LeaseToken, thirdAt.Add(time.Second))
	if err != nil || ready.State != StateReady || ready.AttemptCount != 0 || ready.ObservedGeneration != ready.DesiredGeneration {
		t.Fatalf("finish provision: %+v %v", ready, err)
	}

	deletingAt := thirdAt.Add(2 * time.Second)
	deleting, err := store.Claim(ctx, accountID, database.ID, "delete-worker", StateDeleting, deletingAt, deletingAt.Add(2*time.Minute))
	if err != nil || deleting.AttemptCount != 1 {
		t.Fatalf("delete claim: %+v %v", deleting, err)
	}
	deleted, err := store.FinishDelete(ctx, database.ID, deleting.LeaseToken, deletingAt.Add(time.Second))
	if err != nil || deleted.State != StateDeleted || deleted.DeletedAt == nil {
		t.Fatalf("finish delete: %+v %v", deleted, err)
	}
	if listed, err := store.List(ctx, accountID); err != nil || len(listed) != 0 {
		t.Fatalf("tombstone listed: %+v %v", listed, err)
	}
	if tombstone, err := store.Get(ctx, accountID, database.ID); err != nil || tombstone.State != StateDeleted {
		t.Fatalf("tombstone unavailable for idempotency: %+v %v", tombstone, err)
	}
	replacement, created, err := store.Reserve(ctx, postgresTestDatabase(accountID, "orders", deletingAt.Add(2*time.Second)), 3)
	if err != nil || !created || replacement.ID == database.ID {
		t.Fatalf("name reuse: created=%v replacement=%+v err=%v", created, replacement, err)
	}
}

type concurrentReadyProvider struct {
	provisionCalls atomic.Int32
}

func (*concurrentReadyProvider) Capabilities() Capabilities { return testCapabilities() }

func (p *concurrentReadyProvider) Provision(_ context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	p.provisionCalls.Add(1)
	return ObservedDatabase{
		ProviderResourceID: "upstream-" + request.ResourceID,
		Status:             ProviderStatusReady,
		Spec:               request.Spec,
	}, nil
}

func (p *concurrentReadyProvider) Restore(_ context.Context, request RestoreRequest) (ObservedDatabase, error) {
	p.provisionCalls.Add(1)
	return ObservedDatabase{ProviderResourceID: "restored-" + request.ResourceID, Status: ProviderStatusReady, Spec: request.Spec}, nil
}

func (*concurrentReadyProvider) Inspect(_ context.Context, providerResourceID string) (ObservedDatabase, error) {
	return ObservedDatabase{ProviderResourceID: providerResourceID, Status: ProviderStatusReady, Spec: testSpec()}, nil
}

func (*concurrentReadyProvider) Update(context.Context, UpdateRequest) (ObservedDatabase, error) {
	return ObservedDatabase{}, ErrUnsupported
}

func (*concurrentReadyProvider) Delete(context.Context, DeleteRequest) (DeleteResult, error) {
	return DeleteResult{Done: true}, nil
}

func (*concurrentReadyProvider) IssueCredentials(context.Context, CredentialRequest) (CredentialMaterial, error) {
	return CredentialMaterial{}, ErrUnsupported
}

func (*concurrentReadyProvider) RevokeCredentials(context.Context, CredentialRequest) error {
	return ErrUnsupported
}

func (*concurrentReadyProvider) Usage(_ context.Context, _ string, window UsageWindow) (Usage, error) {
	return Usage{Window: window}, nil
}

func TestPostgresStoreConcurrentReconcilersContactProviderOnce(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	provider := &concurrentReadyProvider{}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 23, 0, 0, 0, time.UTC)
	input := postgresTestDatabase(accountID, "concurrent", now)
	input.BackendFingerprint = backend.Fingerprint
	database, created, err := store.Reserve(ctx, input, 3)
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v %v", database, created, err)
	}

	service, err := NewService(registry, store, ServiceOptions{
		LeaseDuration:       2 * time.Minute,
		ProviderTimeout:     time.Second,
		PollInterval:        time.Second,
		ProvisioningEnabled: func() bool { return true },
		Now:                 func() time.Time { return now },
		NewID:               uuid.NewString,
		NewLeaseToken:       uuid.NewString,
	})
	if err != nil {
		t.Fatal(err)
	}
	newReconciler := func() *Reconciler {
		reconciler, reconcileErr := NewReconciler(service, ReconcilerOptions{
			Interval:            time.Second,
			Now:                 func() time.Time { return now },
			IncludeProvisioning: func() bool { return true },
		})
		if reconcileErr != nil {
			t.Fatal(reconcileErr)
		}
		return reconciler
	}

	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, reconciler := range []*Reconciler{newReconciler(), newReconciler()} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, sweepErr := reconciler.Sweep(ctx)
			if sweepErr != nil && !errors.Is(sweepErr, ErrConflict) {
				results <- sweepErr
			}
		}()
	}
	wait.Wait()
	close(results)
	for sweepErr := range results {
		t.Fatalf("sweep: %v", sweepErr)
	}
	if got := provider.provisionCalls.Load(); got != 1 {
		t.Fatalf("provider provision calls = %d, want 1", got)
	}
	ready, err := store.Get(ctx, accountID, database.ID)
	if err != nil || ready.State != StateReady {
		t.Fatalf("ready database: %+v %v", ready, err)
	}
}

func TestPostgresBindingStoreOwnsSecretTargetAndFencesLifecycle(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	stateStore := state.NewPgStore(pool)
	app, err := stateStore.CreateApp(ctx, state.App{
		AccountID: accountID,
		Slug:      "binding-" + uuid.NewString(),
		Type:      state.AppTypeApp,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	start := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	database := postgresReadyDatabase(t, store, accountID, "binding-db", start)
	input := testBinding(accountID, database.ID, app.ID, uuid.NewString(), start.Add(3*time.Second))

	// A customer secret write and a binding reservation share one advisory
	// lock. Whichever transaction commits first owns the target; the other
	// must receive a stable conflict rather than overwriting it.
	startRace := make(chan struct{})
	secretResult := make(chan error, 1)
	bindResult := make(chan error, 1)
	go func() {
		<-startRace
		secretResult <- stateStore.UpsertAppSecretInScope(ctx, accountID, app.ID, input.Scope, input.EnvironmentKey, []byte("customer-ciphertext"))
	}()
	go func() {
		<-startRace
		_, _, reserveErr := store.ReserveBinding(ctx, input)
		bindResult <- reserveErr
	}()
	close(startRace)
	secretErr, bindingErr := <-secretResult, <-bindResult
	if (secretErr == nil) == (bindingErr == nil) {
		t.Fatalf("exactly one target claimant must win: secret=%v binding=%v", secretErr, bindingErr)
	}
	if secretErr != nil && !errors.Is(secretErr, state.ErrConflict) {
		t.Fatalf("secret race error = %v, want state.ErrConflict", secretErr)
	}
	if bindingErr != nil && !errors.Is(bindingErr, ErrConflict) {
		t.Fatalf("binding race error = %v, want ErrConflict", bindingErr)
	}
	if secretErr == nil {
		if err := stateStore.DeleteAppSecretInScope(ctx, accountID, app.ID, input.Scope, input.EnvironmentKey); err != nil {
			t.Fatalf("remove winning customer secret: %v", err)
		}
		if _, created, err := store.ReserveBinding(ctx, input); err != nil || !created {
			t.Fatalf("reserve after customer delete: created=%v err=%v", created, err)
		}
	}

	binding, err := store.GetBinding(ctx, accountID, input.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if _, created, err := store.ReserveBinding(ctx, testBinding(accountID, database.ID, app.ID, uuid.NewString(), start.Add(4*time.Second))); err != nil || created {
		t.Fatalf("idempotent target reservation: created=%v err=%v", created, err)
	}
	if err := stateStore.UpsertAppSecretInScope(ctx, accountID, app.ID, input.Scope, input.EnvironmentKey, []byte("replacement")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("customer overwrite of claimed target = %v, want state.ErrConflict", err)
	}

	claimAt := start.Add(5 * time.Second)
	claimed, err := store.ClaimBinding(ctx, accountID, binding.ID, "binding-worker", BindingStateProvisioning, claimAt, claimAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("claim binding: %v", err)
	}
	credentialRef := "managed-secret-" + binding.ID
	managedSecret := state.AppSecret{
		AccountID:                   accountID,
		AppID:                       app.ID,
		Scope:                       input.Scope,
		Key:                         input.EnvironmentKey,
		Ciphertext:                  []byte("managed-ciphertext"),
		Kid:                         "age1test",
		ManagedPostgresBindingID:    binding.ID,
		ManagedCredentialRef:        credentialRef,
		ManagedCredentialGeneration: binding.CredentialGeneration,
	}
	if err := stateStore.PutManagedPostgresSecret(ctx, managedSecret); err != nil {
		t.Fatalf("insert managed secret: %v", err)
	}
	if err := stateStore.PutManagedPostgresSecret(ctx, managedSecret); err != nil {
		t.Fatalf("idempotent managed secret write: %v", err)
	}
	conflictingSecret := managedSecret
	conflictingSecret.ManagedCredentialRef = credentialRef + "-other"
	if err := stateStore.PutManagedPostgresSecret(ctx, conflictingSecret); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("same-generation credential replacement = %v, want state.ErrConflict", err)
	}
	ownedSecret, err := stateStore.GetAppSecretInScope(ctx, accountID, app.ID, input.Scope, input.EnvironmentKey)
	if err != nil || ownedSecret.ManagedPostgresBindingID != binding.ID || ownedSecret.ManagedCredentialRef != credentialRef {
		t.Fatalf("managed secret ownership did not round-trip: secret=%+v err=%v", ownedSecret, err)
	}
	for name, list := range map[string]func() ([]state.AppSecret, error){
		"scope": func() ([]state.AppSecret, error) {
			return stateStore.ListAppSecretsInScope(ctx, accountID, app.ID, input.Scope)
		},
		"all": func() ([]state.AppSecret, error) {
			return stateStore.ListAllAppSecrets(ctx, accountID, app.ID)
		},
		"rekey": func() ([]state.AppSecret, error) {
			return stateStore.ListAppSecretsForRekey(ctx, 100, "")
		},
	} {
		secrets, listErr := list()
		if listErr != nil || len(secrets) != 1 || secrets[0].ManagedPostgresBindingID != binding.ID {
			t.Fatalf("%s managed ownership list: secrets=%+v err=%v", name, secrets, listErr)
		}
	}
	ready, err := store.FinishBindingProvision(ctx, binding.ID, claimed.LeaseToken, "provider-role-1", credentialRef, claimAt.Add(time.Second))
	if err != nil || ready.State != BindingStateReady || ready.CredentialRef != credentialRef {
		t.Fatalf("finish binding: binding=%+v err=%v", ready, err)
	}
	if err := stateStore.DeleteAppSecretInScope(ctx, accountID, app.ID, input.Scope, input.EnvironmentKey); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("customer delete of managed secret = %v, want state.ErrConflict", err)
	}
	if err := stateStore.ResealAppSecretWithKidAndValueHashInScope(ctx, accountID, app.ID, input.Scope, input.EnvironmentKey, "age1rotated", "0123456789abcdef", []byte("resealed")); err != nil {
		t.Fatalf("maintenance reseal of managed secret: %v", err)
	}

	deleteAt := claimAt.Add(2 * time.Second)
	deleting, err := store.ClaimBinding(ctx, accountID, binding.ID, "delete-worker", BindingStateDeleting, deleteAt, deleteAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("claim binding delete: %v", err)
	}
	if _, err := store.FinishBindingDelete(ctx, binding.ID, deleting.LeaseToken, deleteAt.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete with managed secret present = %v, want ErrConflict", err)
	}
	if err := stateStore.DeleteManagedPostgresSecret(ctx, credentialRef); err != nil {
		t.Fatalf("delete managed secret: %v", err)
	}
	deleted, err := store.FinishBindingDelete(ctx, binding.ID, deleting.LeaseToken, deleteAt.Add(time.Second))
	if err != nil || deleted.State != BindingStateDeleted || deleted.DeletedAt == nil {
		t.Fatalf("finish binding delete: binding=%+v err=%v", deleted, err)
	}
	if err := stateStore.UpsertAppSecretInScope(ctx, accountID, app.ID, input.Scope, input.EnvironmentKey, []byte("customer-after-unbind")); err != nil {
		t.Fatalf("customer write after target release: %v", err)
	}
}

type postgresBindingCredentialSink struct {
	store *state.PgStore
}

func (s *postgresBindingCredentialSink) Put(ctx context.Context, binding Binding, _ CredentialMaterial) (string, error) {
	credentialRef := "integration-secret-" + binding.ID
	err := s.store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID:                   binding.AccountID,
		AppID:                       binding.AppID,
		Scope:                       binding.Scope,
		Key:                         binding.EnvironmentKey,
		Ciphertext:                  []byte("sealed-integration-credential"),
		Kid:                         "age1integration",
		ManagedPostgresBindingID:    binding.ID,
		ManagedCredentialRef:        credentialRef,
		ManagedCredentialGeneration: binding.CredentialGeneration,
	})
	return credentialRef, err
}

func (s *postgresBindingCredentialSink) Delete(ctx context.Context, binding Binding) error {
	credentialRef := binding.CredentialRef
	if credentialRef == "" {
		credentialRef = "integration-secret-" + binding.ID
	}
	return s.store.DeleteManagedPostgresSecret(ctx, credentialRef)
}

func TestPostgresBindingServiceCommitsSecretBeforeReadyAndRemovesItBeforeTombstone(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	stateStore := state.NewPgStore(pool)
	app, err := stateStore.CreateApp(ctx, state.App{
		AccountID: accountID,
		Slug:      "binding-service-" + uuid.NewString(),
		Type:      state.AppTypeApp,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	provider := &bindingProvider{}
	provider.capabilities = testCapabilities()
	provider.material = bindingTestMaterial()
	registry := testRegistry(t, provider, func(config *Config) { config.ProvisioningEnabled = true })
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 15, 0, 0, 0, time.UTC)
	databaseInput := postgresTestDatabase(accountID, "binding-service", now)
	databaseInput.BackendFingerprint = backend.Fingerprint
	database, created, err := store.Reserve(ctx, databaseInput, 3)
	if err != nil || !created {
		t.Fatalf("reserve database: created=%v database=%+v err=%v", created, database, err)
	}
	claimedDatabase, err := store.Claim(ctx, accountID, database.ID, uuid.NewString(), StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claimedDatabase.LeaseToken, "provider-binding-service", now); err != nil {
		t.Fatal(err)
	}
	database, err = store.FinishProvision(ctx, database.ID, claimedDatabase.LeaseToken, now)
	if err != nil {
		t.Fatal(err)
	}

	service, err := NewBindingService(registry, store, store, &postgresBindingCredentialSink{store: stateStore}, BindingServiceOptions{
		LeaseDuration:       time.Minute,
		ProviderTimeout:     time.Second,
		ProvisioningEnabled: func() bool { return true },
		Now:                 func() time.Time { return now },
		NewID:               uuid.NewString,
		NewLeaseToken:       uuid.NewString,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.Create(ctx, CreateBindingRequest{
		AccountID: accountID, DatabaseID: database.ID, AppID: app.ID,
		Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite,
	})
	if err != nil || binding.State != BindingStateReady || provider.issueCalls != 1 {
		t.Fatalf("create binding: binding=%+v issue_calls=%d err=%v", binding, provider.issueCalls, err)
	}
	secret, err := stateStore.GetAppSecretInScope(ctx, accountID, app.ID, binding.Scope, binding.EnvironmentKey)
	if err != nil || secret.ManagedPostgresBindingID != binding.ID || secret.ManagedCredentialRef != binding.CredentialRef {
		t.Fatalf("ready binding secret: secret=%+v err=%v", secret, err)
	}

	deleted, err := service.Delete(ctx, accountID, binding.ID)
	if err != nil || deleted.State != BindingStateDeleted || provider.revokeCalls != 1 {
		t.Fatalf("delete binding: binding=%+v revoke_calls=%d err=%v", deleted, provider.revokeCalls, err)
	}
	if _, err := stateStore.GetAppSecretInScope(ctx, accountID, app.ID, binding.Scope, binding.EnvironmentKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("secret survived binding tombstone: %v", err)
	}
}
