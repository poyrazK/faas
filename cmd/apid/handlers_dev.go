package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

const devSessionTTL = 24 * time.Hour

// devSessionSlug creates a stable, globally unique app slug for one account,
// project, and local developer workspace. An empty workspace ID deliberately
// retains the pre-workspace digest so older CLIs can refresh and destroy the
// environments they already created.
func devSessionSlug(accountID, project, workspaceID string) string {
	identity := accountID + "\x00" + project
	if workspaceID != "" {
		identity += "\x00" + workspaceID
	}
	sum := sha256.Sum256([]byte(identity))
	suffix := hex.EncodeToString(sum[:6])
	const maxProjectLen = 23 // len("dev-") + 23 + len("-") + 12 == 40
	readable := project
	if len(readable) > maxProjectLen {
		readable = strings.Trim(readable[:maxProjectLen], "-")
	}
	return "dev-" + readable + "-" + suffix
}

func validDevWorkspaceID(workspaceID string) bool {
	if workspaceID == "" {
		return true
	}
	if len(workspaceID) != 32 {
		return false
	}
	for _, ch := range workspaceID {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

// upsertDevSession creates or refreshes the dedicated, expiring app that backs
// `gregale dev`. Developer sessions reuse the preview lifecycle with PR
// number zero, so the existing janitor and Firecracker scheduling path remain
// the only infrastructure lifecycle.
func (s *server) upsertDevSession(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project := r.PathValue("project")
	if !validSlug(project) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid project", "project must be 3–40 chars, lowercase letters, digits, and hyphens"))
		return
	}
	var req api.UpsertDevSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if !validDevWorkspaceID(req.WorkspaceID) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid workspace ID", "workspace_id must be 32 lowercase hexadecimal characters"))
		return
	}

	slug := devSessionSlug(acct.ID, project, req.WorkspaceID)
	expiresAt := time.Now().UTC().Add(devSessionTTL)
	limits := api.MustLimitsFor(acct.Plan)
	app, prob := s.buildApp(acct, api.CreateAppRequest{Slug: slug, Type: req.Type, Runtime: req.Runtime}, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	app.PreviewOfSlug = project
	app.PreviewPrNumber = 0
	app.PreviewPrState = state.PreviewPrStateOpen
	app.PreviewExpiresAt = &expiresAt

	if existing, err := s.store.AppBySlug(r.Context(), slug); err == nil {
		if existing.AccountID != acct.ID || existing.PreviewOfSlug != project || existing.PreviewPrNumber != 0 {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Developer session conflict", "the stable developer session slug is already in use"))
			return
		}
		if existing.Type != app.Type || (app.Type == state.AppTypeFunction && existing.Runtime != app.Runtime) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Developer session shape changed", "run 'gregale dev --stop' once, then start the session again"))
			return
		}
		refreshed, refreshErr := s.store.RefreshDevSession(r.Context(), existing.ID, expiresAt)
		if refreshErr != nil {
			api.WriteProblem(w, api.ErrCapacity("refresh developer session"))
			return
		}
		s.audit.Emit(r.Context(), "dev_session.refreshed", &acct.ID, map[string]any{
			"app_id": refreshed.ID, "slug": refreshed.Slug, "project": project, "workspace_id": req.WorkspaceID,
		})
		postgres, postgresErr := s.ensureDevPostgres(r.Context(), acct, refreshed, req.Postgres)
		if postgresErr != nil {
			managedPostgresProblem(w, postgresErr)
			return
		}
		writeJSON(w, http.StatusOK, api.DevSessionResponse{
			App: s.appResponse(refreshed, acct.Plan), ExpiresAt: expiresAt, Postgres: postgres,
		})
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("load developer session"))
		return
	}

	created, err := s.store.CreateAppIfUnderQuota(r.Context(), app, limits)
	if err != nil {
		var quotaErr *state.QuotaError
		switch {
		case errors.As(err, &quotaErr):
			if quotaErr.Kind == state.QuotaErrorKindDeveloperApps {
				api.WriteProblem(w, api.ErrPlanLimitDeveloperApps(limits, quotaErr.Observed))
			} else {
				api.WriteProblem(w, api.ErrPlanLimitApps(limits, quotaErr.Observed))
			}
		case errors.Is(err, state.ErrConflict):
			// A concurrent PUT may have inserted the same deterministic row
			// after our initial lookup. Fold that race into the idempotent
			// refresh path; a different row at the slug remains a conflict.
			existing, lookupErr := s.store.AppBySlug(r.Context(), slug)
			if lookupErr != nil || existing.AccountID != acct.ID || existing.PreviewOfSlug != project || existing.PreviewPrNumber != 0 ||
				existing.Type != app.Type || (app.Type == state.AppTypeFunction && existing.Runtime != app.Runtime) {
				api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
					"Developer session conflict", fmt.Sprintf("developer session slug %q is already in use", slug)))
				return
			}
			refreshed, refreshErr := s.store.RefreshDevSession(r.Context(), existing.ID, expiresAt)
			if refreshErr != nil {
				api.WriteProblem(w, api.ErrCapacity("refresh developer session"))
				return
			}
			s.audit.Emit(r.Context(), "dev_session.refreshed", &acct.ID, map[string]any{
				"app_id": refreshed.ID, "slug": refreshed.Slug, "project": project, "workspace_id": req.WorkspaceID,
			})
			postgres, postgresErr := s.ensureDevPostgres(r.Context(), acct, refreshed, req.Postgres)
			if postgresErr != nil {
				managedPostgresProblem(w, postgresErr)
				return
			}
			writeJSON(w, http.StatusOK, api.DevSessionResponse{
				App: s.appResponse(refreshed, acct.Plan), ExpiresAt: expiresAt, Postgres: postgres,
			})
			return
		default:
			api.WriteProblem(w, api.ErrCapacity("create developer session"))
		}
		return
	}
	s.emitAppCreated(r.Context(), created)
	s.audit.Emit(r.Context(), "dev_session.created", &acct.ID, map[string]any{
		"app_id": created.ID, "slug": created.Slug, "project": project, "workspace_id": req.WorkspaceID,
	})
	s.log.Info("developer session created", "app", created.ID,
		"slug", logsanitize.Field(created.Slug), "account", acct.ID)
	postgres, postgresErr := s.ensureDevPostgres(r.Context(), acct, created, req.Postgres)
	if postgresErr != nil {
		managedPostgresProblem(w, postgresErr)
		return
	}
	writeJSON(w, http.StatusCreated, api.DevSessionResponse{
		App: s.appResponse(created, acct.Plan), ExpiresAt: expiresAt, Postgres: postgres,
	})
}

func (s *server) destroyDevSession(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project := r.PathValue("project")
	if !validSlug(project) {
		s.notFound(w, "no such developer session")
		return
	}
	workspaceID := r.URL.Query().Get("workspace_id")
	if !validDevWorkspaceID(workspaceID) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid workspace ID", "workspace_id must be 32 lowercase hexadecimal characters"))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), devSessionSlug(acct.ID, project, workspaceID))
	if err != nil || app.AccountID != acct.ID || app.PreviewOfSlug != project || app.PreviewPrNumber != 0 {
		s.notFound(w, "no such developer session")
		return
	}
	s.destroyPreviewApp(w, r, acct, app, "dev_session.destroyed")
}

const (
	devSyncHistoryDefaultLimit = 20
	devSyncHistoryMaxLimit     = 100
)

var devSyncHistoryPhases = map[string]bool{
	"sync": true, "cache": true, "build": true,
	"boot": true, "ready": true, "route": true,
}

func (s *server) developerSessionApp(r *http.Request, acct state.Account, project, workspaceID string) (state.App, bool) {
	app, err := s.store.AppBySlug(r.Context(), devSessionSlug(acct.ID, project, workspaceID))
	if err != nil || app.AccountID != acct.ID || app.PreviewOfSlug != project || app.PreviewPrNumber != 0 || app.Status == state.AppDeleted {
		return state.App{}, false
	}
	return app, true
}

func parseDevSyncHistoryLimit(r *http.Request) (int, *api.Problem) {
	limit := devSyncHistoryDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > devSyncHistoryMaxLimit {
			return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be between 1 and 100")
		}
		limit = parsed
	}
	return limit, nil
}

func validateRecordDevSync(req api.RecordDevSyncRequest) *api.Problem {
	if !validDevWorkspaceID(req.WorkspaceID) {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid workspace ID", "workspace_id must be 32 lowercase hexadecimal characters")
	}
	if req.DeploymentID == "" || len(req.DeploymentID) > 64 {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deployment ID", "deployment_id is required")
	}
	if req.Status != "live" && req.Status != "failed" {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid sync status", "status must be live or failed")
	}
	if req.EditToLiveMS < 0 || req.EditToLiveMS > 3600000 || req.SLOTargetMS < 1 || req.SLOTargetMS > 3600000 {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid sync timing", "timings must be between 0 and 3600000 milliseconds")
	}
	if len(req.Phases) > 8 {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Too many sync phases", "phases must contain at most 8 entries")
	}
	for _, phase := range req.Phases {
		if !devSyncHistoryPhases[phase.Phase] || (phase.Status != "completed" && phase.Status != "failed" && phase.Status != "in_progress") || phase.DurationMS < 0 || phase.DurationMS > 3600000 || len(phase.Reason) > 512 {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid sync phase", "phase names, statuses, durations, and reasons are bounded")
		}
	}
	return nil
}

func devSyncHistoryItem(row state.DevSyncHistory) api.DevSyncHistoryItem {
	var phases []api.DevSyncPhase
	if err := json.Unmarshal(row.Phases, &phases); err != nil {
		phases = []api.DevSyncPhase{}
	}
	return api.DevSyncHistoryItem{
		DeploymentID: row.DeploymentID,
		Status:       row.Status,
		EditToLiveMS: row.EditToLiveMS,
		SLOTargetMS:  row.SLOTargetMS,
		WithinSLO:    row.WithinSLO,
		Phases:       phases,
		CreatedAt:    row.CreatedAt,
	}
}

func summarizeDevSyncHistory(rows []state.DevSyncHistory) api.DevSyncHistorySummary {
	if len(rows) == 0 {
		return api.DevSyncHistorySummary{}
	}
	durations := make([]int64, 0, len(rows))
	summary := api.DevSyncHistorySummary{Count: len(rows)}
	for _, row := range rows {
		durations = append(durations, row.EditToLiveMS)
		if row.WithinSLO {
			summary.WithinSLOCount++
		}
		if summary.SLOTargetMS == 0 {
			summary.SLOTargetMS = row.SLOTargetMS
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	summary.P50EditToLiveMS = durations[devSyncPercentileIndex(len(durations), 50)]
	summary.P95EditToLiveMS = durations[devSyncPercentileIndex(len(durations), 95)]
	latest := rows[0]
	latestItem := devSyncHistoryItem(latest)
	for _, phase := range latestItem.Phases {
		if phase.DurationMS > summary.SlowestPhaseMS {
			summary.SlowestPhase = phase.Phase
			summary.SlowestPhaseMS = phase.DurationMS
		}
	}
	if !latest.WithinSLO {
		if summary.SlowestPhase != "" {
			summary.Guidance = fmt.Sprintf("latest sync missed the SLO; %s was the slowest phase", summary.SlowestPhase)
		} else {
			summary.Guidance = "latest sync missed the SLO; inspect the deployment stages"
		}
	} else if summary.SLOTargetMS > 0 && summary.P95EditToLiveMS > summary.SLOTargetMS {
		summary.Guidance = "recent p95 is above the SLO; inspect the slowest phase before the loop regresses further"
	}
	return summary
}

func devSyncPercentileIndex(count, percentile int) int {
	if count < 2 {
		return 0
	}
	// Nearest-rank percentile: ceil(count*p/100)-1. This keeps p95 on
	// the slowest observation for the small histories developers usually
	// inspect, rather than hiding a two-sync regression behind the faster row.
	index := (count*percentile+99)/100 - 1
	if index < 0 {
		return 0
	}
	if index >= count {
		return count - 1
	}
	return index
}

// recordDevSync persists the CLI's redacted receipt. It is intentionally
// best-effort from the CLI's perspective, but strict at the API boundary so
// history cannot become an unbounded or cross-account log sink.
func (s *server) recordDevSync(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project := r.PathValue("project")
	if !validSlug(project) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid project", "project must be a valid project slug"))
		return
	}
	var req api.RecordDevSyncRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if prob := validateRecordDevSync(req); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	app, ok := s.developerSessionApp(r, acct, project, req.WorkspaceID)
	if !ok {
		s.notFound(w, "no such developer session")
		return
	}
	deployment, err := s.store.DeploymentByID(r.Context(), req.DeploymentID)
	if err != nil || deployment.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}
	history, ok := s.store.(state.DevSyncHistoryStore)
	if !ok || history == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "developer_sync_history_unavailable", "Developer sync history unavailable", "the control plane has not enabled developer sync history yet"))
		return
	}
	// The local receipt may carry a platform error reason for terminal
	// rendering. Persist only the phase identity/status/timing; reasons can
	// contain request-specific text and do not belong in durable history.
	safePhases := make([]api.DevSyncPhase, len(req.Phases))
	copy(safePhases, req.Phases)
	for i := range safePhases {
		safePhases[i].Reason = ""
	}
	phases, _ := json.Marshal(safePhases)
	row, err := history.RecordDevSyncHistory(r.Context(), state.DevSyncHistory{
		AppID: app.ID, DeploymentID: req.DeploymentID, Status: req.Status,
		EditToLiveMS: req.EditToLiveMS, SLOTargetMS: req.SLOTargetMS,
		WithinSLO: req.WithinSLO, Phases: phases,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("record developer sync history"))
		return
	}
	writeJSON(w, http.StatusCreated, devSyncHistoryItem(row))
}

// listDevSyncHistory returns only the session selected by the account,
// project, and optional workspace identity; it never accepts an app ID.
func (s *server) listDevSyncHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project := r.PathValue("project")
	if !validSlug(project) {
		s.notFound(w, "no such developer session")
		return
	}
	workspaceID := r.URL.Query().Get("workspace_id")
	if !validDevWorkspaceID(workspaceID) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid workspace ID", "workspace_id must be 32 lowercase hexadecimal characters"))
		return
	}
	limit, prob := parseDevSyncHistoryLimit(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	app, ok := s.developerSessionApp(r, acct, project, workspaceID)
	if !ok {
		s.notFound(w, "no such developer session")
		return
	}
	history, ok := s.store.(state.DevSyncHistoryStore)
	if !ok || history == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "developer_sync_history_unavailable", "Developer sync history unavailable", "the control plane has not enabled developer sync history yet"))
		return
	}
	rows, err := history.ListDevSyncHistory(r.Context(), app.ID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("load developer sync history"))
		return
	}
	items := make([]api.DevSyncHistoryItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, devSyncHistoryItem(row))
	}
	writeJSON(w, http.StatusOK, api.DevSyncHistoryResponse{
		Project: project, WorkspaceID: workspaceID, Items: items, Summary: summarizeDevSyncHistory(rows),
	})
}
