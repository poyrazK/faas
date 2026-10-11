package main

// ADR-122 / issue #975 item #1 — endpoint discovery. Three routes:
//
//   GET    /v1/apps/{slug}/deployments/{deployment}/openapi
//   PATCH  /v1/apps/{slug}/deployments/{deployment}/openapi
//   DELETE /v1/apps/{slug}/deployments/{deployment}/openapi
//
// All three flow through authLimited → requireMFA → requireScope
// → loadApp → loadDeployment → enforceOpenAPIPlan. The PATCH
// surface takes a Set-bit body (mirroring pkg/apid/handlers_ext.go:570
// updateApp) so a missing field is "leave alone", not "set to
// zero".
//
// Plan-tier gate runs BEFORE loadApp so a Free customer posting
// to a non-existent slug still gets 402 CodePlanOpenAPIDocsNotAllowed,
// not 404. Same pattern as createAlertRule / createEdgeRule.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// enforceOpenAPIPlan returns true if the customer's plan has the
// endpoint-discovery feature on. Free returns false (402
// CodePlanOpenAPIDocsNotAllowed at the apid surface). The microVM
// still captures the doc during cold boot — the apid never serves
// the doc on Free, so the per-account quota is unreachable.
func enforceOpenAPIPlan(plan api.Plan) bool {
	return plan.OpenAPIDocsPerDeployment() > 0
}

// openAPIDocAppOwnedBy is the IDOR floor's defence-in-depth check.
// The Store layer already enforces (deployment_id, account_id)
// at the SQL WHERE clause; the apid-side check is the app
// ownership check, which catches a stale deployment_id that points
// at a deleted app's row.
func (s *server) openAPIDocAppOwnedBy(app state.App, acct state.Account) bool {
	return app.AccountID == acct.ID
}

// getOpenAPIDoc handles GET /v1/apps/{slug}/deployments/{deployment}/openapi.
// Returns 200 with the JSON body + Cache-Control: public, max-age=300,
// 404 when no doc exists OR when the deployment is cross-tenant.
// 402 on Free plan (CodePlanOpenAPIDocsNotAllowed).
func (s *server) getOpenAPIDoc(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !enforceOpenAPIPlan(acct.Plan) {
		api.WriteProblem(w, api.ErrPlanOpenAPIDocsNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	depID := r.PathValue("deployment")
	dep, err := s.store.DeploymentByID(r.Context(), depID)
	if err != nil || !s.openAPIDocAppOwnedBy(app, acct) {
		s.notFound(w, "no such deployment")
		return
	}
	if dep.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}
	doc, meta, err := s.store.GetDeploymentOpenAPIDoc(r.Context(), depID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no OpenAPI document captured for this deployment")
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read OpenAPI document", err.Error()))
		return
	}
	// Cache-Control mirrors the CORS preset GET (5 min) — the
	// doc rarely changes but the customer can PATCH at any time.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if meta.Truncated {
		w.Header().Set("X-OpenAPI-Doc-Truncated", "1")
	}
	w.Header().Set("X-OpenAPI-Doc-Deployment-ID", meta.DeploymentID)
	w.Header().Set("X-OpenAPI-Doc-App-ID", meta.AppID)
	w.Header().Set("X-OpenAPI-Doc-SHA256", fmt.Sprintf("%x", meta.DocSHA256))
	w.Header().Set("X-OpenAPI-Doc-Captured-At", meta.CapturedAt.UTC().Format(time.RFC3339Nano))
	w.Header().Set("X-OpenAPI-Doc-Updated-At", meta.UpdatedAt.UTC().Format(time.RFC3339Nano))
	w.Header().Set("X-OpenAPI-Doc-Source", meta.Source)
	w.Header().Set("X-OpenAPI-Doc-Byte-Size", fmt.Sprintf("%d", meta.ByteSize))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc)
}

// patchOpenAPIDocRequest is the Set-bit body shape for the PATCH
// surface. Mirrors the updateApp pattern in
// pkg/apid/handlers_ext.go:570 updateApp. SetDoc=true means the
// caller is replacing the doc; SetSource=true means the caller is
// retagging the provenance (manual_upload vs cold_boot). Body
// changes are atomic — caller ships the full doc on SetDoc=true.
type patchOpenAPIDocRequest struct {
	Doc       *json.RawMessage `json:"doc,omitempty"`
	SetDoc    bool             `json:"set_doc,omitempty"`
	Source    *string          `json:"source,omitempty"`
	SetSource bool             `json:"set_source,omitempty"`
	// Truncated is the api-side flag the caller can stamp when
	// re-uploading a doc that was clipped at the global cap.
	// The PATCH surface stamps it through as-is.
	Truncated *bool `json:"truncated,omitempty"`
}

// patchOpenAPIDoc handles PATCH /v1/apps/{slug}/deployments/{deployment}/openapi.
// Set-bit body. Validates doc shape (top-level "openapi" or "swagger"
// key + JSON parse), checks per-plan per-account quota, calls
// Store.UpsertDeploymentOpenAPIDoc, emits audit.
func (s *server) patchOpenAPIDoc(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !enforceOpenAPIPlan(acct.Plan) {
		api.WriteProblem(w, api.ErrPlanOpenAPIDocsNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	depID := r.PathValue("deployment")
	dep, err := s.store.DeploymentByID(r.Context(), depID)
	if err != nil || !s.openAPIDocAppOwnedBy(app, acct) {
		s.notFound(w, "no such deployment")
		return
	}
	if dep.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}

	// Read + parse body. Cap the read at the per-plan limit +
	// 1 KiB of overhead so an oversized payload short-circuits
	// before allocation.
	planMaxBytes := acct.Plan.OpenAPIDocMaxBytes()
	maxRead := int64(planMaxBytes) + 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxRead)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.ErrPlanOpenAPIDocTooLarge(acct.Plan, planMaxBytes, 0))
		return
	}
	var req patchOpenAPIDocRequest
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_body",
			"invalid JSON body", err.Error()))
		return
	}
	if !req.SetDoc {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "missing_doc",
			"set_doc=true with a non-empty doc is required", ""))
		return
	}
	if req.Doc == nil || len(*req.Doc) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "missing_doc",
			"doc field is empty", ""))
		return
	}
	docBytes := []byte(*req.Doc)
	// Per-plan byte cap.
	if len(docBytes) > planMaxBytes {
		api.WriteProblem(w, api.ErrPlanOpenAPIDocTooLarge(acct.Plan, planMaxBytes, len(docBytes)))
		return
	}
	// Default source to manual_upload when SetDoc=true but the
	// caller didn't tag a source. The whole point of the PATCH
	// surface is a deliberate replacement.
	source := state.OpenAPIDocSourceManualUpload
	if req.SetSource && req.Source != nil {
		switch *req.Source {
		case state.OpenAPIDocSourceColdBoot, state.OpenAPIDocSourceManualUpload:
			source = *req.Source
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_source",
				"source must be one of: cold_boot, manual_upload",
				fmt.Sprintf("observed=%s", *req.Source)))
			return
		}
	}
	// Strip BOM + leading whitespace before shape sniff.
	trimmed := trimLeftWhitespace(docBytes)
	if !looksLikeOpenAPIDoc(trimmed) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_openapi_shape",
			"doc must be a JSON object with a top-level 'openapi' or 'swagger' key", ""))
		return
	}
	// Per-account quota gate. The COUNT is computed server-side
	// via a SELECT COUNT(*) so the apid doesn't load the full
	// body slice. The same deployment's doc is allowed to be
	// overwritten (last-write-wins) — the count is the current
	// row count, not the post-upsert count.
	_, _, getErr := s.store.GetDeploymentOpenAPIDoc(r.Context(), depID, acct.ID)
	alreadyExists := getErr == nil
	if !alreadyExists {
		count, err := s.store.CountOpenAPIDocsByAccount(r.Context(), acct.ID)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
				"failed to count OpenAPI documents", err.Error()))
			return
		}
		if count >= acct.Plan.OpenAPIDocsPerAccount() {
			api.WriteProblem(w, api.ErrPlanOpenAPIDocQuota(acct.Plan, acct.Plan.OpenAPIDocsPerAccount(), count))
			return
		}
	}
	truncated := false
	if req.Truncated != nil {
		truncated = *req.Truncated
	}
	if err := s.store.UpsertDeploymentOpenAPIDoc(r.Context(), depID, acct.ID, app.ID, docBytes, source, truncated); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to persist OpenAPI document", err.Error()))
		return
	}
	// Audit emit. The kind follows the dotted
	// `app.<primitive>.<verb>` convention (mirrors alert_rule
	// updater at handlers_alerts.go:447).
	s.audit.Emit(r.Context(), "app.openapi_doc.updated", &acct.ID, map[string]any{
		"app_id":        app.ID,
		"deployment_id": depID,
		"source":        source,
		"byte_size":     len(docBytes),
		"truncated":     truncated,
	})
	// 200 with the updated row.
	gotDoc, gotMeta, err := s.store.GetDeploymentOpenAPIDoc(r.Context(), depID, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read back OpenAPI document", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deployment_id": gotMeta.DeploymentID,
		"app_id":        gotMeta.AppID,
		"account_id":    gotMeta.AccountID,
		"source":        gotMeta.Source,
		"byte_size":     gotMeta.ByteSize,
		"truncated":     gotMeta.Truncated,
		"captured_at":   gotMeta.CapturedAt,
		"updated_at":    gotMeta.UpdatedAt,
		"doc_sha256":    fmt.Sprintf("%x", gotMeta.DocSHA256),
		"doc":           json.RawMessage(gotDoc),
	})
}

// openAPIDocDelete handles DELETE
// /v1/apps/{slug}/deployments/{deployment}/openapi. Companion to
// the PATCH surface — a customer can explicitly wipe a captured
// doc without re-deploying the app. 204 on success, 404 when no
// row, 402 on Free.
func (s *server) openAPIDocDelete(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !enforceOpenAPIPlan(acct.Plan) {
		api.WriteProblem(w, api.ErrPlanOpenAPIDocsNotAllowed(acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	depID := r.PathValue("deployment")
	dep, err := s.store.DeploymentByID(r.Context(), depID)
	if err != nil || !s.openAPIDocAppOwnedBy(app, acct) {
		s.notFound(w, "no such deployment")
		return
	}
	if dep.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}
	if err := s.store.DeleteDeploymentOpenAPIDoc(r.Context(), depID, acct.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no OpenAPI document for this deployment")
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to delete OpenAPI document", err.Error()))
		return
	}
	s.audit.Emit(r.Context(), "app.openapi_doc.deleted", &acct.ID, map[string]any{
		"app_id":        app.ID,
		"deployment_id": depID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// bytesReader wraps a []byte in an io.Reader. Avoids importing
// bytes.NewReader directly (the test files use it freely; the
// handler's own import set is intentionally narrow).
func bytesReader(b []byte) io.Reader {
	return &sliceReader{b: b}
}

type sliceReader struct {
	b []byte
	i int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

// trimLeftWhitespace strips leading ASCII whitespace + UTF-8 BOM
// from a JSON body. The probeHTTP sniff in guest-init does the
// same — we mirror here so the apid-side check matches the
// guest-side check.
func trimLeftWhitespace(b []byte) []byte {
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		// UTF-8 BOM.
		if i+2 < len(b) && b[i] == 0xEF && b[i+1] == 0xBB && b[i+2] == 0xBF {
			i += 2
			continue
		}
		return b[i:]
	}
	return nil
}

// looksLikeOpenAPIDoc mirrors the guest-init probe
// (guest/init/characterize_linux.go::looksLikeOpenAPIDoc). The
// apid-side sniff is the same shape: top-level "openapi" or
// "swagger" key within the first 4 KiB. The 4 KiB bound keeps
// the scan cheap on the apid's hot path.
func looksLikeOpenAPIDoc(body []byte) bool {
	for i, b := range body {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b != '{' {
			return false
		}
		head := body[i:]
		if len(head) > 4096 {
			head = head[:4096]
		}
		return strings.Contains(string(head), `"openapi"`) ||
			strings.Contains(string(head), `"swagger"`)
	}
	return false
}
