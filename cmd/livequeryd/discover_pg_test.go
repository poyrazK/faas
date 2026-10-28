package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/schemalog"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// adr: 20261016151947 — livequeryd serves a database it discovers from the
// control plane: its sealed login opens, its live key opens seals, and a
// subscription gets its rows. Disabling live queries (the keys gone) stops
// it: its path is not found again and its checkpoint is removed.
func TestDiscoverDatabases(t *testing.T) {
	base := os.Getenv("LIVEQUERY_DATABASE_URL")
	if base == "" {
		t.Skip("LIVEQUERY_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	var level string
	if err := admin.QueryRow(ctx, "SHOW wal_level").Scan(&level); err != nil || level != "logical" {
		t.Skipf("wal_level is %q, need logical", level)
	}
	suffix := fmt.Sprintf("%d_%d", os.Getpid(), rand.Uint32())
	cpName, customerName := "lqd_cp_"+suffix, "lqd_db_"+suffix
	dsnOf := func(name string) string { return strings.Replace(base, "/postgres", "/"+name, 1) }
	for _, name := range []string{cpName, customerName} {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx := context.Background()
			for range 100 {
				var left int
				_ = admin.QueryRow(ctx, `SELECT count(*) FROM pg_replication_slots WHERE database = $1`, name).Scan(&left)
				if left == 0 {
					break
				}
				_, _ = admin.Exec(ctx, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE database = $1 AND NOT active`, name)
				time.Sleep(100 * time.Millisecond)
			}
			_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		})
	}

	// The customer database, prepared as apid's provider would.
	customer, err := pgxpool.New(ctx, dsnOf(customerName))
	if err != nil {
		t.Fatal(err)
	}
	defer customer.Close()
	if _, err := customer.Exec(ctx, `CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL); INSERT INTO items VALUES (1, 'discovered')`); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginTxFunc(ctx, customer, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := schemalog.Install(ctx, tx); err != nil {
			return err
		}
		return schemalog.EnsurePublication(ctx, tx, schemalog.Publication)
	}); err != nil {
		t.Fatal(err)
	}

	// The control plane: a live database with a key and a login.
	cp, err := pgxpool.New(ctx, dsnOf(cpName))
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if _, err := cp.Exec(ctx, `CREATE TABLE managed_postgres_databases (id uuid PRIMARY KEY, account_id uuid NOT NULL, state text NOT NULL,
		backend_id text NOT NULL, backend_fingerprint text NOT NULL DEFAULT 'fp', deleted_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261017121336518_managed_postgres_live_keys.sql", "20261021100642318_managed_postgres_live_logins.sql", "20261028161544217_managed_postgres_live_signatures.sql", "20261028161546331_managed_postgres_live_signatures_validate.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := cp.Exec(ctx, up); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dbID, account := uuid.NewString(), uuid.NewString()
	secretText, err := seal.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := seal.ParseSecret(secretText)
	if err != nil {
		t.Fatal(err)
	}
	sealedKey, err := secretbox.SealBytes(identity.Recipient(), livekeys.SealNamespace, []byte(secretText), 256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Exec(ctx, `INSERT INTO managed_postgres_databases (id, account_id, state, backend_id) VALUES ($1, $2, 'ready', 'neon-a')`, dbID, account); err != nil {
		t.Fatal(err)
	}
	store := livekeys.New(cp)
	login := &livekeys.Login{Sealed: sealLogin(t, identity, livekeys.LoginSealNamespace, dsnOf(customerName)), Recipient: identity.Recipient().String(),
		Identity: "gregale_live_test", Slot: schemalog.Slot(dbID)}
	if err := store.Enable(ctx, account, dbID, livekeys.NewKey{KID: secret.KID, Sealed: sealedKey, Recipient: identity.Recipient().String()},
		func(context.Context) (*livekeys.Login, error) { return login, nil }); err != nil {
		t.Fatal(err)
	}

	checkpoints := t.TempDir()
	saved := *checkpointDir
	*checkpointDir = checkpoints
	defer func() { *checkpointDir = saved }()
	keys := &keySource{ring: store.Ring, identities: []*age.X25519Identity{identity}, pool: cp}
	log := slog.New(slog.NewTextHandler(t.Output(), nil))
	routes := newRoutes(nil)
	runCtx, stop := context.WithCancel(ctx)
	f := newFleet(runCtx, routes, liveserver.NewMetricsSet(prometheus.NewRegistry()), keys, log)
	discovered := make(chan struct{})
	go func() {
		defer close(discovered)
		discover(runCtx, f, keys, discovery{backends: []string{"neon-a"}, every: 200 * time.Millisecond}, log)
	}()
	srv := httptest.NewServer(realtimedMux(routes, func(h *liveserver.Hub) http.Handler {
		return liveserver.WebSocket(h, func(*http.Request) bool { return true })
	}))
	stopped := false
	shutdown := func() {
		if !stopped {
			stopped = true
			srv.Close()
			stop()
			<-discovered
			f.wait()
		}
	}
	defer shutdown()

	waitUntil(t, "the discovered database to run", func() bool {
		st := routes.status()
		return len(st) == 1 && st[0].ID == dbID && st[0].Running
	})
	c := dialLive(t, srv.URL+"/"+dbID+"/live")
	query := "SELECT id, name FROM items"
	// The hub reads its key ring from the control plane within a moment.
	waitUntil(t, "a subscription the key opens", func() bool {
		c.subscribe("s1", sealFor(t, secret, dbID, query))
		deadline := time.After(3 * time.Second)
		for {
			select {
			case frame := <-c.frames:
				c.apply(frame)
				if r := c.results["s1"]; r.Ready() {
					return true
				} else if r.Err != nil {
					return false
				}
			case <-deadline:
				return false
			}
		}
	})
	if got := names(c.results["s1"]); got != "discovered" {
		t.Fatalf("rows = %q", got)
	}

	// Disabling live queries removes the database.
	if _, err := store.Disable(ctx, account, dbID, nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the disabled database to stop", func() bool { return len(routes.status()) == 0 })
	resp, err := http.Get(srv.URL + "/" + dbID + "/live")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a disabled database answers %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(checkpoints, dbID+".checkpoint")); !os.IsNotExist(err) {
		t.Error("the disabled database's checkpoint was kept")
	}
	shutdown()
}
