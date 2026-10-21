// Command livequeryd maintains live queries for PostgreSQL databases
// (ADR-20261016151947). For each database it decodes the database's logical
// slot, keeps every subscribed query's result current, checkpoints its state
// so a restart or a park resumes from the slot, and serves clients. In
// production realtimed carries the client connections; for development,
// -dev-listen serves the live frames over a local WebSocket.
//
// The flags below serve one database. -databases-file serves several from
// one process instead (databases.go): each database gets its own hub, slot,
// checkpoint and route, and one that fails is retried without stopping the
// others.
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
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/wire"
)

var (
	databaseURL       = flag.String("database-url", "", "PostgreSQL connection string; the role needs REPLICATION, and superuser to install the schema-change log")
	databaseURLFile   = flag.String("database-url-file", "", "file holding the -database-url connection string, which keeps its password out of the process list")
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
	databasesFile     = flag.String("databases-file", "", "JSON list of the databases to serve from this process, in place of -database-url, -database-id and the other per-database flags")
	checkpointDir     = flag.String("checkpoint-dir", "", "with -databases-file: directory of the checkpoint files, one <database-id>.checkpoint per database; empty disables checkpoints")
)

func main() {
	wire.Daemon("livequeryd", run)
}

// The roles split livequeryd in two (ADR-20261016151947): the engine runs beside the
// database, inside its VM, and never parses SQL; the compiler runs on the
// host with the parser and the memory it needs. -role all runs both in one
// process.
func run(ctx context.Context, log *slog.Logger) error {
	if *databasesFile != "" {
		return runDatabases(ctx, log)
	}
	if *checkpointDir != "" {
		return errors.New("livequeryd: -checkpoint-dir needs -databases-file")
	}
	dsn, err := databaseDSN()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn)
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
		defer wire.NotifyReadyWhen(ctx, func() bool { return true })()
		defer wire.StartWatchdog(ctx, wire.NewLiveness(), nil, log)()
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
	readyProbe := &wire.ReadyzProbe{}
	readySignal := readyProbe.Register()
	readyProbe.SetReadyObserver(func(ready bool, reason string) { ops.MarkReady("livequeryd", ready, reason) })
	defer readyProbe.Drain("livequeryd", log)

	keyring, err := devKeyring()
	if err != nil {
		return err
	}
	routes := newRoutes([]string{*databaseID})
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
	if keyring != nil && (*keysDSNFile != "" || *keysIdentity != "") {
		return errors.New("livequeryd: -dev-secret-file and -keys-* are exclusive")
	}
	keys, err := openKeySource(ctx)
	if err != nil {
		return err
	}
	defer keys.Close()
	if *listen != "" {
		srv, err := serveRealtimed(routes, log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
	}
	if *metricsListen != "" {
		srv, err := serve(*metricsListen, controlMux(ops, readyProbe, routes), log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
	}
	if *devListen != "" {
		if keyring == nil && keys == nil {
			return errors.New("livequeryd: -dev-listen needs -dev-secret-file or -keys-dsn-file")
		}
		mux := http.NewServeMux()
		origins := originChecker(*devAllowOrigins)
		mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
			routes.serve(w, r, *databaseID, func(h *liveserver.Hub) http.Handler { return liveserver.WebSocket(h, origins) })
		})
		srv, err := serve(*devListen, mux, log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
		log.Warn("livequeryd: serving live frames without realtimed; development only", "addr", *devListen)
	}
	defer wire.NotifyReadyWhen(ctx, readyProbe.ReadyFunc())()
	defer wire.StartWatchdog(ctx, wire.NewLiveness(), ops, log)()
	db := database{
		ID: *databaseID, DSN: dsn, Slot: *slotName, Publication: *publication, Install: *install,
		CreatePublication: *createPublication, Checkpoint: *checkpointPath,
		ReleaseAfter: *releaseAfter, Awake: awake, AwakeEvery: *awakeEvery,
	}
	deps := hubDeps{compiler: compiler, keyring: keyring, keys: keys, pollLag: *metricsListen != "", log: log}
	// Ready once every listener is up and the hub runs: a released hub
	// still serves the results it holds, so releasing does not make it
	// unready.
	var ready atomic.Bool
	return runDatabase(ctx, db, pool, deps, metrics, func(h *liveserver.Hub) {
		routes.set(db.ID, h)
		if h != nil && !ready.Swap(true) {
			readySignal.Set(true, "")
		}
	})
}

// databaseDSN returns the database connection string from -database-url or
// -database-url-file.
func databaseDSN() (string, error) {
	switch {
	case *databaseURL != "" && *databaseURLFile != "":
		return "", errors.New("livequeryd: -database-url and -database-url-file are exclusive")
	case *databaseURL != "":
		return *databaseURL, nil
	case *databaseURLFile != "":
		data, err := os.ReadFile(*databaseURLFile) //nolint:forbidigo // operator-provisioned file named by a flag
		if err != nil {
			return "", fmt.Errorf("livequeryd: read -database-url-file: %w", err)
		}
		if dsn := strings.TrimSpace(string(data)); dsn != "" {
			return dsn, nil
		}
		return "", errors.New("livequeryd: -database-url-file is empty")
	default:
		return "", errors.New("livequeryd: -database-url or -database-url-file is required")
	}
}

// openEngine restores the engine from its checkpoint, or starts a new one.
func openEngine(ctx context.Context, pool *pgxpool.Pool, capture *livequery.Capture, compiler livequery.Compiler, checkpoint string, log *slog.Logger) (*livequery.Engine, error) {
	if checkpoint != "" {
		f, err := os.Open(checkpoint) //nolint:forbidigo // livequeryd's own state file, from its flags
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
	engine.Checkpointed = checkpoint != ""
	engine.Compiler = compiler
	return engine, nil
}

func compilerMux(pool *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/compile", livequery.CompilerHandler(pool))
	return mux
}

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
