package apidserver_test

import (
	"encoding/csv"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestWriteDebugTelemetryNDJSONIsOneSafeObjectPerLine(t *testing.T) {
	row := debugExportTestRow()
	out := httptest.NewRecorder()
	apidserver.WriteDebugTelemetryNDJSON(out, []sqlc.ListRequestTelemetryByAppRow{row})
	lines := strings.Split(strings.TrimSpace(out.Body.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1: %q", len(lines), out.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &item); err != nil {
		t.Fatalf("NDJSON line is not JSON: %v", err)
	}
	if item["route"] != "GET /users/{id}" || item["status"] != float64(404) {
		t.Fatalf("exported item = %#v", item)
	}
	if _, leaked := item["headers"]; leaked {
		t.Fatal("request headers must not be present in the export")
	}
}

func TestWriteDebugTelemetryCSVHasStableHeaderAndEscapesFields(t *testing.T) {
	row := debugExportTestRow()
	row.Route = "GET /search/{id}, detail"
	out := httptest.NewRecorder()
	apidserver.WriteDebugTelemetryCSV(out, []sqlc.ListRequestTelemetryByAppRow{row})
	parsed, err := csv.NewReader(strings.NewReader(out.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v\n%s", err, out.Body.String())
	}
	if len(parsed) != 2 {
		t.Fatalf("CSV records = %d, want header + row: %s", len(parsed), out.Body.String())
	}
	if got, want := parsed[0][0], "id"; got != want {
		t.Fatalf("first header = %q, want %q", got, want)
	}
	if got, want := parsed[0][len(parsed[0])-1], "guest_error_class"; got != want {
		t.Fatalf("last header = %q, want %q", got, want)
	}
	if got, want := parsed[1][2], row.Route; got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
}

func TestDebugTelemetryExportFilenameFallsBackForEmptySlug(t *testing.T) {
	if got, want := apidserver.DebugTelemetryExportFilename("", "csv"), "app-request-logs.csv"; got != want {
		t.Fatalf("filename = %q, want %q", got, want)
	}
	if got, want := apidserver.DebugTelemetryExportFilename("orders", "ndjson"), "orders-request-logs.ndjson"; got != want {
		t.Fatalf("filename = %q, want %q", got, want)
	}
}

func TestDebugTelemetryExportRejectsUnknownFormat(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if _, err := (*e.Store()).CreateApp(t.Context(), state.App{
		AccountID: (*e.Acct()).ID,
		Slug:      "debug-export",
		Status:    state.AppActive,
	}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.Do(t, "GET", "/v1/apps/debug-export/debug/requests/export?format=xml", nil, nil)
	apidserver.AssertProblem(t, rec, 400, api.CodeValidation)
	if !strings.Contains(rec.Body.String(), "ndjson, csv") {
		t.Fatalf("problem detail = %s, want format guidance", rec.Body.String())
	}
}

func debugExportTestRow() sqlc.ListRequestTelemetryByAppRow {
	return sqlc.ListRequestTelemetryByAppRow{
		ID:              pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Valid: true},
		DeploymentID:    pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Valid: true},
		Route:           "GET /users/{id}",
		Method:          "GET",
		Status:          404,
		LatencyMs:       42,
		Count:           3,
		ColdBoot:        true,
		TraceID:         pgtype.Text{String: "0123456789abcdef0123456789abcdef", Valid: true},
		ReceivedAt:      pgtype.Timestamptz{Time: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC), Valid: true},
		WakeID:          pgtype.Text{String: "wake-1", Valid: true},
		InstanceID:      pgtype.Text{String: "instance-1", Valid: true},
		GuestDurationMs: 17,
		GuestRuntime:    "node22",
		GuestOutcome:    "http_error",
		GuestErrorClass: "http_5xx",
		ConsumerID:      pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Valid: true},
	}
}
