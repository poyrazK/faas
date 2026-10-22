package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRealtimeCommitReceiptSurvivesExpiryAndEndpointDeletion(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-realtime-commit")
	src, err := s.CreateCommitSource(ctx, state.CommitSource{AccountID: account, AppID: app, Name: "realtime-commit"})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(account, app), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	input := state.ManagedRealtimeCommit{AccountID: account, SourceID: src.ID, EventID: uuid.NewString(), EndpointID: ep.ID, Channel: "orders", Data: []byte(`{"status":"shipped"}`)}
	var wg sync.WaitGroup
	results := make(chan state.ManagedRealtimeChannelMessage, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			message, err := s.PublishManagedRealtimeCommit(ctx, input)
			results <- message
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	for message := range results {
		if message.Sequence != 1 {
			t.Fatalf("duplicate sequence: %d", message.Sequence)
		}
	}
	// Customer checkpoint loss must not reopen identity when the channel's
	// bounded history is expired or its endpoint is deleted.
	if _, err = pool.Exec(ctx, `DELETE FROM managed_realtime_channel_messages WHERE endpoint_id=$1`, ep.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.PublishManagedRealtimeCommit(ctx, input)
	if err != nil || replay.Sequence != 1 {
		t.Fatalf("expired replay: %+v %v", replay, err)
	}
	changed := input
	changed.Data = []byte(`{"status":"cancelled"}`)
	if _, err = s.PublishManagedRealtimeCommit(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("identity conflict: %v", err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM managed_realtime_endpoints WHERE id=$1`, ep.ID); err != nil {
		t.Fatal(err)
	}
	replay, err = s.PublishManagedRealtimeCommit(ctx, input)
	if err != nil || replay.Sequence != 1 {
		t.Fatalf("deleted endpoint replay: %+v %v", replay, err)
	}
}

func TestPgRealtimeCommitRejectsOtherAppAndPausedSource(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, _ := seedLiveDeploy(t, s, ctx, "-realtime-commit-owner")
	src, err := s.CreateCommitSource(ctx, state.CommitSource{AccountID: account, AppID: app, Name: "realtime-commit"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account, Slug: "realtime-other-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(account, other.ID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	input := state.ManagedRealtimeCommit{AccountID: account, SourceID: src.ID, EventID: uuid.NewString(), EndpointID: ep.ID, Channel: "orders", Data: []byte(`{}`)}
	if _, err = s.PublishManagedRealtimeCommit(ctx, input); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-app publish: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE commit_sources SET enabled=false WHERE id=$1::uuid`, src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishManagedRealtimeCommit(ctx, input); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("paused publish: %v", err)
	}
	var receipts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM managed_realtime_commit_receipts WHERE source_id=$1::uuid`, src.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("rejected receipts: %d %v", receipts, err)
	}
}
