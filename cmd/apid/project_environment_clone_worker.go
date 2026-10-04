package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/daemonenv"
	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A second APID process preserves customer-intent ownership while isolating
// clone CPU/RSS and subprocesses from API listeners. Full data admission remains
// closed in the shared coordinator until coordinated capture is qualified.
func runProjectEnvironmentCloneWorker(ctx context.Context, log *slog.Logger) (err error) {
	log = log.With("worker", "project_environment_clone")
	if _, err = daemonenv.Load("apid"); err != nil {
		return err
	}
	if err = runtimecheck.MustCheckOnBoot(capsDecl, log, nil); err != nil {
		return err
	}
	cfg, err := LoadConfig(apidConfigPath(flag.Lookup))
	if err != nil {
		return fmt.Errorf("clone worker: load config: %w", err)
	}
	if err = role.Require("apid", cfg.Role, role.RoleControlPlane); err != nil {
		return err
	}
	if err = rejectProductionDevEnvironment(cfg.Role, os.Getenv); err != nil {
		return err
	}
	if err = checkProjectEnvironmentCloneWorkerResources(ctx); err != nil {
		return err
	}
	reads, err := copycontents.NewReadPool(os.Getenv("FAAS_CLONE_WORKER_SPOOL_DIR"), copycontents.ReadPoolLimits{
		Readers: api.PostgresCopyContentsReadersPerWorkerMax, MemoryBytes: api.PostgresCopyContentsSortMemoryPerWorkerMax,
		DiskBytes: api.PostgresCopyContentsSortDiskPerWorkerMax, MinFreeBytes: api.PostgresCopyContentsSpoolFreeReserveMin})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, reads.Close()) }()
	admit := func(ctx context.Context) error {
		if err := checkProjectEnvironmentCloneWorkerResources(ctx); err != nil {
			return err
		}
		return reads.CheckForWorker(ctx)
	}
	if err = admit(ctx); err != nil {
		return err
	}
	if err = loadProjectEnvironmentCloneWorkerKeys(os.Getenv); err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, cfg.DBURL, "faas-apid-clone-worker")
	if err != nil {
		return errors.New("clone worker: control-plane database unavailable")
	}
	defer pool.Close()
	if err = db.MigrateUp(ctx, pool); err != nil {
		return fmt.Errorf("clone worker: migrate: %w", err)
	}
	openapidiff.RegisterStateCapture()
	store := state.NewPgStore(pool)
	srv := newServer(store, log, cfg.GetAppsDomain(os.Getenv), pgNotifier{pool: pool, log: log}).WithCompanionImages(cfg.CompanionImages)
	srv.clonePostgresContentsReadPool, srv.cloneWorkerAdmission = reads, admit
	srv.WithRuntimeConfigManager(newRuntimeConfigManager(os.Getenv)).WithDataPlacement(dataPlacementEnabledFromEnv(os.Getenv))
	objects, err := objectstorage.Load(os.Getenv)
	if err != nil {
		return errors.New("clone worker: object provider configuration unavailable")
	}
	srv.WithObjectStorage(objects)
	service, reconciler, bindings, bindingReconciler, usage, health, err := loadManagedPostgres(pool, os.Getenv, log)
	if err != nil {
		return errors.New("clone worker: PostgreSQL provider configuration unavailable")
	}
	srv.WithManagedPostgres(service, reconciler, bindings, bindingReconciler, usage, health)
	if err = srv.runtimeConfig.reconcile(ctx, store); err != nil {
		return err
	}
	live := wire.NewLiveness()
	live.Register("clone_coordinator", projectEnvironmentCloneWorkerLeaseDuration+api.PostgresCopyMaintenanceCleanupTimeout+2*projectEnvironmentCloneWorkerInterval)
	stopWatchdog := daemonunit.WatchdogFromEnv(ctx, live.Healthy)
	defer stopWatchdog()
	stopReady := daemonunit.NotifyReadyWhen(ctx, func() bool { return ctx.Err() == nil })
	defer stopReady()
	// Durable operator overrides are refreshed before each claim. This worker
	// subscribes to no LISTEN hub and starts no provider reconciler or API loop.
	return srv.runProjectEnvironmentCloneCoordinatorWithAdmission(ctx, func(ctx context.Context) error {
		if err := admit(ctx); err != nil {
			return err
		}
		return srv.runtimeConfig.reconcile(ctx, store)
	}, func() { live.Beat("clone_coordinator") })
}

func loadProjectEnvironmentCloneWorkerKeys(getenv func(string) string) error {
	identityPath, recipientPath, hmacPath := getenv("FAAS_FLEET_AGE_IDENTITY_PATH"), getenv("FAAS_FLEET_AGE_RECIPIENT_PATH"), getenv("FAAS_HOST_HMAC_KEY_PATH")
	if identityPath == "" || recipientPath == "" || hmacPath == "" {
		return errors.New("clone worker: fleet identity, recipient and HMAC credentials are required")
	}
	identities, err := secretbox.LoadFleetAndHostKeys(filepath.Dir(identityPath))
	if err != nil {
		return errors.New("clone worker: fleet/host identities unavailable")
	}
	identity, err := secretbox.LoadHostKey(identityPath)
	if err != nil || len(identities) == 0 || identities[0].Recipient().String() != identity.Recipient().String() {
		return errors.New("clone worker: fleet identity mismatch")
	}
	recipient, err := secretbox.LoadRecipient(recipientPath)
	if err != nil || recipient.String() != identity.Recipient().String() {
		return errors.New("clone worker: fleet recipient mismatch")
	}
	key, err := loadHostHMACKey(hmacPath)
	if err != nil {
		return errors.New("clone worker: HMAC credential unavailable")
	}
	// Install only after all credentials authenticate; no partial key fallback.
	setSecretRecipient = func() *age.X25519Recipient { return recipient }
	mfaIdentities = func() []*age.X25519Identity { return identities }
	hostHMACKey = func() []byte { return key }
	return nil
}
