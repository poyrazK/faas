package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	platformTenantActivityDefaultLimit = 100
	platformTenantActivityMaxLimit     = 200
)

// listPlatformTenantActivity exposes only retained request-debugger evidence
// carrying the verified tenant identity stamped at request time. It is
// intentionally separate from the durable usage ledger, which remains the
// accounting source of truth.
func (s *server) listPlatformTenantActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	query := r.URL.Query()
	sinceRaw := strings.TrimSpace(query.Get("since"))
	since, err := parsePlatformTenantActivitySince(sinceRaw)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	appID := strings.TrimSpace(query.Get("app_id"))
	if appID != "" {
		parsed, parseErr := uuid.Parse(appID)
		if parseErr != nil {
			api.WriteProblem(w, api.ErrValidation("app_id must be an app UUID"))
			return
		}
		appID = parsed.String()
	}
	statusFilter := int32(0)
	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		parsedStatus, parseErr := strconv.ParseInt(raw, 10, 32)
		if parseErr != nil || parsedStatus < 100 || parsedStatus > 599 {
			api.WriteProblem(w, api.ErrValidation("status must be an integer from 100 to 599"))
			return
		}
		statusFilter = int32(parsedStatus)
	}
	limit := platformTenantActivityDefaultLimit
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > platformTenantActivityMaxLimit {
			api.WriteProblem(w, api.ErrValidation("limit must be an integer from 1 to 200"))
			return
		}
	}

	now := time.Now().UTC()
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if retention <= 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	retentionClamped := false
	if since > retention {
		since = retention
		retentionClamped = true
	}
	windowStart, windowEnd := now.Add(-since), now
	cursorRaw := strings.TrimSpace(query.Get("cursor"))
	cursorReceivedAt := pgtype.Timestamptz{}
	cursorID := pgtype.UUID{}
	if cursorRaw != "" {
		cursor, cursorErr := decodePlatformTenantActivityCursor(cursorRaw)
		if cursorErr != nil {
			api.WriteProblem(w, api.ErrValidation(cursorErr.Error()))
			return
		}
		if cursor.TenantID != tenant.ID || cursor.AppID != appID || cursor.Status != int(statusFilter) || (sinceRaw != "" && int64(since) != cursor.SinceNanos) {
			api.WriteProblem(w, api.ErrValidation("cursor does not match this tenant activity query"))
			return
		}
		windowStart, windowEnd = cursor.WindowStart.UTC(), cursor.WindowEnd.UTC()
		retentionClamped = cursor.RetentionClamped
		if time.Duration(cursor.SinceNanos) > retention || windowEnd.After(now.Add(time.Minute)) || windowStart.Before(now.Add(-retention-time.Minute)) || windowEnd.Sub(windowStart) > retention {
			api.WriteProblem(w, api.ErrValidation("cursor window is outside plan retention"))
			return
		}
		cursorReceivedAt = pgtype.Timestamptz{Time: cursor.ReceivedAt.UTC(), Valid: true}
		parsedID, _ := uuid.Parse(cursor.ID) // decoder validates the UUID.
		cursorID = pgtype.UUID{Bytes: parsedID, Valid: true}
		since = time.Duration(cursor.SinceNanos)
	}

	rows, err := s.store.ListRequestTelemetryByPlatformTenant(r.Context(), sqlc.ListRequestTelemetryByPlatformTenantParams{
		AccountID:        stringToPgUUID(acct.ID),
		PlatformTenantID: stringToPgUUID(tenant.ID),
		ReceivedFrom:     pgtype.Timestamptz{Time: windowStart, Valid: true},
		ReceivedUntil:    pgtype.Timestamptz{Time: windowEnd, Valid: true},
		CursorReceivedAt: cursorReceivedAt,
		CursorID:         cursorID,
		AppIDFilter:      appID,
		StatusFilter:     statusFilter,
		Limit:            int32(limit + 1),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list platform tenant activity"))
		return
	}
	if rows == nil {
		rows = []sqlc.ListRequestTelemetryByPlatformTenantRow{}
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	response := api.PlatformTenantActivityResponse{
		TenantID: tenant.ID, Since: echoDebugSince(sinceRaw, since),
		WindowStart: windowStart, WindowEnd: windowEnd,
		PlanRetentionDays: limits.DebugTelemetryRetentionDays,
		RetentionClamped:  retentionClamped,
		PageTelemetryRows: int64(len(rows)), PageComplete: !hasMore,
		Filters:  api.PlatformTenantActivityFilters{AppID: appID, Status: int(statusFilter)},
		Requests: make([]api.PlatformTenantActivityItem, 0, len(rows)),
	}
	for _, row := range rows {
		request := debugTelemetryItemFromFields(
			row.ID, row.DeploymentID, row.Route, row.Method, row.Status,
			row.LatencyMs, row.Count, row.ColdBoot, row.TraceID, row.ReceivedAt,
			row.WakeID, row.InstanceID, row.GuestDurationMs, row.GuestRuntime,
			row.GuestOutcome, row.GuestErrorClass, row.ConsumerID, row.NodeID,
			row.Region, row.CommitSha, row.DeploymentTag, row.DeploymentCreatedAt,
			row.ImageDigest,
		)
		response.Requests = append(response.Requests, api.PlatformTenantActivityItem{
			AppID: uuidFromPg(row.AppID), Request: request,
		})
		response.PageRepresentedRequests += int64(row.Count)
		if row.Status >= http.StatusBadRequest {
			response.PageErrorRequests += int64(row.Count)
		}
	}
	if hasMore && len(rows) > 0 {
		response.NextCursor, err = encodePlatformTenantActivityCursor(
			tenant.ID, appID, int(statusFilter), since, windowStart, windowEnd,
			retentionClamped, rows[len(rows)-1],
		)
		if err != nil {
			api.WriteProblem(w, api.ErrInternal("could not encode platform tenant activity cursor"))
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func parsePlatformTenantActivitySince(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 24 * time.Hour, nil
	}
	if len(raw) > 32 {
		return 0, errors.New("since must be a positive duration (for example 30m, 24h, or 3d)")
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(raw, "d"), 10, 64)
		const maxDuration = time.Duration(1<<63 - 1)
		maxDays := int64(maxDuration / (24 * time.Hour))
		if err != nil || days <= 0 || days > maxDays {
			return 0, errors.New("since must be a positive duration (for example 30m, 24h, or 3d)")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, errors.New("since must be a positive duration (for example 30m, 24h, or 3d)")
	}
	return duration, nil
}
