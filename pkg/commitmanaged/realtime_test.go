package commitmanaged

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type realtimeDispatchFixture struct {
	Store
	publish func(context.Context, state.ManagedRealtimeCommit) (state.ManagedRealtimeChannelMessage, error)
}

func (f *realtimeDispatchFixture) PublishManagedRealtimeCommit(ctx context.Context, e state.ManagedRealtimeCommit) (state.ManagedRealtimeChannelMessage, error) {
	return f.publish(ctx, e)
}

type realtimeQualificationDisconnect string

func (prefix realtimeQualificationDisconnect) TraceQueryStart(ctx context.Context, conn *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	if len(query.Args) == 1 && query.Args[0] == "public.gregale_realtime_outbox" && strings.HasPrefix(query.SQL, string(prefix)) {
		_ = conn.PgConn().Conn().Close()
	}
	return ctx
}

func (realtimeQualificationDisconnect) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestCommitRelayRetriesRealtimeQualificationTransportFailure(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, test := range []struct {
		name        string
		queryPrefix string
	}{
		{"outbox discovery", "SELECT to_regclass"},
		{"schema columns", "SELECT a.attname"},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newChangesEnv(t, true)
			ctx := t.Context()
			if _, err := e.customer.Exec(ctx, commit.RealtimeSchema+`INSERT INTO public.orders VALUES (1, 'new', NULL);`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.control.Exec(ctx, `UPDATE commit_sources SET relay_poll_backoff_ms=$2 WHERE id=$1::uuid`,
				e.source.ID, relayMaxBackoff.Milliseconds()); err != nil {
				t.Fatal(err)
			}
			e.manager.RealtimeEnabled = true
			e.manager.Open = func(ctx context.Context, _ string, _ commit.NetworkPolicy) (*pgxpool.Pool, error) {
				cfg := e.customer.Config().Copy()
				cfg.ConnConfig.Tracer = realtimeQualificationDisconnect(test.queryPrefix)
				return pgxpool.NewWithConfig(ctx, cfg)
			}
			var accepted int
			e.manager.Report = func(_, _ string, count int) { accepted = count }
			if err := e.manager.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			var status string
			var backoffMS int64
			if err := e.control.QueryRow(ctx, `SELECT relay_status,relay_poll_backoff_ms FROM commit_sources WHERE id=$1::uuid`,
				e.source.ID).Scan(&status, &backoffMS); err != nil {
				t.Fatal(err)
			}
			backoff := time.Duration(backoffMS) * time.Millisecond
			if status != "database_unavailable" || backoff < relayTransportCeiling-relayTransportSpread || backoff > relayTransportCeiling {
				t.Fatalf("status=%s backoff=%s", status, backoff)
			}
			if pending := e.pending(t); accepted != 1 || pending != 0 {
				t.Fatalf("accepted=%d pending=%d", accepted, pending)
			}
		})
	}
}

func TestRealtimeDispatcherSeesOnlyCommittedEventsAndFencesCheckpoint(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, commit.Schema+commit.RealtimeSchema); err != nil {
		t.Fatal(err)
	}
	src := state.CommitRelaySource{CommitSource: state.CommitSource{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString()}}
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_commit_binding(source_id) VALUES($1::uuid)`, src.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	event := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO gregale_realtime_outbox(event_id,endpoint_id,channel,data) VALUES($1::uuid,$2,'orders',$3)`, event, "0123456789abcdef0123456789abcdef", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	published := time.Now().UTC()
	fixture := &realtimeDispatchFixture{publish: func(ctx context.Context, e state.ManagedRealtimeCommit) (state.ManagedRealtimeChannelMessage, error) {
		calls++
		if e.SourceID != src.ID || e.EventID != event {
			t.Fatalf("wrong source event: %+v", e)
		}
		if calls == 1 {
			// Another owner takes over after the platform committed the message.
			if _, err := pool.Exec(ctx, `UPDATE gregale_realtime_outbox SET lease_token=$2::uuid,lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1::uuid`, event, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
		}
		return state.ManagedRealtimeChannelMessage{Sequence: 7, CreatedAt: published}, nil
	}}
	manager := &Manager{Store: fixture}
	n, backlog, err := manager.tickRealtime(ctx, pool, src)
	if err != nil || n != 0 || calls != 0 || backlog.Pending != 0 {
		t.Fatalf("uncommitted visibility: %d %+v %v", n, backlog, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	n, backlog, err = manager.tickRealtime(ctx, pool, src)
	if err != nil || n != 0 || backlog.Pending != 1 {
		t.Fatalf("stale checkpoint: %d %+v %v", n, backlog, err)
	}
	n, backlog, err = manager.tickRealtime(ctx, pool, src)
	if err != nil || n != 1 || backlog.Pending != 0 || calls != 2 {
		t.Fatalf("checkpoint recovery: %d %+v %v", n, backlog, err)
	}
	var sequence int64
	if err = pool.QueryRow(ctx, `SELECT sequence FROM gregale_realtime_outbox WHERE event_id=$1::uuid`, event).Scan(&sequence); err != nil || sequence != 7 {
		t.Fatalf("publication receipt: %d %v", sequence, err)
	}
}
