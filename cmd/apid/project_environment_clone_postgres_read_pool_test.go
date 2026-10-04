//go:build !no_pg

// adr: 583
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneContentsReadConfig(t *testing.T) copycontents.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	pool, err := copycontents.NewReadPool(dir, copycontents.ReadPoolLimits{Readers: 1, MemoryBytes: api.PostgresCopyContentsSortMemoryMax,
		DiskBytes: 1 << 20, MinFreeBytes: api.PostgresCopyContentsSpoolFreeReserveMin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error("live contents read reservation", err)
		}
	})
	return copycontents.Config{ReadPool: pool, SpoolDir: dir, MaxBytes: 16 << 20, SortMemoryBytes: 64, SortDiskBytes: 1 << 20}
}

func TestPGClonePostgresContentsOwnedReaderReadPoolAdmissionPrecedesBorrow(t *testing.T) {
	for _, mode := range []string{"busy", "missing", "alternate"} {
		t.Run(mode, func(t *testing.T) {
			f := newContentsReaderFixture(t)
			original := f.reserve(t)
			cfg := f.cfg
			var release func()
			want := managedpostgres.ErrUnavailable
			if mode == "busy" {
				var err error
				_, release, err = cfg.ReserveReadForWorker(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				want = managedpostgres.ErrQuotaExceeded
			} else if mode == "missing" {
				f.x.f.srv.clonePostgresContentsReadPool = nil
			} else {
				f.cfg = cloneContentsReadConfig(t)
				want = managedpostgres.ErrConflict
			}
			if m, err := f.capture(t.Context()); !errors.Is(err, want) || m.Fingerprint() != "" || f.p.readerSelectedSQLCalls != 0 || f.p.placementChecks != 0 {
				t.Fatal("source borrowed SQL before host admission", err)
			}
			f.unpublished(t, original)
			if release != nil {
				release()
			}
			f.cfg = cfg
			f.x.f.srv.clonePostgresContentsReadPool = cfg.ReadPool
			if m, err := f.capture(t.Context()); err != nil || m.Fingerprint() == "" || f.p.readerSelectedSQLCalls != 1 {
				t.Fatal("admitted source did not resume", err)
			}
		})
	}
}

func TestPGClonePostgresVerificationReadPoolRefusalKeepsNeverOpenedOwner(t *testing.T) {
	for _, attempt := range []int{1, 2} {
		t.Run(map[int]string{1: "original", 2: "retry"}[attempt], func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			if attempt == 2 {
				failFirstVerificationWorker(t, v)
			}
			beforeReads := v.dataReads
			_, release, err := v.cfg.ReserveReadForWorker(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrQuotaExceeded) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != beforeReads {
				t.Fatal("capacity denial read/published data", err)
			}
			history := verificationRetryHistory(t, v)
			owner := v.owner(t)
			id := owner.VerificationID
			if attempt == 1 && len(history) != 0 || attempt == 2 && (len(history) != 2 || history[1].State != "verifying") {
				t.Fatal("denial consumed/failed native attempt")
			}
			if attempt == 2 {
				id = history[1].VerificationID
			}
			x := v.f.db.x
			budget, err := v.store.ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(t.Context(), x.f.lease, x.source.source.ID, v.f.sourceOID)
			if err != nil || len(budget.Allocations) != attempt-1 {
				t.Fatal("capacity denial spent read debit", err)
			}
			var count int
			if err := x.targetRoot.QueryRow(t.Context(), `SELECT count(*) FROM pg_database WHERE oid=$1::oid AND datallowconn`, v.f.target.DatabaseOID).Scan(&count); err != nil || count != 0 {
				t.Fatal("capacity denial opened SQL", err)
			}
			release()
			v.handoff(t)
			verified, err := runVerificationRetryWorker(t, v)
			if err != nil || verified.VerificationID != id || verified.Attempt != int32(attempt) || verified.State != "verified" || v.dataReads != beforeReads+1 {
				t.Fatal("denied owner not resumed after capacity returned", err)
			}
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationReadPoolRequiredAndRetainedThroughPostcheck(t *testing.T) {
	for _, mode := range []string{"missing", "alternate", "postcheck", "lost_identity"} {
		t.Run(mode, func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			cfg := v.cfg
			if mode == "missing" {
				v.f.db.x.f.srv.clonePostgresContentsReadPool = nil
			}
			if mode == "alternate" {
				v.cfg = cloneContentsReadConfig(t)
			}
			if mode == "postcheck" {
				v.f.afterChild = func(ctx context.Context, _ *pgx.Conn) error {
					if _, release, err := cfg.ReserveReadForWorker(ctx); !errors.Is(err, managedpostgres.ErrQuotaExceeded) {
						if release != nil {
							release()
						}
						t.Error("provider postcheck surrendered host capacity", err)
					}
					return nil
				}
				v.childTrace = &verificationWorkerReadTrace{onRead: func() {
					if _, release, err := cfg.ReserveReadForWorker(t.Context()); !errors.Is(err, managedpostgres.ErrQuotaExceeded) {
						if release != nil {
							release()
						}
						t.Error("live read surrendered host capacity", err)
					}
				}}
			}
			if mode == "lost_identity" {
				v.childTrace = &verificationWorkerReadTrace{onRead: func() {
					if err := os.Rename(filepath.Join(cfg.SpoolDir, ".gregale-contents-read-pool"), filepath.Join(cfg.SpoolDir, "moved-lock")); err != nil {
						t.Fatal(err)
					}
				}}
			}
			got, err := runVerificationRetryWorker(t, v)
			if mode == "postcheck" {
				if err != nil || got.State != "verified" {
					t.Fatal("admitted real read", err)
				}
			} else if err == nil || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) {
				t.Fatal("unowned host resources became proof", err)
			}
			if (mode == "missing" || mode == "alternate") && v.dataReads != 0 {
				t.Fatal("missing pool read target")
			}
			if mode == "lost_identity" {
				if err := os.Rename(filepath.Join(cfg.SpoolDir, "moved-lock"), filepath.Join(cfg.SpoolDir, ".gregale-contents-read-pool")); err != nil {
					t.Fatal(err)
				}
				if len(v.owner(t).Sealed.Ciphertext) != 0 {
					t.Fatal("lost pool identity published match")
				}
			}
			v.cfg = cfg
			v.f.db.x.f.srv.clonePostgresContentsReadPool = cfg.ReadPool
			if _, release, err := cfg.ReserveReadForWorker(context.Background()); err != nil {
				t.Fatal("terminal read leaked host reservation", err)
			} else {
				release()
			}
			v.assertClosed(t, mode != "missing" && mode != "alternate")
		})
	}
}
