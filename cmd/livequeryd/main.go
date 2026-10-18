// Command livequeryd maintains live queries for one PostgreSQL database
// (ADR-20261016151947). It runs beside the database: it decodes the database's logical
// slot, keeps every subscribed query's result current, checkpoints its state
// so a restart or a park resumes from the slot, and serves clients. In
// production realtimed carries the client connections; for development,
// -dev-listen serves the live frames over a local WebSocket.
package main

import (
	"context"
	"crypto/tls"
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
	role              = flag.String("role", "all", "all (engine and compiler in one process), engine, or compiler")
	compilerURL       = flag.String("compiler-url", "", "with -role engine: the compiler to send queries to; the engine then never starts the SQL parser")
	compilerListen    = flag.String("compiler-listen", "", "with -role compiler or all: serve compilation at http://<addr>/compile")
	perConnection     = flag.Int("max-per-connection", 0, "subscriptions per client connection (default the plan limit); raise it when realtimed proxies many clients over one connection")
)

func main() {
	wire.Daemon("livequeryd", run)
}

// The roles split livequeryd in two (ADR-20261016151947): the engine runs beside the
// database, inside its VM, and never parses SQL; the compiler runs on the
// host with the parser and the memory it needs. -role all runs both in one
// process.
func run(ctx context.Context, log *slog.Logger) error {
	if *databaseURL == "" {
		return errors.New("livequeryd: -database-url is required")
	}
	pool, err := pgxpool.New(ctx, *databaseURL)
	if err != nil {
		return fmt.Errorf("livequeryd: connect: %w", err)
	}
	defer pool.Close()
	switch *role {
	case "compiler":
		if *compilerListen == "" {
			return errors.New("livequeryd: -role compiler needs -compiler-listen")
		}
		srv, err := serve(*compilerListen, compilerMux(pool), log)
		if err != nil {
			return err
		}
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case "engine", "all":
	default:
		return fmt.Errorf("livequeryd: unknown -role %q", *role)
	}
	if *databaseID == "" {
		return errors.New("livequeryd: -database-id is required")
	}
	var compiler livequery.Compiler
	if *compilerURL != "" {
		compiler = livequery.RemoteCompiler(*compilerURL, &http.Client{Timeout: 30 * time.Second})
	} else if *role == "engine" {
		return errors.New("livequeryd: -role engine needs -compiler-url")
	}
	awake, err := awakeProbe()
	if err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("livequeryd")
	metrics := liveserver.NewMetrics(ops.Registry())
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
	engine, err := openEngine(ctx, pool, capture, compiler, log)
	if err != nil {
		return err
	}
	engine.VerifyEachRound = *verify
	// Close the capture the engine holds at exit, which a resume may have
	// replaced.
	defer func() { _ = engine.Release() }() //nolint:contextcheck // Release bounds its own shutdown
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
		CheckpointEvery: *checkpointEvery, Metrics: metrics, Logger: log, MaxPerConnection: *perConnection,
		Pool:         pool,
		ReleaseAfter: *releaseAfter, Awake: awake, AwakeEvery: *awakeEvery,
		Reconnect: func(ctx context.Context) (*livequery.Capture, error) {
			return livequery.OpenSlot(ctx, *databaseURL, *slotName, *publication)
		},
	})

	var servers []*http.Server
	if *compilerListen != "" {
		srv, err := serve(*compilerListen, compilerMux(pool), log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
	}
	defer func() {
		for _, srv := range servers {
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = srv.Shutdown(shutdown)
			cancel()
		}
	}()
	if *keysDSNFile != "" || *keysIdentity != "" {
		if keyring != nil {
			return errors.New("livequeryd: -dev-secret-file and -keys-* are exclusive")
		}
		closeKeys, err := watchKeys(ctx, hub, log)
		if err != nil {
			return err
		}
		defer closeKeys()
	}
	if *listen != "" {
		srv, err := serveRealtimed(hub, log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
	}
	if *metricsListen != "" {
		srv, err := serve(*metricsListen, ops.Handler(), log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
		go pollSlotLag(ctx, pool, *slotName, metrics, hub.Released, log)
	}
	if *devListen != "" {
		if keyring == nil && *keysDSNFile == "" {
			return errors.New("livequeryd: -dev-listen needs -dev-secret-file or -keys-dsn-file")
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
func openEngine(ctx context.Context, pool *pgxpool.Pool, capture *livequery.Capture, compiler livequery.Compiler, log *slog.Logger) (*livequery.Engine, error) {
	if *checkpointPath != "" {
		f, err := os.Open(*checkpointPath) //nolint:forbidigo // livequeryd's own state file, from its flags
		switch {
		case err == nil:
			defer func() { _ = f.Close() }()
			engine, err := livequery.RestoreEngineWith(ctx, pool, capture, f, compiler)
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
	engine.Compiler = compiler
	return engine, nil
}

func compilerMux(pool *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/compile", livequery.CompilerHandler(pool))
	return mux
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

// devKeyring reads the development secret, if any. In production the key
// ring comes from the control plane (-keys-dsn-file).
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
	return serveTLS(addr, h, nil, log)
}

// serveTLS serves h on addr, with TLS when conf is set.
func serveTLS(addr string, h http.Handler, conf *tls.Config, log *slog.Logger) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: listen %s: %w", addr, err)
	}
	if conf != nil {
		ln = tls.NewListener(ln, conf)
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
func pollSlotLag(ctx context.Context, pool *pgxpool.Pool, slot string, metrics *liveserver.Metrics, released func() bool, log *slog.Logger) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		// A released database is left alone: reading the lag would wake it.
		if !released() {
			var lag int64
			err := pool.QueryRow(ctx, `SELECT coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn), 0)::bigint
				FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&lag)
			if err == nil {
				metrics.SetSlotLag(lag)
			} else if ctx.Err() == nil {
				log.Warn("livequeryd: read slot lag", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
