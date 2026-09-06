package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
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
		writeJSON(w, http.StatusOK, api.DevSessionResponse{
			App: s.appResponse(refreshed, acct.Plan), ExpiresAt: expiresAt,
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
			api.WriteProblem(w, api.ErrPlanLimitApps(limits, quotaErr.Observed))
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
			writeJSON(w, http.StatusOK, api.DevSessionResponse{
				App: s.appResponse(refreshed, acct.Plan), ExpiresAt: expiresAt,
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
	writeJSON(w, http.StatusCreated, api.DevSessionResponse{
		App: s.appResponse(created, acct.Plan), ExpiresAt: expiresAt,
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
