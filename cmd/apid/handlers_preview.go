// Package main: preview destroy handlers (Mega-C PR-1 / issue #961
// leaf 3 — one-click preview destroy from a PR comment).
//
// The new POST /v1/preview/{slug}/destroy endpoint tears down a
// preview apps row at the customer's explicit request (typically
// fired from a "Tear down this preview" link in a GitHub PR
// comment, posted by pkg/githubd's previewCommentOnce helper).
// Distinct from DELETE /v1/apps/{slug} because (a) the preview
// teardown semantic also stamps preview_pr_state='torn_down' so
// the janitor doesn't race the destroy on a subsequent tick, and
// (b) the audit kind is distinct (`preview.destroyed_by_customer`
// vs `app.deleted`) so SIEM dashboards can branch.
//
// Pre-flight invariants:
//
//   - The row MUST be a preview (PreviewOfSlug != ""). A bug in
//     the destroy chain cannot destroy a production app through
//     this route — the handler refuses with 404 before any
//     write.
//   - The destroy reuses the janitor's ordering:
//     SetPreviewPrState('torn_down') → SoftDeleteAppCascade →
//     NotifyAppDelete. Same audit + same notify as the janitor's
//     teardown, so dashboards render preview destroy and janitor
//     teardown identically.
package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// destroyPreview handles POST /v1/preview/{slug}/destroy.
//
// Auth: authLimited → requireMFA → requireScope(DeployWriteSurface).
// Returns 204 on success, 404 on missing-or-not-preview rows, 403
// if the row belongs to a different account (loadApp's contract).
//
// The audit kind `preview.destroyed_by_customer` is the canonical
// signal for "customer explicitly tore this preview down"; the
// janitor's `preview_pr_state='torn_down'` tombstone is the
// signal for "TTL elapsed, automatic teardown". Branching on
// `kind` in audit dashboards separates customer vs. janitor
// outcomes cleanly.
func (s *server) destroyPreview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	// Preview-only gate: a production app id is the canonical
	// "this handler is the wrong tool" outcome. Returning 404
	// (rather than 400) so the wire shape is indistinguishable
	// from "row not found" — a customer who reaches this route
	// with a production slug should not be able to probe the
	// preview-only shape via error-message diff.
	if app.PreviewOfSlug == "" {
		api.WriteProblem(w, api.NewProblem(
			http.StatusNotFound, "preview_not_found",
			"Preview not found",
			"the slug does not identify a preview app; use DELETE /v1/apps/{slug} to destroy a production app"))
		return
	}

	s.destroyPreviewApp(w, r, acct, app, "preview.destroyed_by_customer")
}

// destroyPreviewApp applies the shared preview tombstone ordering used by
// both GitHub previews and CLI-managed developer sessions.
func (s *server) destroyPreviewApp(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, auditKind string) {
	if _, err := s.store.SetPreviewPrState(r.Context(), app.ID, state.PreviewPrStateTornDown); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Race: the row vanished between loadApp and the
			// tombstone. Treat as success — the customer's
			// intent ("destroy this preview") is satisfied by
			// the deletion. Return 204 to keep the wire stable
			// (no 404 surprise after a successful POST).
			w.WriteHeader(http.StatusNoContent)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("set preview torn_down"))
		return
	}
	if _, err := s.store.SoftDeleteAppCascade(r.Context(), app.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Same race as above.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("soft delete preview"))
		return
	}

	// Notify AFTER both writes succeed — schedd's subscriber
	// will see a row that's already status='deleted', which
	// avoids any chance of a wake racing the tombstone. The
	// payload mirrors the janitor's: kind='preview_teardown'
	// so the schedd-side fan-out can route the same way.
	if s.notif != nil {
		payload := fmt.Sprintf(`{"app_id":"%s","slug":"%s","kind":"preview_teardown"}`, app.ID, app.Slug)
		if err := s.notif.Notify(r.Context(), db.NotifyAppDelete, payload); err != nil {
			s.log.Warn("apid: notify app_delete failed (destroy preview)",
				"app_id", app.ID, "err", err)
		}
	}

	// ADR-035 audit emit. Distinct kind from `app.deleted` so
	// the SIEM dashboard's "customer-initiated vs. TTL-driven
	// teardown" branch is observable. data carries enough to
	// reconstruct the destroy intent (parent slug + PR number)
	// after the row is soft-deleted.
	s.audit.Emit(r.Context(), auditKind, &acct.ID, map[string]any{
		"app_id":          app.ID,
		"slug":            app.Slug,
		"preview_of_slug": app.PreviewOfSlug,
		"preview_pr":      app.PreviewPrNumber,
	})

	s.log.Info("preview destroyed by customer",
		"app", app.ID, "slug", app.Slug,
		"audit_kind", auditKind,
		"parent_slug", app.PreviewOfSlug,
		"pr_number", app.PreviewPrNumber,
		"account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}
