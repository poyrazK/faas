//go:build !no_pg

package pgproxy

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/dbqueryerrors"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type errorHistoryBrokerFixture struct {
	scheddpb.ScheddClient
	scheduler *dbsched.Scheduler
	accepted  atomic.Int32
	rawSeen   atomic.Bool
}

func (b *errorHistoryBrokerFixture) ExchangeDatabaseErrorHistory(ctx context.Context, req *scheddpb.ExchangeDatabaseErrorHistoryRequest, _ ...grpc.CallOption) (*scheddpb.ExchangeDatabaseErrorHistoryResponse, error) {
	events := make([]dbqueryerrors.Event, 0, len(req.GetEvents()))
	for _, event := range req.GetEvents() {
		events = append(events, dbqueryerrors.Decode(event))
	}
	wire, _ := json.Marshal(req)
	if strings.Contains(string(wire), "history-secret-marker") {
		b.rawSeen.Store(true)
	}
	policies, err := b.scheduler.ExchangeErrorHistory(ctx, req.GetRuntimeDatabaseIds(), events)
	if err != nil {
		return nil, err
	}
	if len(events) > 0 && b.accepted.Add(1) == 1 {
		return nil, status.Error(codes.Unavailable, "simulated lost acknowledgment")
	}
	out := &scheddpb.ExchangeDatabaseErrorHistoryResponse{}
	for _, policy := range policies {
		out.Policies = append(out.Policies, &scheddpb.DatabaseErrorHistoryPolicy{RuntimeDatabaseId: policy.RuntimeID, Epoch: policy.Epoch})
	}
	return out, nil
}

func TestPooledErrorHistoryRecordsDeadlockAndTimeout(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 4 })
	catalog := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	if _, err := catalog.Exec(ctx, `
		CREATE TABLE managed_postgres_databases (id uuid PRIMARY KEY, state text NOT NULL, data_resource_id text, provider_resource_id text);
		CREATE TABLE managed_postgres_bindings (id uuid PRIMARY KEY, database_id uuid NOT NULL, provider_identity_id text, state text NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/20261125145127097_native_postgres_error_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = catalog.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}

	runtime, untracked := uuid.NewString(), uuid.NewString()
	logical, other, binding := uuid.New(), uuid.New(), uuid.New()
	role, password := h.addRole(t, runtime)
	otherRole, otherPassword := h.addRole(t, untracked)
	if _, err := catalog.Exec(ctx, `INSERT INTO managed_postgres_databases VALUES
		($1, 'ready', $2, NULL), ($3, 'ready', $4, NULL)`, logical, runtime, other, untracked); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Exec(ctx, `INSERT INTO managed_postgres_bindings VALUES ($1,$2,$3,'ready')`, binding, logical, role); err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	database := pgtype.UUID{Bytes: logical, Valid: true}
	if err := q.SetNativePostgresErrorHistory(ctx, catalog, sqlc.SetNativePostgresErrorHistoryParams{
		DatabaseID: database, Enabled: true, Epoch: pgtype.UUID{Bytes: uuid.New(), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	catalogStore, err := dbsched.NewPgStore(catalog)
	if err != nil {
		t.Fatal(err)
	}
	broker := &errorHistoryBrokerFixture{scheduler: dbsched.New(catalogStore, nil, dbsched.Config{}, nil)}
	go h.proxy.RunErrorHistory(ctx, broker)

	open := func(id, user, password string) *pgx.Conn {
		t.Helper()
		conn, err := h.connect(ctx, id+"-pooler."+testSuffix, user, password, h.dbName, "require", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
		return conn
	}
	first, second := open(runtime, role, password), open(runtime, role, password)
	excluded := open(untracked, otherRole, otherPassword)
	waitFor := func(check func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			if check() {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("query error history did not settle")
	}
	waitFor(func() bool {
		h.proxy.errorHistory.mu.RLock()
		defer h.proxy.errorHistory.mu.RUnlock()
		return time.Now().Before(h.proxy.errorHistory.policies[runtime].expiresAt)
	})

	execSQL := func(conn *pgx.Conn, statement string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(first, "BEGIN")
	execSQL(second, "BEGIN")
	execSQL(first, "SELECT pg_advisory_xact_lock($1)", 91841)
	execSQL(second, "SELECT pg_advisory_xact_lock($1)", 91842)
	finished := make(chan error, 2)
	go func() {
		_, err := first.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", 91842)
		finished <- err
	}()
	go func() {
		_, err := second.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", 91841)
		finished <- err
	}()
	a, b := <-finished, <-finished
	if !(sqlState(a) == "40P01" && b == nil || sqlState(b) == "40P01" && a == nil) {
		t.Fatalf("deadlock: %v and %v", a, b)
	}
	execSQL(first, "ROLLBACK")
	execSQL(second, "ROLLBACK")

	execSQL(first, "BEGIN")
	execSQL(first, "SET LOCAL statement_timeout = '30ms'")
	if _, err := first.Exec(ctx, "SELECT pg_sleep(0.3)"); sqlState(err) != "57014" {
		t.Fatalf("statement timeout: %v", err)
	}
	execSQL(first, "ROLLBACK")
	for _, conn := range []*pgx.Conn{first, excluded} {
		if _, err := conn.Exec(ctx, "DO $$BEGIN RAISE EXCEPTION 'history-secret-marker'; END$$"); sqlState(err) != "P0001" {
			t.Fatalf("fixture exception: %v", err)
		}
	}

	var rows []sqlc.ListNativePostgresErrorEventsRow
	waitFor(func() bool {
		var err error
		rows, err = q.ListNativePostgresErrorEvents(ctx, catalog, sqlc.ListNativePostgresErrorEventsParams{
			DatabaseID: database, Since: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}, ResultLimit: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		return len(rows) >= 3 && broker.accepted.Load() >= 2
	})
	if len(rows) != 3 || broker.rawSeen.Load() {
		t.Fatalf("redaction or duplicate delivery failed, rows=%d raw=%t", len(rows), broker.rawSeen.Load())
	}
	codes := map[string]bool{}
	for _, row := range rows {
		if row.BindingID.String() != binding.String() || row.Severity != "ERROR" {
			t.Fatalf("binding or severity was lost: %+v", row)
		}
		codes[row.Sqlstate] = true
	}
	if !codes["40P01"] || !codes["57014"] || !codes["P0001"] {
		t.Fatalf("recorded codes: %v", codes)
	}
	var otherCount int
	if err := catalog.QueryRow(ctx, "SELECT count(*) FROM native_postgres_error_events WHERE database_id=$1", other).Scan(&otherCount); err != nil || otherCount != 0 {
		t.Fatalf("history was recorded for a non-opted-in database: count=%d, err=%v", otherCount, err)
	}
}
