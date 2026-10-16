package apidserver_test

// adr: 958 — a database dependency is broken down by normalized query, and
// the suspect names the query that regressed.

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func ordersQueryRow(t *testing.T, deployment, tag string, created time.Time, statement string, queryMS uint64) sqlc.ListRequestTelemetryDependencySpansRow {
	t.Helper()
	spans, err := json.Marshal([]debugger.StoredSpan{
		{SpanID: "s1", Name: "GET /orders", Kind: "SPAN_KIND_SERVER", DurationNanos: (queryMS + 10) * 1_000_000},
		{SpanID: "s2", ParentSpanID: "s1", Name: "pg.query", Kind: "SPAN_KIND_CLIENT", DurationNanos: queryMS * 1_000_000,
			DBStatement: statement, Attributes: map[string]string{"db.system": "postgresql"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sqlc.ListRequestTelemetryDependencySpansRow{
		Route: "GET /orders", Method: "GET", Count: 1, Status: 200,
		ReceivedAt:   pgtype.Timestamptz{Time: created.Add(time.Minute), Valid: true},
		SpansSummary: spans, DeploymentID: deployment, DeploymentTag: tag,
		DeploymentCreatedAt: created.Format(time.RFC3339Nano),
	}
}

func TestDependencyQueryBreakdownNamesRegressedQuery(t *testing.T) {
	v80 := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	v81 := v80.Add(time.Hour)
	var rows []sqlc.ListRequestTelemetryDependencySpansRow
	for i := 0; i < 12; i++ {
		byCustomer := fmt.Sprintf("SELECT * FROM orders WHERE customer_id = %d", 100+i)
		byID := fmt.Sprintf("SELECT * FROM orders WHERE id = %d", i)
		rows = append(rows,
			ordersQueryRow(t, "dep-80", "v80", v80, byCustomer, 40),
			ordersQueryRow(t, "dep-81", "v81", v81, byCustomer, 210),
			ordersQueryRow(t, "dep-80", "v80", v80, byID, 5),
			ordersQueryRow(t, "dep-81", "v81", v81, byID, 5),
		)
	}
	comparison := apidserver.BuildDebugDependencyDeploymentComparison(rows, "", "GET /orders", apidserver.DebugDependencyComparisonMaxItems)
	if comparison == nil {
		t.Fatal("comparison = nil")
	}
	var found bool
	for _, item := range comparison.Dependencies {
		if item.Type != debugger.AppDependencyType || item.Name != "SELECT orders" {
			continue
		}
		found = true
		if len(item.Queries) != 2 {
			t.Fatalf("queries = %+v, want two patterns (literals folded)", item.Queries)
		}
		slow := item.Queries[0]
		if slow.Pattern != "SELECT * FROM orders WHERE customer_id = ?" || !slow.Regression || slow.Calls != 24 {
			t.Fatalf("first query = %+v, want the regressed customer_id pattern", slow)
		}
		if item.Queries[1].Regression {
			t.Fatalf("the id lookup did not regress: %+v", item.Queries[1])
		}
	}
	if !found {
		t.Fatalf("no SELECT orders dependency in %+v", comparison.Dependencies)
	}
	suspect := apidserver.SuspectedDependency(comparison)
	if suspect == nil || suspect.Query != "SELECT * FROM orders WHERE customer_id = ?" {
		t.Fatalf("suspect = %+v, want the regressed query named", suspect)
	}
	if raw := apidserver.EncodeSuspectedDependency(suspect); raw == nil || len(raw) > 1024 {
		t.Fatalf("suspect with query must stay within the stored bound: %d bytes", len(raw))
	}
}
