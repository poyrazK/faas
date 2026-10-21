package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/schemalog"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/wire"
)

// database is one database a hub serves.
type database struct {
	ID                string
	DSN               string
	Slot, Publication string
	Install           bool
	CreatePublication bool
	Checkpoint        string // empty disables checkpoints
	ReleaseAfter      time.Duration
	Awake             func(context.Context) (bool, error)
	AwakeEvery        time.Duration
	// awakeName names Awake's target for a discovered database
	// ("neon:<project>/<endpoint>"), so a change restarts it.
	awakeName string
}

// hubDeps is what the hubs of one process share.
type hubDeps struct {
	compiler livequery.Compiler
	keyring  *seal.Keyring // the development secret's; nil in production
	keys     *keySource    // the control plane's live keys; nil without -keys-*
	pollLag  bool
	log      *slog.Logger
}

// runDatabase serves db until ctx ends or its hub stops on an error. It hands
// publish the running hub, and nil once the hub has stopped.
func runDatabase(ctx context.Context, db database, pool *pgxpool.Pool, deps hubDeps, metrics *liveserver.Metrics, publish func(*liveserver.Hub)) error {
	log := deps.log
	if db.Install {
		if err := livequery.Install(ctx, pool); err != nil {
			return err
		}
	}
	if db.CreatePublication {
		if err := schemalog.EnsurePublication(ctx, pool, db.Publication); err != nil {
			return err
		}
	}
	capture, err := livequery.OpenSlot(ctx, db.DSN, db.Slot, db.Publication)
	if err != nil {
		return err
	}
	defer func() { _ = capture.Close() }() //nolint:contextcheck // Close bounds its own shutdown
	engine, err := openEngine(ctx, pool, capture, deps.compiler, db.Checkpoint, log)
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
	hub := liveserver.New(liveserver.Config{
		Database: db.ID, Keyring: deps.keyring, Engine: engine, Checkpoint: db.Checkpoint,
		CheckpointEvery: *checkpointEvery, Metrics: metrics, Logger: log, MaxPerConnection: *perConnection,
		Pool:         pool,
		ReleaseAfter: db.ReleaseAfter, Awake: db.Awake, AwakeEvery: db.AwakeEvery,
		Reconnect: func(ctx context.Context) (*livequery.Capture, error) {
			return livequery.OpenSlot(ctx, db.DSN, db.Slot, db.Publication)
		},
	})
	hubCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if deps.keys != nil {
		go deps.keys.watch(hubCtx, hub, log)
	}
	if deps.pollLag {
		go pollSlotLag(hubCtx, pool, db.Slot, metrics, hub.Released, log)
	}
	publish(hub)
	defer publish(nil)
	metrics.SetUp(true)
	defer metrics.SetUp(false)
	return hub.Run(hubCtx)
}

// runDatabases serves every database of -databases-file, and those
// -discover-backends finds, from this process. Each runs on its own: one
// that cannot be opened, or whose hub stops on an error, is retried with
// backoff while the others keep serving.
func runDatabases(ctx context.Context, log *slog.Logger) error {
	if err := refusePerDatabaseFlags(flag.CommandLine); err != nil {
		return err
	}
	if *listen == "" || *keysDSNFile == "" || *keysIdentity == "" {
		return errors.New("livequeryd: -databases-file and -discover-backends need -listen, -keys-dsn-file and -keys-identity")
	}
	var dbs []database
	if *databasesFile != "" {
		var err error
		if dbs, err = loadDatabases(*databasesFile, *checkpointDir); err != nil {
			return err
		}
	}
	discovery, err := discoveryConfig()
	if err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("livequeryd")
	set := liveserver.NewMetricsSet(ops.Registry())
	readyProbe := &wire.ReadyzProbe{}
	readySignal := readyProbe.Register()
	readyProbe.SetReadyObserver(func(ready bool, reason string) { ops.MarkReady("livequeryd", ready, reason) })
	defer readyProbe.Drain("livequeryd", log)
	keys, err := openKeySource(ctx)
	if err != nil {
		return err
	}
	defer keys.Close()

	routes := newRoutes(nil)
	var servers []*http.Server
	defer func() {
		for _, srv := range servers {
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = srv.Shutdown(shutdown)
			cancel()
		}
	}()
	srv, err := serveRealtimed(routes, log)
	if err != nil {
		return err
	}
	servers = append(servers, srv)
	if *metricsListen != "" {
		srv, err := serve(*metricsListen, controlMux(ops, readyProbe, routes), log)
		if err != nil {
			return err
		}
		servers = append(servers, srv)
	}
	// Ready once the listeners are up. A database that cannot be served yet
	// answers 503 and shows in /databases and livequery_hub_up; holding the
	// whole process unready for it would stop the others too.
	readySignal.Set(true, "")
	defer wire.NotifyReadyWhen(ctx, readyProbe.ReadyFunc())()
	defer wire.StartWatchdog(ctx, wire.NewLiveness(), ops, log)()
	log.Info("livequeryd: serving databases", "listed", len(dbs), "discover_backends", discovery.backends)

	f := newFleet(ctx, routes, set, keys, log)
	for _, db := range dbs {
		f.start(db, true)
	}
	if len(discovery.backends) > 0 {
		go discover(ctx, f, keys, discovery, log)
	}
	<-ctx.Done()
	f.wait()
	return nil
}

// supervise serves db until ctx ends, starting it again whenever opening it
// fails or its hub stops on an error.
func supervise(ctx context.Context, db database, deps hubDeps, metrics *liveserver.Metrics, routes *routes) {
	failures := 0
	for {
		started := time.Now()
		pool, err := pgxpool.New(ctx, db.DSN)
		if err == nil {
			err = runDatabase(ctx, db, pool, deps, metrics, func(h *liveserver.Hub) { routes.set(db.ID, h) })
			pool.Close()
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("the hub stopped") // Run returns nil only once ctx ends
		}
		if time.Since(started) >= time.Minute {
			failures = 0 // it ran for a while: start backing off afresh
		}
		failures++
		routes.failed(db.ID)
		metrics.Restarted()
		wait := restartBackoff(failures)
		deps.log.Error("livequeryd: database stopped; starting it again", "err", err, "in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// restartBackoff is how long to wait before the failures'th consecutive
// start: one second, doubling, at most a minute.
func restartBackoff(failures int) time.Duration {
	return min(time.Second<<min(failures-1, 6), time.Minute)
}

// databasesConfig is the -databases-file format.
type databasesConfig struct {
	Databases []databaseEntry `json:"databases"`
}

// databaseEntry is one database of -databases-file. File paths may name
// environment variables, so systemd credentials are
// "${CREDENTIALS_DIRECTORY}/<name>". Omitted settings take the defaults of
// the matching single-database flags, except the slot: gregale_live_<id>.
type databaseEntry struct {
	ID                string `json:"id"`
	DatabaseURLFile   string `json:"database_url_file"`
	Slot              string `json:"slot,omitempty"`
	Publication       string `json:"publication,omitempty"`
	Install           *bool  `json:"install,omitempty"`
	CreatePublication *bool  `json:"create_publication,omitempty"`
	ReleaseAfter      string `json:"release_after,omitempty"`
	Awake             string `json:"awake,omitempty"`
	AwakeEvery        string `json:"awake_every,omitempty"`
	NeonProject       string `json:"neon_project,omitempty"`
	NeonEndpoint      string `json:"neon_endpoint,omitempty"`
	// NeonAPIKeyFile defaults to -neon-api-key-file, one key for every
	// database of a Neon account.
	NeonAPIKeyFile string `json:"neon_api_key_file,omitempty"`
}

var (
	databaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	pgNamePattern     = regexp.MustCompile(`^[a-z0-9_]{1,63}$`)
)

// loadDatabases reads and checks -databases-file. Each database's
// checkpoint is <checkpointDir>/<id>.checkpoint, or none without a
// directory.
func loadDatabases(path, checkpointDir string) ([]database, error) {
	data, err := os.ReadFile(path) //nolint:forbidigo // operator-provisioned file named by a flag
	if err != nil {
		return nil, fmt.Errorf("livequeryd: read -databases-file: %w", err)
	}
	var cfg databasesConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("livequeryd: -databases-file: %w", err)
	}
	if len(cfg.Databases) == 0 {
		return nil, errors.New("livequeryd: -databases-file lists no database")
	}
	seen := map[string]bool{}
	slots := map[string]string{} // connection target and slot → database
	dbs := make([]database, 0, len(cfg.Databases))
	for i, e := range cfg.Databases {
		db, err := e.database(checkpointDir)
		if err != nil {
			return nil, fmt.Errorf("livequeryd: -databases-file: database %d (%q): %w", i, e.ID, err)
		}
		if seen[db.ID] {
			return nil, fmt.Errorf("livequeryd: -databases-file: database %q is listed twice", db.ID)
		}
		seen[db.ID] = true
		target, err := slotTarget(db)
		if err != nil {
			return nil, fmt.Errorf("livequeryd: -databases-file: database %q: %w", db.ID, err)
		}
		if other, ok := slots[target]; ok {
			return nil, fmt.Errorf("livequeryd: -databases-file: databases %q and %q use slot %s on one PostgreSQL server; slot names are server-wide", other, db.ID, db.Slot)
		}
		slots[target] = db.ID
		dbs = append(dbs, db)
	}
	return dbs, nil
}

func (e databaseEntry) database(checkpointDir string) (database, error) {
	if !databaseIDPattern.MatchString(e.ID) {
		return database{}, errors.New("id must be 1-128 letters, digits, _ or -")
	}
	if e.DatabaseURLFile == "" {
		return database{}, errors.New("database_url_file is required")
	}
	dsn, err := readSecretFile(os.ExpandEnv(e.DatabaseURLFile))
	if err != nil {
		return database{}, fmt.Errorf("database_url_file: %w", err)
	}
	db := database{ID: e.ID, DSN: dsn, Slot: cmp.Or(e.Slot, schemalog.Slot(e.ID)), Publication: cmp.Or(e.Publication, *publication),
		Install: boolOr(e.Install, *install), CreatePublication: boolOr(e.CreatePublication, *createPublication), AwakeEvery: *awakeEvery}
	if !pgNamePattern.MatchString(db.Slot) || !pgNamePattern.MatchString(db.Publication) {
		return database{}, errors.New("slot and publication must be 1-63 lower-case letters, digits or _")
	}
	if checkpointDir != "" {
		db.Checkpoint = filepath.Join(checkpointDir, db.ID+".checkpoint")
	}
	awake := awakeConfig{Source: e.Awake, NeonKeyFile: os.ExpandEnv(cmp.Or(e.NeonAPIKeyFile, *neonKeyFile)), NeonProject: e.NeonProject, NeonEndpoint: e.NeonEndpoint}
	if e.ReleaseAfter != "" {
		if awake.ReleaseAfter, err = time.ParseDuration(e.ReleaseAfter); err != nil || awake.ReleaseAfter < 0 {
			return database{}, fmt.Errorf("release_after %q is not a duration", e.ReleaseAfter)
		}
	}
	if e.AwakeEvery != "" {
		if db.AwakeEvery, err = time.ParseDuration(e.AwakeEvery); err != nil || db.AwakeEvery <= 0 {
			return database{}, fmt.Errorf("awake_every %q is not a positive duration", e.AwakeEvery)
		}
	}
	db.ReleaseAfter = awake.ReleaseAfter
	if db.Awake, err = newAwakeProbe(awake); err != nil {
		return database{}, err
	}
	return db, nil
}

// slotTarget names the server and slot db decodes, so two entries that
// would fight over one slot are caught before either connects.
func slotTarget(db database) (string, error) {
	cfg, err := pgxpool.ParseConfig(db.DSN)
	if err != nil {
		return "", errors.New("database_url_file does not hold a PostgreSQL connection string")
	}
	c := cfg.ConnConfig
	return fmt.Sprintf("%s:%d/%s", c.Host, c.Port, db.Slot), nil
}

// readSecretFile reads a one-line secret, such as a connection string.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:forbidigo // operator-provisioned secret named by the databases file
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", errors.New("the file is empty")
	}
	return s, nil
}

func boolOr(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}

// perDatabaseFlags configure the one database of a process without
// -databases-file; with it, each database's entry does instead.
var perDatabaseFlags = []string{
	"database-url", "database-url-file", "database-id", "slot", "publication", "install", "create-publication",
	"checkpoint", "release-after", "awake", "awake-every", "neon-project", "neon-endpoint",
	"dev-listen", "dev-secret-file", "dev-allow-origin", "compiler-url", "compiler-listen",
}

// refusePerDatabaseFlags rejects single-database flags set in fs beside
// -databases-file, which would otherwise be ignored silently.
func refusePerDatabaseFlags(fs *flag.FlagSet) error {
	var set []string
	fs.Visit(func(f *flag.Flag) {
		if slices.Contains(perDatabaseFlags, f.Name) {
			set = append(set, "-"+f.Name)
		}
	})
	if len(set) > 0 {
		return fmt.Errorf("livequeryd: %s configure a single database; with -databases-file set them in the file", strings.Join(set, ", "))
	}
	if *role != "all" {
		return errors.New("livequeryd: -databases-file runs every database with -role all")
	}
	return nil
}

// routes holds each served database's running hub. A database whose hub is
// starting or restarting has none.
type routes struct {
	mu  sync.RWMutex
	dbs map[string]*route
}

type route struct {
	hub      *liveserver.Hub
	restarts int
}

func newRoutes(ids []string) *routes {
	r := &routes{dbs: make(map[string]*route, len(ids))}
	for _, id := range ids {
		r.dbs[id] = &route{}
	}
	return r
}

// add serves a database from now on; it answers 503 until its hub runs.
func (r *routes) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dbs[id] == nil {
		r.dbs[id] = &route{}
	}
}

// remove stops serving a database: its path is not found again.
func (r *routes) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.dbs, id)
}

// set makes h the database's hub; nil while it has none.
func (r *routes) set(id string, h *liveserver.Hub) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt := r.dbs[id]; rt != nil {
		rt.hub = h
	}
}

// failed counts a failed start or a hub stopped on an error.
func (r *routes) failed(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt := r.dbs[id]; rt != nil {
		rt.restarts++
	}
}

// only returns the database when the process serves exactly one.
func (r *routes) only() (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.dbs) != 1 {
		return "", false
	}
	for id := range r.dbs {
		return id, true
	}
	return "", false
}

// serve hands the request to live over the database's hub: not found for a
// database this process does not serve, 503 while its hub is not running.
func (r *routes) serve(w http.ResponseWriter, req *http.Request, id string, live func(*liveserver.Hub) http.Handler) {
	r.mu.RLock()
	rt, known := r.dbs[id]
	var hub *liveserver.Hub
	if known {
		hub = rt.hub
	}
	r.mu.RUnlock()
	switch {
	case !known:
		http.NotFound(w, req)
	case hub == nil:
		w.Header().Set("Retry-After", "2")
		http.Error(w, "live queries for this database are starting", http.StatusServiceUnavailable)
	default:
		live(hub).ServeHTTP(w, req)
	}
}

// databaseStatus is one database in GET /databases.
type databaseStatus struct {
	ID       string `json:"id"`
	Running  bool   `json:"running"`
	Restarts int    `json:"restarts"`
}

// status lists the databases in ID order.
func (r *routes) status() []databaseStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]databaseStatus, 0, len(r.dbs))
	for id, rt := range r.dbs {
		out = append(out, databaseStatus{ID: id, Running: rt.hub != nil, Restarts: rt.restarts})
	}
	slices.SortFunc(out, func(a, b databaseStatus) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// controlMux serves metrics, /readyz and /healthz, and GET /databases: which
// databases have a running hub, for operators and the deploy's check.
func controlMux(ops *wire.OpsMetrics, ready *wire.ReadyzProbe, routes *routes) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", ops.Handler())
	mux.HandleFunc("GET /databases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"databases": routes.status()})
	})
	wire.ControlMuxLite(mux, ready.ReadyFunc(), ready.ReasonFunc())
	return mux
}
