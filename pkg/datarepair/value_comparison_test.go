package datarepair

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func timezoneEngine(t *testing.T, e *Engine, zone string) *Engine {
	t.Helper()
	cfg := e.Pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["TimeZone"] = zone
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &Engine{Pool: pool, Rehearser: e.Rehearser, CheckSQL: e.CheckSQL, Checks: e.Checks}
}

func timestampCapture(t *testing.T, e *Engine, scope Scope) string {
	t.Helper()
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE temporal_products(id bigint PRIMARY KEY,stamp timestamptz,stamps timestamptz[],optional_stamp timestamptz,payload jsonb);
SELECT gregale_repair.enroll('temporal_products',ARRAY['stamp','optional_stamp','payload']);
INSERT INTO temporal_products(id,stamp,stamps,optional_stamp,payload) VALUES(1,'2026-11-03T10:00:00Z',ARRAY['2026-11-03T10:00:00Z'::timestamptz],NULL,'{"stamp":"original"}')`); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL TIME ZONE 'Europe/Istanbul'`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := Capture(ctx, tx, scope, id, "timezone-writer", "temporal_products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE temporal_products SET stamp='2026-11-03T11:00:00Z',optional_stamp='2026-11-03T11:00:00Z',payload='{"stamp":"changed"}'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSelectiveRepairTimestampTimezonesAndOwnerUpgrade(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	e = timezoneEngine(t, e, "UTC")
	source := timestampCapture(t, e, scope)
	// A new plan must admit the unchanged timestamp captured in another zone.
	p, err := e.Plan(ctx, scope, source)
	if err != nil || p.Candidates != 1 || p.Conflicts != 0 || !strings.Contains(string(p.Items[0].After), "+03:00") {
		t.Fatalf("cross-timezone capture was excluded or rewritten: %+v %v", p, err)
	}
	// Recreate a saved plan prepared under its writer's timezone. The upgrade
	// must preserve its original bytes, journal and approval, then allow other
	// executor sessions to validate, replace and apply it.
	writerZone := timezoneEngine(t, e, "Europe/Istanbul")
	p = approved(t, writerZone, scope, source)
	before := validationState(t, e, p.ID)
	if _, err := e.Pool.Exec(ctx, `DROP FUNCTION gregale_repair.normalize_values(regclass,jsonb)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("owner upgrade rewrote retained evidence or approval")
	}
	for _, zone := range []string{"UTC", "America/New_York", "Asia/Kathmandu"} {
		observer := timezoneEngine(t, e, zone)
		if r, err := observer.Validate(ctx, scope, p.ID, p.Digest); err != nil || !r.Matches || r.Unchanged != 1 {
			t.Fatalf("%s validation: %+v %v", zone, r, err)
		}
	}
	child, err := e.Replace(ctx, scope, p.ID, p.Digest, uuid.NewString())
	if err != nil || child.Candidates != 1 || child.Conflicts != 0 {
		t.Fatalf("replacement excluded unchanged timestamps: %+v %v", child, err)
	}
	child, err = e.Rehearse(ctx, scope, child.ID, child.Digest)
	if err != nil {
		t.Fatal(err)
	}
	child, err = e.Approve(ctx, scope, child.ID, child.Digest, "timezone-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	executor := timezoneEngine(t, e, "Asia/Kathmandu")
	if _, err := executor.Apply(ctx, scope, child.ID, child.Digest, uuid.NewString(), "timezone-repair"); err != nil {
		t.Fatal(err)
	}
	var restored bool
	if err := e.Pool.QueryRow(ctx, `SELECT stamp='2026-11-03T10:00:00Z'::timestamptz AND stamps=ARRAY['2026-11-03T10:00:00Z'::timestamptz] AND optional_stamp IS NULL AND payload='{"stamp":"original"}'::jsonb AND gregale_row_version=3 FROM temporal_products`).Scan(&restored); err != nil || !restored {
		t.Fatalf("inverse did not restore the original typed values: %v %v", restored, err)
	}
	if r, err := e.Verify(ctx, scope, child.ID, child.Digest); err != nil || !r.Available || r.Unchanged != 1 || r.Edited != 0 {
		t.Fatalf("verification reported timezone drift: %+v %v", r, err)
	}
}

func TestSelectiveRepairTimestampValueDriftStillConflicts(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	e = timezoneEngine(t, e, "UTC")
	source := timestampCapture(t, e, scope)
	p := approved(t, e, scope, source)
	// Trusted-owner bypass isolates the value guard from the separate version
	// guard. Timezone normalization must not erase a change to the actual instant.
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE temporal_products DISABLE TRIGGER gregale_repair_guard;
UPDATE temporal_products SET stamp=stamp+interval '1 hour';
ALTER TABLE temporal_products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Validate(ctx, scope, p.ID, p.Digest); err != nil || r.Matches || len(r.Rows) != 1 || r.Rows[0].Reason != ReasonValuesChanged {
		t.Fatalf("actual timestamp drift was hidden: %+v %v", r, err)
	}
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "must-conflict"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed timestamp could be overwritten: %v", err)
	}
}

func TestSelectiveRepairTimestampIsolatedRehearsalWithFailover(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, _ := fixture(t, 1)
	e = timezoneEngine(t, e, "UTC")
	e.CheckSQL = "SELECT 1=1"
	source := timestampCapture(t, e, scope)
	cfg := e.Pool.Config().ConnConfig
	connection := url.URL{Scheme: "postgresql", User: url.UserPassword(cfg.User, cfg.Password), Host: "localhost", Path: "/" + cfg.Database}
	connection.RawQuery = url.Values{
		"host": {"127.0.0.1," + cfg.Host}, "port": {"1," + strconv.Itoa(int(cfg.Port))},
		"sslmode": {"disable"}, "connect_timeout": {"2"}, "target_session_attrs": {"read-write"},
	}.Encode()
	e.Rehearser = DockerRehearser{SourceURL: connection.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
	p := approved(t, e, scope, source)
	if p.Candidates != 1 || p.Rehearsal == nil || !p.Rehearsal.Passed {
		t.Fatalf("failover rehearsal excluded unchanged timestamp values: %+v", p)
	}
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "isolated-timezone-repair"); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("isolated repair did not verify: %+v %v", r, err)
	}
}
