package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	previewCreateDefaultTTL = 7 * 24 * time.Hour
	previewCreateMaxTTL     = 30 * 24 * time.Hour
)

// createPreview provisions the stable PR preview row for a production app.
// The source-ref deploy endpoint remains the separate source transport; this
// split lets callers create the environment once and retry a deployment into
// it without accidentally creating another app.
func (s *server) createPreview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	parent, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if parent.PreviewOfSlug != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Production app required", "a preview cannot be used as the parent for another preview"))
		return
	}
	req, ttl, prob := decodePreviewCreateRequest(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	slug := fmt.Sprintf("pr-%d-%s", req.PRNumber, parent.Slug)
	if !validSlug(slug) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Preview slug too long", "shorten the parent app slug so the generated preview slug is at most 40 characters"))
		return
	}
	expiresAt := time.Now().UTC().Add(ttl)
	created, isNew, prob := s.provisionPreview(r, acct, parent, req, slug, expiresAt)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if isNew {
		s.log.Info("preview created", "app", created.ID, "slug", created.Slug, "parent", parent.Slug, "pr", req.PRNumber, "account", acct.ID)
		s.audit.Emit(r.Context(), "preview.created", &acct.ID, map[string]any{
			"app_id": created.ID, "slug": created.Slug, "parent_slug": parent.Slug, "pr_number": req.PRNumber,
			"ttl_hours": int(ttl / time.Hour),
		})
		s.emitAppCreated(r.Context(), created)
	}
	resp := s.appResponseWithContext(r.Context(), created, acct.Plan)
	if isNew {
		resp.Status = api.AppStatusUndeployed
	}
	status := http.StatusOK
	if isNew {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.withParkedDeploymentRef(r.Context(), resp, created))
}

func decodePreviewCreateRequest(r *http.Request) (api.CreatePreviewRequest, time.Duration, *api.Problem) {
	var req api.CreatePreviewRequest
	if err := decodeJSON(r, &req); err != nil {
		return req, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error())
	}
	if req.PRNumber <= 0 {
		return req, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid pull request", "pr_number must be greater than zero")
	}
	ttl, prob := previewCreateTTL(req.TTLHours)
	return req, ttl, prob
}

func (s *server) provisionPreview(r *http.Request, acct state.Account, parent state.App, req api.CreatePreviewRequest, slug string, expiresAt time.Time) (state.App, bool, *api.Problem) {
	if existing, err := s.store.AppBySlug(r.Context(), slug); err == nil {
		if !samePreview(existing, parent, req.PRNumber) {
			return state.App{}, false, previewSlugTakenProblem(slug)
		}
		if reopened, reopenErr := s.store.RefreshPRPreview(r.Context(), existing.ID, expiresAt); reopenErr == nil {
			existing = reopened
		} else if !errors.Is(reopenErr, state.ErrNotFound) {
			return state.App{}, false, api.ErrCapacity("refresh preview")
		}
		return existing, false, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.App{}, false, api.ErrCapacity("look up preview")
	}
	created, err := s.store.CreateAppIfUnderQuota(r.Context(), previewAppFromParent(parent, slug, req.PRNumber, expiresAt), api.MustLimitsFor(acct.Plan))
	if err == nil {
		return created, true, nil
	}
	if errors.Is(err, state.ErrConflict) {
		existing, lookupErr := s.store.AppBySlug(r.Context(), slug)
		if lookupErr == nil && samePreview(existing, parent, req.PRNumber) {
			return existing, false, nil
		}
		return state.App{}, false, previewSlugTakenProblem(slug)
	}
	var qe *state.QuotaError
	if errors.As(err, &qe) {
		return state.App{}, false, api.ErrPlanLimitApps(api.MustLimitsFor(acct.Plan), qe.Observed)
	}
	return state.App{}, false, api.ErrCapacity("create preview")
}

func previewSlugTakenProblem(slug string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeValidation,
		"Preview slug taken", fmt.Sprintf("generated preview slug %q is already in use", slug))
}

func previewCreateTTL(hours int) (time.Duration, *api.Problem) {
	if hours == 0 {
		return previewCreateDefaultTTL, nil
	}
	if hours < 1 || hours > int(previewCreateMaxTTL/time.Hour) {
		return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid preview TTL",
			"ttl_hours must be between 1 and 720")
	}
	return time.Duration(hours) * time.Hour, nil
}

func samePreview(app, parent state.App, prNumber int) bool {
	return app.AccountID == parent.AccountID && app.PreviewOfSlug == parent.Slug && app.PreviewPrNumber == prNumber
}

func previewAppFromParent(parent state.App, slug string, prNumber int, expiresAt time.Time) state.App {
	return state.App{
		AccountID: parent.AccountID, Slug: slug, Visibility: parent.Visibility, Type: parent.Type,
		Runtime: parent.Runtime, RAMMB: parent.RAMMB, CPUMillicores: parent.CPUMillicores,
		IdleTimeoutS: parent.IdleTimeoutS, MaxConcurrency: parent.MaxConcurrency, MinInstances: parent.MinInstances,
		EgressAllowlist:       append([]netip.Prefix(nil), parent.EgressAllowlist...),
		PublicAuthIPAllowlist: append([]netip.Prefix(nil), parent.PublicAuthIPAllowlist...),
		ProjectID:             parent.ProjectID, RootDir: parent.RootDir, WorkloadName: parent.WorkloadName,
		WorkloadClass: parent.WorkloadClass, StreamingEnabled: parent.StreamingEnabled,
		WebSocketEnabled: parent.WebSocketEnabled, RouteMetricsEnabled: parent.RouteMetricsEnabled,
		AppProtocol: parent.AppProtocol, MaintenanceMode: parent.MaintenanceMode,
		OnlyAllowDeclaredRoutes: parent.OnlyAllowDeclaredRoutes,
		DeclaredRoutes:          append([]state.DeclaredRoute(nil), parent.DeclaredRoutes...),
		RequireSigned:           parent.RequireSigned, StartCommand: parent.StartCommand, Manifest: parent.Manifest,
		WarmSnapshotEnabled: parent.WarmSnapshotEnabled, RequireAuthn: parent.RequireAuthn,
		PublicAuthMode: parent.PublicAuthMode, ConsumerAuthMode: parent.ConsumerAuthMode,
		WarmSnapshotMinRequests: parent.WarmSnapshotMinRequests, WarmSnapshotMinMs: parent.WarmSnapshotMinMs,
		EvictionPriority: parent.EvictionPriority, Status: state.AppActive,
		PreviewOfSlug: parent.Slug, PreviewPrNumber: prNumber, PreviewPrState: state.PreviewPrStateOpen,
		PreviewExpiresAt: &expiresAt,
	}
}
