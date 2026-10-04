// adr:531
package copydatabases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func retryFixture(t *testing.T) (*fixture, Receipt, uuid.UUID, VerificationClosure) {
	t.Helper()
	f := newFixture(t)
	r := f.prepare(t, f.ordinaryOID)
	imported, _ := verificationImport(t, f, r)
	owner := uuid.New()
	if got, err := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, func(context.Context, VerificationTarget) error { return pgerrors.ErrUnavailable }); got != (VerificationClosure{}) || !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatal("first failed comparison", err)
	}
	c, err := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize)
	if err != nil || c.AttemptForWorker() != 1 {
		t.Fatal("first actual native closure", err)
	}
	return f, r, imported, c
}
func retryStatus(t *testing.T, f *fixture, owner uuid.UUID) (string, time.Time, time.Time) {
	t.Helper()
	var status string
	var opened time.Time
	var closed *time.Time
	if err := f.target.QueryRow(t.Context(), "SELECT state,opened_at,closed_at FROM gregale_copy_database_verification_retries.windows WHERE owner_id=$1", owner).Scan(&status, &opened, &closed); err != nil {
		t.Fatal(err)
	}
	if closed == nil {
		return status, opened, time.Time{}
	}
	return status, opened, *closed
}
func retryOriginalRows(t *testing.T, f *fixture) string {
	t.Helper()
	var rows string
	if err := f.target.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(w) ORDER BY source_oid) FROM gregale_copy_database_maintenance.windows w),
 (SELECT jsonb_agg(to_jsonb(w) ORDER BY source_oid) FROM gregale_copy_database_verification.windows w))::text`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
func crashRetry(t *testing.T, f *fixture, r Receipt, imported, owner uuid.UUID, previous VerificationClosure) *verification {
	t.Helper()
	v, err := newVerificationRetry(r, f.target, f.exports, imported, owner, previous, f.authorize)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, rows, parent, err := v.read(t.Context())
	if err == nil {
		err = v.checkRetryPosition(rows)
	}
	if err == nil {
		err = v.open(t.Context(), parent)
	}
	releaseLock(t.Context(), f.target, v.q)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCopyDatabaseVerificationRetryBoundedOwnersReadOnlyAndOriginalHistory(t *testing.T) {
	f, r, imported, previous := retryFixture(t)
	original := retryOriginalRows(t, f)
	var retained VerificationTarget
	calls := 0
	for attempt := int32(2); attempt <= api.PostgresCopyVerificationAttemptsMax; attempt++ {
		owner := uuid.New()
		prior := previous
		got, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, prior, f.authorize,
			func(ctx context.Context, access VerificationTarget) error {
				calls++
				retained = access
				binding, err := access.IdentityForWorker()
				if err != nil || binding.OwnerID != owner || binding.ImportID != imported || binding.Attempt != attempt {
					return pgerrors.ErrConflict
				}
				target, err := access.TargetForWorker()
				if err != nil {
					return err
				}
				child := maintenanceChild(t, f, target)
				defer child.Close(context.Background())
				return access.WithReadOnly(ctx, child, verificationPlacement(f, child, target), func(ctx context.Context, tx pgx.Tx) error {
					var ro bool
					var isolation, value string
					if err := tx.QueryRow(ctx, "SELECT current_setting('transaction_read_only')::boolean,current_setting('transaction_isolation'),value FROM public.verification_data WHERE id=1").Scan(&ro, &isolation, &value); err != nil {
						return err
					}
					if !ro || isolation != "repeatable read" || value != "original imported row" {
						return pgerrors.ErrConflict
					}
					nested, err := tx.Begin(ctx)
					if err != nil {
						return err
					}
					_, err = nested.Exec(ctx, "UPDATE public.verification_data SET value='unwanted retry write' WHERE id=1")
					var pe *pgconn.PgError
					if !errors.As(err, &pe) || pe.Code != "25006" {
						t.Fatal("retry allowed a write", err)
					}
					return nested.Rollback(ctx)
				})
			})
		if err != nil || got.AttemptForWorker() != attempt || !got.MatchesForWorker(r, imported, owner, got.openedAt) || got.openedAt.Before(prior.ClosedAt()) || calls != int(attempt-1) {
			t.Fatal("bounded new owned read window", err)
		}
		if retryOriginalRows(t, f) != original {
			t.Fatal("retry replaced original import/verification history")
		}
		if _, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, prior, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil }); !errors.Is(err, pgerrors.ErrConflict) || calls != int(attempt-1) {
			t.Fatal("retry repeated its read callback", err)
		}
		recovered, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, prior.ownerID, f.authorize)
		if err != nil || recovered != got {
			t.Fatal("original retry closure changed on recovery", err)
		}
		if err := retained.check(t.Context()); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("retained retry capability reopened access", err)
		}
		assertMaintenanceOriginal(t, f, r)
		previous = got
	}
	if _, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, uuid.New(), previous, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil }); !errors.Is(err, pgerrors.ErrQuotaExceeded) || calls != api.PostgresCopyVerificationAttemptsMax-1 {
		t.Fatal("unbounded additional retry", err)
	}
	var count int
	if err := f.target.QueryRow(t.Context(), "SELECT count(*) FROM gregale_copy_database_verification_retries.windows").Scan(&count); err != nil || count != api.PostgresCopyVerificationAttemptsMax-1 {
		t.Fatal("native history exceeded its cap", err)
	}
}

func TestCopyDatabaseVerificationRetryActualDataMatchAndClosure(t *testing.T) {
	f, r, d, source, cfg := contentsFixture(t, "")
	manifest, err := copycontents.Capture(t.Context(), source, d, cfg, contentsSourcePlacement(f, source, d))
	if err != nil {
		t.Fatal(err)
	}
	imported := contentsRestore(t, f, r, d, source)
	first := uuid.New()
	if _, err := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, first, f.authorize, func(context.Context, VerificationTarget) error { return pgerrors.ErrUnavailable }); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatal(err)
	}
	previous, err := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, first, f.authorize)
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	key, _ := age.GenerateX25519Identity()
	var sealed copycontents.SealedMatch
	var target copyarchive.RestoreTarget
	c, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous, f.authorize, func(ctx context.Context, access VerificationTarget) error {
		var err error
		target, err = access.TargetForWorker()
		if err != nil {
			return err
		}
		child := maintenanceChild(t, f, target)
		place := verificationPlacement(f, child, target)
		var match copycontents.Match
		err = access.WithReadOnly(ctx, child, place, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			match, err = manifest.CompareTarget(ctx, tx, target, cfg, place)
			return err
		})
		_ = child.Close(context.Background())
		if err != nil {
			return err
		}
		binding, err := access.IdentityForWorker()
		if err != nil {
			return err
		}
		sealed, err = copycontents.SealMatch(key.Recipient(), manifest, target, owner, imported, binding.OpenedAt, match)
		return err
	})
	if err != nil || c.AttemptForWorker() != 2 || !c.MatchesForWorker(r, imported, owner, sealed.OpenedAt) {
		t.Fatal("actual independent retry comparison", err)
	}
	match, err := copycontents.OpenMatch([]*age.X25519Identity{key}, manifest, target, owner, imported, sealed)
	if err != nil || !match.Matches(manifest, target, sealed) {
		t.Fatal("retry did not bind retained proof", err)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(fmt.Sprintf("%+v %#v %s", c, c, raw), target.DatabaseName) {
		t.Fatal("retry closure exposed private SQL identity")
	}
	assertMaintenanceOriginal(t, f, r)
}

func TestCopyDatabaseVerificationRetryRejectsForeignStaleAndMetadataPredecessors(t *testing.T) {
	f, r, imported, previous := retryFixture(t)
	for _, mode := range []string{"zero", "source", "target", "import", "fingerprint", "opened", "closed", "attempt", "reuse owner", "import owner", "nil run"} {
		t.Run(mode, func(t *testing.T) {
			p := previous
			owner := uuid.New()
			run := VerificationRun(func(context.Context, VerificationTarget) error {
				t.Fatal("foreign predecessor read target")
				return nil
			})
			switch mode {
			case "zero":
				p = VerificationClosure{}
			case "source":
				p.sourceOID++
			case "target":
				p.targetOID++
			case "import":
				p.importOwnerID = uuid.New()
			case "fingerprint":
				p.planFingerprint = strings.Repeat("a", 64)
			case "opened":
				p.openedAt = p.openedAt.Add(-time.Microsecond)
			case "closed":
				p.closedAt = p.closedAt.Add(time.Microsecond)
			case "attempt":
				p.attempt++
			case "reuse owner":
				owner = p.ownerID
			case "import owner":
				owner = imported
			case "nil run":
				run = nil
			}
			if got, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, p, f.authorize, run); err == nil || got != (VerificationClosure{}) {
				t.Fatal("foreign/metadata predecessor admitted", err)
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
	owner := uuid.New()
	c, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous, f.authorize, func(context.Context, VerificationTarget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, uuid.New(), previous, f.authorize, func(context.Context, VerificationTarget) error {
		t.Fatal("stale predecessor branched history")
		return nil
	}); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatal("stale predecessor created a branch", err)
	}
	if got, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, uuid.New(), f.authorize); !errors.Is(err, pgerrors.ErrConflict) || got != (VerificationClosure{}) {
		t.Fatal("foreign previous-owner assertion adopted closure", err)
	}
	if got, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, uuid.New(), previous.ownerID, f.authorize); !errors.Is(err, pgerrors.ErrNotFound) || got != (VerificationClosure{}) {
		t.Fatal("absent owner manufactured closure", err)
	}
	if got, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous.ownerID, f.authorize); err != nil || got != c {
		t.Fatal("original retry no longer recoverable", err)
	}
}

func TestCopyDatabaseVerificationRetryFailureAndAuthorityLossCloseOriginalOwner(t *testing.T) {
	for _, mode := range []string{"callback", "authority", "cancel", "leaked child"} {
		t.Run(mode, func(t *testing.T) {
			f, r, imported, previous := retryFixture(t)
			owner := uuid.New()
			live := true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			authorize := func(ctx context.Context, target copyarchive.RestoreTarget) error {
				if !live {
					return pgerrors.ErrConflict
				}
				return f.authorize(ctx, target)
			}
			var leaked *pgx.Conn
			calls := 0
			got, err := r.WithVerificationRetryAccess(ctx, f.target, f.exports, imported, owner, previous, authorize, func(ctx context.Context, access VerificationTarget) error {
				calls++
				target, err := access.TargetForWorker()
				if err != nil {
					return err
				}
				child := maintenanceChild(t, f, target)
				if mode == "leaked child" {
					leaked = child
				} else {
					defer child.Close(context.Background())
				}
				return access.WithReadOnly(ctx, child, verificationPlacement(f, child, target), func(ctx context.Context, tx pgx.Tx) error {
					var value string
					if err := tx.QueryRow(ctx, "SELECT value FROM public.verification_data WHERE id=1").Scan(&value); err != nil {
						return err
					}
					switch mode {
					case "callback":
						return pgerrors.ErrUnavailable
					case "authority":
						live = false
					case "cancel":
						cancel()
					}
					return nil
				})
			})
			if err == nil || got != (VerificationClosure{}) || calls != 1 {
				t.Fatal("failed retry became closure", err)
			}
			state, _, closed := retryStatus(t, f, owner)
			if mode == "leaked child" {
				if leaked.IsClosed() || state != "closing" || !closed.IsZero() {
					t.Fatal("leaked child was killed or closure invented")
				}
				_ = leaked.Close(context.Background())
			} else if state != "closed" || closed.IsZero() {
				t.Fatal("failed read retained admission")
			}
			c, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous.ownerID, f.authorize)
			if err != nil || c.AttemptForWorker() != 2 || (!closed.IsZero() && !c.ClosedAt().Equal(closed)) {
				t.Fatal("failure recovery lost original window", err)
			}
			if _, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil }); !errors.Is(err, pgerrors.ErrConflict) || calls != 1 {
				t.Fatal("failed owner repeated read", err)
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}

type retryLostReplyTracer struct{ verificationLostReplyTracer }

func (tr *retryLostReplyTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "pg_temp.gregale_copy_database_verification_retries_change(") && len(d.Args) > 7 {
		tr.armed, _ = d.Args[7].(string)
	}
	return context.WithValue(ctx, maintenanceCommitTraceKey{}, strings.EqualFold(strings.TrimSpace(d.SQL), "commit"))
}
func TestCopyDatabaseVerificationRetryLostNativeReplyRecoversWithoutReadReplay(t *testing.T) {
	for _, phase := range []string{"open", "finish"} {
		t.Run(phase, func(t *testing.T) {
			f, r, imported, previous := retryFixture(t)
			owner := uuid.New()
			calls := 0
			_ = f.target.Close(context.Background())
			cfg := f.target.Config().Copy()
			tr := &retryLostReplyTracer{verificationLostReplyTracer{phase: phase}}
			cfg.Tracer = tr
			var err error
			f.target, err = pgx.ConnectConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil })
			if err == nil || got != (VerificationClosure{}) || !tr.fired {
				t.Fatal("lost committed retry response became closure", err)
			}
			cfg.Tracer = nil
			f.target, err = pgx.ConnectConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			state, opened, closed := retryStatus(t, f, owner)
			if phase == "open" && (state != "open" || calls != 0) || phase == "finish" && (state != "closed" || calls != 1 || closed.IsZero()) {
				t.Fatal("lost response did not preserve its committed phase")
			}
			c, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous.ownerID, f.authorize)
			if err != nil || !c.openedAt.Equal(opened) || phase == "finish" && !c.ClosedAt().Equal(closed) {
				t.Fatal("lost reply recovery changed original window", err)
			}
			if _, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil }); !errors.Is(err, pgerrors.ErrConflict) || phase == "open" && calls != 0 || phase == "finish" && calls != 1 {
				t.Fatal("unknown native retry replayed read", err)
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}

func TestCopyDatabaseVerificationRetryActiveAndQuiescedHistoryFencesEveryEntry(t *testing.T) {
	f := newFixtureConfigured(t, func(f *fixture) {
		run(t, f.sourceRoot, "ALTER DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" CONNECTION LIMIT "+fmt.Sprint(api.PostgresCopyMaintenanceConnections))
	})
	r := f.prepare(t, f.ordinaryOID)
	imported, _ := verificationImport(t, f, r)
	first := uuid.New()
	previous, err := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, first, f.authorize, func(context.Context, VerificationTarget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	v := crashRetry(t, f, r, imported, owner, previous)
	for _, phase := range []string{"open", "closing"} {
		if phase == "closing" {
			_, _, parent, err := v.read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err = v.changeCommitted(t.Context(), "quiesce", parent); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.VerifyForWorker(t.Context(), f.target, f.exports, f.authorize); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("strict preparation admitted retry", phase, err)
		}
		if _, err := r.CloseMaintenance(t.Context(), f.target, f.exports, imported, f.authorize); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("import adopted active retry", phase, err)
		}
		if _, err := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, first, f.authorize); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("original closure admitted newer access", phase, err)
		}
		if _, err := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, first, f.authorize, func(context.Context, VerificationTarget) error { t.Fatal("first callback replayed"); return nil }); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("first verification adopted retry", phase, err)
		}
	}
	if _, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, first, f.authorize); err != nil {
		t.Fatal(err)
	}
	if err := r.VerifyForWorker(t.Context(), f.target, f.exports, f.authorize); err != nil {
		t.Fatal("closed retry prevented ordinary preparation", err)
	}
	assertMaintenanceOriginal(t, f, r)
}

func TestCopyDatabaseVerificationRetryRejectsDamagedHistoryOnAllReaders(t *testing.T) {
	for _, mode := range []string{"scope fingerprint", "import owner", "predecessor owner", "predecessor time", "attempt gap", "schema grant", "table grant", "columns", "active index", "gap in retained history"} {
		t.Run(mode, func(t *testing.T) {
			f, r, imported, previous := retryFixture(t)
			owner := uuid.New()
			second, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous, f.authorize, func(context.Context, VerificationTarget) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "scope fingerprint":
				run(t, f.target, "UPDATE gregale_copy_database_verification_retries.windows SET plan_fingerprint=$2 WHERE owner_id=$1", owner, strings.Repeat("a", 64))
			case "import owner":
				run(t, f.target, "UPDATE gregale_copy_database_verification_retries.windows SET import_owner_id=$2 WHERE owner_id=$1", owner, uuid.New())
			case "predecessor owner":
				run(t, f.target, "UPDATE gregale_copy_database_verification_retries.windows SET previous_owner_id=$2 WHERE owner_id=$1", owner, uuid.New())
			case "predecessor time":
				run(t, f.target, "UPDATE gregale_copy_database_verification_retries.windows SET previous_closed_at=previous_closed_at-interval '1 microsecond' WHERE owner_id=$1", owner)
			case "attempt gap":
				run(t, f.target, "UPDATE gregale_copy_database_verification_retries.windows SET attempt=3 WHERE owner_id=$1", owner)
			case "schema grant":
				run(t, f.target, "GRANT USAGE ON SCHEMA gregale_copy_database_verification_retries TO PUBLIC")
			case "table grant":
				run(t, f.target, "GRANT SELECT ON gregale_copy_database_verification_retries.windows TO PUBLIC")
			case "columns":
				run(t, f.target, "ALTER TABLE gregale_copy_database_verification_retries.windows ADD COLUMN changed text")
			case "active index":
				run(t, f.target, "DROP INDEX gregale_copy_database_verification_retries.windows_one_active")
				run(t, f.target, "CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_verification_retries.windows ((1)) WHERE state='closed'")
			case "gap in retained history":
				if _, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, uuid.New(), second, f.authorize, func(context.Context, VerificationTarget) error { return nil }); err != nil {
					t.Fatal(err)
				}
				run(t, f.target, "DELETE FROM gregale_copy_database_verification_retries.windows WHERE owner_id=$1", owner)
			}
			if _, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, previous.ownerID, f.authorize); !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatal("damaged retry recovered closure", err)
			}
			if err := r.VerifyForWorker(t.Context(), f.target, f.exports, f.authorize); !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatal("preparation ignored damaged retry history", err)
			}
			if _, err := r.CloseMaintenance(t.Context(), f.target, f.exports, imported, f.authorize); !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatal("import ignored damaged retry history", err)
			}
			allow, template, limit, state := maintenanceStatus(t, f, r.sourceOID)
			d, _, _ := r.plan.creationDatabase(r.sourceOID)
			if allow || template != d.Template || limit != d.ConnectionLimit || state != "closed" {
				t.Fatal("history rejection changed original database admission")
			}

		})
	}
}

func TestCopyDatabaseVerificationRetrySerializesConcurrentOwnersForSamePredecessor(t *testing.T) {
	f, r, imported, previous := retryFixture(t)
	var calls atomic.Int32
	type result struct {
		owner   uuid.UUID
		closure VerificationClosure
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for range 2 {
		conn, err := pgx.ConnectConfig(t.Context(), f.target.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		owner := uuid.New()
		wg.Go(func() {
			defer conn.Close(context.Background())
			c, err := r.WithVerificationRetryAccess(ctx, conn, f.exports, imported, owner, previous, f.authorize, func(context.Context, VerificationTarget) error { calls.Add(1); return nil })
			results <- result{owner, c, err}
		})
	}
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for got := range results {
		if got.err == nil && got.closure.AttemptForWorker() == 2 && got.closure.MatchesForWorker(r, imported, got.owner, got.closure.openedAt) {
			succeeded++
		} else if errors.Is(got.err, pgerrors.ErrConflict) && got.closure == (VerificationClosure{}) {
			conflicted++
		} else {
			t.Fatal("concurrent retry result", got.err)
		}
	}
	if succeeded != 1 || conflicted != 1 || calls.Load() != 1 {
		t.Fatal("concurrent predecessor admitted more than one owner")
	}
	assertMaintenanceOriginal(t, f, r)
}

func TestCopyDatabaseVerificationRetryCrossDatabaseOwnersAndActiveAccess(t *testing.T) {
	f := newFixtureConfigured(t, func(f *fixture) { existingClosedTemplate(t, f) })
	r := f.prepare(t, f.ordinaryOID)
	other := f.prepare(t, f.templateOID)
	imported, _ := verificationImport(t, f, r)
	otherImport, _ := verificationImport(t, f, other)
	first, err := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, uuid.New(), f.authorize, func(context.Context, VerificationTarget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.WithVerificationRetryAccess(t.Context(), f.target, f.exports, imported, uuid.New(), first, f.authorize, func(context.Context, VerificationTarget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []uuid.UUID{first.ownerID, second.ownerID, imported} {
		if _, err := other.WithVerificationAccess(t.Context(), f.target, f.exports, otherImport, owner, f.authorize, func(context.Context, VerificationTarget) error {
			t.Fatal("first window reused another database's owner")
			return nil
		}); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("cross-database original owner admitted", err)
		}
	}
	otherFirst, err := other.WithVerificationAccess(t.Context(), f.target, f.exports, otherImport, uuid.New(), f.authorize, func(context.Context, VerificationTarget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []uuid.UUID{first.ownerID, second.ownerID, imported} {
		if _, err := other.WithVerificationRetryAccess(t.Context(), f.target, f.exports, otherImport, owner, otherFirst, f.authorize, func(context.Context, VerificationTarget) error {
			t.Fatal("retry reused another database's owner")
			return nil
		}); !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("cross-database retry owner admitted", err)
		}
	}
	owner := uuid.New()
	crashRetry(t, f, r, imported, owner, second)
	if _, err := other.WithVerificationRetryAccess(t.Context(), f.target, f.exports, otherImport, uuid.New(), otherFirst, f.authorize, func(context.Context, VerificationTarget) error {
		t.Fatal("second database admitted while first retry active")
		return nil
	}); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatal("cross-database simultaneous access admitted", err)
	}
	if _, err := r.CloseVerificationRetryAccess(t.Context(), f.target, f.exports, imported, owner, second.ownerID, f.authorize); err != nil {
		t.Fatal(err)
	}
	if _, err := other.WithVerificationRetryAccess(t.Context(), f.target, f.exports, otherImport, uuid.New(), otherFirst, f.authorize, func(context.Context, VerificationTarget) error { return nil }); err != nil {
		t.Fatal("second database retry unavailable after original closure", err)
	}
	assertMaintenanceOriginal(t, f, r)
	assertMaintenanceOriginal(t, f, other)
}
