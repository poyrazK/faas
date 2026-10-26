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
		CREATE TABLE managed_postgres_bindings (account_id uuid NOT NULL, database_id uuid NOT NULL, app_id uuid NOT NULL, scope text NOT NULL DEFAULT 'default',
			access text NOT NULL DEFAULT 'read_write', provider_identity_id text, state text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), deleted_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261017121336518_managed_postgres_live_keys.sql", "20261021100642318_managed_postgres_live_logins.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
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
	if err := store.Enable(ctx, other, dbID, key("aaaaaaaaaaaaaaaa"), nil); !errors.Is(err, livekeys.ErrNotFound) {
		t.Fatalf("enable from another account: %v, want ErrNotFound", err)
	}
	if err := store.Enable(ctx, account, dbID, key("aaaaaaaaaaaaaaaa"), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Enable(ctx, account, dbID, key("bbbbbbbbbbbbbbbb"), nil); !errors.Is(err, livekeys.ErrEnabled) {
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

	removed, err := store.Disable(ctx, account, dbID, nil)
	if err != nil || !removed {
		t.Fatalf("disable = %v, %v", removed, err)
	}
	if got := ring(); len(got) != 0 {
		t.Fatalf("ring after disable = %v", got)
	}
	if removed, err := store.Disable(ctx, account, dbID, nil); err != nil || removed {
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
		if err := store.Enable(ctx, account, dbID, k, nil); err == nil {
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
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings (account_id, database_id, app_id, state) VALUES ($1, $2, $3, $4)`, account, db, app, state); err != nil {
			t.Fatal(err)
		}
	}
	login := func(app, db, access, role string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings (account_id, database_id, app_id, access, provider_identity_id, state)
			VALUES ($1, $2, $3, $4, $5, 'ready')`, account, db, app, access, role); err != nil {
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
	if err := store.Enable(ctx, account, dbID, key("aaaaaaaaaaaaaaaa"), nil); err != nil {
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

	// The logins live queries read as: sealed queries as the read-only
	// binding when there is one, RLS-mode reads as the Data API binding.
	roles := func() string {
		t.Helper()
		got, err := store.AppRoutes(ctx, account, app)
		if err != nil || len(got) == 0 {
			t.Fatalf("routes %v, %v", got, err)
		}
		return got[0].SealedRole + "|" + got[0].DataAPIRole
	}
	if got := roles(); got != "|" {
		t.Fatalf("roles without logins = %q", got)
	}
	login(app, dbID, "read_write", "gregale_rw_1")
	login(app, dbID, "data_api", "gregale_api_1")
	if got := roles(); got != "gregale_rw_1|gregale_api_1" {
		t.Fatalf("roles = %q", got)
	}
	login(app, dbID, "read_only", "gregale_ro_1")
	login(otherApp, dbID, "read_only", "gregale_ro_other")
	if got := roles(); got != "gregale_ro_1|gregale_api_1" {
		t.Fatalf("roles with a read-only binding = %q", got)
	}
}

// adr: 20261016151947 — a database enabled with a login is one livequeryd
// discovers on its backend, until live queries are disabled or the database
// is deleted; the login's sealed connection string and slot come with it.
func TestStoreLogins(t *testing.T) {
	pool := open(t)
	store := livekeys.New(pool)
	ctx := context.Background()
	discover := func(backends ...string) []livekeys.LiveDatabase {
		t.Helper()
		dbs, err := store.LiveDatabases(ctx, backends)
		if err != nil {
			t.Fatal(err)
		}
		return dbs
	}
	login := &livekeys.Login{Sealed: []byte("sealed-login"), Recipient: recipient, Identity: "gregale_live_ab12", Slot: "gregale_live_db", Awake: "neon:proj-1/ep-a"}
	if _, ok, err := store.LoginOf(ctx, account, dbID); err != nil || ok {
		t.Fatalf("login before enable: %v, %v", ok, err)
	}
	issue := func(context.Context) (*livekeys.Login, error) { return login, nil }
	if err := store.Enable(ctx, account, dbID, key("aaaaaaaaaaaaaaaa"), issue); err != nil {
		t.Fatal(err)
	}
	got := discover("native-a", "neon-b")
	if len(got) != 1 || got[0].DatabaseID != dbID || got[0].BackendID != "native-a" || string(got[0].Sealed) != "sealed-login" ||
		got[0].Recipient != recipient || got[0].Slot != "gregale_live_db" || got[0].Awake != "neon:proj-1/ep-a" {
		t.Fatalf("discovered %+v", got)
	}
	if got := discover("neon-b"); len(got) != 0 {
		t.Fatalf("another backend discovered %+v", got)
	}
	if l, ok, err := store.LoginOf(ctx, account, dbID); err != nil || !ok || l.Identity != "gregale_live_ab12" || l.Slot != "gregale_live_db" || l.Sealed != nil {
		t.Fatalf("login of = %+v, %v, %v", l, ok, err)
	}
	if _, ok, err := store.LoginOf(ctx, other, dbID); err != nil || ok {
		t.Fatalf("another account read the login: %v, %v", ok, err)
	}
	// Rotation keeps the login.
	if err := store.Rotate(ctx, account, dbID, key("bbbbbbbbbbbbbbbb"), 0); err != nil {
		t.Fatal(err)
	}
	if got := discover("native-a"); len(got) != 1 {
		t.Fatalf("after rotation discovered %+v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, dbID); err != nil {
		t.Fatal(err)
	}
	if got := discover("native-a"); len(got) != 0 {
		t.Fatalf("a deleting database was discovered: %+v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='ready' WHERE id=$1`, dbID); err != nil {
		t.Fatal(err)
	}
	refuse := func(context.Context, livekeys.Login) error { return errors.New("provider unavailable") }
	if _, err := store.Disable(ctx, account, dbID, refuse); err == nil {
		t.Fatal("disable succeeded although revoking the login failed")
	}
	if got := discover("native-a"); len(got) != 1 {
		t.Fatalf("a failed revoke removed the database: %+v", got)
	}
	var revoked []livekeys.Login
	if _, err := store.Disable(ctx, account, dbID, func(_ context.Context, l livekeys.Login) error { revoked = append(revoked, l); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(revoked) != 1 || revoked[0].Identity != "gregale_live_ab12" || revoked[0].Slot != "gregale_live_db" {
		t.Fatalf("revoked %+v", revoked)
	}
	if got := discover("native-a"); len(got) != 0 {
		t.Fatalf("a disabled database was discovered: %+v", got)
	}
	if _, ok, err := store.LoginOf(ctx, account, dbID); err != nil || ok {
		t.Fatalf("login after disable: %v, %v", ok, err)
	}
	bad := *login
	bad.Slot = "Bad Slot"
	if err := store.Enable(ctx, account, dbID, key("cccccccccccccccc"), func(context.Context) (*livekeys.Login, error) { return &bad, nil }); err == nil {
		t.Error("a malformed slot name was stored")
	}
	// A login that cannot be issued leaves live queries off.
	if err := store.Enable(ctx, account, dbID, key("dddddddddddddddd"), func(context.Context) (*livekeys.Login, error) {
		return nil, errors.New("provider unavailable")
	}); err == nil {
		t.Fatal("enable succeeded although issuing the login failed")
	}
	if keys, err := store.Keys(ctx, account, dbID); err != nil || len(keys) != 0 {
		t.Fatalf("a failed issue stored keys %v, %v", keys, err)
	}
}

// The operator re-seal pages every key and login and swaps a seal only while
// the row still holds the one it read, so a rotation or a revocation made
// meanwhile wins (ADR-20261016151947, at-rest binding, release 2).
func TestStoreResealSwapsOnlyTheSealItRead(t *testing.T) {
	pool := open(t)
	store := livekeys.New(pool)
	ctx := context.Background()
	login := &livekeys.Login{Sealed: []byte("sealed-login"), Recipient: recipient, Identity: "gregale_live_ab12", Slot: "gregale_live_db"}
	if err := store.Enable(ctx, account, dbID, key("aaaaaaaaaaaaaaaa"), func(context.Context) (*livekeys.Login, error) { return login, nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Rotate(ctx, account, dbID, key("bbbbbbbbbbbbbbbb"), time.Hour); err != nil {
		t.Fatal(err)
	}
	keys, err := store.SealedKeysAfter(ctx, "", 1)
	if err != nil || len(keys) != 1 || keys[0].ID != "aaaaaaaaaaaaaaaa" || keys[0].DatabaseID != dbID || string(keys[0].Blob) != "sealed-aaaaaaaaaaaaaaaa" || keys[0].Recipient != recipient {
		t.Fatalf("first key page = %+v, %v", keys, err)
	}
	next, err := store.SealedKeysAfter(ctx, keys[0].ID, 10)
	if err != nil || len(next) != 1 || next[0].ID != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("second key page = %+v, %v; want the retiring and the active key across pages", next, err)
	}
	if swapped, err := store.ResealKey(ctx, keys[0], []byte("bound-a")); err != nil || !swapped {
		t.Fatalf("re-seal key = %v, %v", swapped, err)
	}
	if swapped, err := store.ResealKey(ctx, keys[0], []byte("bound-again")); err != nil || swapped {
		t.Fatalf("re-seal from a stale read = %v, %v; want it refused", swapped, err)
	}
	ring, err := store.Ring(ctx, dbID)
	if err != nil || len(ring) != 2 || string(ring[0].Sealed) != "bound-a" || string(ring[1].Sealed) != "sealed-bbbbbbbbbbbbbbbb" {
		t.Fatalf("ring after re-seal = %+v, %v", ring, err)
	}

	logins, err := store.SealedLoginsAfter(ctx, "", 10)
	if err != nil || len(logins) != 1 || logins[0].ID != dbID || logins[0].DatabaseID != dbID || string(logins[0].Blob) != "sealed-login" {
		t.Fatalf("login page = %+v, %v", logins, err)
	}
	if rest, err := store.SealedLoginsAfter(ctx, logins[0].ID, 10); err != nil || len(rest) != 0 {
		t.Fatalf("login page after the last = %+v, %v", rest, err)
	}
	if swapped, err := store.ResealLogin(ctx, logins[0], []byte("bound-login")); err != nil || !swapped {
		t.Fatalf("re-seal login = %v, %v", swapped, err)
	}
	if swapped, err := store.ResealLogin(ctx, logins[0], []byte("bound-again")); err != nil || swapped {
		t.Fatalf("re-seal login from a stale read = %v, %v; want it refused", swapped, err)
	}
	dbs, err := store.LiveDatabases(ctx, []string{"native-a"})
	if err != nil || len(dbs) != 1 || string(dbs[0].Sealed) != "bound-login" {
		t.Fatalf("discovered after re-seal = %+v, %v", dbs, err)
	}
}
