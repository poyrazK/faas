// object-storaged runs Gregale's private, single-node S3 storage prototype.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("object storage node stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	directory := flag.String("data-dir", "./object-data", "private data directory on a dedicated local filesystem")
	address := flag.String("listen", "127.0.0.1:19000", "loopback S3 listener; public traffic goes through s3-gatewayd")
	metricsAddress := flag.String("metrics-listen", "127.0.0.1:19001", "loopback metrics listener; empty disables metrics HTTP")
	region := flag.String("region", "us-east-1", "S3 signing region")
	maxBytes := flag.Int64("max-object-bytes", api.DefaultObjectUploadBytes, "maximum bytes in a single PUT")
	maxPart := flag.Int64("max-part-bytes", api.DefaultMultipartPartBytes, "maximum bytes in one multipart part")
	maxMultipart := flag.Int64("max-multipart-bytes", api.MaxObjectUploadBytes, "maximum selected bytes per multipart upload")
	concurrency := flag.Int("max-concurrent-writes", api.DefaultObjectConcurrentUploads, "maximum simultaneous uploads")
	minFree := flag.Int64("min-free-bytes", api.ObjectUploadSpoolMinFreeBytes, "free disk bytes retained after upload reservations")
	scrub := flag.Bool("scrub", true, "run background SHA256 verification")
	scrubBytes := flag.Int64("scrub-bytes-per-second", api.ObjectNodeScrubDefaultBytesPerStep, "maximum blob bytes per background scrub tick")
	auditOptions := objectnode.DefaultInventoryAuditOptions()
	flag.IntVar(&auditOptions.MaxRecords, "inventory-audit-records", auditOptions.MaxRecords, "maximum source inventory work units per checkpointed step")
	flag.IntVar(&auditOptions.MaxPending, "inventory-audit-backlog-records", auditOptions.MaxPending, "maximum coalesced source changes before restarting audit coverage")
	flag.DurationVar(&auditOptions.StepTime, "inventory-audit-time", auditOptions.StepTime, "soft source inventory step time budget; publication proceeds between steps")
	compact := flag.Bool("compact", true, "run bounded automatic journal retention")
	recoveryCleanup := flag.Bool("recovery-cleanup", true, "collect acknowledged recovery metadata on nodes already opted into format 5")
	expiryOptions := objectnode.DefaultMultipartExpiryOptions()
	flag.IntVar(&expiryOptions.MaxSessions, "multipart-expiry-sessions", expiryOptions.MaxSessions, "maximum due multipart candidate turns per expiry tick")
	flag.IntVar(&expiryOptions.MaxParts, "multipart-expiry-parts", expiryOptions.MaxParts, "maximum multipart part directories reclaimed per expiry tick")
	flag.DurationVar(&expiryOptions.StepTime, "multipart-expiry-time", expiryOptions.StepTime, "soft multipart expiry time budget, checked between durable decisions")
	recoveryOptions := objectnode.DefaultRecoveryCleanupOptions()
	flag.IntVar(&recoveryOptions.MaxRecords, "recovery-cleanup-records", recoveryOptions.MaxRecords, "maximum due recovery identities examined per cleanup tick")
	flag.DurationVar(&recoveryOptions.StepTime, "recovery-cleanup-time", recoveryOptions.StepTime, "soft cleanup time budget, checked between durable retirements")
	compaction := objectnode.DefaultCompactionOptions()
	flag.Int64Var(&compaction.TriggerRecords, "compact-records", compaction.TriggerRecords, "retained journal/payload file count triggering maintenance")
	flag.Int64Var(&compaction.TriggerJournalBytes, "compact-journal-bytes", compaction.TriggerJournalBytes, "retained journal bytes triggering maintenance")
	flag.Int64Var(&compaction.TriggerBlobBytes, "compact-replay-bytes", compaction.TriggerBlobBytes, "retained replay bytes triggering maintenance")
	flag.Int64Var(&compaction.MinFreeBytes, "compact-min-free-bytes", compaction.MinFreeBytes, "free-space maintenance watermark; 25 percent also triggers")
	flag.IntVar(&compaction.MaxRecords, "compact-step-records", compaction.MaxRecords, "maximum journal records per maintenance tick")
	flag.IntVar(&compaction.MaxBlobs, "compact-step-blobs", compaction.MaxBlobs, "maximum replay directory entries per maintenance tick")
	admission := writeAdmissionFlags(flag.CommandLine)
	flag.Parse()
	if err := objectstorage.RequireOwnedNodeServer(os.Getenv); err != nil {
		return err
	}
	if err := auditOptions.Validate(); err != nil {
		return err
	}
	if err := resolveWriteAdmission(admission, *minFree); err != nil {
		return err
	}
	if err := compaction.Validate(); err != nil {
		return err
	}
	if err := recoveryOptions.Validate(); err != nil {
		return err
	}
	if err := expiryOptions.Validate(); err != nil {
		return err
	}
	if *scrubBytes < 1 || *scrubBytes > api.ObjectNodeScrubMaxBytesPerStep {
		return errors.New("object node: invalid scrub read budget")
	}
	if !s3gateway.IsLoopbackAddress(*address) {
		return errors.New("object node: this prototype requires a loopback listener")
	}
	if *metricsAddress != "" && !s3gateway.IsLoopbackAddress(*metricsAddress) {
		return errors.New("object node: metrics requires a loopback listener")
	}
	store, err := objectnode.Open(*directory)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	handler, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: store, AccessKeyID: os.Getenv("FAAS_OBJECT_NODE_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("FAAS_OBJECT_NODE_SECRET_ACCESS_KEY"), Region: *region, MaxObjectBytes: *maxBytes, MaxPartBytes: *maxPart, MaxMultipartBytes: *maxMultipart, MaxConcurrentWrites: *concurrency, MinFreeBytes: *minFree, WriteAdmission: admission})
	if err != nil {
		return err
	}
	cleanup, stopCleanup := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	if *recoveryCleanup {
		workers.Go(func() {
			store.RunRecoveryCleanup(cleanup, recoveryOptions, func(_ objectnode.RecoveryCleanupStatus, err error) {
				if err != nil {
					log.Warn("object recovery cleanup paused", "error", err)
				}
			})
		})
	}
	if *compact {
		workers.Go(func() {
			store.RunCompactor(cleanup, compaction, func(_ objectnode.CompactionStatus, err error) {
				if err != nil {
					log.Warn("object journal compaction paused", "error", err)
				}
			})
		})
	}
	workers.Go(func() { sweepMultipart(cleanup, log, store, expiryOptions) })
	workers.Go(func() {
		handler.RunInventoryWithOptions(cleanup, auditOptions, func(err error) {
			if err != nil {
				log.Warn("object inventory audit failed", "error", err)
			}
		})
	})
	if *scrub {
		workers.Go(func() {
			handler.RunScrubber(cleanup, *scrubBytes, api.ObjectNodeScrubInterval, func(err error) { log.Warn("object checksum scrub failed", "error", err) })
		})
	}
	defer func() { stopCleanup(); workers.Wait() }()
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("object node: listen: %w", err)
	}
	defer func() { _ = listener.Close() }()
	var metrics net.Listener
	if *metricsAddress != "" {
		metrics, err = net.Listen("tcp", *metricsAddress)
		if err != nil {
			return fmt.Errorf("object node: metrics listen: %w", err)
		}
		defer func() { _ = metrics.Close() }()
	}
	return serve(log, listener, handler, metrics)
}

func sweepMultipart(ctx context.Context, log *slog.Logger, store *objectnode.Store, options objectnode.MultipartExpiryOptions) {
	if errors.Is(store.CanServe(), objectnode.ErrFenced) {
		return
	}
	ticker := time.NewTicker(api.ObjectMultipartCleanupRetry)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if _, err := store.ExpireMultipartStep(ctx, now.UTC(), options); errors.Is(err, objectnode.ErrFenced) {
				return
			} else if err != nil && ctx.Err() == nil {
				log.Warn("multipart expiry sweep failed", "error", err)
			}
		}
	}
}

func serve(log *slog.Logger, listener net.Listener, handler *s3gateway.DiskHandler, metrics net.Listener) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: api.ObjectTransferTimeout, WriteTimeout: api.ObjectTransferTimeout, IdleTimeout: time.Minute, MaxHeaderBytes: api.MaxObjectURLRequestBytes}
	server.RegisterOnShutdown(handler.CancelReplicationWaits)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 2)
	go func() { done <- server.Serve(listener) }()
	var metricsServer *http.Server
	if metrics != nil {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", handler.MetricsHandler())
		metricsServer = &http.Server{Handler: mux, ReadHeaderTimeout: time.Duration(api.MetricsReadTimeoutSecondsDefault) * time.Second, ReadTimeout: time.Duration(api.MetricsReadTimeoutSecondsDefault) * time.Second, WriteTimeout: time.Duration(api.MetricsWriteTimeoutSecondsDefault) * time.Second, IdleTimeout: time.Duration(api.MetricsIdleTimeoutSecondsDefault) * time.Second, MaxHeaderBytes: api.DefaultMaxHeaderBytes}
		go func() { done <- metricsServer.Serve(metrics) }()
		defer func() { _ = metricsServer.Close() }()
	}
	defer func() { _ = server.Close() }()
	log.Info("private object storage node listening", "address", listener.Addr().String())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if metricsServer != nil {
		_ = metricsServer.Shutdown(shutdown)
	}
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return fmt.Errorf("object node: graceful shutdown: %w", err)
	}
	err := <-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
