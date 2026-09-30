package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type featureFlagEvidenceCursor struct {
	EnvironmentID string    `json:"environment_id"`
	Filter        string    `json:"filter"`
	CustomerID    string    `json:"customer_id"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	At            time.Time `json:"at"`
	ID            string    `json:"id"`
}
type featureFlagRequestEvidence struct {
	ID           string    `json:"id"`
	AppID        string    `json:"app_id"`
	DeploymentID string    `json:"deployment_id"`
	CustomerID   string    `json:"customer_id,omitempty"`
	ReceivedAt   time.Time `json:"received_at"`
	Route        string    `json:"route"`
	Method       string    `json:"method"`
	Status       int       `json:"status"`
	LatencyMS    int       `json:"latency_ms"`
	Count        int       `json:"count"`
	ColdBoot     bool      `json:"cold_boot"`
	TraceID      string    `json:"trace_id,omitempty"`
	// Application-reported evidence, not a platform assertion of business effects.
	Flags json.RawMessage `json:"flags"`
}
type featureFlagEvidencePage struct {
	Items       []featureFlagRequestEvidence `json:"items"`
	NextCursor  string                       `json:"next_cursor,omitempty"`
	WindowStart time.Time                    `json:"window_start"`
	WindowEnd   time.Time                    `json:"window_end"`
}

func flagEvidenceQuery(r *http.Request, scope state.FeatureFlagScope, retention time.Duration) (sqlc.ListFeatureFlagRequestEvidenceParams, featureFlagEvidenceCursor, error) {
	filter := map[string]any{"flag": r.PathValue("key")}
	if !flags.ValidKey(r.PathValue("key")) {
		return sqlc.ListFeatureFlagRequestEvidenceParams{}, featureFlagEvidenceCursor{}, state.ErrInvalidArgument
	}
	for _, k := range []string{"value", "used"} {
		if raw := r.URL.Query().Get(k); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return sqlc.ListFeatureFlagRequestEvidenceParams{}, featureFlagEvidenceCursor{}, state.ErrInvalidArgument
			}
			filter[k] = v
		}
	}
	raw, _ := json.Marshal([]any{filter})
	now := time.Now().UTC()
	lookback := 24 * time.Hour
	if q := r.URL.Query().Get("since"); q != "" {
		v, err := time.ParseDuration(q)
		if err != nil || v <= 0 {
			return sqlc.ListFeatureFlagRequestEvidenceParams{}, featureFlagEvidenceCursor{}, state.ErrInvalidArgument
		}
		lookback = v
	}
	if lookback > retention {
		lookback = retention
	}
	c := featureFlagEvidenceCursor{EnvironmentID: scope.EnvironmentID, Filter: string(raw), CustomerID: r.URL.Query().Get("customer_id"), Start: now.Add(-lookback), End: now}
	if c.CustomerID != "" {
		id, err := uuid.Parse(c.CustomerID)
		if err != nil {
			return sqlc.ListFeatureFlagRequestEvidenceParams{}, c, state.ErrInvalidArgument
		}
		c.CustomerID = id.String()
	}
	p := sqlc.ListFeatureFlagRequestEvidenceParams{AccountID: stringToPgUUID(scope.AccountID), EnvironmentSlug: r.PathValue("environment"), CustomerID: c.CustomerID, EvidenceFilter: raw}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		if len(cursor) > base64.RawURLEncoding.EncodedLen(api.FlagsMaxCursorBytes) {
			return p, c, state.ErrInvalidArgument
		}
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(decoded) > api.FlagsMaxCursorBytes {
			return p, c, state.ErrInvalidArgument
		}
		var old featureFlagEvidenceCursor
		if err = json.Unmarshal(decoded, &old); err != nil || old.EnvironmentID != c.EnvironmentID || old.Filter != c.Filter || old.CustomerID != c.CustomerID || old.Start.Before(now.Add(-retention-time.Minute)) || old.End.After(now) || !old.End.After(old.Start) || old.End.Sub(old.Start) > retention || old.At.Before(old.Start) || old.At.After(old.End) {
			return p, c, state.ErrInvalidArgument
		}
		id, err := uuid.Parse(old.ID)
		if err != nil {
			return p, c, state.ErrInvalidArgument
		}
		c = old
		p.CursorAt = pgtype.Timestamptz{Time: old.At, Valid: true}
		p.CursorID = pgtype.UUID{Bytes: id, Valid: true}
	}
	p.ReceivedFrom = pgtype.Timestamptz{Time: c.Start, Valid: true}
	p.ReceivedUntil = pgtype.Timestamptz{Time: c.End, Valid: true}
	return p, c, nil
}
func (s *server) listFeatureFlagEvidence(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	p, c, err := flagEvidenceQuery(r, scope, time.Duration(limits.DebugTelemetryRetentionDays)*24*time.Hour)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, scope.ProjectID)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	for _, app := range apps {
		if app.PreviewOfSlug == "" {
			p.AppIds = append(p.AppIds, stringToPgUUID(app.ID))
		}
	}
	store, ok := s.store.(state.FeatureFlagEvidenceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("request evidence requires Postgres"))
		return
	}
	rows, err := store.ListFeatureFlagRequestEvidence(r.Context(), p)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, makeFeatureFlagEvidencePage(rows, c))
}

func makeFeatureFlagEvidencePage(rows []sqlc.ListFeatureFlagRequestEvidenceRow, c featureFlagEvidenceCursor) featureFlagEvidencePage {
	page := featureFlagEvidencePage{Items: []featureFlagRequestEvidence{}, WindowStart: c.Start, WindowEnd: c.End}
	if len(rows) > api.FlagsMaxRequestPage {
		rows = rows[:api.FlagsMaxRequestPage]
		last := rows[len(rows)-1]
		c.At, c.ID = last.ReceivedAt.Time, uuidFromPg(last.ID)
		raw, _ := json.Marshal(c)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	for _, row := range rows {
		page.Items = append(page.Items, featureFlagRequestEvidence{ID: uuidFromPg(row.ID), AppID: uuidFromPg(row.AppID), DeploymentID: uuidFromPg(row.DeploymentID), CustomerID: uuidFromPg(row.PlatformTenantID), ReceivedAt: row.ReceivedAt.Time, Route: row.Route, Method: row.Method, Status: int(row.Status), LatencyMS: int(row.LatencyMs), Count: int(row.Count), ColdBoot: row.ColdBoot, TraceID: row.TraceID.String, Flags: json.RawMessage(row.FlagEvidence)})
	}
	return page
}
