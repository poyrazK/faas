package main

// Handler tests for the per-deployment OpenAPI document endpoints
// (ADR-122 / issue #975 item #1, slot 00330). The surface:
//
//   GET    /v1/apps/{slug}/deployments/{deployment}/openapi
//   PATCH  /v1/apps/{slug}/deployments/{deployment}/openapi
//   DELETE /v1/apps/{slug}/deployments/{deployment}/openapi
//
// Test surface:
//   - 200 GET happy path + headers (Cache-Control, X-OpenAPI-Doc-*)
//   - 404 GET on missing doc
//   - 402 GET on Free plan
//   - 200 PATCH happy path (manual_upload, audit emit)
//   - 400 PATCH on invalid OpenAPI shape
//   - 413 PATCH on body > per-plan cap
//   - 402 PATCH on Free plan
//   - 204 DELETE happy path
//   - 404 DELETE on missing doc
//
// Body-marshaling note: e.do in cmd/apid/server_test.go re-marshals
// any `body` arg via json.Marshal. We pass `map[string]any` shaped
// like patchOpenAPIDocRequest so the marshaled bytes reach the
// handler intact.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOpenAPITriggerContractDocumentsCapabilitiesAndRedaction(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read source OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, required := range []string{
		"trigger_kinds:", "trigger_batch_window_max_ms:", "writeOnly: true",
		"password_set:", "client_key_set:", "plan_triggers_not_allowed",
		"trigger_kind_not_allowed", "plan_trigger_quota", "secret_store_unavailable",
	} {
		if !strings.Contains(spec, required) {
			t.Errorf("OpenAPI missing %q", required)
		}
	}
	if strings.Count(spec, "writeOnly: true") < 2 {
		t.Errorf("OpenAPI has %d write-only credential leaves, want at least 2", strings.Count(spec, "writeOnly: true"))
	}
	for _, internal := range []string{"password_sealed", "client_key_sealed"} {
		if strings.Contains(spec, internal) {
			t.Errorf("OpenAPI exposes internal field %q", internal)
		}
	}
}

// patchOpenAPIDocJSON pre-marshals the body as a `map[string]any`
// (matches the handler's `patchOpenAPIDocRequest` Set-bit shape).
// We need this because cmd/apid/server_test.go::e.do re-marshals
// any passed value, which would collapse a raw []byte to `null`.
func patchOpenAPIDocJSON(doc string, source string, setSource bool) map[string]any {
	body := map[string]any{
		"set_doc": true,
		"doc":     json.RawMessage(doc),
	}
	if setSource {
		body["source"] = source
		body["set_source"] = true
	}
	return body
}

// rawPatchOpenAPIDoc drives the request with a raw []byte body.
// e.do's json.Marshal collapse is bypassed by writing the request
// directly.
func (e testEnv) rawPatchOpenAPIDoc(t *testing.T, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", path, nilReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// nilReader turns a []byte (possibly nil) into an io.Reader
// that yields {} when the input is empty.
func nilReader(b []byte) *jsonReader {
	if len(b) == 0 {
		b = []byte("{}")
	}
	return &jsonReader{b: b}
}

type jsonReader struct {
	b []byte
	i int
}

func (r *jsonReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

// openAPIDocHarness sets up a test env + a seeded deployment row
// owned by the caller. Returns the env, the slug, and the
// deployment ID. The slug is a local variable because the
// testEnv struct doesn't carry it.
func openAPIDocHarness(t *testing.T, plan api.Plan) (e testEnv, slug, depID string) {
	t.Helper()
	e = setup(t, plan)
	slug = fmt.Sprintf("openapi-doc-%s", strings.ReplaceAll(t.Name(), "/", "-"))
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID:      acctIDFor(e),
		Slug:           slug,
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:" + uuid.NewString(),
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return e, slug, dep.ID
}

// acctIDFor peels the caller's account ID off the testEnv.
func acctIDFor(e testEnv) string {
	return e.acct.ID
}

func openAPIDocPath(slug, depID string) string {
	return fmt.Sprintf("/v1/apps/%s/deployments/%s/openapi", slug, depID)
}

// TestGetOpenAPIDoc_MissingReturns404 pins the 404 path on a
// PATCH-less deployment.
func TestGetOpenAPIDoc_MissingReturns404(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	rec := e.do(t, "GET", openAPIDocPath(slug, depID), nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET missing: code = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestGetOpenAPIDoc_FreePlanBlocks pins the Free-plan gate.
func TestGetOpenAPIDoc_FreePlanBlocks(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanFree)
	rec := e.do(t, "GET", openAPIDocPath(slug, depID), nil, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("GET on Free: code = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "openapi_docs_not_allowed") {
		t.Errorf("body missing openapi_docs_not_allowed code: %s", rec.Body.String())
	}
}

// TestPatchOpenAPIDoc_HappyPath pins the 200 PATCH.
func TestPatchOpenAPIDoc_HappyPath(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	doc := `{"openapi":"3.1.0","info":{"title":"captured"}}`
	body, _ := json.Marshal(patchOpenAPIDocJSON(doc, "manual_upload", true))
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH happy path: code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["source"] != "manual_upload" {
		t.Errorf("source: got %v, want manual_upload", resp["source"])
	}
	if int(resp["byte_size"].(float64)) != len(doc) {
		t.Errorf("byte_size: got %v, want %d", resp["byte_size"], len(doc))
	}
	if resp["doc_sha256"] == "" {
		t.Errorf("doc_sha256: empty")
	}

	// Verify the row by re-reading via GET.
	rec = e.do(t, "GET", openAPIDocPath(slug, depID), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after PATCH: code = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=300") {
		t.Errorf("Cache-Control: got %q, want max-age=300", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-OpenAPI-Doc-Source") != "manual_upload" {
		t.Errorf("X-OpenAPI-Doc-Source: got %q, want manual_upload", rec.Header().Get("X-OpenAPI-Doc-Source"))
	}
	if rec.Body.String() != doc {
		t.Errorf("body: got %q, want %q", rec.Body.String(), doc)
	}
}

// TestPatchOpenAPIDoc_InvalidShapeReturns400 pins the OpenAPI
// shape sniff.
func TestPatchOpenAPIDoc_InvalidShapeReturns400(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	body, _ := json.Marshal(patchOpenAPIDocJSON(`{"name":"not-an-openapi-doc"}`, "manual_upload", true))
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH bad shape: code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_openapi_shape") {
		t.Errorf("body missing invalid_openapi_shape code: %s", rec.Body.String())
	}
}

// TestPatchOpenAPIDoc_TooLargeReturns413 pins the per-plan byte cap.
func TestPatchOpenAPIDoc_TooLargeReturns413(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	prefix := `{"openapi":"3.1.0","info":{"title":"big"},"x":"`
	pad := strings.Repeat("x", 128*1024+1-len(prefix)-2)
	doc := prefix + pad + `"}`
	if len(doc) <= 128*1024 {
		t.Fatalf("test setup: doc too small (%d bytes)", len(doc))
	}
	body, _ := json.Marshal(patchOpenAPIDocJSON(doc, "manual_upload", true))
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PATCH too large: code = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plan_openapi_doc_too_large") {
		t.Errorf("body missing plan_openapi_doc_too_large code: %s", rec.Body.String())
	}
}

// TestPatchOpenAPIDoc_FreePlanBlocks pins the Free-plan gate on the write surface.
func TestPatchOpenAPIDoc_FreePlanBlocks(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanFree)
	body, _ := json.Marshal(patchOpenAPIDocJSON(`{"openapi":"3.1.0"}`, "manual_upload", true))
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), body)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("PATCH on Free: code = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteOpenAPIDoc_HappyPath pins the 204 DELETE.
func TestDeleteOpenAPIDoc_HappyPath(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	body, _ := json.Marshal(patchOpenAPIDocJSON(`{"openapi":"3.1.0"}`, "manual_upload", true))
	if rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), body); rec.Code != http.StatusOK {
		t.Fatalf("seed PATCH: code = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec := e.do(t, "DELETE", openAPIDocPath(slug, depID), nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: code = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "GET", openAPIDocPath(slug, depID), nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE: code = %d, want 404", rec.Code)
	}
}

// TestDeleteOpenAPIDoc_MissingReturns404 pins the 404 path on a
// never-captured deployment.
func TestDeleteOpenAPIDoc_MissingReturns404(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	rec := e.do(t, "DELETE", openAPIDocPath(slug, depID), nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE missing: code = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestLooksLikeOpenAPIDocHandlerSide pins the apid-side shape sniff.
func TestLooksLikeOpenAPIDocHandlerSide(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"openapi_3_1", `{"openapi":"3.1.0"}`, true},
		{"swagger_2", `{"swagger":"2.0"}`, true},
		{"array_root", `[{"openapi":"3.1.0"}]`, false},
		{"primitive", `"openapi"`, false},
		{"empty_obj", `{}`, false},
		{"indented_openapi", "\n  {\n    \"openapi\":\"3.1.0\"\n  }", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeOpenAPIDoc([]byte(tc.body)); got != tc.want {
				t.Errorf("looksLikeOpenAPIDoc(%q): got %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestGetOpenAPIDoc_TruncatedHeader pins the X-OpenAPI-Doc-Truncated
// header.
func TestGetOpenAPIDoc_TruncatedHeader(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	fullBody := map[string]any{
		"set_doc":    true,
		"doc":        json.RawMessage(`{"openapi":"3.1.0"}`),
		"source":     "cold_boot",
		"set_source": true,
		"truncated":  true,
	}
	fullBodyBytes, _ := json.Marshal(fullBody)
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), fullBodyBytes)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed PATCH: code = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "GET", openAPIDocPath(slug, depID), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: code = %d", rec.Code)
	}
	if rec.Header().Get("X-OpenAPI-Doc-Truncated") != "1" {
		t.Errorf("X-OpenAPI-Doc-Truncated: got %q, want 1", rec.Header().Get("X-OpenAPI-Doc-Truncated"))
	}
}

// TestPatchOpenAPIDoc_DefaultSourceIsManualUpload pins the
// default-source contract.
func TestPatchOpenAPIDoc_DefaultSourceIsManualUpload(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	body := map[string]any{
		"set_doc": true,
		"doc":     json.RawMessage(`{"openapi":"3.1.0"}`),
	}
	bodyBytes, _ := json.Marshal(body)
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), bodyBytes)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: code = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"source":"manual_upload"`) {
		t.Errorf("default source: got %s, want manual_upload", rec.Body.String())
	}
}

// TestPatchOpenAPIDoc_InvalidSourceReturns400 pins the closed-set
// source enum.
func TestPatchOpenAPIDoc_InvalidSourceReturns400(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	body, _ := json.Marshal(patchOpenAPIDocJSON(`{"openapi":"3.1.0"}`, "bogus", true))
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid source: code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatchOpenAPIDoc_EmptyDocReturns400 pins the missing-doc rejection.
func TestPatchOpenAPIDoc_EmptyDocReturns400(t *testing.T) {
	e, slug, depID := openAPIDocHarness(t, api.PlanHobby)
	body := map[string]any{
		"set_doc": true,
		"doc":     json.RawMessage(""),
	}
	bodyBytes, _ := json.Marshal(body)
	rec := e.rawPatchOpenAPIDoc(t, openAPIDocPath(slug, depID), bodyBytes)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH empty doc: code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
