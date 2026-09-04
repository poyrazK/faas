// cmd/apid/handlers_source_tarball.go — the local-tarball deploy
// path (issue #961 / Mega-A PR-1). The CLI is the producer/trust root
// for the tarball's provenance; apid still validates archive shape and
// scans the extracted source at ingress. It does NOT consult
// github_installations or attempt a server-side git fetch. See
// docs/adr/0XX-local-tarball-deploy-trust-root.md for the full trust
// model.
//
// Wire shape:
//
//	POST /v1/apps/{slug}/deployments/source-tarball
//	Content-Type: multipart/form-data
//	Fields:
//	  - tarball: gzipped tar archive (required)
//	  - sidecar: JSON {"repo": "owner/name", "ref": "<optional>"}
//
// Auth chain (cmd/apid/server.go):
//
//	authLimited → requireMFA → requireScope(ScopesDeployWriteSurface) → idempotent → handleSourceTarballDeploy
//
// Why a separate file from handlers_source_ref.go: the trust gates
// diverge. handleSourceRefDeploy requires a durable install row + a
// 40-char SHA + a server-side fetch through githubd. This handler
// accepts whatever the CLI produced, validates tarball shape, spools
// it, and enqueues a DeploymentKindTarball build row. The two paths
// share only validateAndSpool + apidsource.Enqueue + the final audit
// + the wire response.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// sidecarPayload is the JSON sidecar the CLI uploads next to the
// tarball. repo + ref are both optional — the handler records them
// on the build row for audit/provenance, but the build pipeline does
// NOT use them to fetch upstream.
type sidecarPayload struct {
	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

// fieldNameTarball is the multipart field name on both
// source-ref + source-tarball deploy routes. Shared const because
// goconst is package-wide (cmd/apid) and the literal "tarball"
// would otherwise appear in three call sites. Keep the const
// here (the new file) so any future field rename lands on a
// single line.
const fieldNameTarball = "tarball"

func (s *server) handleSourceTarballDeploy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok, limits := s.loadAppAndPreflight(w, r, acct)
	if !ok {
		return
	}

	// MED-2 fix: cap the request body BEFORE multipart parsing so a
	// malicious oversize body trips http.MaxBytesError on the first
	// read rather than spooling the whole thing to os.TempDir() and
	// OOMing the daemon. The cap is a hard ceiling — we use
	// (max + 1 MiB headroom) so the multipart boundary + headers
	// don't push a legitimate upload over the line. Content-Length
	// short-circuits chunked / unknown-length attacks at the
	// header layer.
	maxBytes := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	maxBody := maxBytes + (1 << 20)
	if r.ContentLength > maxBody {
		api.WriteProblem(w, api.ErrSourceTooLarge(limits, r.ContentLength))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	// ParseMultipartForm returns the whole body into memory up to
	// maxMemory; beyond that the rest spills to disk under
	// os.TempDir(). The actual size gate is the upstream
	// http.MaxBytesReader (line above) + the Content-Length pre-
	// check, both of which trip CodeSourceTooLarge before this
	// call. The 32 MiB in-memory budget here is the multipart
	// parser's "spill to disk after this many bytes" cap, not the
	// request-body cap.
	//
	// gosec G120 flags this as "unbounded form parsing" — false
	// positive: the upstream MaxBytesReader bound is the request-
	// body cap. The //nolint:gosec directive documents the
	// reasoning; remove only if the upstream cap is removed.
	if err := r.ParseMultipartForm(32 << 20); err != nil { //nolint:gosec // see comment above
		// MaxBytesReader surfaces an oversize body as
		// *http.MaxBytesError on the multipart parser.
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			api.WriteProblem(w, api.ErrSourceTooLarge(limits, -1))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad multipart", err.Error()))
		return
	}

	tarballFile, _, err := r.FormFile(fieldNameTarball)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing tarball", "tarball field is required"))
		return
	}
	defer func() { _ = tarballFile.Close() }()

	// Sidecar is optional. Missing sidecar → empty provenance
	// fields on the build row; the deploy still works.
	var sidecar sidecarPayload
	if raw := r.FormValue("sidecar"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &sidecar); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad sidecar", err.Error()))
			return
		}
	}

	// Issue #977 / ADR-116: read the four annotation form fields.
	// Empty / missing → NULL on the row (pgstore handles the
	// collapse). The CLI side emits them via pkg/api/multipart.go
	// newMultipartWriter. Validation mirrors the DB CHECK:
	//   - reason ≤280 chars (CodeValidation otherwise)
	//   - tag in closed-set (CodeValidation otherwise)
	//   - pr_number > 0 when present (CodeValidation otherwise)
	ann := annotationFormFromRequest(r)
	if prob := validateAnnotationForm(ann); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	// Cap the read at the plan limit so a malicious oversize body
	// trips CodeSourceTooLarge before we spool it to disk. The
	// outer http.MaxBytesReader above already bounded the request
	// body; this io.LimitReader is the second-line belt-and-braces
	// check inside validateAndSpool.
	spoolPath, spoolBytes, prob := validateAndSpool(io.LimitReader(tarballFile, maxBytes+1), limits)
	if prob != nil {
		api.WriteProblem(w, prob)
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

	sourceURL := "local-tar://" + sidecar.Repo
	commitSHA := sidecar.Ref // informational only; not used by the build pipeline

	res, err := apidsource.Enqueue(r.Context(), s.store, s.notif, apidsource.EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  spoolPath,
		SourceBytes: spoolBytes,
		SourceURL:   sourceURL,
		CommitSHA:   commitSHA,
		LogSpool:    spoolRoot(),
		Log:         s.log,
		// Issue #606 / SAFE-RELEASES-E.1: server-stamped actor
		// attribution (cmd/apid/deploy_actor.go). The local
		// tarball path is HTTP-routed, so the via classifier is
		// request-shape-derived.
		ActorUserID: acct.ID,
		ActorVia:    routeKindForRequest(r),
		ActorFromIP: middleware.ClientIP(r),
		// Issue #977 / ADR-116: annotation surface forwarded onto
		// the deployment row from the request's annotationForm.
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

	s.auditLocalTarballDeploy(r.Context(), acct, app, res, sidecar, spoolBytes, ann)

	d, err := s.store.LatestDeployment(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read deployment"))
		return
	}
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(d, app))
}

// auditLocalTarballDeploy emits the `deploy.local_tarball` audit row
// with the canonical {repo, ref, source_bytes} payload. Distinct
// from `deploy.source_ref` so the audit log can branch on wire shape
// without inspecting source URLs.
//
// Issue #606 / SAFE-RELEASES-E.1: per-call actor attribution.
// Re-reads the just-written deployment row (apidsource.Enqueue
// stamped the four actor columns in its tx) so the audit row
// carries the resolved "<via>:<id>" actor on events.actor AND
// the actor_* payload keys (via mergeActorAudit). The
// constructor-baked "apid" actor only shows up if the
// DeploymentByID read fails AND the via classifier defaults.
// Issue #977 / ADR-116: the audit data{} map gains 4 keys
// (reason / tag / deployed_by / pr_number) when present. nil/zero
// values are omitted from the map so pre-feature rows stay byte-
// identical at the JSON layer.
func (s *server) auditLocalTarballDeploy(ctx context.Context, acct state.Account, app state.App, res apidsource.EnqueueResult, sidecar sidecarPayload, sourceBytes int64, ann annotationForm) {
	s.log.Info("local-tarball deployment enqueued",
		"deployment", res.DeploymentID,
		"app", app.ID,
		"repo", sidecar.Repo,
		"ref", sidecar.Ref,
		"source_bytes", sourceBytes,
		"deployed_by", ann.DeployedBy,
		"pr_number", ann.PRNumber,
		"tag", ann.Tag,
	)
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
		s.log.Warn("auditLocalTarballDeploy: skip audit row, read deployment for actor attribution failed",
			"deployment", res.DeploymentID, "err", dErr)
		return
	}
	resolvedActor := resolvedActorString(d.DeployedVia, d.DeployedByUserID, d.PusherLogin)
	data := map[string]any{
		auditKeyAppID:        app.ID,
		auditKeyDeploymentID: res.DeploymentID,
		auditKeyBuildID:      res.BuildID,
		auditKeyRepo:         sidecar.Repo,
		auditKeyRef:          sidecar.Ref,
		auditKeySourceBytes:  sourceBytes,
		auditKeyTrustRoot:    "cli",
	}
	// Issue #977 / ADR-116: mirror the annotation surface into
	// the deploy.local_tarball audit row. mergeAnnotationAudit
	// is "omit when zero" so pre-feature rows stay byte-identical.
	mergeAnnotationAudit(data, ann)
	s.audit.EmitAs(ctx, resolvedActor, "deploy.local_tarball", &acct.ID, mergeActorAudit(data, d.DeployedByUserID, d.DeployedVia, d.DeployedFromIP, d.PusherLogin))
}
