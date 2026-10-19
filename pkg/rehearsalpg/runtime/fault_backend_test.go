// adr: 20261018000200
package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresFaultCutsOnlyItsDatabaseAndRollsBackOpenTransaction(t *testing.T) {
	p, s := newPostgresTest(t)
	a, c := createPostgresTest(t, s)
	b, sibling := createPostgresTest(t, s)
	ctx := t.Context()
	r, err := s.Catalog.ScenarioPostgresForApp(ctx, a.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Exec(ctx, "INSERT INTO public.imports VALUES ('committed')"); err != nil {
		t.Fatal(err)
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO public.imports VALUES ('uncommitted')"); err != nil {
		t.Fatal(err)
	}
	config := c.Config()
	authorize := func(ctx context.Context) error {
		// Recovery must not overtake the authority check and its ensuing cut.
		// A separate admin connection should wait for this physical lock.
		probe, cancel := context.WithTimeout(ctx, 75*time.Millisecond)
		defer cancel()
		if err := p.RecoverPostgres(probe, r); err == nil {
			t.Fatal("recovery overtook the cut's authority check")
		}
		return nil
	}
	closed, err := p.CutPostgres(ctx, r, authorize)
	if err != nil || closed < 1 {
		t.Fatalf("disconnect was not confirmed: %d %v", closed, err)
	}
	if _, err = tx.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("old transaction survived disconnect")
	}
	denied, err := pgx.ConnectConfig(ctx, config)
	if err == nil {
		_ = denied.Close(ctx)
		t.Fatal("reconnect was accepted during outage")
	}
	if _, err = sibling.Exec(ctx, "INSERT INTO public.imports VALUES ('sibling')"); err != nil {
		t.Fatalf("another run was disrupted: %v", err)
	}
	if b.ResourceID == a.ResourceID {
		t.Fatal("fixtures shared a database")
	}
	if err = p.RecoverPostgres(ctx, r); err != nil {
		t.Fatal(err)
	}
	recovered, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatalf("same credentials did not recover: %v", err)
	}
	defer func() { _ = recovered.Close(ctx) }()
	var committed, rolledBack int
	if err = recovered.QueryRow(ctx, "SELECT count(*) FILTER (WHERE item='committed'), count(*) FILTER (WHERE item='uncommitted') FROM public.imports").Scan(&committed, &rolledBack); err != nil || committed != 1 || rolledBack != 0 {
		t.Fatalf("transaction boundary changed: %d/%d %v", committed, rolledBack, err)
	}
	foreign := r
	foreign.BackendSHA256 = "another backend"
	if _, err = p.CutPostgres(ctx, foreign, authorize); err == nil {
		t.Fatal("foreign backend resource was accepted")
	}
	foreign = r
	foreign.DatabaseName = "postgres"
	if _, err = p.CutPostgres(ctx, foreign, authorize); err == nil {
		t.Fatal("administrative database was accepted")
	}
	if _, err = p.CutPostgres(ctx, r, func(context.Context) error { return ErrFixture }); err == nil {
		t.Fatal("abandoned disconnect was accepted")
	}
	if _, err = recovered.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("stale disconnect interrupted the recovered connection: %v", err)
	}
}
