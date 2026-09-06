// Whitebox tests for handleSourceTarballDeploy
// (cmd/apid/handlers_source_tarball.go, issue #961 / Mega-A PR-1).
// Pins the wire contract + audit shape + sidecar-optional + cap-trip
// behaviour for the new local-tarball route.
//
// The handler shares the createDeployment multipart-tarball spool
// helpers (validateAndSpool + validateTarballShape) so the cap and
// shape gates are already covered by cmd/apid/deploy_inputs_test.go.
// This file focuses on what is NEW: the sidecar JSON, the missing-
// tarball 400, and the deploy.local_tarball audit row.
//
// Coverage:
//   - missing tarball field         → 400
//   - happy path no sidecar         → 202 + deployment row + audit
//   - happy path with sidecar       → 202 + audit payload carries repo+ref
//   - cap trip (oversize body)      → 413 code=source_too_large

package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSourceTarball_MissingField: a multipart body without a `tarball`
// field must 400. Mirrors TestCreateDeploymentMultipart_EmptySourceRejected
// for the parallel route.
func TestSourceTarball_MissingField(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "no-tarball"}, nil)

	body, ct := multipartUpload(t, map[string]multipartPart{
		"sidecar": {body: []byte(`{"repo":"o/r","ref":"abc123"}`)},
	})
	req := httptest.NewRequest("POST", "/v1/apps/no-tarball/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
	assertSourceTarballDeprecationHeaders(t, rec)
	if !strings.Contains(rec.Body.String(), "tarball") {
		t.Errorf("body should mention tarball, got %s", rec.Body)
	}
}

// TestSourceTarball_HappyPath_NoSidecar: a valid tarball with no
// sidecar must succeed (sidecar is optional). The audit row must
// carry kind=deploy.local_tarball.
func TestSourceTarball_HappyPath_NoSidecar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "happy"}, nil)

	entries := []tar.Header{
		{Name: "index.js"},
		{Name: "package.json"},
	}
	bodies := map[string][]byte{
		"index.js":     []byte("exports.handler = () => 'ok';\n"),
		"package.json": []byte(`{"name":"happy","version":"0.0.0"}`),
	}
	tarBytes := buildTestTarGz(t, entries, bodies)

	body, ct := multipartUpload(t, map[string]multipartPart{
		"tarball": {filename: "src.tar.gz", body: tarBytes},
	})
	req := httptest.NewRequest("POST", "/v1/apps/happy/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Idempotency-Key", "test-idem-happy")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body)
	}
	assertSourceTarballDeprecationHeaders(t, rec)

	var resp api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec.Body)
	}
	if resp.ID == "" {
		t.Errorf("response missing deployment ID")
	}
	if resp.Kind != "tarball" {
		t.Errorf("kind = %q, want %q", resp.Kind, "tarball")
	}

	// Audit row pins: kind=deploy.local_tarball.
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var row *state.Event
	for i := range rows {
		if rows[i].Kind == "deploy.local_tarball" {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no deploy.local_tarball audit row; events=%+v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(row.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	// sidecar absent → repo + ref empty strings.
	if data["repo"] != "" {
		t.Errorf("audit repo = %v, want empty", data["repo"])
	}
}

func assertSourceTarballDeprecationHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Deprecation"); got != "true" {
		t.Errorf("Deprecation: got %q, want %q", got, "true")
	}
	if got := rec.Header().Get("Sunset"); got != "Wed, 01 Oct 2026 00:00:00 GMT" {
		t.Errorf("Sunset: got %q, want RFC 7231 IMF-fixdate", got)
	}
	if got := rec.Header().Get("Link"); got != `</v1/uploads>; rel="successor-version"` {
		t.Errorf("Link: got %q, want resumable upload successor", got)
	}
}

func TestSourceTarball_DeprecationHeadersOnAuthFailure(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/unauthenticated/deployments/source-tarball", strings.NewReader(""))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertSourceTarballDeprecationHeaders(t, rec)
}

// TestSourceTarball_HappyPath_WithSidecar: a valid tarball WITH a
// sidecar JSON {repo, ref} must record both fields on the audit row.
// The build pipeline does NOT use them to fetch upstream — that's
// what the local-tarball trust model buys us — but they MUST be
// recorded for provenance.
func TestSourceTarball_HappyPath_WithSidecar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "sidecar"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	bodies := map[string][]byte{"index.js": []byte("ok\n")}
	tarBytes := buildTestTarGz(t, entries, bodies)

	sidecar := `{"repo":"octocat/hello-world","ref":"0123456789abcdef0123456789abcdef01234567"}`
	body, ct := multipartUpload(t, map[string]multipartPart{
		"tarball": {filename: "src.tar.gz", body: tarBytes},
		"sidecar": {body: []byte(sidecar)},
	})
	req := httptest.NewRequest("POST", "/v1/apps/sidecar/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body)
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var row *state.Event
	for i := range rows {
		if rows[i].Kind == "deploy.local_tarball" {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no deploy.local_tarball audit row; events=%+v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(row.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["repo"] != "octocat/hello-world" {
		t.Errorf("audit repo = %v, want octocat/hello-world", data["repo"])
	}
	if data["ref"] != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("audit ref = %v, want 40-char SHA", data["ref"])
	}
	if data["trust_root"] != "cli" {
		t.Errorf("audit trust_root = %v, want cli", data["trust_root"])
	}
}

// TestSourceTarball_BadSidecar: a malformed sidecar JSON must 400.
// The tarball field is valid; only the sidecar is broken.
func TestSourceTarball_BadSidecar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "bad-sidecar"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	tarBytes := buildTestTarGz(t, entries, map[string][]byte{"index.js": []byte("ok\n")})

	body, ct := multipartUpload(t, map[string]multipartPart{
		"tarball": {filename: "src.tar.gz", body: tarBytes},
		"sidecar": {body: []byte(`{this is not json`)},
	})
	req := httptest.NewRequest("POST", "/v1/apps/bad-sidecar/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "sidecar") {
		t.Errorf("body should mention sidecar, got %s", rec.Body)
	}
}

// TestSourceTarball_OversizeContentLength: MED-2 fix. The handler
// must trip 413 CodeSourceTooLarge BEFORE parsing the multipart
// body, so a Content-Length-declared oversize body doesn't spool
// the whole upload to os.TempDir(). The Pro plan caps at 250 MB
// per pkg/api/limits.go; we send a Content-Length of 300 MB and
// assert the handler short-circuits.
func TestSourceTarball_OversizeContentLength(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "oversize"}, nil)

	// Build a minimal valid body — we won't read it because the
	// Content-Length pre-check rejects before parsing.
	body := strings.NewReader("placeholder")
	req := httptest.NewRequest("POST", "/v1/apps/oversize/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=---x")
	// 300 MB > Pro's 250 MB cap → must trip CodeSourceTooLarge.
	req.ContentLength = 300 * 1024 * 1024
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "source_too_large") {
		t.Errorf("body should carry code=source_too_large, got %s", rec.Body)
	}
}

// TestSourceTarball_HappyPath_WithAnnotations (issue #977 / ADR-116)
// pins the deploy-annotation multipart fields end-to-end. A POST
// that ships reason / tag / deployed-by / pr-number as form fields
// must:
//   - land on the deployment row (Kind=tarball, ImageDigest unchanged)
//   - land on the audit row's data{} map
//   - surface on the wire DeploymentResponse (Reason/Tag/DeployedBy/
//     PRNumber) so the dashboard + CLI can render them without an
//     additional GET.
//
// The annotation fields are OPTIONAL — a baseline multipart POST
// without them must continue to round-trip with all four fields
// empty (the pre-#977 back-compat pin, mirrors the mergeAnnotationAudit
// docstring in handlers_annotations.go).
func TestSourceTarball_HappyPath_WithAnnotations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "annotated"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	bodies := map[string][]byte{"index.js": []byte("ok\n")}
	tarBytes := buildTestTarGz(t, entries, bodies)

	// Ship the four annotation fields as plain multipart form
	// fields. No file metadata — the validator reads them as
	// strings/ints.
	body, ct := multipartUpload(t, map[string]multipartPart{
		"tarball":     {filename: "src.tar.gz", body: tarBytes},
		"reason":      {body: []byte("Hotfix: payment retry path")},
		"tag":         {body: []byte("hotfix")},
		"deployed_by": {body: []byte("alice@example.com")},
		"pr_number":   {body: []byte("4242")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/annotated/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body)
	}

	// Wire response must carry the four fields.
	var resp api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec.Body)
	}
	if resp.Reason != "Hotfix: payment retry path" {
		t.Errorf("response reason = %q, want %q", resp.Reason, "Hotfix: payment retry path")
	}
	if resp.Tag != "hotfix" {
		t.Errorf("response tag = %q, want %q", resp.Tag, "hotfix")
	}
	if resp.DeployedBy != "alice@example.com" {
		t.Errorf("response deployed_by = %q, want %q", resp.DeployedBy, "alice@example.com")
	}
	if resp.PRNumber != 4242 {
		t.Errorf("response pr_number = %d, want 4242", resp.PRNumber)
	}

	// Deployment row must carry the same four fields.
	app, err := e.store.AppBySlug(context.Background(), "annotated")
	if err != nil {
		t.Fatalf("AppBySlug(annotated): %v", err)
	}
	deps, err := e.store.ListDeploymentsForApp(context.Background(), app.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("deployment count = %d, want 1", len(deps))
	}
	dep := deps[0]
	if dep.Reason != "Hotfix: payment retry path" {
		t.Errorf("dep.reason = %q, want %q", dep.Reason, "Hotfix: payment retry path")
	}
	if dep.Tag != "hotfix" {
		t.Errorf("dep.tag = %q, want %q", dep.Tag, "hotfix")
	}
	if dep.DeployedBy != "alice@example.com" {
		t.Errorf("dep.deployed_by = %q, want %q", dep.DeployedBy, "alice@example.com")
	}
	if dep.PRNumber != 4242 {
		t.Errorf("dep.pr_number = %d, want 4242", dep.PRNumber)
	}

	// Audit row must carry the four keys.
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var row *state.Event
	for i := range rows {
		if rows[i].Kind == "deploy.local_tarball" {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no deploy.local_tarball audit row; events=%+v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(row.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["reason"] != "Hotfix: payment retry path" {
		t.Errorf("audit reason = %v, want annotation string", data["reason"])
	}
	if data["tag"] != "hotfix" {
		t.Errorf("audit tag = %v, want hotfix", data["tag"])
	}
	if data["deployed_by"] != "alice@example.com" {
		t.Errorf("audit deployed_by = %v, want alice@example.com", data["deployed_by"])
	}
	// PRNumber serialises as float64 through json.Unmarshal.
	if pr, ok := data["pr_number"].(float64); !ok || int(pr) != 4242 {
		t.Errorf("audit pr_number = %v, want 4242", data["pr_number"])
	}
}

// TestSourceTarball_BadTag (issue #977 / ADR-116) pins the 400
// rejection path for an out-of-set tag value. The closed-set
// validator mirrors the DB CHECK so a malformed value never reaches
// the store layer.
func TestSourceTarball_BadTag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "bad-tag"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	tarBytes := buildTestTarGz(t, entries, map[string][]byte{"index.js": []byte("ok\n")})

	body, ct := multipartUpload(t, map[string]multipartPart{
		"tarball": {filename: "src.tar.gz", body: tarBytes},
		"tag":     {body: []byte("rogue_tag")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/bad-tag/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body)
	}
	// The handler emits an RFC 7807 problem with a descriptive
	// code. The body carries the closed-set vocabulary so an
	// operator reading the error can see which values are
	// accepted (the rejected value is not echoed — the closed
	// set of accepted values is the actionable hint).
	if !strings.Contains(rec.Body.String(), "incident_recovery") {
		t.Errorf("body should mention closed-set vocabulary, got %s", rec.Body)
	}
}

// TestSourceTarball_ReasonTooLong (issue #977 / ADR-116) pins the
// length cap on the free-text reason field. The 280-char ceiling
// matches the literal example in the issue body and is enforced
// server-side (not CLI-side) so the Action path is also covered.
func TestSourceTarball_ReasonTooLong(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "long-reason"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	tarBytes := buildTestTarGz(t, entries, map[string][]byte{"index.js": []byte("ok\n")})

	// 281 ASCII chars → one over the 280-char ceiling.
	reason := strings.Repeat("x", 281)

	body, ct := multipartUpload(t, map[string]multipartPart{
		"tarball": {filename: "src.tar.gz", body: tarBytes},
		"reason":  {body: []byte(reason)},
	})
	req := httptest.NewRequest("POST", "/v1/apps/long-reason/deployments/source-tarball", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "reason") {
		t.Errorf("body should mention reason field, got %s", rec.Body)
	}
}
