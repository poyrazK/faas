package managedpostgres

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresCutoverPreflightDurableRetryIsolationAndCancellation(t *testing.T) {
	s, _, c, _ := admissionCutoverFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			p, err := s.RequestCutoverPreflight(t.Context(), c.AccountID, c.ID, now)
			if err != nil || p.State != "pending" || p.Revision != 1 {
				t.Error("concurrent request", p, err)
			}
		})
	}
	wg.Wait()
	// A fresh adapter discovers and finishes the durable request after restart.
	restarted, err := NewPostgresStore(s.pool)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := restarted.PendingCutoverPreflights(t.Context(), 1)
	if err != nil || len(rows) != 1 || rows[0].ID != c.ID {
		t.Fatal(rows, err)
	}
	p, err := restarted.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, now)
	if err != nil || p.State != "blocked" || p.Revision != 1 || !p.SourceMatches || !p.TargetMatches || !p.BindingsMatch || !p.VerificationFresh || len(p.BindingIDs) != 2 || !slices.Contains(p.Blockers, "activation_disabled") {
		t.Fatal(p, err)
	}
	for _, account := range []string{uuid.NewString()} {
		if _, err = s.GetCutoverPreflight(t.Context(), account, c.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("tenant read leak", err)
		}
		if _, err = s.RequestCutoverPreflight(t.Context(), account, c.ID, now); !errors.Is(err, ErrNotFound) {
			t.Fatal("tenant mutation leak", err)
		}
		if _, err = s.EvaluateCutoverPreflight(t.Context(), account, c.ID, now); !errors.Is(err, ErrNotFound) {
			t.Fatal("tenant evaluation leak", err)
		}
	}
	p, err = s.RequestCutoverPreflight(t.Context(), c.AccountID, c.ID, now.Add(time.Second))
	if err != nil || p.Revision != 2 || !p.CheckedAt.IsZero() || p.VerificationFresh || len(p.Blockers) != 0 {
		t.Fatal("stale evidence retained", p, err)
	}
	// A failed completion must leave the durable request available for recovery.
	if _, err = s.pool.Exec(t.Context(), `CREATE FUNCTION reject_preflight_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected write failure'; END $$;
CREATE TRIGGER reject_preflight_completion BEFORE UPDATE ON managed_postgres_cutover_preflights FOR EACH ROW EXECUTE FUNCTION reject_preflight_completion()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, now); err == nil {
		t.Fatal("write failure ignored")
	}
	p, err = s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || p.State != "pending" || !p.CheckedAt.IsZero() || p.Revision != 2 {
		t.Fatal("failed evaluation lost request", p, err)
	}
	if _, err = s.pool.Exec(t.Context(), `DROP TRIGGER reject_preflight_completion ON managed_postgres_cutover_preflights; DROP FUNCTION reject_preflight_completion()`); err != nil {
		t.Fatal(err)
	}
	p, err = s.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, now.Add(CutoverVerificationMaxAge+time.Second))
	if err != nil || p.VerificationFresh || !slices.Contains(p.Blockers, "verification_required") {
		t.Fatal("expired evidence accepted", p, err)
	}
	if _, err = s.RequestCutoverPreflight(t.Context(), c.AccountID, c.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelCutover(t.Context(), c.AccountID, c.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	p, err = s.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, now.Add(time.Second))
	if err != nil || !slices.Contains(p.Blockers, "cutover_inactive") {
		t.Fatal("cancellation lost", p, err)
	}
	if _, err = s.RequestCutoverPreflight(t.Context(), c.AccountID, c.ID, now); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled cutover refreshed", err)
	}
	var fenced bool
	if err = s.pool.QueryRow(t.Context(), `SELECT managed_postgres_admission_cutover_id IS NOT NULL FROM apps WHERE id=$1`, c.AppID).Scan(&fenced); err != nil || fenced {
		t.Fatal("preflight fenced admissions", err)
	}
	for _, m := range c.Credentials {
		b, err := s.GetBinding(t.Context(), c.AccountID, m.SourceBindingID)
		if err != nil || b.DatabaseID != c.Source.ID {
			t.Fatal("preflight switched bindings", b, err)
		}
	}
}
