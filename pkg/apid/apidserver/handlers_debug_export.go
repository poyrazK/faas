package apidserver

// handlers_debug_export.go exposes a bounded export of the durable request
// telemetry stream. The debugger list endpoint is intentionally JSON-shaped
// and page-oriented; exports are for incident workflows that need a portable
// artifact for jq, spreadsheets, or an external support ticket.
//
// The export is deliberately metadata-only. It uses the same plan retention
// boundary and query as the debugger list, never reads request bodies or
// headers, and caps the number of rows held before the response is committed.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	debugTelemetryExportMaxRows   = 10_000
	debugTelemetryExportBatchSize = 200
)

// debugTelemetryExportHandler serves GET /v1/apps/{slug}/debug/requests/export.
//
// format is ndjson (the default) or csv. since and route have the same
// semantics as the paginated debugger list. The plan's retention limit always
// wins, even when a caller asks for a wider window. A bounded row cap keeps an
// export from becoming an unbounded database read or response allocation.
func (s *server) debugTelemetryExportHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	since, err := parseDebugSinceStrict(sinceRaw, 24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	retentionClamped := retention > 0 && since > retention
	if retentionClamped {
		since = retention
	}

	route := r.URL.Query().Get("route")
	if len(route) > 256 {
		api.WriteProblem(w, api.ErrValidation("route must be at most 256 characters"))
		return
	}
	// The list's filters apply to the export too (ADR-127 amendment
	// 2026-11-05), so an export is the list a customer was looking at.
	filters, err := parseDebugTelemetryFilters(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "ndjson"
	}
	if format != "ndjson" && format != "csv" {
		api.WriteProblem(w, api.ErrValidation("format must be one of: ndjson, csv"))
		return
	}

	limit := debugTelemetryExportMaxRows
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > debugTelemetryExportMaxRows {
			api.WriteProblem(w, api.ErrValidation("limit must be an integer between 1 and 10000"))
			return
		}
		limit = parsed
	}

	now := time.Now().UTC()
	// One row past the limit tells a capped export from a complete one.
	rows, err := s.loadDebugTelemetryExportRows(r, app.ID, now.Add(-since), now, route, filters, limit+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("export request telemetry"))
		return
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}

	ext := "ndjson"
	contentType := "application/x-ndjson; charset=utf-8"
	if format == "csv" {
		ext = "csv"
		contentType = "text/csv; charset=utf-8"
	}
	filename := debugTelemetryExportFilename(app.Slug, ext)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	// These headers make the retention decision reproducible for clients that
	// save an export without also retaining the original query string.
	w.Header().Set("X-Faas-Request-Log-Window", since.String())
	w.Header().Set("X-Faas-Request-Log-Retention-Clamped", strconv.FormatBool(retentionClamped))
	w.Header().Set("X-Faas-Request-Log-Truncated", strconv.FormatBool(truncated))
	w.WriteHeader(http.StatusOK)

	if format == "csv" {
		writeDebugTelemetryCSV(w, rows)
		return
	}
	writeDebugTelemetryNDJSON(w, rows)
}

// loadDebugTelemetryExportRows walks the same stable (received_at DESC, id
// DESC) ordering used by the list endpoint, but keeps the cursor internal so
// the exported artifact is a single bounded document.
func (s *server) loadDebugTelemetryExportRows(r *http.Request, appID string, from, until time.Time, route string, filters debugTelemetryFilters, limit int) ([]sqlc.ListRequestTelemetryByAppRow, error) {
	rows := make([]sqlc.ListRequestTelemetryByAppRow, 0, minInt(limit, debugTelemetryExportBatchSize))
	cursorReceivedAt := pgtype.Timestamptz{}
	cursorID := pgtype.UUID{}
	for len(rows) < limit {
		batchLimit := minInt(limit-len(rows), debugTelemetryExportBatchSize)
		batch, err := s.store.ListRequestTelemetryByApp(r.Context(), sqlc.ListRequestTelemetryByAppParams{
			AppID:             stringToPgUUID(appID),
			ReceivedAt:        pgtype.Timestamptz{Time: from.UTC(), Valid: true},
			ReceivedAt_2:      pgtype.Timestamptz{Time: until.UTC(), Valid: true},
			CursorReceivedAt:  cursorReceivedAt,
			CursorID:          cursorID,
			Route:             route,
			DeploymentID:      filters.DeploymentID,
			StatusFilter:      int32(filters.Status),
			ColdBootFilter:    filters.sqlColdBootFilter(),
			ConsumerID:        filters.sqlConsumerID(),
			ConsumerAnonymous: filters.sqlConsumerAnonymous(),
			MinLatencyMs:      int32(filters.MinLatencyMS),
			Limit:             int32(batchLimit),
		})
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		rows = append(rows, batch...)
		if len(batch) < batchLimit {
			break
		}
		last := batch[len(batch)-1]
		if !last.ID.Valid || !last.ReceivedAt.Valid {
			// A malformed row must not make an export loop forever. The
			// database schema makes both fields required, so this is a
			// defensive stop for a degraded/mock store.
			break
		}
		cursorReceivedAt = last.ReceivedAt
		cursorID = last.ID
	}
	return rows, nil
}

func writeDebugTelemetryNDJSON(w http.ResponseWriter, rows []sqlc.ListRequestTelemetryByAppRow) {
	enc := json.NewEncoder(w)
	for _, row := range rows {
		_ = enc.Encode(debugTelemetryRowToItem(row))
	}
}

var debugTelemetryCSVHeader = []string{
	"id", "deployment_id", "route", "method", "status", "latency_ms", "count",
	"cold_boot", "trace_id", "received_at", "wake_id", "instance_id", "consumer_id",
	"guest_runtime", "guest_duration_ms", "guest_outcome", "guest_error_class",
}

func writeDebugTelemetryCSV(w http.ResponseWriter, rows []sqlc.ListRequestTelemetryByAppRow) {
	writer := csv.NewWriter(w)
	_ = writer.Write(debugTelemetryCSVHeader)
	for _, row := range rows {
		item := debugTelemetryRowToItem(row)
		guestRuntime, guestDuration, guestOutcome, guestErrorClass := "", "", "", ""
		if item.Guest != nil {
			guestRuntime = item.Guest.Runtime
			guestDuration = strconv.Itoa(item.Guest.DurationMS)
			guestOutcome = item.Guest.Outcome
			guestErrorClass = item.Guest.ErrorClass
		}
		traceID := ""
		if item.TraceID != nil {
			traceID = *item.TraceID
		}
		record := []string{
			item.ID, item.DeploymentID, item.Route, item.Method,
			strconv.Itoa(item.Status), strconv.Itoa(item.LatencyMS), strconv.Itoa(item.Count),
			strconv.FormatBool(item.ColdBoot), traceID, item.ReceivedAt, item.WakeID,
			item.InstanceID, item.ConsumerID, guestRuntime, guestDuration, guestOutcome,
			guestErrorClass,
		}
		for i := range record {
			record[i] = csvSafeCell(record[i])
		}
		_ = writer.Write(record)
	}
	writer.Flush()
}

// csvSafeCell keeps a spreadsheet from running a cell as a formula. Route
// labels and guest error classes come from the app's own traffic, so a value
// that starts with =, +, -, @, a tab or a carriage return is written with a
// leading apostrophe (OWASP CSV injection guidance).
func csvSafeCell(value string) string { return api.CSVSafeCell(value) }

func debugTelemetryExportFilename(slug, ext string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = "app"
	}
	return slug + "-request-logs." + ext
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
