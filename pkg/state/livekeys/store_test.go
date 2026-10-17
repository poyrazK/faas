package livekeys_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

const (
	account = "11111111-1111-1111-1111-111111111111"
	other   = "22222222-2222-2222-2222-222222222222"
	dbID    = "33333333-3333-3333-3333-333333333333"
	// A syntactically valid recipient; the store never opens what it holds.
	recipient = "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq"
)

// open applies the live-key migration over a minimal managed database table,
// so the store runs against the real constraints without the full schema.
func open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE managed_postgres_databases (id uuid PRIMARY KEY, account_id uuid NOT NULL, state text NOT NULL,
		backend_id text NOT NULL DEFAULT 'native-a', backend_fingerprint text NOT NULL DEFAULT 'fp', deleted_at timestamptz);
		CREATE TABLE managed_postgres_bindings (account_id uuid NOT NULL, database_id uuid NOT NULL, app_id uuid NOT NULL, state text NOT NULL, deleted_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261017121336518_managed_postgres_live_keys.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases (id, account_id, state) VALUES ($1, $2, 'ready')`, dbID, account); err != nil {
		t.Fatal(err)
	}
	return pool
}

func key(kid string) livekeys.NewKey {
	return livekeys.NewKey{KID: kid, Sealed: []byte("sealed-" + kid), Recipient: recipient}
}

func kids[T any](keys []T, kid func(T) string) []string {
	var out []string
	for _, k := range keys {
		out = append(out, kid(k))
	}
	return out
}

func TestStoreLifecycle(t *testing.T) {
	pool := open(t)
	store := livekeys.New(pool)
	ctx := context.Background()
	ring := func() []string {
		t.Helper()
		keys, err := store.Ring(ctx, dbID)
		if err != nil {
			t.Fatal(err)
		}
		return kids(keys, func(k livekeys.SealedKey) string { return k.KID })
	}

	if err := store.Rotate(ctx, account, dbID, key("aaaaaaaaaaaaaaaa"), time.Hour); !errors.Is(err, livekeys.ErrDisabled) {
		t.Fatalf("rotate before enable: %v, want ErrDisabled", err)
	}
	if err := store.Enable(ctx, other, dbID, key("aaaaaaaaaaaaaaaa")); !errors.Is(err, livekeys.ErrNotFound) {
		t.Fatalf("enable from another account: %v, want ErrNotFound", err)
	}
	if err := store.Enable(ctx, account, dbID, key("aaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	if err := store.Enable(ctx, account, dbID, key("bbbbbbbbbbbbbbbb")); !errors.Is(err, livekeys.ErrEnabled) {
		t.Fatalf("second enable: %v, want ErrEnabled", err)
	}

	// Rotation with overlap keeps the previous key until it retires.
	if err := store.Rotate(ctx, account, dbID, key("bbbbbbbbbbbbbbbb"), time.Hour); err != nil {
		t.Fatal(err)
	}
	keys, err := store.Keys(ctx, account, dbID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].KID != "bbbbbbbbbbbbbbbb" || !keys[0].RetiresAt.IsZero() || keys[1].RetiresAt.IsZero() {
		t.Fatalf("keys after rotation = %+v, want the new active key then the retiring one", keys)
	}
	if got := ring(); strings.Join(got, ",") != "aaaaaaaaaaaaaaaa,bbbbbbbbbbbbbbbb" {
		t.Fatalf("ring = %v", got)
	}

	// A second rotation drops the older retiring key: at most two keys.
	if err := store.Rotate(ctx, account, dbID, key("cccccccccccccccc"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := ring(); strings.Join(got, ",") != "bbbbbbbbbbbbbbbb,cccccccccccccccc" {
		t.Fatalf("ring after second rotation = %v", got)
	}

	// Rotation without overlap revokes every previous key at once.
	if err := store.Rotate(ctx, account, dbID, key("dddddddddddddddd"), 0); err != nil {
		t.Fatal(err)
	}
	if got := ring(); strings.Join(got, ",") != "dddddddddddddddd" {
		t.Fatalf("ring after immediate rotation = %v", got)
	}

	// A deleting database serves no keys.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, dbID); err != nil {
		t.Fatal(err)
	}
	if got := ring(); len(got) != 0 {
		t.Fatalf("ring of a deleting database = %v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='ready' WHERE id=$1`, dbID); err != nil {
		t.Fatal(err)
	}

	removed, err := store.Disable(ctx, account, dbID)
	if err != nil || !removed {
		t.Fatalf("disable = %v, %v", removed, err)
	}
	if got := ring(); len(got) != 0 {
		t.Fatalf("ring after disable = %v", got)
	}
	if removed, err := store.Disable(ctx, account, dbID); err != nil || removed {
		t.Fatalf("second disable = %v, %v", removed, err)
	}
}

func TestStoreRefusesMalformedKeys(t *testing.T) {
	pool := open(t)
	store := livekeys.New(pool)
	ctx := context.Background()
	bad := []livekeys.NewKey{
		{KID: "NOTHEX0000000000", Sealed: []byte("x"), Recipient: recipient},
		{KID: "aaaaaaaaaaaaaaaa", Sealed: nil, Recipient: recipient},
		{KID: "aaaaaaaaaaaaaaaa", Sealed: []byte("x"), Recipient: "not-a-recipient"},
	}
	for _, k := range bad {
		if err := store.Enable(ctx, account, dbID, k); err == nil {
			t.Fatalf("enable accepted %+v", k)
		}
	}
}

// An app routes the keys of the live databases it is bound to, and only
// those: through a ready or retiring binding of its own account.
func TestStoreRoutes(t *testing.T) {
	pool := open(t)
	store := livekeys.New(pool)
	ctx := context.Background()
	const (
		app      = "55555555-5555-5555-5555-555555555555"
		otherApp = "66666666-6666-6666-6666-666666666666"
		quietDB  = "77777777-7777-7777-7777-777777777777"
	)
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases (id, account_id, state) VALUES ($1, $2, 'ready')`, quietDB, account); err != nil {
		t.Fatal(err)
	}
	bind := func(app, db, state string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings VALUES ($1, $2, $3, $4, NULL)`, account, db, app, state); err != nil {
			t.Fatal(err)
		}
	}
	routes := func(app string) string {
		t.Helper()
		got, err := store.AppRoutes(ctx, account, app)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(kids(got, func(r livekeys.Route) string { return r.KID + "@" + r.DatabaseID + "/" + r.BackendID }), ",")
	}
	if err := store.Enable(ctx, account, dbID, key("aaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	bind(app, dbID, "provisioning")
	bind(app, quietDB, "ready") // bound, but no live queries there
	if got := routes(app); got != "" {
		t.Fatalf("routes through a provisioning binding = %q", got)
	}
	bind(app, dbID, "ready")
	bind(otherApp, dbID, "deleted")
	if got := routes(app); got != "aaaaaaaaaaaaaaaa@"+dbID+"/native-a" {
		t.Fatalf("routes = %q", got)
	}
	if got := routes(otherApp); got != "" {
		t.Fatalf("routes through a deleted binding = %q", got)
	}
	if got, err := store.AppRoutes(ctx, other, app); err != nil || len(got) != 0 {
		t.Fatalf("routes for another account = %v, %v", got, err)
	}
	if err := store.Rotate(ctx, account, dbID, key("bbbbbbbbbbbbbbbb"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := routes(app); got != "aaaaaaaaaaaaaaaa@"+dbID+"/native-a,bbbbbbbbbbbbbbbb@"+dbID+"/native-a" {
		t.Fatalf("routes during an overlap = %q", got)
	}
	apps, err := store.DatabaseApps(ctx, dbID)
	if err != nil || strings.Join(apps, ",") != app {
		t.Fatalf("bound apps = %v, %v", apps, err)
	}
}
