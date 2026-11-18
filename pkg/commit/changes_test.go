// adr: 20261112140000
package commit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestValidateChangeFeeds(t *testing.T) {
	ok := ChangeFeed{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}}
	for _, tc := range []struct {
		name  string
		feeds []ChangeFeed
		valid bool
	}{
		{"one", []ChangeFeed{ok}, true},
		{"ops", []ChangeFeed{{Name: "o", Table: "o", Columns: []string{"id"}, Operations: []string{"insert", "delete"}}}, true},
		{"none", nil, false},
		{"upper-name", []ChangeFeed{{Name: "Orders", Table: "o", Columns: []string{"id"}}}, false},
		{"long-name", []ChangeFeed{{Name: strings.Repeat("a", 41), Table: "o", Columns: []string{"id"}}}, false},
		{"no-table", []ChangeFeed{{Name: "o", Columns: []string{"id"}}}, false},
		{"control-table", []ChangeFeed{{Name: "o", Table: "o\n", Columns: []string{"id"}}}, false},
		{"no-columns", []ChangeFeed{{Name: "o", Table: "o"}}, false},
		{"dup-column", []ChangeFeed{{Name: "o", Table: "o", Columns: []string{"id", "id"}}}, false},
		{"long-column", []ChangeFeed{{Name: "o", Table: "o", Columns: []string{strings.Repeat("c", 64)}}}, false},
		{"bad-op", []ChangeFeed{{Name: "o", Table: "o", Columns: []string{"id"}, Operations: []string{"truncate"}}}, false},
		{"dup-op", []ChangeFeed{{Name: "o", Table: "o", Columns: []string{"id"}, Operations: []string{"insert", "insert"}}}, false},
		{"dup-feed", []ChangeFeed{ok, ok}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateChangeFeeds(tc.feeds); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	many := make([]ChangeFeed, MaxChangeFeeds+1)
	for i := range many {
		many[i] = ChangeFeed{Name: "f" + strings.Repeat("a", i), Table: "t", Columns: []string{"id"}}
	}
	if ValidateChangeFeeds(many) == nil {
		t.Fatal("more than MaxChangeFeeds feeds accepted")
	}
}

func TestChangesRelayRoleAndURL(t *testing.T) {
	id := uuid.New()
	role, err := ChangesRelayRole(id.String())
	if err != nil || !strings.HasPrefix(role, ChangesRelayRolePrefix) || len(role) > 63 {
		t.Fatalf("role=%q err=%v", role, err)
	}
	if _, err := ChangesRelayRole("not-a-uuid"); err == nil {
		t.Fatal("non-UUID source accepted")
	}
	raw, err := ChangesRelayURL("db.example.com", 0, "shop", role, "p@ss/word")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "sslmode=verify-full") || !strings.Contains(raw, ":5432/shop") {
		t.Fatalf("url %q", raw)
	}
}

// changesFixture is a database with an orders table written by a role that
// holds no privilege on the outbox.
func changesFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	writer := "writer_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	for _, stmt := range []string{
		`CREATE TABLE public.orders (id int PRIMARY KEY, status text NOT NULL, total int, secret text)`,
		`CREATE TABLE public.invoices (id int PRIMARY KEY, amount int)`,
		`CREATE TABLE public.nokey (id int)`,
		`CREATE ROLE ` + writer + ` NOLOGIN`,
		`GRANT INSERT, UPDATE, DELETE, SELECT ON public.orders, public.invoices TO ` + writer,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+writer)
		_, _ = pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+writer)
	})
	return pool, writer
}

func install(t *testing.T, pool *pgxpool.Pool, in ChangesInstall) (ChangesInstallResult, error) {
	t.Helper()
	ctx := t.Context()
	if in.RelayPassword == "" {
		pw, err := NewRelayPassword()
		if err != nil {
			t.Fatal(err)
		}
		in.RelayPassword = pw
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := InstallChanges(ctx, tx, in)
	if err != nil {
		return res, err
	}
	role := res.RelayRole
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+role)
		_, _ = pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+role)
	})
	return res, tx.Commit(ctx)
}

// asRole runs stmt as role inside a transaction that is rolled back on error.
func asRole(ctx context.Context, pool *pgxpool.Pool, role string, stmts ...string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
		return err
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type outboxRow struct {
	Type    string
	Payload map[string]any
}

func outboxRows(t *testing.T, pool *pgxpool.Pool) []outboxRow {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT event_type, payload FROM public.gregale_outbox ORDER BY created_at, event_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		var raw []byte
		if err := rows.Scan(&r.Type, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPostgresChangesCaptureFromUngrantedWriter(t *testing.T) {
	pool, writer := changesFixture(t)
	ctx := t.Context()
	source := uuid.NewString()
	res, err := install(t, pool, ChangesInstall{SourceID: source, Feeds: []ChangeFeed{
		{Name: "orders", Table: "public.orders", Columns: []string{"id", "status", "total"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The writer has no outbox grant; capture must not refuse its writes.
	if err := asRole(ctx, pool, writer,
		`INSERT INTO public.orders VALUES (1, 'new', 10, 's1')`,
		`UPDATE public.orders SET status = 'paid' WHERE id = 1`,
		`UPDATE public.orders SET secret = 's2' WHERE id = 1`, // not allowlisted: no event
		`DELETE FROM public.orders WHERE id = 1`,
	); err != nil {
		t.Fatal(err)
	}
	got := outboxRows(t, pool)
	wantTypes := []string{"database.orders.insert", "database.orders.update", "database.orders.delete"}
	if len(got) != len(wantTypes) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(wantTypes), got)
	}
	for i, w := range wantTypes {
		if got[i].Type != w {
			t.Fatalf("event %d type %q, want %q", i, got[i].Type, w)
		}
		b, _ := json.Marshal(got[i].Payload)
		if strings.Contains(string(b), "secret") || strings.Contains(string(b), "s1") {
			t.Fatalf("event %d leaks a column that is not allowlisted: %s", i, b)
		}
		if got[i].Payload["table"] != "public.orders" || got[i].Payload["feed"] != "orders" {
			t.Fatalf("event %d identity: %s", i, b)
		}
		if key, _ := got[i].Payload["key"].(map[string]any); key["id"] != float64(1) {
			t.Fatalf("event %d key: %s", i, b)
		}
	}
	if got[0].Payload["before"] != nil || got[2].Payload["after"] != nil {
		t.Fatalf("insert before / delete after must be null: %+v", got)
	}
	upd := got[1].Payload
	if upd["before"].(map[string]any)["status"] != "new" || upd["after"].(map[string]any)["status"] != "paid" {
		t.Fatalf("update images: %+v", upd)
	}
	// Writers cannot forge change events.
	if err := asRole(ctx, pool, writer, `INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES (gen_random_uuid(),'database.orders.insert','{}')`); err == nil {
		t.Fatal("a writer without a grant inserted into the outbox")
	}
	// The relay role reads, deletes and writes delivery columns only.
	if err := asRole(ctx, pool, res.RelayRole,
		`SELECT event_id FROM public.gregale_outbox FOR UPDATE SKIP LOCKED`,
		`UPDATE public.gregale_outbox SET lease_token = gen_random_uuid(), attempts = attempts + 1`,
		`SELECT source_id FROM public.gregale_commit_binding`,
	); err != nil {
		t.Fatalf("relay role lacks delivery access: %v", err)
	}
	for _, forbidden := range []string{
		`UPDATE public.gregale_outbox SET payload = '{}'`,
		`UPDATE public.gregale_outbox SET event_type = 'x'`,
		`INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES (gen_random_uuid(),'x','{}')`,
		`UPDATE public.gregale_commit_binding SET source_id = gen_random_uuid()`,
		`SELECT * FROM public.orders`,
	} {
		if err := asRole(ctx, pool, res.RelayRole, forbidden); err == nil {
			t.Fatalf("relay role allowed %q", forbidden)
		}
	}
	// The verifier, never the plaintext, is stored.
	var stored string
	if err := pool.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname = $1`, res.RelayRole).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "SCRAM-SHA-256$4096:") {
		t.Fatalf("relay password stored as %q", stored[:min(len(stored), 20)])
	}
}

func TestPostgresChangesNarrowOperationsAndDroppedColumn(t *testing.T) {
	pool, writer := changesFixture(t)
	ctx := t.Context()
	if _, err := install(t, pool, ChangesInstall{SourceID: uuid.NewString(), Feeds: []ChangeFeed{
		{Name: "orders", Table: "orders", Columns: []string{"id", "status", "total"}, Operations: []string{"update"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := asRole(ctx, pool, writer, `INSERT INTO public.orders VALUES (1, 'new', 10, NULL)`); err != nil {
		t.Fatal(err)
	}
	if n := len(outboxRows(t, pool)); n != 0 {
		t.Fatalf("insert captured by an update-only feed: %d events", n)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE public.orders DROP COLUMN total`); err != nil {
		t.Fatal(err)
	}
	// A dropped allowlisted column is reported, and the write still succeeds.
	if err := asRole(ctx, pool, writer, `UPDATE public.orders SET status = 'paid' WHERE id = 1`); err != nil {
		t.Fatalf("capture refused a write after a column drop: %v", err)
	}
	got := outboxRows(t, pool)
	if len(got) != 1 {
		t.Fatalf("want one update event, got %+v", got)
	}
	missing, _ := got[0].Payload["missing_columns"].([]any)
	if len(missing) != 1 || missing[0] != "total" {
		t.Fatalf("missing_columns: %+v", got[0].Payload)
	}
}

func TestPostgresChangesReconcileAndRefusals(t *testing.T) {
	pool, _ := changesFixture(t)
	ctx := t.Context()
	source := uuid.NewString()
	both := []ChangeFeed{
		{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}},
		{Name: "invoices", Table: "public.invoices", Columns: []string{"id", "amount"}},
	}
	if _, err := install(t, pool, ChangesInstall{SourceID: source, Feeds: both}); err != nil {
		t.Fatal(err)
	}
	// Re-installing with one feed removes the other and rotates the relay.
	res, err := install(t, pool, ChangesInstall{SourceID: source, Feeds: both[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || !strings.HasSuffix(res.Removed[0], "gregale_changes_invoices") {
		t.Fatalf("removed %v", res.Removed)
	}
	var triggers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgname LIKE 'gregale\_changes\_%' AND NOT tgisinternal`).Scan(&triggers); err != nil || triggers != 1 {
		t.Fatalf("triggers=%d err=%v", triggers, err)
	}
	// A feed moved to another table leaves nothing behind on the old one.
	moved := []ChangeFeed{{Name: "orders", Table: "public.invoices", Columns: []string{"id", "amount"}}}
	if res, err = install(t, pool, ChangesInstall{SourceID: source, Feeds: moved}); err != nil || len(res.Removed) != 1 {
		t.Fatalf("move: removed=%v err=%v", res.Removed, err)
	}
	// One database feeds exactly one source.
	if _, err := install(t, pool, ChangesInstall{SourceID: uuid.NewString(), Feeds: moved}); !errors.Is(err, ErrDatabaseBound) {
		t.Fatalf("second source: %v", err)
	}
	for name, feeds := range map[string][]ChangeFeed{
		"no primary key":   {{Name: "n", Table: "public.nokey", Columns: []string{"id"}}},
		"key not allowed":  {{Name: "o", Table: "public.orders", Columns: []string{"status"}}},
		"unknown column":   {{Name: "o", Table: "public.orders", Columns: []string{"id", "nope"}}},
		"unknown table":    {{Name: "o", Table: "public.nope", Columns: []string{"id"}}},
		"outbox as source": {{Name: "o", Table: "public.gregale_outbox", Columns: []string{"event_id"}}},
	} {
		if _, err := install(t, pool, ChangesInstall{SourceID: source, Feeds: feeds}); err == nil {
			t.Fatalf("%s: install accepted", name)
		}
	}
}

func TestPostgresChangesSchemaOwnerAndUnsafeRole(t *testing.T) {
	pool, _ := changesFixture(t)
	ctx := t.Context()
	owner := "owner_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	for _, stmt := range []string{
		`CREATE ROLE ` + owner + ` NOLOGIN`,
		`ALTER TABLE public.orders OWNER TO ` + owner,
		`GRANT CREATE ON SCHEMA public TO ` + owner,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+owner)
		_, _ = pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+owner)
	})
	if _, err := install(t, pool, ChangesInstall{SourceID: uuid.NewString(), SchemaOwner: owner,
		Feeds: []ChangeFeed{{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}}}}); err != nil {
		t.Fatal(err)
	}
	var fnOwner, outboxOwner string
	if err := pool.QueryRow(ctx, `SELECT pg_get_userbyid(proowner) FROM pg_proc WHERE proname = 'gregale_changes_capture'`).Scan(&fnOwner); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname='public' AND tablename='gregale_outbox'`).Scan(&outboxOwner); err != nil {
		t.Fatal(err)
	}
	if fnOwner != owner || outboxOwner != owner {
		t.Fatalf("capture function owned by %q and outbox by %q, want %q", fnOwner, outboxOwner, owner)
	}

	other := uuid.NewString()
	role, _ := ChangesRelayRole(other)
	if _, err := pool.Exec(ctx, `CREATE ROLE `+role+` LOGIN CREATEDB`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+role) })
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	pw, _ := NewRelayPassword()
	if _, err := InstallChanges(ctx, tx, ChangesInstall{SourceID: other, RelayPassword: pw,
		Feeds: []ChangeFeed{{Name: "orders", Table: "public.orders", Columns: []string{"id"}}}}); !errors.Is(err, ErrRelayRoleUnsafe) {
		t.Fatalf("elevated relay role: %v", err)
	}
}

func TestPostgresChangesUnqualifiedTableAndRemove(t *testing.T) {
	pool, writer := changesFixture(t)
	ctx := t.Context()
	source := uuid.NewString()
	// The managed owner session pins search_path to pg_catalog: an
	// unqualified table must still resolve in public.
	if _, err := pool.Exec(ctx, `ALTER DATABASE `+pgx.Identifier{pgtestDatabase(t, pool)}.Sanitize()+` SET search_path = pg_catalog`); err != nil {
		t.Fatal(err)
	}
	pool.Reset()
	res, err := install(t, pool, ChangesInstall{SourceID: source, Feeds: []ChangeFeed{
		{Name: "orders", Table: "orders", Columns: []string{"id", "status"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveChanges(ctx, tx, source, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed %v", removed)
	}
	var roleLeft bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)`, res.RelayRole).Scan(&roleLeft); err != nil || roleLeft {
		t.Fatalf("relay role left behind (%v)", err)
	}
	if err := asRole(ctx, pool, writer, `INSERT INTO public.orders VALUES (9, 'new', 1, NULL)`); err != nil {
		t.Fatal(err)
	}
	if n := len(outboxRows(t, pool)); n != 0 {
		t.Fatalf("capture continued after removal: %d events", n)
	}
	// Removing again is a no-op.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if again, err := RemoveChanges(ctx, tx, source, ""); err != nil || len(again) != 0 {
		t.Fatalf("second removal: %v %v", again, err)
	}
}

func pgtestDatabase(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var name string
	if err := pool.QueryRow(t.Context(), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

// The verifier must match what PostgreSQL computes for the same password and
// salt, or the relay login would never authenticate.
func TestPostgresChangesSCRAMVerifierMatchesServer(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	role := "scram_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	password := "correct-horse-battery-staple-0123456789"
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL password_encryption = 'scram-sha-256'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatal(err)
	}
	var server string
	if err := tx.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname = $1`, role).Scan(&server); err != nil {
		t.Fatal(err)
	}
	// SCRAM-SHA-256$<iter>:<salt>$<stored>:<server>
	parts := strings.SplitN(strings.TrimPrefix(server, "SCRAM-SHA-256$"), "$", 2)
	iterSalt := strings.SplitN(parts[0], ":", 2)
	if iterSalt[0] != "4096" {
		t.Skipf("server uses %s iterations", iterSalt[0])
	}
	salt, err := base64.StdEncoding.DecodeString(iterSalt[1])
	if err != nil {
		t.Fatal(err)
	}
	mine, err := scramVerifierWithSalt(password, salt)
	if err != nil {
		t.Fatal(err)
	}
	if mine != server {
		t.Fatalf("verifier mismatch:\n mine   %s\n server %s", mine, server)
	}
}

// Captured rows flow through the unchanged Commit relay.
func TestPostgresChangesRelayTick(t *testing.T) {
	pool, writer := changesFixture(t)
	ctx := t.Context()
	source := uuid.NewString()
	if _, err := install(t, pool, ChangesInstall{SourceID: source,
		Feeds: []ChangeFeed{{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := asRole(ctx, pool, writer, `INSERT INTO public.orders VALUES (7, 'new', 1, NULL)`); err != nil {
		t.Fatal(err)
	}
	acc := &recordingAcceptor{}
	relay := Relay{ContractVersion: 1, SourceID: source, Pool: pool, Acceptor: acc, Lease: time.Second, RetryDelay: time.Second}
	if err := QualifySchemaVersion(ctx, pool, 1); err != nil {
		t.Fatal(err)
	}
	n, err := relay.Tick(ctx)
	if err != nil || n != 1 || len(acc.events) != 1 {
		t.Fatalf("accepted=%d err=%v events=%+v", n, err, acc.events)
	}
	e := acc.events[0]
	if e.Type != "database.orders.insert" || e.Validate() != nil {
		t.Fatalf("event %+v", e)
	}
}

type recordingAcceptor struct{ events []Event }

func (a *recordingAcceptor) Accept(_ context.Context, e Event) (Receipt, error) {
	a.events = append(a.events, e)
	return Receipt{ID: uuid.NewString(), InvocationID: uuid.NewString()}, nil
}
