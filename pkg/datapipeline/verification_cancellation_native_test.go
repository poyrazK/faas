package datapipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNativeVerificationCancellationDuringBillingGrace(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders SELECT i,'original' FROM generate_series(1,20000)i`)
	ctx := t.Context()
	account := state.Account{ID: f.pipeline.AccountID, Plan: api.PlanPro, Status: state.AccountPastDue}
	if !account.Active() || account.MayDeploy() {
		t.Fatal("fixture is not in billing grace")
	}
	f.service.Allowed = func(context.Context, string) bool { return account.Active() }
	f.service.CanCreate = func(context.Context, string) bool { return account.MayDeploy() }
	if _, err := f.service.Control(ctx, account.ID, f.pipeline.ID, api.ControlDataPipelineRequest{ExpectedRevision: f.pipeline.Revision, Action: "verify"}); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	v := f.pipeline.VerificationProgress
	if v == nil || v.State != "running" || v.Rows < 1 {
		t.Fatal("manual verification did not start", v)
	}
	f.service.Allowed = func(context.Context, string) bool { return false }
	if err := f.service.CancelVerification(ctx, account.ID, f.pipeline.ID, f.pipeline.Revision, v.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("operator admission was bypassed", err)
	}
	f.service.Allowed = func(context.Context, string) bool { return account.Active() }
	if err := f.service.CancelVerification(ctx, account.ID, f.pipeline.ID, f.pipeline.Revision, v.ID); err != nil {
		t.Fatal("billing grace prevented stopping a running check", err)
	}
	f.tick(t)
	if f.pipeline.VerificationProgress.State != "cancelled" || f.pipeline.VerifyRequested {
		t.Fatal("cancellation was not retained")
	}
	assertVerificationRemoved(t, f)
}

func assertVerificationRemoved(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	var triggers int
	if err := f.target.QueryRow(t.Context(), `SELECT count(*) FROM pg_trigger WHERE tgrelid='public.orders'::regclass AND tgname IN ('gregale_verify_rows','gregale_verify_truncate')`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	c, err := loadVerification(t.Context(), f.target, f.pipeline)
	if err != nil || c != nil || triggers != 0 {
		t.Fatal("cancelled verification retained tracking", c, triggers, err)
	}
	ranges, fn := verificationNames(f.pipeline, 0)
	var gone bool
	if err = f.target.QueryRow(t.Context(), `SELECT to_regclass($1) IS NULL AND to_regprocedure($2) IS NULL`, ranges, fn+"()").Scan(&gone); err != nil || !gone {
		t.Fatal("cancelled verification retained its range table or function", gone, err)
	}
}

func TestNativeVerificationCancellationWithoutSourceAccess(t *testing.T) {
	for _, unavailable := range []string{"missing-key", "failed-login"} {
		t.Run(unavailable, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders SELECT i,'original' FROM generate_series(1,20000)i`)
			ctx := t.Context()
			f.control(t, "verify")
			f.tick(t)
			v := f.pipeline.VerificationProgress
			if v == nil || v.State != "running" {
				t.Fatal("verification did not start", v)
			}
			if unavailable == "missing-key" {
				key, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				f.worker.Identities = []*age.X25519Identity{key}
			} else if _, err := f.source.Exec(ctx, "ALTER ROLE "+ident(f.role)+" PASSWORD 'changed'"); err != nil {
				t.Fatal(err)
			}
			if err := f.service.CancelVerification(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, v.ID); err != nil {
				t.Fatal(err)
			}
			f.tick(t)
			f.tick(t) // Cleanup is idempotent while source access remains unavailable.
			assertVerificationRemoved(t, f)
			if f.pipeline.VerificationProgress.State != "cancelled" {
				t.Fatal("source error replaced cancellation")
			}
			if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='after stop' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			for until := time.Now().Add(5 * time.Second); ; {
				var value string
				if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				if value == "after stop" {
					break
				}
				if time.Now().After(until) {
					t.Fatal("cancellation interrupted native replication")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestNativeVerificationCancellationCleanupKeepsRunOwnershipAndLeaseFences(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders SELECT i,'original' FROM generate_series(1,20000)i`)
	ctx := t.Context()
	f.control(t, "verify")
	f.tick(t)
	p := f.pipeline
	progress := *p.VerificationProgress
	progress.State = "cancelled"
	p.VerificationProgress = &progress
	checkRetained := func() {
		t.Helper()
		c, err := loadVerification(ctx, f.target, p)
		if err != nil || c == nil || c.Progress.ID != f.pipeline.VerificationProgress.ID {
			t.Fatal("cleanup removed another or unowned proof", c, err)
		}
	}
	f.engine.reconcileUnavailable(ctx, p, func(context.Context) error { return ErrConflict })
	checkRetained()
	wrong := progress
	wrong.ID, wrong.IntentID = strings.Repeat("f", 64), strings.Repeat("e", 64)
	p.VerificationProgress = &wrong
	f.engine.reconcileUnavailable(ctx, p, func(context.Context) error { return nil })
	checkRetained()
	p.VerificationProgress = &progress
	p.SchemaChange = &SchemaChange{Cancellation: &TableCancellation{State: "approved"}}
	f.engine.reconcileUnavailable(ctx, p, func(context.Context) error { return nil })
	checkRetained()
	p.SchemaChange = nil
	ranges, _ := verificationNames(p, 0)
	if _, err := f.target.Exec(ctx, "COMMENT ON TABLE "+ranges+" IS 'foreign'"); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCancelledVerification(ctx, p, f.target, func(context.Context) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal("cleanup adopted foreign tracking", err)
	}
	checkRetained()
	if _, err := f.target.Exec(ctx, "COMMENT ON TABLE "+ranges+" IS "+literal(verificationMarker(p, progress.ID))); err != nil {
		t.Fatal(err)
	}
	// Physical rewrites may change the run ID while preserving its logical intent.
	progress.ID = strings.Repeat("d", 64)
	f.engine.reconcileUnavailable(ctx, p, func(context.Context) error { return nil })
	assertVerificationRemoved(t, f)
}

func TestNativeVerificationCancellationCleanupDoesNotBlockReaderRevocation(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders SELECT i,'original' FROM generate_series(1,20000)i`)
	ctx := t.Context()
	role := nativeReaderRole(t, f)
	approveNativeReaders(t, f, []string{"alice", "bob"})
	f.tick(t)
	f.control(t, "verify")
	f.tick(t)
	v := f.pipeline.VerificationProgress
	if v == nil || v.State != "running" {
		t.Fatal("verification did not start", v)
	}
	if err := f.service.CancelVerification(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, v.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	f.pipeline, err = f.store.Get(ctx, f.pipeline.AccountID, f.pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	approveNativeReaders(t, f, []string{"alice"})
	ranges, _ := verificationNames(f.pipeline, 0)
	if _, err = f.target.Exec(ctx, "COMMENT ON TABLE "+ranges+" IS 'foreign'"); err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.worker.Identities = []*age.X25519Identity{key}
	f.tick(t)
	if nativeReadAs(t, f, role, "bob") != 0 || nativeReadAs(t, f, role, "alice") != 20000 || f.pipeline.ReaderChange.State != "applied" {
		t.Fatal("verification cleanup conflict blocked approved reader revocation")
	}
	if _, err = f.target.Exec(ctx, "COMMENT ON TABLE "+ranges+" IS "+literal(verificationMarker(f.pipeline, v.ID))); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	assertVerificationRemoved(t, f)
}
