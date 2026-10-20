// Command dbbackupd keeps native managed databases' WAL and base backups
// (ADR-1027 durability). It runs on each database node: vmmd relays every
// database VM's archive stream to its unix socket after stamping the
// database id, dbbackupd stores it durably on local disk, and an uploader
// ships it to S3-compatible storage. Restores read local copies first and
// fall back to the bucket. See pkg/dbarchive.
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
	"path/filepath"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	configPath := flag.String("config", "/etc/faas/dbbackupd.toml", "path to dbbackupd.toml")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *configPath, log); err != nil {
		log.Error("dbbackupd: exiting", "err", err)
		os.Exit(1)
	}
}

// metrics are dbbackupd's off-node recovery point: how much archive is
// still only on this node, and for how long; and the off-site mirror's.
type metrics struct {
	pendingFiles  prometheus.Gauge
	pendingOldest prometheus.Gauge
	uploadErrors  prometheus.Counter
	pruned        *prometheus.CounterVec
	pruneErrors   prometheus.Counter
	offsiteFiles  prometheus.Gauge
	offsiteOldest prometheus.Gauge
	offsiteErrors prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		pendingFiles: prometheus.NewGauge(prometheus.GaugeOpts{Name: "dbarchive_pending_files",
			Help: "Archived WAL and base backup files not yet uploaded off the node."}),
		pendingOldest: prometheus.NewGauge(prometheus.GaugeOpts{Name: "dbarchive_pending_oldest_seconds",
			Help: "Age of the oldest archive file not yet uploaded: the off-node recovery point."}),
		uploadErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: "dbarchive_upload_errors_total",
			Help: "Upload passes that stopped on an error."}),
		pruned: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "dbarchive_pruned_total",
			Help: "Archive files removed past their retention, by kind (backup, wal)."}, []string{"kind"}),
		pruneErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: "dbarchive_prune_errors_total",
			Help: "Prune passes that failed for at least one database."}),
		offsiteFiles: prometheus.NewGauge(prometheus.GaugeOpts{Name: "dbarchive_offsite_pending_files",
			Help: "Archive files and journaled deletions, holds and handoffs not yet applied to the off-site mirror."}),
		offsiteOldest: prometheus.NewGauge(prometheus.GaugeOpts{Name: "dbarchive_offsite_pending_oldest_seconds",
			Help: "Age of the oldest change not yet in the off-site mirror: the off-site recovery point."}),
		offsiteErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: "dbarchive_offsite_errors_total",
			Help: "Off-site mirror passes that stopped on an error."}),
	}
	reg.MustRegister(m.pendingFiles, m.pendingOldest, m.uploadErrors, m.pruned, m.pruneErrors, m.offsiteFiles, m.offsiteOldest, m.offsiteErrors)
	return m
}

func run(ctx context.Context, configPath string, log *slog.Logger) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	local, err := dbarchive.NewLocalStore(cfg.StoreDir)
	if err != nil {
		return fmt.Errorf("dbbackupd: store: %w", err)
	}
	store := &dbarchive.TieredStore{LocalStore: local, MinRetention: cfg.MinRetention}
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := newMetrics(reg)
	var uploader *dbarchive.Uploader
	if cfg.Remote != nil {
		s3cfg, err := cfg.Remote.s3Config()
		if err != nil {
			return err
		}
		remote, err := dbarchive.NewS3Remote(s3cfg)
		if err != nil {
			return err
		}
		store.Remote, store.Prefix = remote, cfg.Remote.Prefix
		uploader = &dbarchive.Uploader{Store: local, Remote: remote, Prefix: cfg.Remote.Prefix, OnPending: func(n int, oldest time.Duration) {
			m.pendingFiles.Set(float64(n))
			m.pendingOldest.Set(oldest.Seconds())
		}}
		go uploader.Run(ctx, cfg.Remote.UploadInterval, func(err error) {
			m.uploadErrors.Inc()
			log.Warn("dbbackupd: upload pass failed", "err", err)
		})
		// Before pruning or serving starts: the journal must see every
		// deletion.
		if err := startOffsite(ctx, cfg, local, store, m, log); err != nil {
			return err
		}
	} else {
		log.Warn("dbbackupd: no remote configured; the archive lives only on this node")
	}

	go prune(ctx, store, cfg, m, log)

	ln, err := listenUnix(cfg.SocketPath)
	if err != nil {
		return err
	}
	var probe wire.ReadyzProbe
	listening := probe.Register()
	if err := serveOps(ctx, cfg.OpsAddr, reg, &probe, log); err != nil {
		_ = ln.Close()
		return err
	}
	listening.Set(true, "")
	stopNotify := daemonunit.NotifyReadyWhen(ctx, probe.ReadyFunc())
	defer stopNotify()
	stopWatchdog := daemonunit.WatchdogFromEnv(ctx, func() bool { return true })
	defer stopWatchdog()
	log.Info("dbbackupd: serving", "socket", cfg.SocketPath, "store_dir", cfg.StoreDir, "remote", cfg.Remote != nil, "offsite", cfg.Offsite != nil)
	srv := &dbarchive.Server{Store: store, Backups: store, Retention: store, Log: log}
	if uploader != nil {
		srv.Handoff = uploader
	}
	return srv.Serve(ctx, ln)
}

// startOffsite starts the off-site mirror, if one is configured, and makes
// the store's reads fall back to it.
func startOffsite(ctx context.Context, cfg *Config, local *dbarchive.LocalStore, store *dbarchive.TieredStore, m *metrics, log *slog.Logger) error {
	if cfg.Offsite == nil {
		return nil
	}
	s3cfg, err := cfg.Offsite.s3Config()
	if err != nil {
		return err
	}
	mirror, err := dbarchive.NewS3Remote(s3cfg)
	if err != nil {
		return err
	}
	offsite := dbarchive.NewOffsite(local, store.Remote, cfg.Remote.Prefix, mirror, cfg.Offsite.Prefix)
	offsite.OnPending = func(n int, oldest time.Duration) {
		m.offsiteFiles.Set(float64(n))
		m.offsiteOldest.Set(oldest.Seconds())
	}
	store.Offsite, store.OffsitePrefix = mirror, cfg.Offsite.Prefix
	go offsite.Run(ctx, cfg.Offsite.UploadInterval, func(err error) {
		m.offsiteErrors.Inc()
		log.Warn("dbbackupd: off-site pass failed", "err", err)
	})
	return nil
}

// prune trims every archive to its retention now and every interval.
func prune(ctx context.Context, store *dbarchive.TieredStore, cfg *Config, m *metrics, log *slog.Logger) {
	ticker := time.NewTicker(cfg.PruneInterval)
	defer ticker.Stop()
	for {
		res, err := store.PruneAll(ctx, time.Now(), cfg.MinRetention)
		m.pruned.WithLabelValues("backup").Add(float64(res.Backups))
		m.pruned.WithLabelValues("wal").Add(float64(res.WAL))
		if err != nil && ctx.Err() == nil {
			m.pruneErrors.Inc()
			log.Warn("dbbackupd: prune pass failed", "err", err)
		} else if res.Backups+res.WAL > 0 {
			log.Info("dbbackupd: pruned", "backups", res.Backups, "wal", res.WAL)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// listenUnix replaces a stale socket and keeps it owner-only: vmmd, the
// only client, runs as root.
func listenUnix(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("dbbackupd: listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// serveOps serves /healthz, /readyz and /metrics until ctx ends.
func serveOps(ctx context.Context, addr string, reg *prometheus.Registry, probe *wire.ReadyzProbe, log *slog.Logger) error {
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, probe.ReadyFunc(), probe.ReasonFunc())
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dbbackupd: ops listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("dbbackupd: ops server", "err", err)
		}
	}()
	return nil
}
