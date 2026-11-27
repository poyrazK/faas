package apidserver

import (
	"context"
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
	"github.com/onebox-faas/faas/pkg/apid/devloop"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

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

// devSessionLease resolves the lease a developer session renews on this
// request. Omission keeps the original 24-hour lease so older CLIs behave as
// before; an explicit value is bounded below by DeveloperLeaseMin and above
// by the plan's DeveloperLeaseMaxHours.
func devSessionLease(leaseSeconds int64, limits api.Limits) (time.Duration, *api.Problem) {
	if leaseSeconds == 0 {
		return api.DeveloperLeaseDefault, nil
	}
	minSeconds := int64(api.DeveloperLeaseMin / time.Second)
	if leaseSeconds < minSeconds {
		return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid lease", fmt.Sprintf("lease_seconds must be at least %d (%s)", minSeconds, api.DeveloperLeaseMin))
	}
	// Compare in seconds so an absurd value cannot overflow time.Duration.
	if leaseSeconds > int64(limits.Plan.DeveloperLeaseMax()/time.Second) {
		return 0, api.ErrPlanLimitDeveloperLease(limits, leaseSeconds)
	}
	return time.Duration(leaseSeconds) * time.Second, nil
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

	if len(req.Buckets) != 0 && !middleware.HasScope(r, api.ScopesStorageManageSurface...) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope", "developer bucket declarations require storage:manage or admin"))
		return
	}
	if err := s.object.ValidateDevBuckets(&req, acct.Plan); err != nil {
		object.BucketProblem(w, err)
		return
	}

	watch, prob := devloop.ValidateWatch(req.Watch)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	req.Watch = watch
	if prob := devloop.ValidateGoCommand(req); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	lease, prob := devSessionLease(req.LeaseSeconds, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	slug := devSessionSlug(acct.ID, project, req.WorkspaceID)
	expiresAt := time.Now().UTC().Add(lease)
	createReq := api.CreateAppRequest{Slug: slug, Type: req.Type, Runtime: req.Runtime}
	if watch != nil {
		// A development server needs more memory than the app's production
		// server; the 512 MB default leaves it swapping through its first
		// compile. An existing environment keeps its size.
		createReq.RAMMB = api.DevWatchRAMMB(limits)
	}
	app, prob := s.buildApp(acct, createReq, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	app.PreviewOfSlug = project
	app.PreviewPrNumber = 0
	app.PreviewPrState = state.PreviewPrStateOpen
	app.PreviewExpiresAt = &expiresAt
	app.Manifest.BuildGoCommand = req.GoCommand

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
		if prob := devloop.WatchUnsupported(existing, acct.Plan, watch); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		refreshed, refreshErr := s.store.RefreshDevSession(r.Context(), existing.ID, expiresAt)
		if refreshErr != nil {
			s.writeDevSessionRefreshError(w, existing, refreshErr)
			return
		}
		s.audit.Emit(r.Context(), "dev_session.refreshed", &acct.ID, map[string]any{
			"app_id": refreshed.ID, "slug": refreshed.Slug, "project": project, "workspace_id": req.WorkspaceID,
		})
		s.writeDevSession(w, r, acct, refreshed, req, http.StatusOK)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		s.writeDevSessionStoreError(w, "load developer session", slug, err)
		return
	}

	if prob := devloop.WatchUnsupported(app, acct.Plan, watch); prob != nil {
		api.WriteProblem(w, prob)
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
			revived, prob, ok := s.reviveDevSession(r, acct, slug, project, app, limits, watch)
			if prob != nil {
				api.WriteProblem(w, prob)
				return
			}
			if ok {
				refreshed, refreshErr := s.store.RefreshDevSession(r.Context(), revived.ID, expiresAt)
				if refreshErr != nil {
					s.writeDevSessionRefreshError(w, revived, refreshErr)
					return
				}
				s.audit.Emit(r.Context(), "dev_session.revived", &acct.ID, map[string]any{
					"app_id": refreshed.ID, "slug": refreshed.Slug, "project": project, "workspace_id": req.WorkspaceID, "ram_mb": refreshed.RAMMB,
				})
				s.writeDevSession(w, r, acct, refreshed, req, http.StatusOK)
				return
			}
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
				s.writeDevSessionRefreshError(w, existing, refreshErr)
				return
			}
			s.audit.Emit(r.Context(), "dev_session.refreshed", &acct.ID, map[string]any{
				"app_id": refreshed.ID, "slug": refreshed.Slug, "project": project, "workspace_id": req.WorkspaceID,
			})
			s.writeDevSession(w, r, acct, refreshed, req, http.StatusOK)
			return
		default:
			s.writeDevSessionStoreError(w, "create developer session", slug, err)
		}
		return
	}
	s.emitAppCreated(r.Context(), created)
	s.audit.Emit(r.Context(), "dev_session.created", &acct.ID, map[string]any{
		"app_id": created.ID, "slug": created.Slug, "project": project, "workspace_id": req.WorkspaceID,
	})
	s.log.Info("developer session created", "app", created.ID,
		"slug", logsanitize.Field(created.Slug), "account", acct.ID)
	s.writeDevSession(w, r, acct, created, req, http.StatusCreated)
}

// writeDevSessionRefreshError explains a failed lease renewal. The renewal
// matches no row once the janitor or `gregale dev --stop` has claimed the
// environment for teardown, which is a state the developer can act on, not a
// capacity problem. Anything else is logged with its cause before the
// generic retry answer, so a "briefly at capacity" reply is traceable.
func (s *server) writeDevSessionRefreshError(w http.ResponseWriter, app state.App, err error) {
	if errors.Is(err, state.ErrNotFound) {
		s.log.Info("developer session renewal refused: teardown in progress",
			"app", app.ID, "slug", logsanitize.Field(app.Slug), "preview_state", app.PreviewPrState)
		api.WriteProblem(w, api.ErrDevSessionTearingDown())
		return
	}
	s.writeDevSessionStoreError(w, "refresh developer session", app.Slug, err)
}

// writeDevSessionStoreError logs an unexpected store failure and answers the
// retryable capacity problem.
func (s *server) writeDevSessionStoreError(w http.ResponseWriter, operation, slug string, err error) {
	s.log.Error("developer session store error", "operation", operation,
		"slug", logsanitize.Field(slug), "err", err)
	api.WriteProblem(w, api.ErrCapacity(operation))
}

// reviveDevSession restores the caller's own deleted developer session when
// its tombstone still holds the deterministic slug. `gregale dev --stop` and
// the lease janitor tear the session down and soft-delete the app, and the
// tombstone keeps the slug for the seven-day restore window, so starting the
// same session again failed with "slug is already in use" for a week (hunt
// #10, H10-87). Only a finished teardown is revived, never another account's
// row, a different project or a changed shape; the caller then renews the
// lease and syncs a fresh deployment as for a new session.
//
// A revived watch session runs at devloop.ReviveRAMMB, and watch mode is
// checked against the session as it will come back (devloop.WatchUnsupported)
// before anything is restored: a refusal is returned as the problem and the
// tombstone stays as it was.
func (s *server) reviveDevSession(r *http.Request, acct state.Account, slug, project string, app state.App, limits api.Limits, watch *api.DevWatch) (state.App, *api.Problem, bool) {
	tomb, err := s.store.AppBySlugIncludingDeleted(r.Context(), slug)
	if err != nil {
		// Nothing to revive: the caller reports the slug conflict.
		if !errors.Is(err, state.ErrNotFound) {
			s.log.Warn("developer session tombstone lookup failed", "app", slug, "err", err)
		}
		tomb = state.App{}
	}
	if tomb.DeletedAt == nil || tomb.AccountID != acct.ID ||
		tomb.PreviewOfSlug != project || tomb.PreviewPrNumber != 0 ||
		tomb.PreviewPrState != state.PreviewPrStateTornDown ||
		tomb.Type != app.Type || (app.Type == state.AppTypeFunction && tomb.Runtime != app.Runtime) {
		return state.App{}, nil, false
	}
	ram := devloop.ReviveRAMMB(tomb.RAMMB, limits, watch)
	candidate := tomb
	candidate.RAMMB = ram
	if prob := devloop.WatchUnsupported(candidate, acct.Plan, watch); prob != nil {
		return state.App{}, prob, false
	}
	// Restore refuses a torn-down preview (ADR-20261030070000), so the session
	// reopens first; the restore's quota check then counts it as open. A failed
	// restore puts the tombstone back, where the restore guard covers it again.
	if _, err := s.store.SetPreviewPrState(r.Context(), tomb.ID, state.PreviewPrStateOpen); err != nil {
		s.log.Warn("developer session reopen failed", "app", tomb.ID, "err", err)
		return state.App{}, nil, false
	}
	restored, err := s.store.RestoreApp(r.Context(), tomb.ID, limits)
	if err != nil {
		s.log.Warn("developer session revive failed", "app", tomb.ID, "err", err)
		if _, err := s.store.SetPreviewPrState(context.WithoutCancel(r.Context()), tomb.ID, state.PreviewPrStateTornDown); err != nil {
			s.log.Warn("developer session tombstone not put back", "app", tomb.ID, "err", err)
		}
		return state.App{}, nil, false
	}
	notifyRestored := func(ctx context.Context) {
		_ = s.notif.Notify(ctx, db.NotifyAppChanged,
			fmt.Sprintf(`{"app_id":"%s","kind":"restored"}`, restored.ID))
	}
	if ram != restored.RAMMB {
		raised, err := s.store.UpdateApp(r.Context(), restored.ID, state.UpdateAppParams{RAMMB: &ram})
		if err != nil {
			// The session is back but too small for watch mode; the retry
			// takes the existing-session path, which checks it again.
			s.log.Warn("developer session RAM not raised for watch mode", "app", restored.ID, "ram_mb", restored.RAMMB, "want_mb", ram, "err", err)
			notifyRestored(r.Context())
			return state.App{}, api.ErrCapacity("revive developer session"), false
		}
		s.log.Info("developer session RAM raised for watch mode", "app", restored.ID, "from_mb", restored.RAMMB, "to_mb", ram)
		restored = raised
	}
	notifyRestored(r.Context())
	return restored, nil, true
}

func (s *server) destroyDevSession(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project := r.PathValue("project")
	if !validSlug(project) {
		s.notFound(w, "no such developer session")
		return
	}
	// A developer environment is normally selected by the workspace that made
	// it. ?app=<dev slug> selects it by its backing app instead, so an
	// environment left behind by a moved, renamed or deleted checkout can be
	// stopped from anywhere (`gregale dev list`, `gregale dev --stop --app`).
	slug := r.URL.Query().Get("app")
	if slug == "" {
		workspaceID := r.URL.Query().Get("workspace_id")
		if !validDevWorkspaceID(workspaceID) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid workspace ID", "workspace_id must be 32 lowercase hexadecimal characters"))
			return
		}
		slug = devSessionSlug(acct.ID, project, workspaceID)
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID || app.PreviewOfSlug != project || !state.IsDeveloperApp(app) {
		s.notFound(w, "no such developer session")
		return
	}
	s.destroyPreviewApp(w, r, acct, app, "dev_session.destroyed")
}

// listDevSessions lists the account's developer environments, wherever they
// were started, so an environment whose checkout moved can still be found
// and stopped. It never renews a lease.
func (s *server) listDevSessions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	rows, err := s.store.ListPreviewsForAccount(r.Context(), acct.ID)
	if err != nil {
		s.writeDevSessionStoreError(w, "list developer sessions", "", err)
		return
	}
	out := api.DevSessionListResponse{Sessions: []api.DevSessionSummary{}}
	for _, app := range rows {
		if app.AccountID != acct.ID || !state.IsDeveloperApp(app) || state.IsAPISandbox(app) {
			continue
		}
		item := api.DevSessionSummary{
			Project: app.PreviewOfSlug, App: app.Slug, URL: appURLForDomain(app.Slug, s.domain),
			State: app.PreviewPrState,
		}
		if app.PreviewExpiresAt != nil {
			item.ExpiresAt = app.PreviewExpiresAt.UTC()
		}
		out.Sessions = append(out.Sessions, item)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// getDevSession reports the developer environment selected by the account,
// project, and optional workspace identity. It is read-only: unlike the PUT
// upsert it never renews the lease or provisions PostgreSQL, so scripts and
// `gregale dev info` can inspect an environment without extending it.
func (s *server) getDevSession(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	app, ok := s.developerSessionApp(r, acct, project, workspaceID)
	if !ok {
		s.notFound(w, "no such developer session")
		return
	}
	postgres, err := s.describeDevPostgres(r.Context(), app)
	if err != nil {
		s.writeDevSessionStoreError(w, "load developer PostgreSQL", app.Slug, err)
		return
	}
	buckets, err := s.object.DescribeDevBuckets(r, app)
	if err != nil {
		object.BucketProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var expiresAt time.Time
	if app.PreviewExpiresAt != nil {
		expiresAt = app.PreviewExpiresAt.UTC()
	}
	writeJSON(w, http.StatusOK, api.DevSessionResponse{
		App: s.appResponseWithContext(r.Context(), app, acct.Plan), ExpiresAt: expiresAt, Postgres: postgres, Buckets: buckets,
		Watch:       devloop.LoadWatch(r.Context(), s.store, app.ID),
		LivePatches: devLivePatches(),
	})
}

const (
	devSyncHistoryDefaultLimit = 20
	devSyncHistoryMaxLimit     = 100
)

var devSyncHistoryPhases = map[string]bool{
	"sync": true, "patch": true, "cache": true, "build": true,
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
	// The target and the verdict are the platform's, not the client's: an old
	// or modified CLI must not move the fleet edit-to-live SLO.
	req.SLOTargetMS = api.DevEditToLiveTarget.Milliseconds()
	req.WithinSLO = req.Status == "live" && req.EditToLiveMS <= req.SLOTargetMS
	row, err := history.RecordDevSyncHistory(r.Context(), state.DevSyncHistory{
		AppID: app.ID, DeploymentID: req.DeploymentID, Status: req.Status,
		EditToLiveMS: req.EditToLiveMS, SLOTargetMS: req.SLOTargetMS,
		WithinSLO: req.WithinSLO, Phases: phases,
	})
	if err != nil {
		s.writeDevSessionStoreError(w, "record developer sync history", app.Slug, err)
		return
	}
	s.devLoopMetrics.ObserveSync(req, devloop.LoadWatch(r.Context(), s.store, app.ID) != nil)
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
		s.writeDevSessionStoreError(w, "load developer sync history", app.Slug, err)
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
