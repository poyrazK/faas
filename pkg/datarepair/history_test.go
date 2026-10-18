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

func TestRepairHistoryRecovery45941AndCompaction(t *testing.T) {
	e, s, source := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	parent := approved(t, e, s, source)
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	failed := uuid.NewString()
	if _, err := e.Apply(ctx, s, parent.ID, parent.Digest, failed, "faulty-attempt"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale correction was not refused", err)
	}
	page, err := e.History(ctx, s, source, HistoryQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ApprovedAt == nil || page.Items[0].AppliedAt != nil || page.Items[0].ApplyResolution != nil {
		t.Fatal("absence of receipt manufactured an outcome", page, err)
	}
	if _, err := e.Resolve(ctx, s, parent.ID, parent.Digest, failed, "reviewer"); err != nil {
		t.Fatal(err)
	}
	child, err := e.Replace(ctx, s, parent.ID, parent.Digest, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Rehearse(ctx, s, child.ID, child.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(ctx, s, child.ID, child.Digest, "replacement-reviewer"); err != nil {
		t.Fatal(err)
	}
	committed := applyVerificationFixture(t, e, s, child)
	// History must not run current business SQL or require a live business schema.
	e.CheckSQL = "invalid policy which must never execute"
	check := func(purged bool) {
		t.Helper()
		beforeParent, beforeChild := validationState(t, e, parent.ID), validationState(t, e, child.ID)
		q := HistoryQuery{Limit: 1}
		first, err := e.History(t.Context(), s, source, q)
		if err != nil || !first.Valid(1) || len(first.Items) != 1 || first.NextCursor == "" {
			t.Fatal("cannot discover child using only the source ID", first, err)
		}
		item := first.Items[0]
		if item.ID != child.ID || item.Digest != child.Digest || item.Candidates != 459 || item.Conflicts != 41 || item.AppliedAt == nil || item.RepairOperationID == nil ||
			*item.RepairOperationID != *committed.RepairOperationID || item.Replacement == nil || item.Replacement.ParentPlanID != parent.ID || (item.PayloadPurgedAt != nil) != purged {
			t.Fatal("replacement receipt/lineage lost", item)
		}
		q.Cursor = first.NextCursor
		second, err := e.History(t.Context(), s, source, q)
		if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
			t.Fatal("cannot follow parent history", second, err)
		}
		item = second.Items[0]
		if item.ID != parent.ID || item.Candidates != 460 || item.Conflicts != 40 || item.ApprovedAt == nil || item.ApplyResolution == nil || item.ApplyResolution.RepairOperationID != failed ||
			item.ReplacementPlanID != child.ID || item.AppliedAt != nil || (item.PayloadPurgedAt != nil) != purged {
			t.Fatal("parent closure or fence lost", item)
		}
		raw, _ := json.Marshal([]HistoryPage{first, second})
		for _, secret := range []string{`"items":[{"row_identity"`, `"before"`, `"after"`, `"category"`, `"original"`, `"human"`, `"key"`, "SELECT", "invalid policy"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("history disclosed business payload or SQL", secret)
			}
		}
		if beforeParent != validationState(t, e, parent.ID) || beforeChild != validationState(t, e, child.ID) {
			t.Fatal("history changed rows, journals or plan lifecycle")
		}
		loaded, err := e.Get(t.Context(), s, first.Items[0].ID)
		if err != nil || loaded.Digest != first.Items[0].Digest {
			t.Fatal("discovered identity cannot be reopened", err)
		}
		report, err := e.Verify(t.Context(), s, loaded.ID, loaded.Digest)
		if err != nil || report.Restored != 459 || report.Excluded != 41 {
			t.Fatal("discovered receipt cannot be verified", report, err)
		}
	}
	check(false)
	ageRetentionFixture(t, e)
	retentionPurge(t, e, s, retentionPreview(t, e, s, api.DataRepairPurgeMaxItems))
	check(true)
	retentionPurge(t, e, s, retentionPreview(t, e, s, api.DataRepairPurgeMaxItems))
	check(true)
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE products ADD COLUMN changed_schema text`); err != nil {
		t.Fatal(err)
	}
	check(true)
}

func TestRepairHistoryPaginationScopeAndUpgrade(t *testing.T) {
	e, s, source := fixture(t, 2)
	ctx := t.Context()
	empty, err := e.History(ctx, s, source, HistoryQuery{})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != "" {
		t.Fatal("known source without plans should have empty history", empty, err)
	}
	want := []string{}
	for range 4 {
		p, err := e.Plan(ctx, s, source)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, p.ID)
	}
	otherSource := captureDiscoveryFixture(t, e, s, "other-release")
	if _, err := e.Plan(ctx, s, otherSource); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE gregale_repair.plans SET created_at='2026-10-01T12:00:00.123456Z';
DROP FUNCTION gregale_repair.list_history(uuid,jsonb,timestamptz,uuid,integer)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(want)
	slices.Reverse(want)
	q := HistoryQuery{Limit: 1}
	first, err := e.History(ctx, s, source, q)
	if err != nil || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	for _, other := range []Scope{
		{AccountID: uuid.NewString(), AppID: s.AppID, Environment: s.Environment},
		{AccountID: s.AccountID, AppID: uuid.NewString(), Environment: s.Environment},
		{AccountID: s.AccountID, AppID: s.AppID, Environment: "staging"},
		{AccountID: s.AccountID, AppID: s.AppID, Environment: s.Environment, PlatformTenantID: uuid.NewString()},
	} {
		if _, err := e.History(ctx, other, source, HistoryQuery{}); !errors.Is(err, ErrNotFound) {
			t.Fatal("history crossed scope", other, err)
		}
		if _, err := e.History(ctx, other, source, HistoryQuery{Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrInvalid) {
			t.Fatal("cursor crossed scope", err)
		}
	}
	for _, query := range []HistoryQuery{{Limit: 2, Cursor: first.NextCursor}, {Cursor: first.NextCursor}, {Cursor: "bad"}, {Limit: -1}, {Limit: 101}} {
		if _, err := e.History(ctx, s, source, query); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid query accepted", query, err)
		}
	}
	if _, err := e.History(ctx, s, otherSource, HistoryQuery{Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor crossed source", err)
	}
	if _, err := e.History(ctx, s, uuid.NewString(), HistoryQuery{}); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown source presented as empty history", err)
	}
	newPlan, err := e.Plan(ctx, s, source)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{first.Items[0].ID}
	q.Cursor = first.NextCursor
	for q.Cursor != "" {
		p, err := e.History(ctx, s, source, q)
		if err != nil || len(p.Items) != 1 || p.Items[0].ID == newPlan.ID {
			t.Fatal("pagination shifted under new plan", p, err)
		}
		got = append(got, p.Items[0].ID)
		q.Cursor = p.NextCursor
	}
	if !slices.Equal(got, want) {
		t.Fatal("history skipped or duplicated a tied timestamp", got, want)
	}
	// The SQL read contract is usable in an explicitly read-only transaction.
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT gregale_repair.list_history($1::uuid,$2::jsonb,NULL,NULL,1)`, source, scopeJSON(s)).Scan(&raw); err != nil {
		t.Fatal("history attempted a write", err)
	}
}

func TestRepairHistoryRejectsMalformedMetadata(t *testing.T) {
	s := Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	p := HistoryPage{Scope: s, SourceOperationID: uuid.NewString(), CheckedAt: time.Now(), Items: []HistoryItem{{ID: uuid.NewString(), Digest: strings.Repeat("a", 64), CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Candidates: 1}}}
	if !p.Valid(1) {
		t.Fatal("valid metadata rejected")
	}
	for _, mutate := range []func(*HistoryPage){
		func(p *HistoryPage) { p.Items[0].Digest = "bad" },
		func(p *HistoryPage) { p.Items[0].Candidates = -1 },
		func(p *HistoryPage) { p.Items[0].Conflicts = api.DataRepairMaxRows },
		func(p *HistoryPage) { now := time.Now(); p.Items[0].AppliedAt = &now },
		func(p *HistoryPage) { p.Items[0].ApplyResolution = &ApplyResolution{} },
		func(p *HistoryPage) { p.Items[0].Replacement = &Replacement{} },
		func(p *HistoryPage) { p.Items[0].ReplacementPlanID = p.Items[0].ID },
		func(p *HistoryPage) {
			p.Items[0].Execution = &HistoryExecution{Availability: "not_retained", Phase: api.DataRepairSucceeded}
		},
		func(p *HistoryPage) { p.Items = nil },
		func(p *HistoryPage) { p.NextCursor = strings.Repeat("x", api.DataRepairSourceMaxCursorBytes+1) },
	} {
		changed := p
		changed.Items = slices.Clone(p.Items)
		mutate(&changed)
		if changed.Valid(1) {
			t.Fatal("malformed history could mislead an operator", changed)
		}
	}
}

func TestRepairHistoryExistingStandaloneIdentityNamespaces(t *testing.T) {
	e, s, source := fixture(t, 1)
	// Standalone preparation has always accepted independently namespaced plan
	// and source IDs. Discovery must not hide a plan accepted by that contract.
	p, err := e.Prepare(t.Context(), s, source, source)
	if err != nil {
		t.Fatal(err)
	}
	history, err := e.History(t.Context(), s, source, HistoryQuery{})
	if err != nil || len(history.Items) != 1 || history.Items[0].ID != p.ID || history.Items[0].Digest != p.Digest {
		t.Fatal("history rejected an existing standalone plan identity", history, err)
	}
}
