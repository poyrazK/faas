// Command livequeryd maintains live queries for one PostgreSQL database
// (ADR-1384). It runs beside the database: it decodes the database's logical
// slot, keeps every subscribed query's result current, checkpoints its state
// so a restart or a park resumes from the slot, and serves clients. In
// production realtimed carries the client connections; for development,
// -dev-listen serves the live frames over a local WebSocket.
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
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/wire"
)

var (
	databaseURL       = flag.String("database-url", "", "PostgreSQL connection string; the role needs REPLICATION, and superuser to install the schema-change log")
	databaseID        = flag.String("database-id", "", "the database ID seals name")
	slotName          = flag.String("slot", "gregale_live", "logical replication slot")
	publication       = flag.String("publication", "gregale_live", "publication the slot decodes")
	createPublication = flag.Bool("create-publication", true, "create the publication FOR ALL TABLES when it is missing")
	install           = flag.Bool("install", true, "install the schema-change log and its event triggers")
	checkpointPath    = flag.String("checkpoint", "", "checkpoint file; empty disables checkpoints")
	checkpointEvery   = flag.Duration("checkpoint-every", time.Minute, "checkpoint interval")
	metricsListen     = flag.String("metrics-listen", "127.0.0.1:9471", "Prometheus metrics address; empty disables")
	devListen         = flag.String("dev-listen", "", "serve live frames over WebSocket at ws://<addr>/live (development only)")
	devSecretFile     = flag.String("dev-secret-file", "", "file holding the database's live secret (glsk1_...), for -dev-listen")
	devAllowOrigins   = flag.String("dev-allow-origin", "", "comma-separated browser origins allowed on -dev-listen; * allows any")
	verify            = flag.Bool("verify", false, "compare every result with PostgreSQL each round (slow; debugging only)")
)

func main() {
	wire.Daemon("livequeryd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	if *databaseURL == "" || *databaseID == "" {
		return errors.New("livequeryd: -database-url and -database-id are required")
	}
	ops := wire.NewOpsMetrics("livequeryd")
	metrics := liveserver.NewMetrics(ops.Registry())

	pool, err := pgxpool.New(ctx, *databaseURL)
	if err != nil {
		return fmt.Errorf("livequeryd: connect: %w", err)
	}
	defer pool.Close()
	if *install {
		if err := livequery.Install(ctx, pool); err != nil {
			return err
		}
	}
	if *createPublication {
		if err := ensurePublication(ctx, pool, *publication); err != nil {
			return err
		}
	}
	capture, err := livequery.OpenSlot(ctx, *databaseURL, *slotName, *publication)
	if err != nil {
		return err
	}
	defer func() { _ = capture.Close() }() //nolint:contextcheck // Close bounds its own shutdown
	engine, err := openEngine(ctx, pool, capture, log)
	if err != nil {
		return err
	}
	engine.VerifyEachRound = *verify
	if capture.Created && engine.Queries() > 0 {
		metrics.SlotLost(engine.Queries())
		log.Warn("livequeryd: the slot was lost; every query starts over", "queries", engine.Queries())
	}

	keyring, err := devKeyring()
	if err != nil {
		return err
	}
	hub := liveserver.New(liveserver.Config{
		Database: *databaseID, Keyring: keyring, Engine: engine, Checkpoint: *checkpointPath,
		CheckpointEvery: *checkpointEvery, Metrics: metrics, Logger: log,
	})

	var servers []*http.Server
	defer func() {
		for _, srv := range servers {
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = srv.Shutdown(shutdown)
			cancel()
		}
	}()
	if *metricsListen != "" {
		srv, err := serve(*metricsListen, ops.Handler(), log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
		go pollSlotLag(ctx, pool, *slotName, metrics, log)
	}
	if *devListen != "" {
		if keyring == nil {
			return errors.New("livequeryd: -dev-listen needs -dev-secret-file")
		}
		mux := http.NewServeMux()
		mux.Handle("/live", liveserver.WebSocket(hub, originChecker(*devAllowOrigins)))
		srv, err := serve(*devListen, mux, log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
		log.Warn("livequeryd: serving live frames without realtimed; development only", "addr", *devListen)
	}
	return hub.Run(ctx)
}

// openEngine restores the engine from its checkpoint, or starts a new one.
func openEngine(ctx context.Context, pool *pgxpool.Pool, capture *livequery.Capture, log *slog.Logger) (*livequery.Engine, error) {
	if *checkpointPath != "" {
		f, err := os.Open(*checkpointPath) //nolint:forbidigo // livequeryd's own state file, from its flags
		switch {
		case err == nil:
			defer func() { _ = f.Close() }()
			engine, err := livequery.RestoreEngine(ctx, pool, capture, f)
			if err == nil {
				log.Info("livequeryd: restored from checkpoint", "queries", engine.Queries(), "subscriptions", engine.Subscriptions())
				return engine, nil
			}
			// Clients re-subscribe; nothing else depends on the checkpoint.
			log.Error("livequeryd: checkpoint unreadable; starting empty", "err", err)
		case !errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("livequeryd: open checkpoint: %w", err)
		}
	}
	engine := livequery.NewEngine(pool, capture)
	engine.Checkpointed = *checkpointPath != ""
	return engine, nil
}

func ensurePublication(ctx context.Context, pool *pgxpool.Pool, name string) error {
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", name).Scan(&exists); err != nil {
		return fmt.Errorf("livequeryd: look up publication: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := pool.Exec(ctx, "CREATE PUBLICATION "+quoteIdent(name)+" FOR ALL TABLES"); err != nil {
		return fmt.Errorf("livequeryd: create publication: %w", err)
	}
	return nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// devKeyring reads the development secret, if any. In production realtimed
// holds the key ring and opens seals itself.
func devKeyring() (*seal.Keyring, error) {
	if *devSecretFile == "" {
		return nil, nil
	}
	data, err := os.ReadFile(*devSecretFile) //nolint:forbidigo // operator-supplied flag, development only
	if err != nil {
		return nil, fmt.Errorf("livequeryd: read secret: %w", err)
	}
	secret, err := seal.ParseSecret(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, err
	}
	return seal.NewKeyring(seal.Key{Database: *databaseID, Secret: secret})
}

// originChecker allows connections without an Origin header (non-browser
// clients) and browsers from the listed origins.
func originChecker(list string) func(*http.Request) bool {
	var allowed []string
	for _, o := range strings.Split(list, ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowed = append(allowed, o)
		}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" || slices.Contains(allowed, "*") || slices.Contains(allowed, origin)
	}
}

func serve(addr string, h http.Handler, log *slog.Logger) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("livequeryd: server stopped", "addr", addr, "err", err)
		}
	}()
	return srv, nil
}

// pollSlotLag records how much WAL the slot holds back.
func pollSlotLag(ctx context.Context, pool *pgxpool.Pool, slot string, metrics *liveserver.Metrics, log *slog.Logger) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		var lag int64
		err := pool.QueryRow(ctx, `SELECT coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn), 0)::bigint
			FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&lag)
		if err == nil {
			metrics.SetSlotLag(lag)
		} else if ctx.Err() == nil {
			log.Warn("livequeryd: read slot lag", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
