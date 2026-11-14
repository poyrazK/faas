package managedpostgres

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type postgresRotationFixture struct {
	store    *PostgresStore
	service  *BindingService
	provider *bindingProvider
	binding  Binding
	now      time.Time
}

func newPostgresRotationFixture(t *testing.T, live bool) *postgresRotationFixture {
	t.Helper()
	store, pool, ctx, account := postgresStoreFixture(t)
	accounts := state.NewPgStore(pool)
	app, err := accounts.CreateApp(ctx, state.App{AccountID: account, Slug: "rotation-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if live {
		deployment, err := accounts.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:rotation", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		// The normal creation writer records build intent before publication.
		if _, err := pool.Exec(ctx, `UPDATE deployments SET status='live',traffic_percent=100 WHERE id=$1`, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	f := &postgresRotationFixture{store: store, provider: &bindingProvider{}, now: time.Now().UTC()}
	f.provider.capabilities, f.provider.material = testCapabilities(), bindingTestMaterial()
	registry := testRegistry(t, f.provider, func(c *Config) { c.ProvisioningEnabled = true })
	backend, err := registry.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	input := postgresTestDatabase(account, "rotation", f.now)
	input.BackendFingerprint = backend.Fingerprint
	database, _, err := store.Reserve(ctx, input, 3)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, account, database.ID, uuid.NewString(), StateProvisioning, f.now, f.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claim.LeaseToken, "provider-rotation", f.now); err != nil {
		t.Fatal(err)
	}
	database, err = store.FinishProvision(ctx, database.ID, claim.LeaseToken, f.now)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = NewBindingService(registry, store, store, &postgresBindingCredentialSink{store: accounts}, BindingServiceOptions{Now: func() time.Time { return f.now }, ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	f.binding, err = f.service.Create(ctx, CreateBindingRequest{AccountID: account, DatabaseID: database.ID, AppID: app.ID, Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *postgresRotationFixture) sweep(t *testing.T, enabled bool) {
	t.Helper()
	// Catalog-side idle completion uses the server clock. Advance the fake
	// worker clock past earlier real writes, while preserving explicit jumps.
	if actual := time.Now().UTC(); actual.After(f.now) {
		f.now = actual
	}
	// Recreate the process-local service and worker; the catalog owns recovery.
	service, err := NewBindingService(f.service.registry, f.store, f.store, f.service.sink, BindingServiceOptions{Now: func() time.Time { return f.now }, ProvisioningEnabled: func() bool { return enabled }})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewBindingReconciler(service, BindingReconcilerOptions{Now: func() time.Time { return f.now }, IncludeProvisioning: func() bool { return enabled }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (f *postgresRotationFixture) refreshCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.store.pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_outbox WHERE channel='runtime_config_restart' AND payload::jsonb->>'app_id'=$1`, f.binding.AppID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPostgresBindingRotationRefreshRecoversProviderFailure(t *testing.T) {
	for _, live := range []bool{true, false} {
		t.Run(map[bool]string{true: "serving", false: "no live deployment"}[live], func(t *testing.T) {
			f := newPostgresRotationFixture(t, live)
			f.provider.credentialErr = ErrUnavailable
			if _, err := f.service.Rotate(t.Context(), f.binding.AccountID, f.binding.ID); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
			f.provider.credentialErr = nil
			f.now = f.now.Add(2 * time.Hour)
			f.sweep(t, true)
			current, err := f.store.GetBinding(t.Context(), f.binding.AccountID, f.binding.ID)
			if err != nil || current.CredentialGeneration != 2 || current.RotationPreviousGeneration != 1 || current.RotationCleanupReady == live || f.provider.revokeCalls != 0 {
				t.Fatalf("recovery lost the retirement barrier: %+v revoked=%d err=%v", current, f.provider.revokeCalls, err)
			}
			want := 0
			if live {
				want = 1
			}
			if n := f.refreshCount(t); n != want {
				t.Fatalf("refresh count=%d, want %d", n, want)
			}
			if live {
				for range 3 {
					if _, err := f.service.Rotate(t.Context(), f.binding.AccountID, f.binding.ID); err != nil {
						t.Fatal(err)
					}
				}
				if f.refreshCount(t) != 1 || f.provider.issueCalls != 3 {
					t.Fatal("replay duplicated a refresh or credential generation")
				}
				accounts := state.NewPgStore(f.store.pool)
				if err := accounts.FinalizeManagedPostgresBindingRotationsForApp(t.Context(), current.AppID, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
				f.sweep(t, false)
				if f.provider.revokeCalls != 0 {
					t.Fatal("wrong wake ID authorized revocation")
				}
				if err := accounts.FinalizeManagedPostgresBindingRotationsForApp(t.Context(), current.AppID, current.RotationWakeID); err != nil {
					t.Fatal(err)
				}
			}
			f.sweep(t, false)
			current, err = f.store.GetBinding(t.Context(), f.binding.AccountID, f.binding.ID)
			if err != nil || current.RotationPreviousGeneration != 0 || f.provider.revokeCalls != 1 {
				t.Fatalf("acknowledged rotation did not retire generation 1: %+v err=%v", current, err)
			}
		})
	}
}

func TestPostgresBindingRotationRefreshFailureRollsBackReady(t *testing.T) {
	f := newPostgresRotationFixture(t, true)
	_, err := f.store.pool.Exec(t.Context(), `CREATE FUNCTION reject_rotation_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'refresh insert failed'; END $$;
CREATE TRIGGER reject_rotation_refresh BEFORE INSERT ON notification_outbox FOR EACH ROW WHEN (NEW.channel='runtime_config_restart') EXECUTE FUNCTION reject_rotation_refresh()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Rotate(t.Context(), f.binding.AccountID, f.binding.ID); err == nil {
		t.Fatal("lost outbox write acknowledged rotation")
	}
	current, err := f.store.GetBinding(t.Context(), f.binding.AccountID, f.binding.ID)
	if err != nil || current.State != BindingStateProvisioning || current.CredentialGeneration != 2 || current.RotationCleanupReady || f.refreshCount(t) != 0 {
		t.Fatalf("ready escaped the failed handoff transaction: %+v %v", current, err)
	}
	if _, err := f.store.pool.Exec(t.Context(), `DROP TRIGGER reject_rotation_refresh ON notification_outbox; DROP FUNCTION reject_rotation_refresh()`); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * time.Hour)
	f.sweep(t, true)
	current, err = f.store.GetBinding(t.Context(), f.binding.AccountID, f.binding.ID)
	if err != nil || current.State != BindingStateReady || current.CredentialGeneration != 2 || f.refreshCount(t) != 1 || f.provider.revokeCalls != 0 {
		t.Fatalf("recovery lost its generation or queued refresh: %+v %v", current, err)
	}
}

func TestPostgresBindingRotationRefreshRepairsLegacyReadyAndTerminalDelivery(t *testing.T) {
	f := newPostgresRotationFixture(t, true)
	ctx := t.Context()
	rotated, _, err := f.store.BeginBindingRotation(ctx, f.binding.AccountID, f.binding.ID, uuid.NewString(), f.now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.store.ClaimBinding(ctx, rotated.AccountID, rotated.ID, uuid.NewString(), BindingStateProvisioning, f.now, f.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := f.service.sink.Put(ctx, claim, bindingTestMaterial())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := postgresUUID(claim.ID)
	// N-1 finishes through the old SQL without the new Go transaction.
	if _, err := sqlc.New().FinishManagedPostgresBindingProvision(ctx, f.store.catalog, sqlc.FinishManagedPostgresBindingProvisionParams{ID: id, LeaseToken: claim.LeaseToken, CredentialRef: ref, ProviderIdentityID: "provider-generation-2", Now: healthTimestamp(f.now)}); err != nil {
		t.Fatal(err)
	}
	if f.refreshCount(t) != 0 {
		t.Fatal("legacy fixture already queued a refresh")
	}
	f.sweep(t, false)
	if f.refreshCount(t) != 1 || f.provider.issueCalls != 1 || f.provider.revokeCalls != 0 {
		t.Fatal("legacy recovery issued new credentials or lost its event")
	}
	for _, terminal := range []string{"dead_letter", "delivered"} {
		if _, err := f.store.pool.Exec(ctx, `UPDATE notification_outbox SET state=$2,attempts=12 WHERE channel='runtime_config_restart' AND payload::jsonb->>'app_id'=$1`, claim.AppID, terminal); err != nil {
			t.Fatal(err)
		}
		f.sweep(t, false)
		var status string
		var attempts int
		if err := f.store.pool.QueryRow(ctx, `SELECT state,attempts FROM notification_outbox WHERE channel='runtime_config_restart' AND payload::jsonb->>'app_id'=$1`, claim.AppID).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || attempts != 0 || f.refreshCount(t) != 1 {
			t.Fatalf("terminal handoff not replayed once: %s %d", status, attempts)
		}
	}
	// Multiple apid replicas and client retries share one durable wake.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := f.store.EnsureBindingRotationRefresh(ctx, claim.AccountID, claim.ID, f.now)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.refreshCount(t) != 1 {
		t.Fatal("concurrent recovery duplicated the durable handoff")
	}
}

func TestPostgresBindingRotationRefreshWaitsForResidentsWithoutLiveDeployment(t *testing.T) {
	f := newPostgresRotationFixture(t, false)
	ctx := t.Context()
	accounts := state.NewPgStore(f.store.pool)
	role := "compute-node"
	node, err := accounts.CreateComputeNode(ctx, state.ComputeNode{Name: "rotation-" + uuid.NewString(), TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Active: true, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := accounts.CreateInstance(ctx, f.binding.AppID, "", string(state.StateWarm), 256, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := f.service.Rotate(ctx, f.binding.AccountID, f.binding.ID)
	if err != nil || rotated.RotationCleanupReady || f.refreshCount(t) != 1 {
		t.Fatalf("resident runtime lost its credential barrier: %+v %v", rotated, err)
	}
	f.sweep(t, false)
	if f.provider.revokeCalls != 0 {
		t.Fatal("credential retired before the resident was removed")
	}
	if _, err := f.store.pool.Exec(ctx, `UPDATE instances SET state='stopped' WHERE id=$1`, instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.pool.Exec(ctx, `UPDATE notification_outbox SET state='dead_letter' WHERE channel='runtime_config_restart' AND payload::jsonb->>'app_id'=$1`, f.binding.AppID); err != nil {
		t.Fatal(err)
	}
	f.sweep(t, false)
	f.sweep(t, false)
	if f.provider.revokeCalls != 1 {
		t.Fatal("drained idle rotation could not retire its old credential")
	}
}
