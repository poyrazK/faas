package financialtest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// adr: 1430, 1642 — ledger recovery preserves USD funding, while an untagged
// pre-release book cannot be silently converted to a different currency.
func TestFinancialFundingCurrencyRecovery(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	f := newEgressFixture(t, store, 0, 0, 1000)
	if _, err := store.AcquireFinancialCompute(ctx, f.request(0)); err != nil {
		t.Fatal(err)
	}
	before := f.balance(t)
	paths, err := filepath.Glob("../../../migrations/*_financial_funding_usd.sql")
	if err != nil || len(paths) != 1 {
		t.Fatalf("USD funding migration: %v, %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(data), "-- +goose Down")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("replay USD funding migration: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after := f.balance(t)
	after.ObservedAt = before.ObservedAt
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("replay changed funding: before=%+v after=%+v", before, after)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "DELETE FROM financial_funding_currency"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, up)
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "require explicit forward repair") {
		t.Fatalf("untagged funding was not refused: %v", err)
	}
}
