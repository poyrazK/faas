package ci_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestControlPlaneStoreMemory(t *testing.T) {
	store, account := ci.NewMemoryStore(), uuid.NewString()
	exerciseStore(t, store, account, uuid.NewString())
	store.RemoveAccount(account)
	rows, err := store.ListRuns(context.Background(), account)
	if err != nil || len(rows) != 0 {
		t.Fatalf("account deletion leaked CI metadata: %v", err)
	}
}

func TestControlPlaneStorePostgres(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "CREATE TABLE accounts (id uuid PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../migrations/20261016175839285_internal_ci.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(data), "-- +goose Down")[0]
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	githubMigration, err := os.ReadFile("../../migrations/20261016175839290_ci_github_sources.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, strings.Split(string(githubMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	account, other := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, "INSERT INTO accounts(id) VALUES ($1),($2)", account, other); err != nil {
		t.Fatal(err)
	}
	exerciseStore(t, ci.NewPostgresStore(pool), account, other)
	// Restarting the adapter preserves the ledger; SQL cannot represent fake success.
	restarted := ci.NewPostgresStore(pool)
	runs, err := restarted.ListRuns(ctx, account)
	if err != nil || len(runs) != api.CIMaxRunsPerAccount {
		t.Fatalf("restart lost runs: %d %v", len(runs), err)
	}
	if _, err := pool.Exec(ctx, "UPDATE ci_runs SET status='succeeded' WHERE account_id=$1", account); err == nil {
		t.Fatal("schema allowed unqualified execution success")
	}
	if _, err := pool.Exec(ctx, "DELETE FROM accounts WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	runs, err = restarted.ListRuns(ctx, account)
	if err != nil || len(runs) != 0 {
		t.Fatalf("account deletion leaked CI metadata: %v", err)
	}
}

func exerciseStore(t *testing.T, s ci.Store, account, other string) {
	t.Helper()
	ctx := context.Background()
	revision, err := s.PutRevision(ctx, account, "checks", definition())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.PutRevision(ctx, account, "checks", definition())
	if err != nil || revision.ID != replay.ID {
		t.Fatalf("revision was not deduplicated: %v", err)
	}
	changed := definition()
	j := changed.Jobs["test"]
	j.Steps[0].Run = "npm run check"
	changed.Jobs["test"] = j
	newRevision, err := s.PutRevision(ctx, account, "checks", changed)
	if err != nil || newRevision.ID == revision.ID {
		t.Fatalf("revision was mutated: %v", err)
	}
	req := ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			run, _, err := s.Submit(ctx, account, "same-request", req)
			if err != nil {
				errs <- err
				return
			}
			ids <- run.ID
		})
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	id := ""
	for next := range ids {
		if id != "" && id != next {
			t.Fatal("concurrent retries created duplicate runs")
		}
		id = next
	}
	run, err := s.Run(ctx, account, id)
	if err != nil || run.Status != ci.StatusBlocked || run.BlockedReason != ci.ComputeUnavailable || run.Plan.DefinitionDigest != revision.Digest || run.FinishedAt != nil {
		t.Fatalf("invalid blocked receipt: %+v %v", run, err)
	}
	run.Plan.Jobs[0].Steps[0].Run = "mutated by reader"
	again, err := s.Run(ctx, account, id)
	if err != nil || again.Plan.Jobs[0].Steps[0].Run == "mutated by reader" {
		t.Fatal("run snapshot aliases caller memory")
	}
	conflict := req
	conflict.CommitSHA = strings.Repeat("b", 40)
	if _, _, err := s.Submit(ctx, account, "same-request", conflict); !errors.Is(err, ci.ErrConflict) {
		t.Fatalf("identity conflict = %v", err)
	}
	if _, err := s.Run(ctx, other, id); !errors.Is(err, ci.ErrNotFound) {
		t.Fatalf("cross-account run leaked: %v", err)
	}
	if _, err := s.Revision(ctx, other, revision.ID); !errors.Is(err, ci.ErrNotFound) {
		t.Fatalf("cross-account revision leaked: %v", err)
	}
	if _, _, err := s.Submit(ctx, other, "foreign-revision", req); !errors.Is(err, ci.ErrNotFound) {
		t.Fatalf("cross-account admission accepted: %v", err)
	}
	if _, err := s.Cancel(ctx, other, id); !errors.Is(err, ci.ErrNotFound) {
		t.Fatalf("cross-account cancellation accepted: %v", err)
	}
	if _, err := s.Events(ctx, other, id); !errors.Is(err, ci.ErrNotFound) {
		t.Fatalf("cross-account events leaked: %v", err)
	}
	for range 16 {
		wg.Go(func() {
			if _, err := s.Cancel(ctx, account, id); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	events, err := s.Events(ctx, account, id)
	if err != nil || len(events) != 2 || events[0].Type != ci.StatusBlocked || events[1].Type != ci.StatusCancelled {
		t.Fatalf("non-atomic or duplicated history: %+v %v", events, err)
	}
	recovered, replayed, err := s.Submit(ctx, account, "same-request", req)
	if err != nil || !replayed || recovered.Status != ci.StatusCancelled {
		t.Fatalf("retry resurrected cancellation: %+v %v", recovered, err)
	}
	for i := 1; i < api.CIMaxRunsPerAccount; i++ {
		if _, _, err := s.Submit(ctx, account, fmt.Sprintf("run-%d", i), req); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Submit(ctx, account, "overflow", req); !errors.Is(err, ci.ErrLimit) {
		t.Fatalf("run quota not enforced: %v", err)
	}
	if _, replayed, err := s.Submit(ctx, account, "same-request", req); err != nil || !replayed {
		t.Fatalf("quota prevented recovery: %v", err)
	}
	for i := 2; i < api.CIMaxRevisionsPerAccount; i++ {
		next := definition()
		job := next.Jobs["test"]
		job.Steps[0].Run = fmt.Sprintf("echo revision-%d", i)
		next.Jobs["test"] = job
		if _, err := s.PutRevision(ctx, account, "checks", next); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PutRevision(ctx, account, "overflow", definition()); !errors.Is(err, ci.ErrLimit) {
		t.Fatalf("revision quota not enforced: %v", err)
	}
	if row, err := s.PutRevision(ctx, account, "checks", definition()); err != nil || row.ID != revision.ID {
		t.Fatalf("quota prevented revision recovery: %v", err)
	}
}
