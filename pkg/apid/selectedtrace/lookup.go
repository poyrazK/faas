// Package selectedtrace implements explicit-app trace correlation without
// fetching account-wide evidence, logs, spans, payloads or arbitrary headers.
package selectedtrace

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type Selection struct {
	TraceID string
	AppIDs  []string
	Limit   int
}

func Parse(traceID string, q url.Values) (Selection, *api.Problem) {
	s := Selection{TraceID: traceID, AppIDs: q["app_id"], Limit: api.SelectedTraceRowsDefault}
	if !api.ValidTraceID(traceID) {
		return s, api.ErrValidation("trace_id must be a nonzero canonical W3C trace ID")
	}
	for key, values := range q {
		if key != "app_id" && key != "limit" || key == "limit" && len(values) != 1 {
			return s, api.ErrValidation("unsupported or repeated selected trace parameter")
		}
	}
	if len(s.AppIDs) < 1 || len(s.AppIDs) > api.SelectedTraceAppsMax {
		return s, api.ErrValidation("select one to five distinct app_id UUIDs")
	}
	seen := map[string]bool{}
	for _, id := range s.AppIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id || seen[id] {
			return s, api.ErrValidation("app_id must contain distinct canonical nonzero UUIDs")
		}
		seen[id] = true
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.SelectedTraceRowsMax {
			return s, api.ErrValidation("limit must be between 1 and 20")
		}
		s.Limit = n
	}
	return s, nil
}

type AppReader interface {
	AppByID(context.Context, string) (state.App, error)
}

func resolve(ctx context.Context, store AppReader, accountID string, ids []string) ([]api.SelectedTraceApp, *api.Problem) {
	apps := make([]api.SelectedTraceApp, 0, len(ids))
	for _, id := range ids {
		app, err := store.AppByID(ctx, id)
		if err != nil || !equalID(app.ID, id) || !equalID(app.AccountID, accountID) || app.Status == state.AppDeleted {
			return nil, api.NewProblem(404, api.CodeNotFound, "Not found", "selected app is unavailable")
		}
		apps = append(apps, api.SelectedTraceApp{ID: id, Slug: app.Slug})
	}
	return apps, nil
}

func Lookup(ctx context.Context, apps AppReader, reader state.SelectedTraceStore, accountID string, s Selection, retentionDays int) (api.SelectedTraceLookupResponse, *api.Problem) {
	out := api.SelectedTraceLookupResponse{Selection: "explicit_app_ids_v1", TraceID: s.TraceID, Limit: s.Limit, Requests: []api.SelectedTraceRequest{}, Invocations: []api.SelectedTraceInvocation{}, RequestRetentionDays: retentionDays, RequestsStatus: "unavailable", InvocationsStatus: "unavailable"}
	if _, problem := Parse(s.TraceID, url.Values{"app_id": s.AppIDs, "limit": {strconv.Itoa(s.Limit)}}); problem != nil {
		return out, problem
	}
	accountIDValue, accountErr := uuid.Parse(accountID)
	if accountErr != nil || accountIDValue == uuid.Nil || retentionDays < 1 || reader == nil {
		return out, api.ErrCapacity("selected trace lookup is unavailable")
	}
	selected, problem := resolve(ctx, apps, accountID, s.AppIDs)
	if problem != nil {
		return out, problem
	}
	out.Apps = selected
	ids := make([]pgtype.UUID, 0, len(s.AppIDs))
	for _, id := range s.AppIDs {
		ids = append(ids, pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true})
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	out.GeneratedAt = now.Format(time.RFC3339Nano)
	out.RequestWindowStart = start.Format(time.RFC3339Nano)
	out.RequestWindowEnd = out.GeneratedAt
	account := pgtype.UUID{Bytes: uuid.MustParse(accountID), Valid: true}
	requests, err := reader.ListSelectedTraceRequests(ctx, sqlc.ListSelectedTraceRequestsParams{AccountID: account, AppIds: ids, TraceID: s.TraceID, ReceivedFrom: pgtype.Timestamptz{Time: start, Valid: true}, ReceivedUntil: pgtype.Timestamptz{Time: now, Valid: true}, RowLimit: int32(s.Limit + 1)})
	if err == nil {
		if len(requests) > s.Limit+1 {
			return out, api.ErrCapacity("invalid selected trace request bound")
		}
		for _, row := range requests {
			if !selectedRow(row.AppID, selected) {
				return out, api.ErrCapacity("invalid selected trace request identity")
			}
		}
		out.RequestsStatus = "ok"
		out.RequestsTruncated = len(requests) > s.Limit
		for _, row := range requests[:min(len(requests), s.Limit)] {
			out.Requests = append(out.Requests, request(row))
		}
	}
	if ctx.Err() != nil {
		return out, api.ErrCapacity("selected trace lookup interrupted")
	}
	invocations, err := reader.ListSelectedTraceInvocations(ctx, sqlc.ListSelectedTraceInvocationsParams{AccountID: account, AppIds: ids, TraceID: s.TraceID, RowLimit: int32(s.Limit + 1)})
	if err == nil {
		if len(invocations) > s.Limit+1 {
			return out, api.ErrCapacity("invalid selected trace invocation bound")
		}
		for _, row := range invocations {
			if !selectedRow(row.AppID, selected) {
				return out, api.ErrCapacity("invalid selected trace invocation identity")
			}
		}
		out.InvocationsStatus = "ok"
		out.InvocationsTruncated = len(invocations) > s.Limit
		for _, row := range invocations[:min(len(invocations), s.Limit)] {
			out.Invocations = append(out.Invocations, invocation(row))
		}
	}
	current, problem := resolve(ctx, apps, accountID, s.AppIDs)
	if problem != nil {
		return out, problem
	}
	for i, app := range current {
		if app != selected[i] {
			return out, api.NewProblem(409, api.CodeConflict, "Selection changed", "app selection changed during lookup")
		}
	}
	if ctx.Err() != nil {
		return out, api.ErrCapacity("selected trace lookup interrupted")
	}
	out.Partial = out.RequestsStatus != "ok" || out.InvocationsStatus != "ok"
	return out, nil
}

func selectedRow(id pgtype.UUID, apps []api.SelectedTraceApp) bool {
	if !id.Valid || id.Bytes == [16]byte{} {
		return false
	}
	for _, app := range apps {
		if uuid.UUID(id.Bytes).String() == app.ID {
			return true
		}
	}
	return false
}

func request(r sqlc.ListSelectedTraceRequestsRow) api.SelectedTraceRequest {
	id := ""
	if r.DeploymentID.Valid {
		id = uuid.UUID(r.DeploymentID.Bytes).String()
	}
	return api.SelectedTraceRequest{AppID: uuid.UUID(r.AppID.Bytes).String(), ID: uuid.UUID(r.ID.Bytes).String(), DeploymentID: id, Route: r.Route, Method: r.Method, Status: int(r.Status), LatencyMS: int(r.LatencyMs), Count: int(r.Count), ColdBoot: r.ColdBoot, ReceivedAt: r.ReceivedAt.Time.UTC().Format(time.RFC3339Nano)}
}

func invocation(r sqlc.ListSelectedTraceInvocationsRow) api.SelectedTraceInvocation {
	out := api.SelectedTraceInvocation{AppID: uuid.UUID(r.AppID.Bytes).String(), ID: uuid.UUID(r.ID.Bytes).String(), Source: r.Source, State: r.State, Attempts: int(r.Attempts), CreatedAt: r.CreatedAt.Time.UTC().Format(time.RFC3339Nano)}
	if r.ReceivedAt.Valid {
		out.StartedAt = r.ReceivedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if r.CompletedAt.Valid {
		out.CompletedAt = r.CompletedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func equalID(a, b string) bool {
	x, e1 := uuid.Parse(a)
	y, e2 := uuid.Parse(b)
	return e1 == nil && e2 == nil && x == y
}
