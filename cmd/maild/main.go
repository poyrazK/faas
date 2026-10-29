// Command maild is an internal-only, opt-in mail service. It has no TCP API
// listener and registers no customer routes. Disabled means no DB, no listener,
// no secrets loaded and no SMTP activity.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() { wire.Daemon("maild", run) }

func run(ctx context.Context, log *slog.Logger) error {
	if !internalmail.Enabled(os.Getenv) {
		log.Info("maild disabled")
		return nil
	}
	cfg, err := internalmail.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, "", "maild")
	if err != nil {
		return fmt.Errorf("maild: open database: %w", err)
	}
	defer pool.Close()
	store, err := internalmail.NewPGStoreWithDailyLimit(pool, cfg.Namespace, cfg.DailyRecipientLimit)
	if err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("maild")
	wire.BootStamps(ctx, "maild", ops)
	wire.RegisterDefaultOps(ops)
	wire.RegisterPoolMetrics(ops, pool)
	if err := store.Ready(ctx); err != nil {
		return errors.New("maild: internal mail schema is not ready; apply migrations through apid first")
	}
	if cfg.RecoveryID != "" {
		if err := store.ConfigureRecovery(ctx, cfg.RecoveryID, func() bool { return internalmail.RecoveryEnabled(os.Getenv) }); err != nil {
			return errors.New("maild: cannot establish configured recovery hold")
		}
	}
	if internalmail.ProducerOutboxEnabled(os.Getenv) {
		if err := store.ProducerOutboxReady(ctx); err != nil {
			return errors.New("maild: producer outbox metadata schema is unavailable")
		}
	}
	if internalmail.DeploymentFailedOutboxEnabled(os.Getenv) {
		if err := store.DeploymentFailedOutboxReady(ctx); err != nil {
			return errors.New("maild: deployment-failure schema unavailable")
		}
	}
	if internalmail.SubscriptionEndedOutboxEnabled(os.Getenv) {
		if err := store.SubscriptionEndedOutboxReady(ctx); err != nil {
			return errors.New("maild: subscription-ended schema unavailable")
		}
	}
	if internalmail.QuotaWarningOutboxEnabled(os.Getenv) {
		if err := store.QuotaWarningOutboxReady(ctx); err != nil {
			return errors.New("maild: quota warning schema unavailable")
		}
	}
	if internalmail.NonpaymentDeletionOutboxEnabled(os.Getenv) {
		if err := store.NonpaymentDeletionOutboxReady(ctx); err != nil {
			return errors.New("maild: non-payment deletion notification schema is unavailable")
		}
	}
	if internalmail.AccountSuspendedOutboxEnabled(os.Getenv) {
		if err := store.AccountSuspendedOutboxReady(ctx); err != nil {
			return errors.New("maild: account suspension notification schema is unavailable")
		}
	}
	if internalmail.ManagedPostgresCapOutboxEnabled(os.Getenv) {
		if err := store.ManagedPostgresCapOutboxReady(ctx); err != nil {
			return errors.New("maild: managed postgres cap notification schema is unavailable")
		}
	}
	if internalmail.AccountRestoredOutboxEnabled(os.Getenv) {
		if err := store.AccountRestoredOutboxReady(ctx); err != nil {
			return errors.New("maild: account restoration notification schema is unavailable")
		}
	}
	if internalmail.PaymentFailedOutboxEnabled(os.Getenv) {
		if err := store.PaymentFailedOutboxReady(ctx); err != nil {
			return errors.New("maild: payment failure notification metadata schema is unavailable")
		}
	}

	codec, err := internalmail.LoadMailCodec(cfg)
	if err != nil {
		return fmt.Errorf("maild: load encryption keyring: %w", err)
	}
	if err := store.ConfigureEncryption(ctx, codec); err != nil {
		return errors.New("maild: required encryption identities or durable selection are unavailable")
	}
	probe := &wire.ReadyzProbe{}
	pgSignal, stopPing := wire.NewPGPingSignal(ctx, pool, 5*time.Second)
	probe.RegisterSignal(pgSignal, stopPing)
	probe.SetReadyObserver(func(ready bool, reason string) { ops.MarkReady("maild", ready, reason) })
	defer probe.Drain("maild", log)
	service := &internalmail.Service{
		Store:                             store,
		Codec:                             codec,
		Resolver:                          net.DefaultResolver,
		SendingIP:                         cfg.SendingIP,
		Enabled:                           func() bool { return internalmail.Enabled(os.Getenv) },
		DeliveryEnabled:                   func() bool { return os.Getenv(internalmail.DeliveryEnableEnv) == "1" },
		ReportsEnabled:                    func() bool { return internalmail.ReportsEnabled(os.Getenv) },
		ReportToken:                       cfg.ReportToken,
		FeedbackEnabled:                   func() bool { return internalmail.FeedbackEnabled(os.Getenv) },
		FeedbackSources:                   cfg.FeedbackSources,
		RetentionEnabled:                  func() bool { return internalmail.RetentionEnabled(os.Getenv) },
		RetentionPolicy:                   cfg.RetentionPolicy,
		OperationsEnabled:                 func() bool { return internalmail.OperationsEnabled(os.Getenv) },
		OperationsToken:                   cfg.OperationsToken,
		RotationEnabled:                   func() bool { return internalmail.RotationEnabled(os.Getenv) },
		ReverificationEnabled:             func() bool { return internalmail.ReverificationEnabled(os.Getenv) },
		DeliveryControlsEnabled:           func() bool { return internalmail.DeliveryControlsEnabled(os.Getenv) },
		ProducersEnabled:                  func() bool { return internalmail.ProducersEnabled(os.Getenv) },
		Producers:                         cfg.Producers,
		SchedulingEnabled:                 func() bool { return internalmail.SchedulingEnabled(os.Getenv) },
		PacingEnabled:                     func() bool { return internalmail.PacingEnabled(os.Getenv) },
		PacingPolicy:                      cfg.PacingPolicy,
		BackpressureEnabled:               func() bool { return internalmail.BackpressureEnabled(os.Getenv) },
		ReputationEnabled:                 func() bool { return internalmail.ReputationEnabled(os.Getenv) },
		EventFeedEnabled:                  func() bool { return internalmail.EventFeedEnabled(os.Getenv) },
		ProducerSchedulingEnabled:         func() bool { return internalmail.ProducerSchedulingEnabled(os.Getenv) },
		ProducerLifecycleEnabled:          func() bool { return internalmail.ProducerLifecycleEnabled(os.Getenv) },
		EncryptionRotationEnabled:         func() bool { return internalmail.EncryptionRotationEnabled(os.Getenv) },
		ProducerOutboxEnabled:             func() bool { return internalmail.ProducerOutboxEnabled(os.Getenv) },
		OutboxReconciliationEnabled:       func() bool { return internalmail.OutboxReconciliationEnabled(os.Getenv) },
		VerificationOutboxEnabled:         func() bool { return internalmail.VerificationOutboxEnabled(os.Getenv) },
		PasswordResetOutboxEnabled:        func() bool { return internalmail.PasswordResetOutboxEnabled(os.Getenv) },
		MagicLinkOutboxEnabled:            func() bool { return internalmail.MagicLinkOutboxEnabled(os.Getenv) },
		MFADisableOutboxEnabled:           func() bool { return internalmail.MFADisableOutboxEnabled(os.Getenv) },
		DeploymentFailedOutboxEnabled:     func() bool { return internalmail.DeploymentFailedOutboxEnabled(os.Getenv) },
		SubscriptionEndedOutboxEnabled:    func() bool { return internalmail.SubscriptionEndedOutboxEnabled(os.Getenv) },
		QuotaWarningOutboxEnabled:         func() bool { return internalmail.QuotaWarningOutboxEnabled(os.Getenv) },
		NonpaymentDeletionOutboxEnabled:   func() bool { return internalmail.NonpaymentDeletionOutboxEnabled(os.Getenv) },
		AccountSuspendedOutboxEnabled:     func() bool { return internalmail.AccountSuspendedOutboxEnabled(os.Getenv) },
		ManagedPostgresCapOutboxEnabled:   func() bool { return internalmail.ManagedPostgresCapOutboxEnabled(os.Getenv) },
		AccountRestoredOutboxEnabled:      func() bool { return internalmail.AccountRestoredOutboxEnabled(os.Getenv) },
		PaymentFailedOutboxEnabled:        func() bool { return internalmail.PaymentFailedOutboxEnabled(os.Getenv) },
		MFAEnabledAlertOutboxEnabled:      func() bool { return internalmail.MFAEnabledAlertOutboxEnabled(os.Getenv) },
		PasswordChangedAlertOutboxEnabled: func() bool { return internalmail.PasswordChangedAlertOutboxEnabled(os.Getenv) },
		MFADisabledAlertOutboxEnabled:     func() bool { return internalmail.MFADisabledAlertOutboxEnabled(os.Getenv) },
		MFARecoveryAlertOutboxEnabled:     func() bool { return internalmail.MFARecoveryAlertOutboxEnabled(os.Getenv) },
		RecoveryEnabled:                   func() bool { return internalmail.RecoveryEnabled(os.Getenv) },
		SuppressionControlsEnabled:        func() bool { return internalmail.SuppressionControlsEnabled(os.Getenv) },
		RecipientAllowed:                  cfg.RecipientAllowed,
		Transport:                         internalmail.SMTP{Address: cfg.SMTPAddr, Hello: "mail.gregale.internal", RequestDSN: cfg.Reports},
		Ready: func(context.Context) error {
			ready, _ := probe.All()
			if !ready {
				return errors.New("maild dependencies not ready")
			}
			return nil
		},
		Metrics: ops.Handler(),
	}
	monitor := internalmail.NewMonitor(store, service.Enabled, service.DeliveryEnabled, service.ReportsEnabled, service.FeedbackEnabled)
	retentionMonitor := internalmail.NewRetentionMonitor(store, func() bool { return internalmail.RetentionEnabled(os.Getenv) }, cfg.RetentionPolicy)
	service.OnRetention = retentionMonitor.Observe
	domainMonitor := internalmail.NewDomainLifecycleMonitor(store, func() bool { return internalmail.RotationEnabled(os.Getenv) }, func() bool { return internalmail.ReverificationEnabled(os.Getenv) }, cfg.SendingIP.String())
	service.OnDomainCheck = domainMonitor.Observe
	controlMonitor := internalmail.NewDeliveryControlMonitor(store, func() bool { return internalmail.DeliveryControlsEnabled(os.Getenv) })
	schedulingMonitor := internalmail.NewSchedulingMonitor(store, func() bool { return internalmail.SchedulingEnabled(os.Getenv) })
	pacingMonitor := internalmail.NewPacingMonitor(store, func() bool { return internalmail.PacingEnabled(os.Getenv) })
	relayMonitor := internalmail.NewRelayHealthMonitor(store, func() bool { return internalmail.BackpressureEnabled(os.Getenv) })
	reputationMonitor := internalmail.NewReputationMonitor(store, func() bool { return internalmail.ReputationEnabled(os.Getenv) })
	eventFeedMonitor := internalmail.NewEventFeedMonitor(store, func() bool { return internalmail.EventFeedEnabled(os.Getenv) })
	producerSchedulerMonitor := internalmail.NewProducerSchedulerMonitor(service)
	producerLifecycleMonitor := internalmail.NewProducerLifecycleMonitor(store, func() bool { return internalmail.ProducerLifecycleEnabled(os.Getenv) })
	encryptionMonitor := internalmail.NewEncryptionMonitor(store, codec, func() bool { return internalmail.EncryptionRotationEnabled(os.Getenv) })
	suppressionMonitor := internalmail.NewSuppressionControlMonitor(store, func() bool { return internalmail.SuppressionControlsEnabled(os.Getenv) })
	producerOutboxMonitor := internalmail.NewProducerOutboxMonitor(store, func() bool { return internalmail.ProducerOutboxEnabled(os.Getenv) })
	ops.Registry().MustRegister(producerOutboxMonitor)
	recoveryMonitor := internalmail.NewRecoveryMonitor(store, func() bool { return internalmail.RecoveryEnabled(os.Getenv) })
	ops.Registry().MustRegister(recoveryMonitor)
	ops.Registry().MustRegister(monitor)
	ops.Registry().MustRegister(retentionMonitor)
	ops.Registry().MustRegister(domainMonitor)
	ops.Registry().MustRegister(controlMonitor)
	ops.Registry().MustRegister(schedulingMonitor)
	ops.Registry().MustRegister(pacingMonitor)
	ops.Registry().MustRegister(relayMonitor)
	ops.Registry().MustRegister(reputationMonitor)
	ops.Registry().MustRegister(eventFeedMonitor)
	ops.Registry().MustRegister(producerSchedulerMonitor)
	ops.Registry().MustRegister(producerLifecycleMonitor)
	ops.Registry().MustRegister(encryptionMonitor)
	ops.Registry().MustRegister(suppressionMonitor)
	monitorRegistry := prometheus.NewRegistry()
	monitorRegistry.MustRegister(producerOutboxMonitor)
	monitorRegistry.MustRegister(recoveryMonitor)
	monitorRegistry.MustRegister(monitor)
	monitorRegistry.MustRegister(retentionMonitor)
	monitorRegistry.MustRegister(domainMonitor)
	monitorRegistry.MustRegister(controlMonitor)
	monitorRegistry.MustRegister(schedulingMonitor)
	monitorRegistry.MustRegister(pacingMonitor)
	monitorRegistry.MustRegister(relayMonitor)
	monitorRegistry.MustRegister(reputationMonitor)
	monitorRegistry.MustRegister(eventFeedMonitor)
	monitorRegistry.MustRegister(producerSchedulerMonitor)
	monitorRegistry.MustRegister(producerLifecycleMonitor)
	monitorRegistry.MustRegister(encryptionMonitor)
	monitorRegistry.MustRegister(suppressionMonitor)
	service.Monitoring = promhttp.HandlerFor(monitorRegistry, promhttp.HandlerOpts{})
	handler, err := internalmail.Handler(service, cfg.Token)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0750); err != nil {
		return err
	}
	lock := flock.New(cfg.Socket + ".lock")
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return errors.New("maild: socket is already owned by another process")
	}
	defer func() { _ = lock.Unlock() }()
	listener, err := wire.ListenOrRecreateByName(cfg.Socket, "faas")
	if err != nil {
		return fmt.Errorf("maild: private listener: %w", err)
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: wire.HTTPMetricsHandler(ops, "maild_request", handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	return serve(ctx, log, server, listener, service, probe, ops)
}

func serve(ctx context.Context, log *slog.Logger, server *http.Server, listener net.Listener, service *internalmail.Service, probe *wire.ReadyzProbe, ops *wire.OpsMetrics) error {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	liveness := wire.NewLiveness()
	liveness.Register("delivery", 45*time.Second)
	service.OnTick = func() { liveness.Beat("delivery") }
	workerDone := make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() {
		service.Run(workerCtx, func(error) { log.Error("maild delivery state update failed") })
	})
	workers.Go(func() {
		service.RunRetention(workerCtx, func(error) { log.Error("maild retention pass failed") })
	})
	workers.Go(func() {
		service.RunDomainChecks(workerCtx, func(error) { log.Error("maild domain check pass failed") })
	})
	workers.Go(func() {
		service.RunEventFeed(workerCtx, func(error) { log.Error("maild event publication pass failed") })
	})
	go func() {
		defer close(workerDone)
		workers.Wait()
	}()
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- server.Serve(listener) }()
	stopNotify := daemonunit.NotifyReadyWhen(ctx, probe.ReadyFunc())
	defer stopNotify()
	stopWatchdog := wire.StartWatchdog(ctx, liveness, ops, log)
	defer stopWatchdog()
	select {
	case <-ctx.Done():
	case err := <-errorsCh:
		cancel()
		<-workerDone
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	err := server.Shutdown(shutdownCtx)
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		return errors.New("maild: timed out draining delivery worker")
	}
	return err
}
