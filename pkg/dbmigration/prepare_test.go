package dbmigration

import (
	"slices"
	"strings"
	"testing"
)

func TestPreparationPlansOnlySupportedIndexes(t *testing.T) {
	valid := Index{Name: "orders_status_idx", Table: "orders", Columns: []string{"status", "created_at"}, Supported: true, Valid: true}
	invalid := valid
	invalid.Name, invalid.Valid = "invalid_idx", false
	unsafe := valid
	unsafe.Columns = []string{`created_at"); DROP TABLE orders; --`}
	unsupported := valid
	unsupported.Name, unsupported.Supported = "unique_idx", false
	inv := PreparationInventory{Indexes: []Index{valid, invalid, unsafe, unsupported}, Constraints: 2, Sequences: 1}
	p, plan := inv.Plan()
	if p.IndexesTotal != 1 || len(plan) != 1 || !plan[0].SameDefinition(valid) || p.Sequences != 1 {
		t.Fatalf("preparation = %+v, plan = %+v", p, plan)
	}
	want := []PreparationBlocker{{Code: "source_secondary_indexes_unsupported", Count: 3}, {Code: "source_constraints", Count: 2}}
	if !slices.Equal(p.Blockers, want) {
		t.Fatalf("blockers = %+v, want %+v", p.Blockers, want)
	}
	sql, err := plan[0].BuildSQL()
	if err != nil || !strings.Contains(sql, `CREATE INDEX CONCURRENTLY "orders_status_idx" ON public."orders" USING btree ("status", "created_at");`) {
		t.Fatalf("index build = %q, %v", sql, err)
	}
}

func TestInterruptedIndexRequiresExactDefinition(t *testing.T) {
	before := Index{Name: "orders_status_idx", Table: "orders", Columns: []string{"status", "created_at"}, Supported: true, Valid: true, Bytes: 8000}
	interrupted := before
	interrupted.Valid, interrupted.Bytes = false, 0
	if !before.SameDefinition(interrupted) {
		t.Fatal("partial build lost its definition")
	}
	interrupted.Columns = []string{"created_at", "status"}
	if before.SameDefinition(interrupted) {
		t.Fatal("different index key order adopted")
	}
	interrupted = before
	interrupted.Supported = false
	if before.SameDefinition(interrupted) {
		t.Fatal("unsupported index adopted")
	}
}

func TestPreparationIndexLimitPreventsPartialPlan(t *testing.T) {
	inv := PreparationInventory{Indexes: make([]Index, MaxPreparationIndexes+1)}
	p, plan := inv.Plan()
	if len(plan) != 0 || len(p.Blockers) != 1 || p.Blockers[0].Code != "source_index_limit_exceeded" {
		t.Fatalf("oversized inventory produced %+v, %+v", p, plan)
	}
}
