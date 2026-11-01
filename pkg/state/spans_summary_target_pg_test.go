//go:build !no_pg

package state_test

// Tracing tenancy (2026-10-31 logs/traces audit, T4/T5): a spans flush
// merges only into the rows its target names.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const spansTargetTrace = "6bf92f3577b34da6a3ce929d0e0e4736"

type spansTargetFixture struct {
	creator, teammate        state.Account
	org                      state.Org
	personal, company, other state.App
	pool                     *pgxpool.Pool
}

// newSpansTargetFixture seeds one trace across three apps: the creator's
// personal app, the creator's company app and a teammate's company app.
// The company apps carry their creators' storage namespaces.
func newSpansTargetFixture(t *testing.T, store *state.PgStore, pool *pgxpool.Pool) spansTargetFixture {
	t.Helper()
	ctx := t.Context()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "spans-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	creator, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "spans-creator@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	teammate, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "spans-teammate@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "spans-team", Name: "Spans Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		id   string
		role state.OrgRole
	}{{owner.Account.ID, state.OrgRoleOwner}, {creator.Account.ID, state.OrgRoleDeveloper}, {teammate.Account.ID, state.OrgRoleDeveloper}} {
		if err := store.AddOrgMember(ctx, org.ID, member.id, member.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	f := spansTargetFixture{creator: creator.Account, teammate: teammate.Account, org: org, pool: pool}
	for _, app := range []struct {
		into    *state.App
		account string
		org     string
		slug    string
	}{
		{&f.personal, creator.Account.ID, creator.PersonalOrg.ID, "spans-personal"},
		{&f.company, creator.Account.ID, org.ID, "spans-company"},
		{&f.other, teammate.Account.ID, org.ID, "spans-teammate"},
	} {
		created, err := store.CreateApp(ctx, state.App{AccountID: app.account, OrgID: app.org, Slug: app.slug})
		if err != nil {
			t.Fatal(err)
		}
		*app.into = created
		if err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: pgtype.UUID{Bytes: uuid.MustParse(app.account), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(created.ID), Valid: true},
			DeploymentID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Route: "GET /orders", Method: "GET",
			Status: 200, LatencyMs: 100, TraceID: pgtype.Text{String: spansTargetTrace, Valid: true},
			ReceivedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, Count: 1,
			UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
		}); err != nil {
			t.Fatalf("insert request telemetry for %s: %v", app.slug, err)
		}
	}
	return f
}

// spansTargetWrite merges one uniquely named span and returns the apps whose
// rows now hold it. A write that matches no row holds nowhere.
func spansTargetWrite(t *testing.T, store *state.PgStore, f spansTargetFixture, target state.SpansSummaryTarget, span string) []string {
	t.Helper()
	target.TraceID = spansTargetTrace
	summary := []byte(fmt.Sprintf(`[{"span_id":%q,"end_time_unix_nano":"1","duration_nanos":1000}]`, span))
	if err := store.UpdateSpansSummary(t.Context(), target, summary, 1000); err != nil && !errors.Is(err, state.ErrRequestTelemetryRowNotFound) {
		t.Fatalf("write %s: %v", span, err)
	}
	slugs := map[string]string{f.personal.ID: "personal", f.company.ID: "company", f.other.ID: "teammate"}
	rows, err := f.pool.Query(t.Context(), `SELECT app_id::text, coalesce(spans_summary, '[]'::jsonb) FROM request_telemetry WHERE trace_id = $1`, spansTargetTrace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var holders []string
	for rows.Next() {
		var appID string
		var raw []byte
		if err := rows.Scan(&appID, &raw); err != nil {
			t.Fatal(err)
		}
		var spans []struct {
			SpanID string `json:"span_id"`
		}
		if err := json.Unmarshal(raw, &spans); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		for _, s := range spans {
			if s.SpanID == span {
				holders = append(holders, slugs[appID])
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(holders)
	return holders
}

func TestPgStoreSpansSummaryTargetNarrowsRows(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	f := newSpansTargetFixture(t, store, pool)
	creator := uuid.MustParse(f.creator.ID)
	teammate := uuid.MustParse(f.teammate.ID)

	for _, tc := range []struct {
		name   string
		target state.SpansSummaryTarget
		want   string
	}{
		// An older producer still merges into every app of the namespace.
		{"account-wide", state.SpansSummaryTarget{AccountID: creator}, "[company personal]"},
		// A producer that knows the app touches only that app's rows.
		{"app", state.SpansSummaryTarget{AccountID: creator, AppID: uuid.MustParse(f.personal.ID)}, "[personal]"},
		{"app outside namespace", state.SpansSummaryTarget{AccountID: creator, AppID: uuid.MustParse(f.other.ID)}, "[]"},
		// A teammate's company key reaches every company app, the creator's
		// included, and none of anyone's personal apps.
		{"company key", state.SpansSummaryTarget{AccountID: teammate, OrgID: uuid.MustParse(f.org.ID), CustomerCredential: true}, "[company teammate]"},
		// A personal key reaches its holder's company apps while a member.
		{"personal key, member", state.SpansSummaryTarget{AccountID: creator, CustomerCredential: true}, "[company personal]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := spansTargetWrite(t, store, f, tc.target, "span-"+tc.name)
			if fmt.Sprint(got) != tc.want {
				t.Fatalf("rows holding the span = %v, want %s", got, tc.want)
			}
		})
	}
}

func TestPgStoreSpansSummaryCustomerKeyStopsAtDepartedCompany(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	f := newSpansTargetFixture(t, store, pool)
	creator := uuid.MustParse(f.creator.ID)
	if _, err := pool.Exec(ctx, `UPDATE org_memberships SET removed_at = now() WHERE org_id = $1 AND account_id = $2`, f.org.ID, f.creator.ID); err != nil {
		t.Fatal(err)
	}
	// The creator left: their personal key keeps writing their personal app,
	// and no longer the company app their namespace still holds.
	if got := spansTargetWrite(t, store, f, state.SpansSummaryTarget{AccountID: creator, CustomerCredential: true}, "after-leaving"); fmt.Sprint(got) != "[personal]" {
		t.Fatalf("departed creator's key wrote %v, want only the personal app", got)
	}
	// Platform producers name the app and are unaffected: the company app's
	// own guest spans still land.
	if got := spansTargetWrite(t, store, f, state.SpansSummaryTarget{AccountID: creator, AppID: uuid.MustParse(f.company.ID)}, "guest"); fmt.Sprint(got) != "[company]" {
		t.Fatalf("company app's own spans landed in %v, want the company app", got)
	}
	// A key whose every target row is out of reach reports the missing row.
	if err := store.UpdateSpansSummary(ctx, state.SpansSummaryTarget{TraceID: spansTargetTrace, AccountID: creator, AppID: uuid.MustParse(f.company.ID), CustomerCredential: true}, []byte(`[]`), 1000); !errors.Is(err, state.ErrRequestTelemetryRowNotFound) {
		t.Fatalf("departed creator's key on the company app: %v, want ErrRequestTelemetryRowNotFound", err)
	}
}
