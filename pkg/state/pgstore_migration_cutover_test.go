//go:build !no_pg

package state_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgMigrationCutoverRestartLeaseAndCancellationFence(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, dep := seedLiveDeploy(t, s, ctx, "cutover")
	now := time.Now().UTC()
	in := api.CreateMigrationCutoverRequest{ID: uuid.NewString(), VerificationID: uuid.NewString(), Hostname: "api.example.test", PreviousRecords: []api.MigrationDNSRecord{{Type: "CNAME", Value: "old.example.test", TTL: 300}}, PreviousProxyEnabled: true, Max5xxRateBPS: 100, MaxP95MS: 1000}
	r := state.MigrationCutoverRecord{AccountID: account, AppID: app, Report: api.MigrationCutover{ID: in.ID, App: "pg-app", DeploymentID: dep, Status: "prepared", Request: in, CreatedAt: now, DNSRecords: []api.DNSRecordInstruction{}, Checks: []api.MigrationVerificationCheck{}}}
	r, err := s.CreateMigrationCutover(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListDueMigrationCutovers(ctx, now, 16); err != nil || len(rows) != 0 {
		t.Fatalf("prepare queued work: %+v %v", rows, err)
	}
	if _, err := s.GetMigrationCutover(ctx, uuid.NewString(), app, in.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account isolation: %v", err)
	}
	if _, err := s.GetMigrationCutover(ctx, account, uuid.NewString(), in.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("app isolation: %v", err)
	}
	altered := r
	altered.Report.Request.Hostname = "other.example.test"
	if _, err := s.CreateMigrationCutover(ctx, altered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	r.Report.Status = "checking"
	r.NextAttemptAt = now
	r, err = s.SaveMigrationCutover(ctx, r, r.Version)
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	rows, err := restarted.ListDueMigrationCutovers(ctx, now.Add(time.Second), 16)
	if err != nil || len(rows) != 1 || rows[0].Report.Request.PreviousRecords[0].Value != "old.example.test" || !rows[0].Report.Request.PreviousProxyEnabled {
		t.Fatalf("restart lost work/recovery: %+v %v", rows, err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			claimed := r
			claimed.LeaseUntil = now.Add(time.Minute)
			if _, err := restarted.SaveMigrationCutover(ctx, claimed, r.Version); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("parallel claims=%d", successes.Load())
	}
	rows, err = restarted.ListDueMigrationCutovers(ctx, now.Add(time.Second), 16)
	if err != nil || len(rows) != 0 {
		t.Fatalf("lease ignored: %+v %v", rows, err)
	}
	claimed, err := restarted.GetMigrationCutover(ctx, account, app, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := claimed
	cancelled.Report.Status = "cancelled"
	cancelled.LeaseUntil = time.Time{}
	if _, err := restarted.SaveMigrationCutover(ctx, cancelled, claimed.Version); err != nil {
		t.Fatal(err)
	}
	claimed.Report.Status = "ready"
	if _, err := s.SaveMigrationCutover(ctx, claimed, claimed.Version); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("late proof replaced cancellation: %v", err)
	}
	loaded, err := state.NewPgStore(pool).GetMigrationCutover(ctx, account, app, in.ID)
	if err != nil || loaded.Report.Status != "cancelled" || loaded.Report.Request.PreviousRecords[0].TTL != 300 {
		t.Fatalf("lost cancellation/recovery: %+v %v", loaded, err)
	}
}
