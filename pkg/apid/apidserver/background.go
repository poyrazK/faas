package apidserver

// apid background workers under ADR-1152 §4.
//
// apid serves HTTP on every control node. Most of its background workers,
// though, act on shared state: rollback workers, managed Postgres, realtime
// and durable reconcilers, retention sweepers, the grace timer, outboxes and
// notification bridges. Several have no claim of their own, so running them
// on three nodes would repeat external effects (emails, rollbacks, audit
// rows). Those run only on the node holding the apid-background lease and
// stop when it is lost; another node's apid takes them over.
//
// Workers that serve or describe this replica (its caches, its SSE clients,
// its local spool and log files, its own metrics) keep running on every
// replica.

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/eventretention"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/logintoken"
	"github.com/onebox-faas/faas/pkg/singleton"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
)

const (
	apidBackgroundLeaseName = "apid-background"
	// apidBackgroundLeaseEnv set to "0" runs the shared workers on every
	// replica, as before ADR-1152 (single control node only).
	apidBackgroundLeaseEnv = "FAAS_APID_BACKGROUND_LEASE"
)

func apidBackgroundLeaseEnabled(getenv func(string) string) bool {
	if getenv == nil {
		return true
	}
	return strings.TrimSpace(getenv(apidBackgroundLeaseEnv)) != "0"
}

// startBackground launches apid's background workers: the per-replica ones
// directly, the shared ones under the apid-background lease.
func startBackground(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, srv *server, getenv func(string) string) {
	startReplicaBackground(ctx, log, pool, srv)
	if !apidBackgroundLeaseEnabled(getenv) {
		startSharedBackground(ctx, log, pool, srv, getenv, func(f func()) { go f() })
		return
	}
	runner, err := singleton.New(state.NewPgStore(pool), apidBackgroundLeaseName, singleton.NewHolderID(), singleton.Timings{}, log)
	if err != nil {
		log.Error("apid: shared background workers not started: build lease runner", "err", err)
		return
	}
	go func() {
		err := runner.Run(ctx, func(leaseCtx context.Context, epoch int64) error {
			log.Info("apid: holding the apid-background lease; starting shared background workers", "epoch", epoch)
			var wg sync.WaitGroup
			startSharedBackground(leaseCtx, log, pool, srv, getenv, wg.Go)
			<-leaseCtx.Done()
			// Join the workers started directly before another node may
			// take over. Helpers that spawn their own goroutines stop on
			// the same context.
			wg.Wait()
			return leaseCtx.Err()
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("apid: apid-background lease runner exited", "err", err)
		}
	}()
}

// startReplicaBackground starts the workers every replica runs for itself.
func startReplicaBackground(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, srv *server) {
	// ADR-132: pg_notify is a low-latency wake-up only. The
	// subscriber re-reads the durable runtime_config_entries row, so a
	// missed notification is repaired by the next reconnect or boot.
	go func() {
		if err := runRuntimeConfigSubscriber(ctx, pool, srv, log); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("runtime_config subscriber exited", "err", err)
		}
	}()
	// PR-1 of issue #1182 §P1 packaging follow-up —
	// in-process upload_sessions reaper. 5-minute ticker,
	// bounds the 24h TTL to within 5 minutes of staleness.
	// Per replica: it removes .part files from this host's spool.
	go func() {
		if err := runUploadSessionReaper(ctx, srv, log); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("upload session reaper exited", "err", err)
		}
	}()
	// Move 3 (M7.5 prep): bridge pg_notify → in-process broadcaster.
	// Runs as a background goroutine for the daemon's lifetime; the
	// SubscribeWithReconnect wrapper reconnects across Postgres
	// restarts. Fails fast at boot if the initial Subscribe errors
	// — the dashboard SSE surfaces the gap rather than silently
	// producing empty frames. Every replica serves its own SSE clients.
	go sseFanIn(ctx, log, pool, srv.events, nil)
	// Issue #562 / PR-A: log archive shipper. Walks the local
	// spool dir every cfg.FlushInterval (5 min default) and
	// gzip-PUTs .partial files to s3://{bucket}/faas-logs/{instance}/
	// {YYYY}/{MM}/{DD}.jsonl.gz. Disabled (returns nil on ctx
	// cancel) when FAAS_LOG_ARCHIVE_BUCKET is unset — the
	// pgAdmin-style on-box ring buffer is the only log surface
	// for Free + Hobby tiers in that mode.
	//
	// Credentials are read from the archive-creds.json envelope
	// unsealed by `gregale backup unseal-archive-creds` and
	// mounted at the systemd credential path. On a host that
	// hasn't been unsealed yet the optional credential is absent
	// and the shipper remains disabled.
	shipCfg, shipErr := logarchive.ConfigFromEnv(os.Getenv, log)
	if shipErr == nil {
		archiveCredsPath := envOr(logarchive.EnvCredentialsPath, logarchive.DefaultCredentialsPath)
		if creds, err := logarchive.ReadCredentials(archiveCredsPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Warn("logarchive.creds_unavailable", "path", archiveCredsPath, "err", err)
			}
		} else {
			shipCfg = shipCfg.WithCredentials(creds)
		}
	}
	switch {
	case shipErr != nil:
		log.Warn("logarchive.config_failed", "err", shipErr)
	case !shipCfg.Enabled():
		log.Info("logarchive.disabled", "reason", "FAAS_LOG_ARCHIVE_BUCKET unset")
	default:
		s3, err := logarchive.NewS3Client(
			shipCfg.Endpoint,
			shipCfg.Region,
			shipCfg.Bucket,
			shipCfg.KeyID,
			shipCfg.Secret,
			shipCfg.AuthMode,
		)
		if err != nil {
			log.Warn("logarchive.s3client_init_failed", "err", err)
		} else {
			spool := logarchive.NewSpool(shipCfg.SpoolRoot, shipCfg.LocalBytesMax)
			shipMetrics := logarchive.NewMetrics(srv.ops.Registry())
			sh, err := logarchive.NewShipper(shipCfg, spool, s3, log, shipMetrics)
			if err != nil {
				log.Warn("logarchive.shipper_init_failed", "err", err)
			} else {
				go func() {
					if err := sh.Run(ctx); err != nil && ctx.Err() == nil {
						log.Warn("logarchive.run_returned_error", "err", err)
					}
					_ = sh.Spool().CloseAll()
				}()
			}
		}
	}
	// Issue #300: topNSampler drives the apid_top_tenant_rps
	// gauge from the rolling per-account count fed by
	// observeWrap (server.go:observeWrap). 5s tick; runs for
	// the daemon's lifetime; stops cleanly on ctx cancel.
	topNSampler := newTopNSampler(srv.ops, log)
	go topNSampler.run(ctx)
	// Issues #250/#1695: publish verified off-host backup and bounded
	// local WAL state from root-written, API-readable marker files.
	pgBackupPushedSampler := newPgBackupPushedSampler(srv.ops, log)
	go pgBackupPushedSampler.run(ctx)
	// ADR-126 / issue #975 item #2: bridge the two pg_notify
	// channels that mutate the `?source=auto` cache inputs
	// (NotifyAppOpenAPIDocChanged + NotifyEdgeRuleChanged)
	// into SpecCache.InvalidateByApp. Nil cache is tolerated
	// by the subscriber (it logs and returns nil) so a
	// misconfigured dev box never spawns a goroutine that
	// loops forever doing nothing. Every replica owns its cache.
	if srv.specCache != nil {
		go func() {
			if err := runOpenAPIDocSubscriber(ctx, pool, srv.specCache, log); err != nil && ctx.Err() == nil {
				log.Error("openapi_doc: subscriber exited", "err", err)
			}
		}()
	}
}

// startSharedBackground starts the workers that act on shared state. spawn
// starts one goroutine (go, or a WaitGroup's Go under the lease).
func startSharedBackground(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, srv *server, getenv func(string) string, spawn func(func())) {
	spawn(func() { srv.runSafeReleaseEmergencyAbort(ctx) })
	spawn(func() { srv.runServiceRolloutBindingWorker(ctx) })
	spawn(func() { srv.runCheckedRollbackWorker(ctx) })
	spawn(func() { srv.runAlertRollbackWorker(ctx) })
	spawn(func() { srv.runRollbackOn5xxWorker(ctx) })
	spawn(func() { srv.operation.RunOperationArtifactCleanup(ctx) })
	spawn(func() { srv.object.RunObjectStorageRecovery(ctx) })
	spawn(func() { srv.object.RunObjectStorageAccounting(ctx) })
	spawn(func() { srv.runManagedPostgresReconciler(ctx) })
	spawn(func() { srv.runManagedPostgresBindingReconciler(ctx) })
	spawn(func() { srv.runProjectEnvironmentCleanupReconciler(ctx) })
	spawn(func() { srv.runProjectEnvironmentCloneCoordinator(ctx) })
	spawn(func() { srv.runManagedPostgresUsageCollector(ctx) })
	spawn(func() { srv.runManagedPostgresHealthCollector(ctx) })
	spawn(func() { srv.runAppHealthCollector(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeEndpointReconciler(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeChannelRouteReconciler(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeOwnerReaper(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeHistoryReaper(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeEntityExpirations(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeSchedules(ctx) })
	spawn(func() { srv.realtime.RunRealtimeConsumerGroupAlerts(ctx) })
	spawn(func() { srv.realtime.RunRealtimeConsumerGroupDeadRetention(ctx) })
	spawn(func() { srv.realtime.RunRealtimeReplayJobs(ctx) })
	spawn(func() { srv.realtime.RunRealtimeConsumerGroupWorkerHealth(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeFallbacks(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimePush(ctx) })
	spawn(func() { srv.realtime.RunManagedRealtimeDrainWorker(ctx) })
	spawn(func() { srv.runManagedExecutionWorkflowWorker(ctx) })
	spawn(func() { srv.durable.RunAlarms(ctx) })
	spawn(func() { srv.durable.RunDurableEntityRegistryCleanup(ctx) })
	spawn(func() { srv.durable.RunOutbox(ctx) })
	spawn(func() { srv.durable.RunMaintenance(ctx) })
	spawn(func() { srv.durable.RunHealth(ctx) })
	spawn(func() { srv.durable.RunBackups(ctx) })
	// ADR-089 PR-C — background re-seal runner. The runner is
	// nil when FAAS_REKEY_ENABLED is unset (or when identities
	// failed to load); we skip the goroutine launch in that
	// case so the boot path is a no-op on the default deploy.
	// The run blocks until ctx.Done() (Runner.Run signature,
	// rekey_runner.go).
	if srv.rekeyRunner != nil {
		spawn(func() { _ = srv.rekeyRunner.Run(ctx) })
	}
	// I1 / issue #1397: deployments.status='failed' is the durable
	// trigger for the deployer notification. The database trigger
	// emits deployment_changed after commit, and this subscriber
	// performs the account lookup, hourly app cooldown claim, and
	// redacted email send.
	spawn(func() {
		if err := runDeployFailedEmailSubscriber(ctx, pool, srv, log); err != nil && ctx.Err() == nil {
			log.Error("deploy_failed_email: subscriber exited", "err", err)
		}
	})
	startDNSPoller(ctx, srv, log)
	// ADR-127 PR-B: regression detector. Mirrors the dns_poller's
	// shape — first-pass-immediate + ticker + ctx-cancel. The
	// cron is gated on FAAS_REQUEST_TELEMETRY_ENABLED so the
	// surface can be dark-launched with the ginstal kill-switch
	// that the apid gRPC receiver (Stage 4) already honors.
	startDebugRegressionCron(ctx, srv, log, getenv)
	startProfileDeploymentChecks(ctx, srv, log)
	startFeatureFlagAutoAdvancer(ctx, srv, log)
	srv.startIssuesMaintenance(ctx)
	// G6 grace timer (spec §17 G6, ADR-021): the 30-day deletion
	// grace sweep lives in apid (not meterd) because the write
	// side (DELETE /v1/account, POST /v1/account/restore) is here
	// and meterd owns quotas/billing only. Default Interval 60s
	// matches the grace-side precision we need; sweep is a
	// ListAllAccounts walk so it stays bounded by the customer
	// count on the one box.
	graceLoop := grace.New(grace.Params{
		Store:               srv.store,
		BeforeAccountDelete: srv.object.CleanupExpiredAccountObjectBuckets,
		Mailer:              graceSenderAdapter{m: srv.mailer},
		Log:                 log,
		Interval:            graceIntervalFromEnv(log),
		Artifacts:           srv.sbomStorage,
		Registry:            srv.ops.Registry(),
		Notif: func(ctx context.Context, ch, payload string) error {
			return srv.notif.Notify(ctx, ch, payload)
		},
		Audit: srv.audit, // issue #755 / PR-5.5: emit account.deleted from the sweep
	})
	spawn(func() { _ = graceLoop.Run(ctx) })
	// Login-token cleanup (issue #165 PR #2, ADR-032). The
	// login_tokens table backs password-reset (15-min TTL) and
	// the legacy magic-link surface PR #1 removed. The
	// /login/forgot → POST /auth/reset pair is the only
	// production caller — we run a 24h ticker so the table
	// stays bounded by (rate of reset requests) × 15min.
	// pkg/logintoken mirrors pkg/grace (same Run / RunOnce
	// shape) so the lifecycle is consistent with the G6 grace
	// timer above.
	loginTokenCleanup := logintoken.New(logintoken.Params{
		Store: srv.store,
		Log:   log,
	})
	spawn(func() { _ = loginTokenCleanup.Run(ctx) })
	// ADR-075: 90-day audit retention. The events table grows
	// ~3-4 GB/year/active-tier through the auth / key / secret
	// / account / stateless audit namespaces plus the future
	// wake-timeline / sidecar surfaces. The daily loop trims
	// rows older than eventretention.DefaultCutoffDays (90d,
	// SOC 2 CC6.2 evidence-retention floor). Mirrors
	// pkg/logintoken byte-for-byte (same Run / RunOnce pattern,
	// same first-pass-error defence-in-depth).
	eventRetentionCleanup := eventretention.New(eventretention.Params{
		Store: srv.store,
		Log:   log,
	})
	// ADR-091 D20.3 / PR-B residual: thread the Ops into the
	// audit-event retention cleanup loop. srv.ops is populated
	// by srv.WithOpsMetrics before deps.bgBefore is invoked, so
	// reading srv.ops here is safe; the loop runs whether or not
	// ops is nil (it logs but does not increment — same
	// trade-off the apid audit auditor makes with
	// pkg/audit.Auditor.SetOps).
	eventRetentionCleanup.SetOps(srv.ops)
	spawn(func() { _ = eventRetentionCleanup.Run(ctx) })
	// Webhook replay-dedupe sweep (issue #294). The
	// webhook_deliveries table is written by all three ingresses
	// (GitHub via gatewayd-internal, Stripe + Paddle via apid); the TTL
	// expires_at column + the partial index keep the per-tick
	// DELETE bounded by (60s tick × ~rows added in that window).
	// 60s matches the meterd dunning sweep cadence.
	webhookSweeper := webhookdedupe.NewSweeper(webhookdedupe.DefaultSweepInterval)
	spawn(func() { _ = webhookSweeper.Run(ctx) })
	// Issue #472 / ADR-058: bridge the `audit_event` pg_notify
	// channel (imaged emits signature-failure events via this
	// channel since imaged is the only fire-and-forget audit
	// publisher that doesn't write the events table directly)
	// into the apid-side auditor. Under the lease only one replica
	// writes each event; the durable outbox below replays anything
	// published while the lease moved.
	if srv.audit != nil {
		spawn(func() {
			// issue #517 / PR-C / ADR-064: thread the
			// events Platform through the audit
			// subscriber so verify-rejection kinds
			// (app.signature_invalid /
			// app.signature_missing) also emit the
			// typed wake.deploy_failed row.
			if err := runAuditSubscriber(ctx, pool, srv.audit, log, srv.eventsPlatform); err != nil && ctx.Err() == nil {
				log.Error("audit: subscriber exited", "err", err)
			}
		})
	}
	// Public-release hardening: pg_notify is only the wakeup for
	// imaged signature audits. The durable outbox replay loop
	// recovers rows missed during a LISTEN reconnect or an apid
	// restart, and also drains rows when the notification subscriber
	// is unavailable.
	if _, ok := srv.store.(state.AuditEventOutboxStore); ok {
		spawn(func() {
			if err := runAuditOutbox(ctx, srv.store, log, srv.eventsPlatform); err != nil && ctx.Err() == nil {
				log.Error("audit: durable outbox exited", "err", err)
			}
		})
	}
	if _, ok := srv.store.(state.OrgActivityOutboxStore); ok {
		spawn(func() {
			if err := runOrgActivityOutbox(ctx, srv.store, log); err != nil && ctx.Err() == nil {
				log.Error("activity: durable outbox exited", "err", err)
			}
		})
	}
}
