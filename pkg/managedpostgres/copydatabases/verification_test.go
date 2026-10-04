// adr:566
package copydatabases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func verificationImport(t *testing.T, f *fixture, r Receipt) (uuid.UUID, MaintenanceClosure) {
	t.Helper()
	imported := uuid.New()
	c, err := r.WithMaintenance(t.Context(), f.target, f.exports, imported, f.authorize, func(ctx context.Context, target copyarchive.RestoreTarget) error {
		conn := maintenanceChild(ctx, t, f, target)
		defer conn.Close(context.WithoutCancel(ctx))
		run(ctx, t, conn, "CREATE TABLE public.verification_data(id integer PRIMARY KEY, value text)")
		run(ctx, t, conn, "INSERT INTO public.verification_data VALUES (1,'original imported row')")
		return nil
	})
	if err != nil || c.ClosedAt().IsZero() {
		t.Fatal("original import window", err)
	}
	return imported, c
}
func verificationStatus(ctx context.Context, t *testing.T, f *fixture, r Receipt) (bool, bool, int32, string, time.Time) {
	t.Helper()
	d, _, _ := r.plan.creationDatabase(r.sourceOID)
	var allow, template bool
	var limit int32
	if err := f.targetRoot.QueryRow(ctx, "SELECT datallowconn,datistemplate,datconnlimit FROM pg_database WHERE oid=$1", d.OID).Scan(&allow, &template, &limit); err != nil {
		t.Fatal(err)
	}
	var present bool
	if err := f.target.QueryRow(ctx, "SELECT to_regclass('gregale_copy_database_verification.windows') IS NOT NULL").Scan(&present); err != nil {
		t.Fatal(err)
	}
	var state string
	var at *time.Time
	if present {
		err := f.target.QueryRow(ctx, "SELECT state,closed_at FROM gregale_copy_database_verification.windows WHERE source_oid=$1", r.sourceOID).Scan(&state, &at)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
	}
	if at == nil {
		return allow, template, limit, state, time.Time{}
	}
	return allow, template, limit, state, *at
}
func verificationPlacement(f *fixture, conn *pgx.Conn, target copyarchive.RestoreTarget) copyarchive.RestorePlacement {
	return func(_ context.Context, c *pgx.Conn, got copyarchive.RestoreTarget) error {
		if c != conn || got != target || c.Config().Host != f.targetRoot.Config().Host || c.Config().Host == f.sourceRoot.Config().Host {
			return pgerrors.ErrConflict
		}
		return nil
	}
}
func verificationCrash(t *testing.T, f *fixture, r Receipt, imported, owner uuid.UUID) *verification {
	t.Helper()
	v, err := newVerification(r, f.target, f.exports, imported, owner, f.authorize)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, _, parent, err := v.read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = v.open(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	releaseLock(t.Context(), f.target, v.q)
	return v
}

func TestCopyDatabaseVerificationReadsSeparatelyAndPreservesOriginalImport(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			f := newFixtureConfigured(t, func(f *fixture) {
				if existing {
					existingClosedTemplate(t, f)
				} else {
					run(t.Context(), t, f.sourceRoot, "ALTER DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" CONNECTION LIMIT 0")
				}
			})
			id := f.ordinaryOID
			if existing {
				id = f.templateOID
			}
			r := f.prepare(t, id)
			imported, original := verificationImport(t, f, r)
			owner, calls := uuid.New(), 0
			var retained VerificationTarget
			closure, err := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, func(ctx context.Context, target VerificationTarget) error {
				calls++
				retained = target
				allow, template, limit, state, _ := verificationStatus(ctx, t, f, r)
				if !allow || template || limit != api.PostgresCopyMaintenanceConnections || state != "open" {
					t.Fatal("verification admission not isolated")
				}
				if e := r.VerifyForWorker(ctx, f.target, f.exports, f.authorize); !errors.Is(e, pgerrors.ErrConflict) {
					t.Fatal("strict preparation admitted active verification", e)
				}
				child, e := target.TargetForWorker()
				if e != nil {
					return e
				}
				c := maintenanceChild(ctx, t, f, child)
				defer c.Close(context.WithoutCancel(ctx))
				placementCalls := 0
				place := verificationPlacement(f, c, child)
				e = target.WithReadOnly(ctx, c, func(ctx context.Context, c *pgx.Conn, got copyarchive.RestoreTarget) error {
					placementCalls++
					return place(ctx, c, got)
				}, func(ctx context.Context, tx pgx.Tx) error {
					var ro bool
					var isolation, value string
					if e := tx.QueryRow(ctx, "SELECT current_setting('transaction_read_only')::boolean,current_setting('transaction_isolation'),value FROM public.verification_data WHERE id=1").Scan(&ro, &isolation, &value); e != nil {
						return e
					}
					if !ro || isolation != "repeatable read" || value != "original imported row" {
						return pgerrors.ErrConflict
					}
					nested, e := tx.Begin(ctx)
					if e != nil {
						return e
					}
					_, e = nested.Exec(ctx, "UPDATE public.verification_data SET value='unwanted write' WHERE id=1")
					var pe *pgconn.PgError
					if !errors.As(e, &pe) || pe.Code != "25006" {
						t.Fatal("verification transaction accepted a write", e)
					}
					return nested.Rollback(ctx)
				})
				if e != nil || placementCalls != 2 || c.PgConn().TxStatus() != 'I' {
					t.Fatal("read-only inspection", e)
				}
				if got, e := r.CloseMaintenance(ctx, f.target, f.exports, imported, f.authorize); got != (MaintenanceClosure{}) || !errors.Is(e, pgerrors.ErrConflict) {
					t.Fatal("import adopted active verification", e)
				}
				return nil
			})
			if err != nil || calls != 1 || closure.ClosedAt().IsZero() {
				t.Fatal("separate verification closure", err)
			}
			assertMaintenanceOriginal(t, f, r)
			parent, e := r.CloseMaintenance(t.Context(), f.target, f.exports, imported, f.authorize)
			if e != nil || parent != original {
				t.Fatal("verification changed original import window", e)
			}
			again, e := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize)
			if e != nil || again != closure {
				t.Fatal("verification closure regenerated", e)
			}
			if got, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil }); got != (VerificationClosure{}) || !errors.Is(e, pgerrors.ErrConflict) || calls != 1 {
				t.Fatal("verification replayed", e)
			}
			if e := retained.WithReadOnly(t.Context(), nil, func(context.Context, *pgx.Conn, copyarchive.RestoreTarget) error { return nil }, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(e, pgerrors.ErrConflict) {
				t.Fatal("retained target reopened access", e)
			}
			raw, _ := json.Marshal(closure)
			targetRaw, _ := json.Marshal(retained)
			for _, display := range []string{string(raw), fmt.Sprint(closure), fmt.Sprintf("%#v", closure), string(targetRaw), fmt.Sprintf("%#v", retained)} {
				for _, secret := range []string{f.owner, f.pins.ProviderResourceID, f.pins.EndpointID, owner.String(), imported.String()} {
					if strings.Contains(display, secret) {
						t.Fatal("verification capability leaked identity")
					}
				}
			}
		})
	}
}

func TestCopyDatabaseVerificationRequiresOriginalClosedImportAndDistinctOwners(t *testing.T) {
	f := newFixture(t)
	r := f.prepare(t, f.ordinaryOID)
	imported, owner := uuid.New(), uuid.New()
	calls := 0
	callback := func(context.Context, VerificationTarget) error { calls++; return nil }
	for _, closeOnly := range []bool{false, true} {
		var err error
		if closeOnly {
			_, err = r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize)
		} else {
			_, err = r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, callback)
		}
		if !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatal("missing import window accepted", err)
		}
	}
	m, e := newMaintenance(r, f.target, f.exports, imported, f.authorize)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.lock(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = m.open(t.Context()); e != nil {
		t.Fatal(e)
	}
	releaseLock(t.Context(), f.target, m.q)
	if _, e = r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, callback); !errors.Is(e, pgerrors.ErrConflict) {
		t.Fatal("open import accepted", e)
	}
	if _, e = r.CloseMaintenance(t.Context(), f.target, f.exports, imported, f.authorize); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"zero_owner", "same_owner", "scope_alias", "wrong_import", "nil_run", "nil_authority", "changed_preparation", "wrong_database", "missing_source"} {
		t.Run(mode, func(t *testing.T) {
			input, source, conn, imp, own, auth, run := r, f.exports, f.target, imported, owner, f.authorize, VerificationRun(callback)
			want := pgerrors.ErrConflict
			switch mode {
			case "zero_owner":
				own = uuid.Nil
				want = pgerrors.ErrInvalid
			case "same_owner":
				own = imp
				want = pgerrors.ErrInvalid
			case "scope_alias":
				own = uuid.MustParse(f.pins.Scope.OperationID)
				want = pgerrors.ErrInvalid
			case "wrong_import":
				imp = uuid.New()
			case "nil_run":
				run = nil
				want = pgerrors.ErrInvalid
			case "nil_authority":
				auth = nil
				want = pgerrors.ErrInvalid
			case "changed_preparation":
				input.createdAt = input.createdAt.Add(-time.Second)
			case "wrong_database":
				conn = f.targetRoot
			case "missing_source":
				source = copyinventory.ExportPlan{}
			}
			if got, e := input.WithVerificationAccess(t.Context(), conn, source, imp, own, auth, run); got != (VerificationClosure{}) || !errors.Is(e, want) {
				t.Fatal("invalid verification reached callback", e)
			}
		})
	}
	if calls != 0 {
		t.Fatal("invalid verification invoked callback")
	}
	allow, _, _, state, _ := verificationStatus(t.Context(), t, f, r)
	if allow || state != "" {
		t.Fatal("invalid input installed verification")
	}
	assertMaintenanceOriginal(t, f, r)
}

func TestCopyDatabaseVerificationFailureCancellationAndAuthorityLossClose(t *testing.T) {
	for _, mode := range []string{"failure", "cancelled", "authority_lost", "authority_lost_open"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			imported, original := verificationImport(t, f, r)
			owner := uuid.New()
			expired := false
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			auth := func(ctx context.Context, target copyarchive.RestoreTarget) error {
				if expired || (mode == "authority_lost_open" && f.target.PgConn().TxStatus() == 'T') {
					return fmt.Errorf("private-lease-secret: %w", pgerrors.ErrConflict)
				}
				return f.authorize(ctx, target)
			}
			got, err := r.WithVerificationAccess(ctx, f.target, f.exports, imported, owner, auth, func(ctx context.Context, target VerificationTarget) error {
				calls++
				if mode == "cancelled" {
					cancel()
				}
				if mode == "authority_lost" {
					expired = true
				}
				return fmt.Errorf("private-read-secret: %w", pgerrors.ErrConflict)
			})
			if got != (VerificationClosure{}) || err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("failure minted/leaked closure", err)
			}
			allow, _, _, state, _ := verificationStatus(t.Context(), t, f, r)
			want := "closed"
			if mode == "authority_lost_open" {
				want = ""
				if calls != 0 {
					t.Fatal("expired opening invoked read")
				}
			}
			if allow || state != want {
				t.Fatal("failed read left admission open", state)
			}
			assertMaintenanceOriginal(t, f, r)
			if at := maintenanceWindowTime(t, f, r.sourceOID); !at.Equal(original.ClosedAt()) {
				t.Fatal("failed read changed original import")
			}
		})
	}
}

func TestCopyDatabaseVerificationQuiescesLeaksAndBlocksImportEvenAtBaselineLimit(t *testing.T) {
	f := newFixtureConfigured(t, func(f *fixture) {
		run(t.Context(), t, f.sourceRoot, "ALTER DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" CONNECTION LIMIT 2")
	})
	r := f.prepare(t, f.ordinaryOID)
	other := f.prepare(t, f.templateOID)
	imported, original := verificationImport(t, f, r)
	owner := uuid.New()
	var leaked *pgx.Conn
	got, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, func(ctx context.Context, target VerificationTarget) error {
		child, _ := target.TargetForWorker()
		leaked = maintenanceChild(ctx, t, f, child)
		return nil
	})
	if got != (VerificationClosure{}) || !errors.Is(e, pgerrors.ErrConflict) {
		t.Fatal("leaked child received successful closure", e)
	}
	defer leaked.Close(context.Background())
	allow, template, limit, state, _ := verificationStatus(t.Context(), t, f, r)
	if allow || template || limit != api.PostgresCopyMaintenanceConnections || state != "closing" {
		t.Fatal("leaked child was not quiesced")
	}
	if e := r.VerifyForWorker(t.Context(), f.target, f.exports, f.authorize); !errors.Is(e, pgerrors.ErrConflict) {
		t.Fatal("strict preparation accepted closing verification at original limit", e)
	}
	if _, e := Prepare(t.Context(), f.target, f.exports, f.plan, r.sourceOID, f.authorize); !errors.Is(e, pgerrors.ErrConflict) {
		t.Fatal("creation replay accepted closing verification at original limit", e)
	}
	calls := 0
	if _, e = r.WithMaintenance(t.Context(), f.target, f.exports, imported, f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil }); !errors.Is(e, pgerrors.ErrConflict) || calls != 0 {
		t.Fatal("import redispatched through closing verification", e)
	}
	if got, e = r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, uuid.New(), f.authorize); got != (VerificationClosure{}) || !errors.Is(e, pgerrors.ErrConflict) {
		t.Fatal("foreign verifier adopted leaked window", e)
	}
	if _, e = other.WithMaintenance(t.Context(), f.target, f.exports, uuid.New(), f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil }); !errors.Is(e, pgerrors.ErrConflict) || calls != 0 {
		t.Fatal("another import opened during verification", e)
	}
	if e = leaked.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	closure, e := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize)
	if e != nil || closure.ClosedAt().IsZero() {
		t.Fatal("leaked verification recovery", e)
	}
	if parent, e := r.CloseMaintenance(t.Context(), f.target, f.exports, imported, f.authorize); e != nil || parent != original {
		t.Fatal("recovery changed original import", e)
	}
	if _, e = other.WithMaintenance(t.Context(), f.target, f.exports, uuid.New(), f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil }); e != nil || calls != 1 {
		t.Fatal("independent import did not proceed after closure", e)
	}
	assertMaintenanceOriginal(t, f, r)
}

func TestCopyDatabaseVerificationRejectsJournalAndOriginalParentSubstitution(t *testing.T) {
	for _, mode := range []string{"parent_owner", "parent_closed_at", "verification_owner", "verification_import", "fingerprint", "privacy", "shape", "active_index"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			imported, _ := verificationImport(t, f, r)
			owner := uuid.New()
			verificationCrash(t, f, r, imported, owner)
			switch mode {
			case "parent_owner":
				run(t.Context(), t, f.target, "UPDATE gregale_copy_database_maintenance.windows SET owner_id=$1 WHERE source_oid=$2", uuid.New(), r.sourceOID)
			case "parent_closed_at":
				run(t.Context(), t, f.target, "UPDATE gregale_copy_database_maintenance.windows SET closed_at=closed_at-interval '1 microsecond' WHERE source_oid=$1", r.sourceOID)
			case "verification_owner":
				run(t.Context(), t, f.target, "UPDATE gregale_copy_database_verification.windows SET owner_id=$1 WHERE source_oid=$2", uuid.New(), r.sourceOID)
			case "verification_import":
				run(t.Context(), t, f.target, "UPDATE gregale_copy_database_verification.windows SET import_owner_id=$1 WHERE source_oid=$2", uuid.New(), r.sourceOID)
			case "fingerprint":
				run(t.Context(), t, f.target, "UPDATE gregale_copy_database_verification.windows SET plan_fingerprint=$1 WHERE source_oid=$2", strings.Repeat("f", 64), r.sourceOID)
			case "privacy":
				run(t.Context(), t, f.target, "GRANT USAGE ON SCHEMA gregale_copy_database_verification TO PUBLIC")
			case "shape":
				run(t.Context(), t, f.target, "ALTER TABLE gregale_copy_database_verification.windows ADD COLUMN substituted text")
			case "active_index":
				run(t.Context(), t, f.target, "DROP INDEX gregale_copy_database_verification.windows_one_active")
				run(t.Context(), t, f.target, "CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_verification.windows ((1)) WHERE state='closed'")
			}
			if got, e := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize); got != (VerificationClosure{}) || !errors.Is(e, pgerrors.ErrConflict) {
				t.Fatal("substituted ownership repaired/adopted", e)
			}
			var state string
			if e := f.target.QueryRow(t.Context(), "SELECT state FROM gregale_copy_database_verification.windows WHERE source_oid=$1", r.sourceOID).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if state != "open" {
				t.Fatal("foreign/damaged journal mutated")
			}
			// Cleanup only fixture admission; public recovery may not adopt this evidence.
			d, _, _ := r.plan.creationDatabase(r.sourceOID)
			run(t.Context(), t, f.targetRoot, "ALTER DATABASE "+pgx.Identifier{d.Name}.Sanitize()+" ALLOW_CONNECTIONS false")
		})
	}
}

func TestCopyDatabaseVerificationReadOnlyChecksIdentityPlacementAndTransaction(t *testing.T) {
	for _, mode := range []string{"wrong_child", "nil_placement", "precheck", "postcheck", "changed_role", "cancelled", "committed_callback", "read_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixtureConfigured(t, func(f *fixture) {
				if mode == "changed_role" {
					for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
						run(t.Context(), t, root, "GRANT "+pgx.Identifier{f.dataOwner}.Sanitize()+" TO "+pgx.Identifier{f.owner}.Sanitize()+" WITH INHERIT TRUE, SET TRUE")
					}
				}
			})
			r := f.prepare(t, f.ordinaryOID)
			imported, _ := verificationImport(t, f, r)
			calls := 0
			got, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, uuid.New(), f.authorize, func(ctx context.Context, target VerificationTarget) error {
				child, _ := target.TargetForWorker()
				c := maintenanceChild(ctx, t, f, child)
				defer c.Close(context.WithoutCancel(ctx))
				place := verificationPlacement(f, c, child)
				placeCalls := 0
				var placement copyarchive.RestorePlacement = func(ctx context.Context, c *pgx.Conn, got copyarchive.RestoreTarget) error {
					placeCalls++
					if (mode == "precheck" && placeCalls == 1) || (mode == "postcheck" && placeCalls == 2) {
						return fmt.Errorf("private-provider-token: %w", pgerrors.ErrConflict)
					}
					return place(ctx, c, got)
				}
				input := c
				if mode == "wrong_child" {
					input = f.targetRoot
				}
				if mode == "nil_placement" {
					placement = nil
				}
				readCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				err := target.WithReadOnly(readCtx, input, placement, func(ctx context.Context, tx pgx.Tx) error {
					calls++
					switch mode {
					case "changed_role":
						_, err := tx.Exec(ctx, "SET ROLE "+pgx.Identifier{f.dataOwner}.Sanitize())
						return err
					case "cancelled":
						cancel()
						return ctx.Err()
					case "committed_callback":
						return tx.Commit(ctx)
					case "read_failure":
						return fmt.Errorf("private-table-value: %w", pgerrors.ErrConflict)
					}
					return nil
				})
				if err == nil || strings.Contains(err.Error(), "private-") {
					t.Fatal("read guard accepted failure or leaked credentials", err)
				}
				if mode == "wrong_child" || mode == "nil_placement" || mode == "precheck" {
					if calls != 0 {
						t.Fatal("invalid child/placement invoked read")
					}
				}
				// SET ROLE survives rollback only if the callback ended its transaction;
				// the dedicated child is always closed before outer bounded closure.
				return err
			})
			if got != (VerificationClosure{}) || e == nil {
				t.Fatal("unqualified inspection minted closure", e)
			}
			allow, _, _, state, _ := verificationStatus(t.Context(), t, f, r)
			if allow || state != "closed" {
				t.Fatal("failed read helper left admission")
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}

type verificationLostReplyTracer struct {
	phase, armed string
	fired        bool
}

func (tr *verificationLostReplyTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "pg_temp.gregale_copy_database_verification_change(") && len(d.Args) > 7 {
		tr.armed, _ = d.Args[7].(string)
	}
	return context.WithValue(ctx, maintenanceCommitTraceKey{}, strings.EqualFold(strings.TrimSpace(d.SQL), "commit"))
}
func (tr *verificationLostReplyTracer) TraceQueryEnd(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryEndData) {
	if commit, _ := ctx.Value(maintenanceCommitTraceKey{}).(bool); commit && d.Err == nil && tr.armed == tr.phase && !tr.fired {
		tr.fired = true
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = c.PgConn().Close(cleanup)
	}
}
func TestCopyDatabaseVerificationRecoversLostOpeningAndClosureReplyWithoutReadReplay(t *testing.T) {
	for _, phase := range []string{"open", "finish"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			imported, original := verificationImport(t, f, r)
			owner := uuid.New()
			cfg := f.target.Config().Copy()
			_ = f.target.Close(context.Background())
			tr := &verificationLostReplyTracer{phase: phase}
			cfg.Tracer = tr
			var e error
			f.target, e = pgx.ConnectConfig(t.Context(), cfg)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			got, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil })
			if got != (VerificationClosure{}) || e == nil || !tr.fired {
				t.Fatal("lost verification reply returned success", e)
			}
			cfg.Tracer = nil
			f.target, e = pgx.ConnectConfig(t.Context(), cfg)
			if e != nil {
				t.Fatal(e)
			}
			allow, _, _, state, at := verificationStatus(t.Context(), t, f, r)
			if phase == "open" && (!allow || state != "open" || calls != 0) {
				t.Fatal("unknown open replayed read")
			}
			if phase == "finish" && (allow || state != "closed" || calls != 1) {
				t.Fatal("unknown finish lost ownership")
			}
			closure, e := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize)
			if e != nil || closure.ClosedAt().IsZero() || (phase == "finish" && !at.Equal(closure.ClosedAt())) {
				t.Fatal("original verification closure lost", e)
			}
			got, e = r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil })
			want := 0
			if phase == "finish" {
				want = 1
			}
			if got != (VerificationClosure{}) || !errors.Is(e, pgerrors.ErrConflict) || calls != want {
				t.Fatal("uncertain verification replayed read", e)
			}
			parent, e := r.CloseMaintenance(t.Context(), f.target, f.exports, imported, f.authorize)
			if e != nil || parent != original {
				t.Fatal("unknown verification changed import", e)
			}
		})
	}
}

func TestCopyDatabaseVerificationSerializesConcurrentWorkersAndRechecksAuthority(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			imported, _ := verificationImport(t, f, r)
			owner := uuid.New()
			cfg := f.target.Config().Copy()
			second, e := pgx.ConnectConfig(t.Context(), cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer second.Close(context.Background())
			v, e := newVerification(r, f.target, f.exports, imported, owner, f.authorize)
			if e != nil {
				t.Fatal(e)
			}
			if e = v.lock(t.Context()); e != nil {
				t.Fatal(e)
			}
			entered := make(chan struct{}, 1)
			done := make(chan error, 1)
			var calls atomic.Int32
			var expired atomic.Bool
			auth := func(ctx context.Context, target copyarchive.RestoreTarget) error {
				select {
				case entered <- struct{}{}:
				default:
				}
				if expired.Load() {
					return pgerrors.ErrConflict
				}
				return f.authorize(ctx, target)
			}
			go func() {
				_, err := r.WithVerificationAccess(t.Context(), second, f.exports, imported, owner, auth, func(context.Context, VerificationTarget) error { calls.Add(1); return nil })
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("waiter did not reach authorization")
			}
			waitCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			for {
				var blocked bool
				if e = f.targetRoot.QueryRow(waitCtx, "SELECT wait_event='advisory' FROM pg_stat_activity WHERE pid=$1", second.PgConn().PID()).Scan(&blocked); e == nil && blocked {
					break
				}
				if waitCtx.Err() != nil {
					releaseLock(t.Context(), f.target, v.q)
					t.Fatal("second verifier never waited on actual lock", e)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if stale {
				expired.Store(true)
			} else {
				_, _, parent, err := v.read(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if err = v.open(t.Context(), parent); err != nil {
					t.Fatal(err)
				}
			}
			releaseLock(t.Context(), f.target, v.q)
			select {
			case err := <-done:
				if !errors.Is(err, pgerrors.ErrConflict) || calls.Load() != 0 {
					t.Fatal("waiter repeated/stale read", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("waiter did not finish")
			}
			allow, _, _, state, _ := verificationStatus(t.Context(), t, f, r)
			if allow || (stale && state != "") || (!stale && state != "closed") {
				t.Fatal("waiter did not preserve/close original window", state)
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}

func TestCopyDatabaseVerificationQuiescesBeforeCatalogueAndSeedDrift(t *testing.T) {
	for _, mode := range []string{"unselected_database", "role_seed"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			imported, _ := verificationImport(t, f, r)
			owner := uuid.New()
			got, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize, func(ctx context.Context, _ VerificationTarget) error {
				if mode == "unselected_database" {
					run(ctx, t, f.targetRoot, "ALTER DATABASE "+pgx.Identifier{f.bootstrap}.Sanitize()+" CONNECTION LIMIT 4")
				} else {
					run(ctx, t, f.targetRoot, "ALTER ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" CREATEDB")
				}
				return nil
			})
			if got != (VerificationClosure{}) || !errors.Is(e, pgerrors.ErrConflict) {
				t.Fatal("catalogue/seed drift received successful closure", e)
			}
			allow, _, _, state, _ := verificationStatus(t.Context(), t, f, r)
			if allow || state != "closing" {
				t.Fatal("drift was not quiesced before validation")
			}
			if mode == "unselected_database" {
				run(t.Context(), t, f.targetRoot, "ALTER DATABASE "+pgx.Identifier{f.bootstrap}.Sanitize()+" CONNECTION LIMIT -1")
			} else {
				run(t.Context(), t, f.targetRoot, "ALTER ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" NOCREATEDB")
			}
			c, e := r.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize)
			if e != nil || c.ClosedAt().IsZero() {
				t.Fatal("original owner could not recover resolved drift", e)
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}

func TestCopyDatabaseVerificationSerializesDifferentDatabasesUntilOriginalClosure(t *testing.T) {
	f := newFixture(t)
	first, second := f.prepare(t, f.ordinaryOID), f.prepare(t, f.templateOID)
	importFirst, _ := verificationImport(t, f, first)
	importSecond, _ := verificationImport(t, f, second)
	ownerFirst, ownerSecond := uuid.New(), uuid.New()
	verificationCrash(t, f, first, importFirst, ownerFirst)
	calls := 0
	if got, e := second.WithVerificationAccess(t.Context(), f.target, f.exports, importSecond, ownerSecond, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil }); got != (VerificationClosure{}) || !errors.Is(e, pgerrors.ErrConflict) || calls != 0 {
		t.Fatal("second verification opened before original closure", e)
	}
	if _, e := first.CloseVerificationAccess(t.Context(), f.target, f.exports, importFirst, ownerFirst, f.authorize); e != nil {
		t.Fatal(e)
	}
	if c, e := second.WithVerificationAccess(t.Context(), f.target, f.exports, importSecond, ownerSecond, f.authorize, func(context.Context, VerificationTarget) error { calls++; return nil }); e != nil || calls != 1 || c.ClosedAt().IsZero() {
		t.Fatal("second verification did not progress after closure", e)
	}
	assertMaintenanceOriginal(t, f, first)
	assertMaintenanceOriginal(t, f, second)
}
