package commitmanaged

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
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
