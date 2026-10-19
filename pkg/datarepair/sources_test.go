package datarepair

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func captureDiscoveryFixture(t *testing.T, e *Engine, s Scope, release string) string {
	t.Helper()
	id := uuid.NewString()
	tx, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if err := Capture(t.Context(), tx, s, id, release, "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE products SET category=CASE WHEN category='bad' THEN 'original' ELSE 'bad' END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRepairSourceDiscoveryScopePaginationAndFilters(t *testing.T) {
	e, scope, first := fixture(t, 2)
	ctx := t.Context()
	want := []string{first}
	for range 3 {
		want = append(want, captureDiscoveryFixture(t, e, scope, "buggy-import-v1"))
	}
	for _, change := range []func(*Scope){
		func(s *Scope) { s.AccountID = uuid.NewString() },
		func(s *Scope) { s.AppID = uuid.NewString() },
		func(s *Scope) { s.Environment = "staging" },
		func(s *Scope) { s.PlatformTenantID = uuid.NewString() },
	} {
		other := scope
		change(&other)
		captureDiscoveryFixture(t, e, other, "buggy-import-v1")
	}
	// Private-fixture timestamp ties prove the UUID tie-breaker, without ever
	// changing business row identities/versions or adding a production bypass.
	if _, err := e.Pool.Exec(ctx, `BEGIN;
ALTER TABLE gregale_repair.operations DISABLE TRIGGER gregale_repair_protect_operation;
UPDATE gregale_repair.operations SET created_at='2026-10-01T12:00:00.123456Z';
ALTER TABLE gregale_repair.operations ENABLE TRIGGER gregale_repair_protect_operation;
COMMIT;`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE other_products(id int PRIMARY KEY,category text NOT NULL);
SELECT gregale_repair.enroll('other_products',ARRAY['category']); INSERT INTO other_products(id,category) VALUES(1,'original')`); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Capture(ctx, tx, scope, uuid.NewString(), "buggy-import-v1", "other_products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE other_products SET category='bad'`)
		return err
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	slices.Reverse(want)
	q := SourceQuery{Limit: 1, Release: "buggy-import-v1", Table: "public.products"}
	p, err := e.Sources(ctx, scope, q)
	if err != nil || len(p.Items) != 1 || p.NextCursor == "" || p.Items[0].ID != want[0] {
		t.Fatalf("first page: %+v %v", p, err)
	}
	for _, modify := range []func(*SourceQuery){
		func(q *SourceQuery) { q.Release = "other" },
		func(q *SourceQuery) { q.Table = "other.products" },
		func(q *SourceQuery) { q.Limit = 2 },
		func(q *SourceQuery) { q.Since = time.Now() },
	} {
		changed := q
		changed.Cursor = p.NextCursor
		modify(&changed)
		if _, err := e.Sources(ctx, scope, changed); !errors.Is(err, ErrInvalid) {
			t.Fatalf("cursor accepted different filters: %v", err)
		}
	}
	other := scope
	other.Environment = "staging"
	changed := q
	changed.Cursor = p.NextCursor
	if _, err := e.Sources(ctx, other, changed); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor crossed scope: %v", err)
	}
	// A newly captured operation above the current boundary does not shift
	// subsequent pages or duplicate items already shown.
	captureDiscoveryFixture(t, e, scope, "buggy-import-v1")
	got := []string{p.Items[0].ID}
	for p.NextCursor != "" {
		q.Cursor = p.NextCursor
		p, err = e.Sources(ctx, scope, q)
		if err != nil || len(p.Items) > 1 {
			t.Fatalf("continuation: %+v %v", p, err)
		}
		for _, item := range p.Items {
			got = append(got, item.ID)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pagination skipped/duplicated/crossed scope: %v want %v", got, want)
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 123456000, time.UTC)
	for _, tc := range []struct {
		q SourceQuery
		n int
	}{
		{SourceQuery{Since: at, Before: at.Add(time.Microsecond)}, 4},
		{SourceQuery{Before: at}, 0},
		{SourceQuery{Release: "buggy%"}, 0},
		{SourceQuery{Table: "products"}, 0},
		{SourceQuery{Table: "public.other_products"}, 1},
	} {
		page, err := e.Sources(ctx, scope, tc.q)
		if err != nil || len(page.Items) != tc.n {
			t.Fatalf("filter %+v: %+v %v", tc.q, page, err)
		}
	}
	page, err := e.Sources(ctx, scope, SourceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	for _, secret := range []string{`"before"`, `"after"`, `"category"`, `"original"`, `"bad"`, `"key"`} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("discovery exposed row payload %s", secret)
		}
	}
	var plans int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM gregale_repair.plans`).Scan(&plans); err != nil || plans != 0 {
		t.Fatalf("discovery prepared a plan: %d %v", plans, err)
	}
}

func TestRepairSourceDiscoveryUpgradeAndPurgedReceipts(t *testing.T) {
	e, scope, source := fixture(t, 3)
	p := approved(t, e, scope, source)
	repairID := uuid.NewString()
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, repairID, "repair-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), `DROP FUNCTION gregale_repair.list_sources(jsonb,text,text,timestamptz,timestamptz,timestamptz,uuid,integer)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	check := func(purged bool) {
		t.Helper()
		page, err := e.Sources(t.Context(), scope, SourceQuery{Release: "buggy-import-v1"})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("source inventory: %+v %v", page, err)
		}
		item := page.Items[0]
		if item.ID != source || item.AppliedRepairs != 1 || item.LatestRepair == nil || item.LatestRepair.PlanID != p.ID || item.LatestRepair.OperationID != repairID {
			t.Fatalf("receipt missing: %+v", item)
		}
		if purged {
			if item.ChangeRows != nil || item.PayloadPurgedAt == nil || item.CanPrepare {
				t.Fatalf("purged payload presented as available: %+v", item)
			}
		} else if item.ChangeRows == nil || *item.ChangeRows != 3 || !item.CanPrepare {
			t.Fatalf("recorded changes missing: %+v", item)
		}
	}
	check(false)
	ageRetentionFixture(t, e)
	retentionPurge(t, e, scope, retentionPreview(t, e, scope, api.DataRepairPurgeMaxItems))
	retentionPurge(t, e, scope, retentionPreview(t, e, scope, api.DataRepairPurgeMaxItems))
	check(true)
	retained, err := e.Get(t.Context(), scope, p.ID)
	if err != nil || retained.Digest != p.Digest || retained.RepairOperationID == nil || *retained.RepairOperationID != repairID {
		t.Fatalf("upgrade/discovery changed receipt: %+v %v", retained, err)
	}
}

func TestRepairSourceDiscoveryRejectsMalformedQueries(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	for _, q := range []SourceQuery{
		{Limit: -1}, {Limit: api.DataRepairSourceMaxPageSize + 1},
		{Release: "\x00"}, {Table: string([]byte{0xff})},
		{Cursor: "not-a-cursor"}, {Cursor: strings.Repeat("x", api.DataRepairSourceMaxCursorBytes+1)},
		{Since: time.Now(), Before: time.Now().Add(-time.Hour)},
	} {
		if _, err := e.Sources(t.Context(), scope, q); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid query %+v: %v", q, err)
		}
	}
}
