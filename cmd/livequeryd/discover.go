package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/schemalog"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// Discovery (ADR-20261016151947): with -discover-backends, livequeryd serves
// every live database of those backends that has a live login, read from the
// control plane. apid issues the login, prepared the database, and sealed its
// connection string to livequeryd's identity when live queries were enabled,
// and revokes it, with its slot, when they are disabled.
var (
	discoverBackends     = flag.String("discover-backends", "", "comma-separated managed Postgres backends whose live databases this process serves, discovered from the control plane (-keys-dsn-file)")
	discoverEvery        = flag.Duration("discover-every", 15*time.Second, "how often to read the live databases of -discover-backends")
	discoverReleaseAfter = flag.Duration("discover-release-after", 0, "release a discovered database after this long with nothing to apply, when its provider names how to learn it runs again (0 never releases)")
)

type discovery struct {
	backends     []string
	every        time.Duration
	releaseAfter time.Duration
}

func discoveryConfig() (discovery, error) {
	d := discovery{every: *discoverEvery, releaseAfter: *discoverReleaseAfter}
	for b := range strings.SplitSeq(*discoverBackends, ",") {
		if b = strings.TrimSpace(b); b != "" {
			d.backends = append(d.backends, b)
		}
	}
	if d.every <= 0 {
		return discovery{}, errors.New("livequeryd: -discover-every must be positive")
	}
	return d, nil
}

// discover keeps the fleet equal to the discovered databases until ctx ends.
// A failed read keeps the current set: a control-plane outage stops no
// database.
func discover(ctx context.Context, f *fleet, keys *keySource, d discovery, log *slog.Logger) {
	store := livekeys.New(keys.pool)
	tick := time.NewTicker(d.every)
	defer tick.Stop()
	for {
		rows, err := store.LiveDatabases(ctx, d.backends)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Warn("livequeryd: reading live databases failed; keeping the current ones", "err", err)
		case err == nil:
			f.sync(discovered(rows, keys.identities, d, log))
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// discovered opens each row's login into a database. A row whose login does
// not open is skipped and logged.
func discovered(rows []livekeys.LiveDatabase, identities []*age.X25519Identity, d discovery, log *slog.Logger) []database {
	out := make([]database, 0, len(rows))
	for _, row := range rows {
		db, err := discoveredDatabase(row, identities, d)
		if err != nil {
			log.Error("livequeryd: a discovered database's login does not open; not serving it", "database", row.DatabaseID, "err", err)
			continue
		}
		out = append(out, db)
	}
	return out
}

func discoveredDatabase(row livekeys.LiveDatabase, identities []*age.X25519Identity, d discovery) (database, error) {
	namespace, plain, err := secretbox.OpenBytesMulti(identities, row.Sealed)
	if err != nil {
		return database{}, err
	}
	if namespace != livekeys.LoginSealNamespace {
		return database{}, fmt.Errorf("sealed for %q, not a live login", namespace)
	}
	if !databaseIDPattern.MatchString(row.DatabaseID) {
		return database{}, fmt.Errorf("database ID %q", row.DatabaseID)
	}
	// apid prepared the database: the login neither installs nor creates.
	db := database{ID: row.DatabaseID, DSN: string(plain), Slot: row.Slot, Publication: schemalog.Publication,
		awakeName: row.Awake}
	if *checkpointDir != "" {
		db.Checkpoint = filepath.Join(*checkpointDir, db.ID+".checkpoint")
	}
	if project, endpoint, ok := strings.Cut(strings.TrimPrefix(row.Awake, "neon:"), "/"); ok && strings.HasPrefix(row.Awake, "neon:") &&
		d.releaseAfter > 0 && *neonKeyFile != "" {
		db.ReleaseAfter = d.releaseAfter
		if db.Awake, err = newAwakeProbe(awakeConfig{ReleaseAfter: d.releaseAfter, Source: "neon", NeonKeyFile: *neonKeyFile,
			NeonProject: project, NeonEndpoint: endpoint}); err != nil {
			return database{}, err
		}
	}
	return db, nil
}

// fleet runs the databases a process serves, adding and removing them while
// it runs.
type fleet struct {
	ctx     context.Context
	routes  *routes
	set     *liveserver.MetricsSet
	keys    *keySource
	log     *slog.Logger
	mu      sync.Mutex
	members map[string]*member
	wg      sync.WaitGroup
}

type member struct {
	db      database
	static  bool // listed in -databases-file: discovery leaves it alone
	cancel  context.CancelFunc
	done    chan struct{}
	metrics *liveserver.Metrics
}

func newFleet(ctx context.Context, routes *routes, set *liveserver.MetricsSet, keys *keySource, log *slog.Logger) *fleet {
	return &fleet{ctx: ctx, routes: routes, set: set, keys: keys, log: log, members: map[string]*member{}}
}

// start serves db until the fleet stops it or its context ends.
func (f *fleet) start(db database, static bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startLocked(db, static)
}

func (f *fleet) startLocked(db database, static bool) {
	ctx, cancel := context.WithCancel(f.ctx)
	m := &member{db: db, static: static, cancel: cancel, done: make(chan struct{}), metrics: f.set.Database(db.ID)}
	f.members[db.ID] = m
	f.routes.add(db.ID)
	deps := hubDeps{keys: f.keys, pollLag: *metricsListen != "", log: f.log.With("database", db.ID)}
	f.wg.Go(func() {
		defer close(m.done)
		supervise(ctx, db, deps, m.metrics, f.routes)
	})
}

// stopLocked stops a database and waits until its hub has checkpointed.
func (f *fleet) stopLocked(id string) {
	m := f.members[id]
	if m == nil {
		return
	}
	m.cancel()
	<-m.done
	m.metrics.Unregister()
	f.routes.remove(id)
	delete(f.members, id)
}

// sync makes the discovered databases the ones served beside the listed
// ones: new ones start, ones whose login changed restart, and ones no longer
// discovered stop and lose their checkpoint, which their next slot could not
// resume.
func (f *fleet) sync(dbs []database) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[string]database{}
	for _, db := range dbs {
		want[db.ID] = db
	}
	for id, m := range f.members {
		if m.static {
			continue
		}
		db, ok := want[id]
		if ok && db.same(m.db) {
			continue
		}
		f.stopLocked(id)
		if !ok {
			if m.db.Checkpoint != "" {
				_ = os.Remove(m.db.Checkpoint) //nolint:forbidigo // livequeryd's own state file
			}
			f.log.Info("livequeryd: stopped serving a database", "database", id)
		}
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f.members[id] != nil {
			continue // served, listed, or unchanged
		}
		f.startLocked(want[id], false)
		f.log.Info("livequeryd: serving a discovered database", "database", id)
	}
}

// wait returns once every database has stopped.
func (f *fleet) wait() { f.wg.Wait() }

// same reports whether serving d needs nothing new: the same login, slot and
// release.
func (d database) same(o database) bool {
	return d.DSN == o.DSN && d.Slot == o.Slot && d.Publication == o.Publication && d.ReleaseAfter == o.ReleaseAfter && d.awakeName == o.awakeName
}
