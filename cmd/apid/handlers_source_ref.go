// Handlers for the headless source-ref deploy path (issue #739,
// DEPLOY-PROV-4 / ADR-092). The customer-facing entry point is
//
//	POST /v1/apps/{slug}/deployments/source-ref
//
// accepting JSON {repo, ref, format}. Server resolves the durable
// install row, asks the githubd gRPC bridge to mint/cache the
// installation token and fetch the upstream tarball,
// spools it under FAAS_SPOOL_ROOT, validates its tarball shape,
// creates the deployment row (Kind=DeploymentKindGitHub), and
// emits the `deploy.source_ref` audit row.
//
// Auth chain (cmd/apid/server.go):
//
//	authLimited → requireMFA → requireScope(ScopesDeployWriteSurface) → idempotent → handleSourceRefDeploy
//
// Why a separate file: the handler reads SourceRefStreamer + IDOR
// + audit + cap helper wiring in one place, mirrors the canonical
// `createDeployment` shape (cmd/apid/handlers.go:309) but without
// the multipart / image / sidecar / override / signature gates —
// none of which apply to a GitHub-tarball pull. Adding the
// gates here would silently double-run; the source-ref path is a
// narrow, well-defined seam.
//
// Tokens: the install token is minted and scoped inside githubd's
// streaming call. It is NOT persisted to the deployment row, NOT
// logged, and NOT returned in the wire response. The raw value
// never crosses the file system or the audit sink.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// handleSourceRefDeploy is the source-ref variant of
// createDeployment (cmd/apid/handlers.go:309). Mirrors the
// gate-chain shape (`loadAppAndPreflight` + IDOR + decode +
// validate + enqueue + audit + maybeFlipMFA) but swaps
// multipart-tarball for githubd-streamed-tarball and pins a
// distinct audit kind (`deploy.source_ref`).
//
// Extracted helpers in this file stay ≤ 50 lines each per the
// CLAUDE.md handler cap:
//
//   - resolveInstallToken     — durable install lookup, 404 on missing row
//   - streamSourceTarball     — gRPC streaming + cap-bound + spool
//   - auditSourceRefDeploy    — emits deploy.source_ref {…} + log
//   - isValidRef              — ref-shape guard before the gRPC fetch
func (s *server) handleSourceRefDeploy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok, limits := s.loadAppAndPreflight(w, r, acct)
	if !ok {
		return
	}
	var req api.SourceRefDeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", err.Error()))
		return
	}
	req.Repo = strings.TrimSpace(req.Repo)
	req.Ref = strings.TrimSpace(req.Ref)
	if req.Repo == "" || req.Ref == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Validation failed", "repo and ref are required"))
		return
	}
	if !isValidRef(req.Ref) {
		api.WriteProblem(w, api.ErrInvalidRef(req.Ref))
		return
	}
	// Forward-compat: only "tarball" is wired in PR-A; any other
	// value is a 400 so future readers don't silently drive a
	// half-implemented format.
	if req.Format != "" && req.Format != fieldNameTarball {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Unsupported format", "format must be '"+fieldNameTarball+"' (PR-A)"))
		return
	}

	installID, p := s.resolveInstallToken(r.Context(), acct)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}

	maxBytes := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	stream, p := s.streamSourceTarball(r.Context(), acct, installID, req.Repo, req.Ref, maxBytes)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	defer func() { _ = stream.Body.Close() }()

	spoolPath, spoolBytes, p := validateAndSpool(stream.Body, limits)
	if p != nil {
		// A gRPC stream can fail after delivering some bytes. In that
		// case validateAndSpool only sees the pipe's read error; close
		// the stream first so the terminal *api.Problem is available
		// and the caller gets 503/source_ref_unavailable rather than a
		// misleading 400/bad-source response.
		_ = stream.Body.Close()
		if stream.Stats != nil && stream.Stats.Err != nil {
			if problem := api.AsProblem(stream.Stats.Err); problem != nil {
				api.WriteProblem(w, problem)
			} else {
				api.WriteProblem(w, api.ErrSourceRefUnavailable("source stream ended with an error"))
			}
			return
		}
		api.WriteProblem(w, p)
		return
	}
	// Close immediately after the reader reaches EOF so the gRPC client
	// publishes terminal stream metadata (including the resolved SHA and
	// any transport error) before the handler makes acceptance decisions.
	if closeErr := stream.Body.Close(); closeErr != nil {
		if problem := api.AsProblem(closeErr); problem != nil {
			api.WriteProblem(w, problem)
		} else {
			api.WriteProblem(w, api.ErrSourceRefUnavailable("source stream ended with an error"))
		}
		return
	}
	// Truncated means the codeload archive exceeded
	// SourceTarballMaxMB mid-stream; map that to RFC 7807 413.
	if stream.Stats != nil && stream.Stats.Truncated {
		api.WriteProblem(w, api.ErrSourceTooLarge(limits, spoolBytes))
		return
	}
	if stream.Stats != nil && stream.Stats.Err != nil {
		if problem := api.AsProblem(stream.Stats.Err); problem != nil {
			api.WriteProblem(w, problem)
		} else {
			api.WriteProblem(w, api.ErrSourceRefUnavailable("source stream ended with an error"))
		}
		return
	}
	resolvedSHA := ""
	if stream.Stats != nil {
		resolvedSHA = stream.Stats.ResolvedCommitSHA
	}
	if resolvedSHA == "" && isCanonicalCommitSHA(req.Ref) {
		resolvedSHA = req.Ref
	}
	if !isCanonicalCommitSHA(resolvedSHA) {
		api.WriteProblem(w, api.ErrSourceRefUnavailable("githubd did not return an immutable commit SHA"))
		return
	}
	sourceAccepted := false
	defer func() {
		if !sourceAccepted {
			_ = os.Remove(spoolPath)
		}
	}()
	if prob := scanSourceTarballSecrets(spoolPath, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	// Issue #977 / ADR-116: validate annotation fields carried on
	// the JSON body. The source-ref path uses the JSON wire (vs the
	// tarball path's multipart), so the values arrive on req.Reason /
	// req.Tag / req.DeployedBy / req.PRNumber directly. Same DB CHECK
	// mirrors as the tarball path; nil/zero values pass through to NULL
	// on the row.
	ann := annotationFromRequest(req)
	if prob := validateAnnotationForm(ann); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	prev, _ := s.store.LatestDeployment(r.Context(), app.ID)
	res, err := apidsource.Enqueue(r.Context(), s.store, s.notif, apidsource.EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindGitHub,
		SourcePath:  spoolPath,
		SourceBytes: spoolBytes,
		SourceURL:   fmt.Sprintf("github://%s@%s", req.Repo, resolvedSHA),
		CommitSHA:   resolvedSHA,
		LogSpool:    spoolRoot(),
		Log:         s.log,
		// Issue #606 / SAFE-RELEASES-E.1: server-stamped actor
		// attribution. The source-ref path is the dashboard +
		// CLI flow that streams a GH repo through the apid
		// pipeline (cmd/apid/handlers_source_ref.go), NOT the
		// githubd_bridge push-triggered path (which stamps
		// "github" + pusher at the bridge itself — see
		// cmd/apid/githubd_bridge.go::EnqueueBuild). This
		// handler runs over HTTP, so the via classifier routes
		// through cmd/apid.deploy_actor.routeKindForRequest.
		ActorUserID: acct.ID,
		ActorVia:    routeKindForRequest(r),
		ActorFromIP: middleware.ClientIP(r),
		// Issue #977 / ADR-116: annotation surface forwarded onto
		// the deployment row from the request's annotationForm.
		// nil/zero values are dropped by EnqueueParams handling.
		Reason:     ann.Reason,
		Tag:        ann.Tag,
		DeployedBy: ann.DeployedBy,
		PRNumber:   ann.PRNumber,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
		return
	}
	sourceAccepted = true
	s.auditSourceRefDeploy(r.Context(), acct, app, res, prev, req, resolvedSHA, installID, ann)
	// Reload the deployment row so the response carries the
	// canonical wire shape (mirrors createDeployment's
	// LatestDeployment re-read).
	d, err := s.store.LatestDeployment(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read deployment"))
		return
	}
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(d, app))
}

// resolveInstallToken reads the durable install row from
// state.Store (state.ErrNotFound → 404 code=github_install_not_found).
// githubd repeats the account/install binding check and owns token
// minting inside StreamSourceRef, so the raw token never crosses
// the apid process boundary.
func (s *server) resolveInstallToken(ctx context.Context, acct state.Account) (int64, *api.Problem) {
	inst, err := s.store.GitHubInstallForAccount(ctx, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return 0, api.ErrGitHubInstallNotFound()
		}
		return 0, api.ErrCapacity("could not load install")
	}
	if inst.InstallationID == 0 {
		return 0, api.ErrGitHubInstallNotFound()
	}
	return inst.InstallationID, nil
}

// streamSourceTarball opens a server-streaming gRPC to githubd.
// Returns the *StreamSourceRefResult so the caller can read
// truncated + bytes_streamed via Stats on Close.
//
// The githubd-side cap is passed through as
// maxArchiveBytes=SourceTarballMaxMB*MiB so the wire shape
// fails closed mid-stream rather than OOM'ing the box.
func (s *server) streamSourceTarball(ctx context.Context, acct state.Account, installID int64, repo, ref string, maxArchiveBytes int64) (*StreamSourceRefResult, *api.Problem) {
	res, err := s.githubd.StreamSourceRef(ctx, acct.ID, installID, repo, ref, maxArchiveBytes)
	if err != nil {
		if p := api.AsProblem(err); p != nil {
			return nil, p
		}
		return nil, api.ErrSourceRefUnavailable("stream source tarball failed")
	}
	if res == nil || res.Body == nil {
		return nil, api.ErrSourceRefUnavailable("githubd returned empty stream")
	}
	return res, nil
}

// auditSourceRefDeploy emits the `deploy.source_ref` audit row
// with the canonical {repo, ref, source_sha, install_id, ...}
// payload. log line mirrors createDeployment's
// "deployment created" — slug, deployment id, source SHA. The
// raw install token is NEVER in the payload (the token stays
// scoped to the streaming call only).
//
// Issue #606 / SAFE-RELEASES-E.1: per-call actor attribution.
// The deployment row was just stamped with the four actor
// columns by apidsource.Enqueue — we re-read it here so the
// audit row carries the resolved "<via>:<id>" actor on
// events.actor AND the actor_* payload keys (via mergeActorAudit).
// Issue #977 / ADR-116: the audit data{} map gains 4 keys
// (reason / tag / deployed_by / pr_number) when present via
// mergeAnnotationAudit (see handlers_source_tarball.go). nil/zero
// values are omitted so pre-feature rows stay byte-identical at
// the JSON layer.
func (s *server) auditSourceRefDeploy(ctx context.Context, acct state.Account, app state.App, res apidsource.EnqueueResult, prev state.Deployment, req api.SourceRefDeployRequest, resolvedSHA string, installID int64, ann annotationForm) {

	s.log.Info("source-ref deployment enqueued",
		"deployment", res.DeploymentID,
		"app", app.ID,
		"repo", req.Repo,
		"ref", req.Ref,
		"source_sha", resolvedSHA,
		"deployed_by", ann.DeployedBy,
		"pr_number", ann.PRNumber,
		"tag", ann.Tag,
	)
	// Re-read the just-written deployment row to pick up the
	// actor columns (apidsource.Enqueue stamped them in its tx).
	d, dErr := s.store.DeploymentByID(ctx, res.DeploymentID)
	if dErr != nil {
		// MEDIUM review #4: when the read-back fails we must
		// NOT fall through with a zero Deployment — that would
		// make resolvedActorString emit ':unknown' (via empty,
		// '<via>:unknown' branch) and bypass EmitAs's actor==''
		// fallback. The audit row would land with corrupt
		// attribution exactly when forensics needs it most.
		// Early-return without an audit row: the durable
		// deployment row is already committed (with the
		// structured actor columns stamped at INSERT time), so
		// the SOC 2 / GDPR audit-trail question still has an
		// answer via deployments.deployed_by_user_id /
		// deployed_via / deployed_from_ip; the events-table
		// row just doesn't get stamped this time. Operator
		// can grep by deployment_id and recover attribution
		// from the row directly.
		s.log.Warn("auditSourceRefDeploy: skip audit row, read deployment for actor attribution failed",
			"deployment", res.DeploymentID, "err", dErr)
		return
	}
	resolvedActor := resolvedActorString(d.DeployedVia, d.DeployedByUserID, d.PusherLogin)
	data := map[string]any{
		"app_id":        app.ID,
		"deployment_id": res.DeploymentID,
		"build_id":      res.BuildID,
		"repo":          req.Repo,
		"ref":           req.Ref,
		"source_sha":    resolvedSHA,
		"install_id":    installID,
		"supersedes":    prev.ID,
	}
	// Issue #977 / ADR-116: mirror the annotation surface into
	// the deploy.source_ref audit row. mergeAnnotationAudit is
	// "omit when zero" so pre-feature rows stay byte-identical
	// at the JSON layer.
	mergeAnnotationAudit(data, ann)
	s.audit.EmitAs(ctx, resolvedActor, "deploy.source_ref", &acct.ID, mergeActorAudit(data, d.DeployedByUserID, d.DeployedVia, d.DeployedFromIP, d.PusherLogin))
}

// isValidRef is the cheap pre-flight ref-shape guard. Anything
// rejected here never reaches the githubd bridge. Branch / tag /
// short-SHA / 40-char SHA all clear the shape predicate; githubd
// resolves non-SHA refs and the handler accepts only its canonical
// 40-character result.
//
// Mirrors the predicate githubd uses for codeload paths
// (cmd/githubd/source_ref_streamer.go::isValidSourceRefRef).
// We intentionally do NOT call that internal helper — apid ↔
// githubd only communicate over the gRPC seam (CLAUDE.md component
// ownership).
func isValidRef(ref string) bool {
	if len(ref) < 1 || len(ref) > 200 || ref == "@" || strings.HasPrefix(ref, "/") ||
		strings.HasSuffix(ref, "/") || strings.Contains(ref, "//") ||
		strings.Contains(ref, "..") || strings.Contains(ref, "@{") ||
		strings.HasSuffix(ref, ".") {
		return false
	}
	if len(ref) < 7 && isLowerHexRef(ref) {
		return false
	}
	if strings.ContainsRune(ref, 92) || strings.ContainsRune(ref, 0) ||
		strings.ContainsRune(ref, 96) || strings.ContainsRune(ref, 63) ||
		strings.ContainsRune(ref, 37) || strings.ContainsRune(ref, 91) ||
		strings.ContainsRune(ref, 93) || strings.ContainsRune(ref, 123) ||
		strings.ContainsRune(ref, 125) || strings.ContainsRune(ref, 60) ||
		strings.ContainsRune(ref, 62) || strings.ContainsRune(ref, 34) ||
		strings.ContainsRune(ref, 39) || strings.ContainsRune(ref, '*') {
		return false
	}
	for _, r := range ref {
		if r <= 0x20 || r == 0x7f || r == 58 || r == 94 || r == 126 {
			return false
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") ||
			strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func isLowerHexRef(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func isCanonicalCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
