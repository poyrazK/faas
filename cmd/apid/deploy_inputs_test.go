// Validation + multipart tests for cmd/apid/deploy_inputs.go (PR-A).
//
// The previous test surface exercised the JSON image: branch via
// server_test.go::TestCreateDeploymentImage — but the multipart tarball
// path (validateTarballShape + validateAndSpool + createDeploymentMultipart)
// had zero coverage. PR-A's symlink/hardlink gate lives inside
// validateTarballShape; this file pins the gate against the wire and
// backstops the byte-cap / file-count / format-error edges so a future
// refactor can't silently regress them.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// buildTestTarGz packs a flat name→content map into a gzipped tar. Files
// are stored with mode 0644 and TypeReg unless the caller overrides
// Typeflag (the symlink/hardlink tests do exactly that). Mirrors the
// shape of cmd/e2e/fixtures_test.go::buildTarGz but kept local because
// the e2e package is build-tagged behind //go:build metal.
func buildTestTarGz(t *testing.T, entries []tar.Header, bodies map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		hdr := h
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if hdr.ModTime.IsZero() {
			hdr.ModTime = time.Unix(0, 0)
		}
		// Body length drives hdr.Size — tar.Writer refuses writes that
		// exceed the declared size ("write too long") and silently pads
		// short writes, so we set Size exactly from the bodies map.
		// Match the zero Typeflag too — most callers leave it unset
		// (the test fixtures do: `tar.Header{Name: "index.js"}`),
		// and tar treats Typeflag == 0 as TypeReg per the spec. The
		// prior `|| tar.TypeRegA` form worked because TypeRegA == 0,
		// but TypeRegA is SA1019-deprecated since Go 1.11.
		if hdr.Typeflag == tar.TypeReg || hdr.Typeflag == 0 {
			hdr.Size = int64(len(bodies[hdr.Name]))
		}
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatalf("buildTestTarGz: WriteHeader(%s): %v", hdr.Name, err)
		}
		if b, ok := bodies[hdr.Name]; ok && len(b) > 0 {
			if _, err := tw.Write(b); err != nil {
				t.Fatalf("buildTestTarGz: Write(%s): %v", hdr.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("buildTestTarGz: tar.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("buildTestTarGz: gzip.Close: %v", err)
	}
	return buf.Bytes()
}

// writeTarToSpool drops a gzipped tar under the canonical spool dir
// (FAAS_SPOOL_ROOT env var, set by the caller via t.Setenv). Returns
// the absolute path validateTarballShape will read.
func writeTarToSpool(t *testing.T, root string, raw []byte) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir spool: %v", err)
	}
	path := filepath.Join(root, fmt.Sprintf("test-%d.tar.gz", time.Now().UnixNano()))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write tar: %v", err)
	}
	return path
}

// mustProblem fails the test when prob is nil and returns it
// non-nil otherwise. Side-steps the SA5011 false positive on
// `if prob == nil { t.Fatal(...) } if prob.X` because t.Fatal
// returning is a runtime fact the linter can't prove.
func mustProblem(t *testing.T, prob *api.Problem) *api.Problem {
	t.Helper()
	if prob == nil {
		t.Fatal("expected a problem, got nil")
	}
	return prob
}

// TestValidateTarballShape_RejectsAbsolutePath covers the existing
// `hdr.Name` check that pre-dates PR-A. Pinned here so the PR-A
// refactor (which moves the symlink check ABOVE the file-count
// increment) doesn't accidentally weaken the Name predicate.
func TestValidateTarballShape_RejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	entries := []tar.Header{{Name: "/etc/passwd"}}
	body := buildTestTarGz(t, entries, nil)
	path := writeTarToSpool(t, dir, body)

	prob := validateTarballShape(path)
	if prob == nil { //nolint:staticcheck // t.Fatal terminates, var proven non-nil below
		t.Fatal("expected reject for absolute path entry, got nil problem")
	}
}

// TestValidateTarballShape_RejectsDotDotPath covers the `..` Name branch.
func TestValidateTarballShape_RejectsDotDotPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	entries := []tar.Header{{Name: "../../etc/passwd"}}
	body := buildTestTarGz(t, entries, nil)
	path := writeTarToSpool(t, dir, body)

	prob := validateTarballShape(path)
	if prob == nil { //nolint:staticcheck // t.Fatal terminates, var proven non-nil below
		t.Fatal("expected reject for .. entry, got nil problem")
	}
}

// TestValidateTarballShape_RejectsSymlinkAbsoluteLinkname is the
// PR-A symlink check: a regular Name + a Linkname that escapes the
// unpack root. The check must run BEFORE the file-count increment
// (see deploy_inputs.go:validateTarballShape doc-comment).
func TestValidateTarballShape_RejectsSymlinkAbsoluteLinkname(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	entries := []tar.Header{{
		Name:     "evil.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	}}
	body := buildTestTarGz(t, entries, nil)
	path := writeTarToSpool(t, dir, body)

	prob := validateTarballShape(path)
	if prob == nil { //nolint:staticcheck // t.Fatal terminates, var proven non-nil below
		t.Fatal("expected reject for symlink with absolute Linkname, got nil problem")
	}
}

// TestValidateTarballShape_RejectsSymlinkDotDotLinkname covers the
// `..` Linkname branch of the symlink check.
func TestValidateTarballShape_RejectsSymlinkDotDotLinkname(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	entries := []tar.Header{{
		Name:     "evil.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "../../../etc/shadow",
	}}
	body := buildTestTarGz(t, entries, nil)
	path := writeTarToSpool(t, dir, body)

	prob := validateTarballShape(path)
	if prob == nil { //nolint:staticcheck // t.Fatal terminates, var proven non-nil below
		t.Fatal("expected reject for symlink with .. Linkname, got nil problem")
	}
}

// TestValidateTarballShape_RejectsHardlinkAbsoluteLinkname covers
// the TypeLink branch (a hard link carries the same escape risk as
// a symlink — the unpack would land on a real file in the target
// directory).
func TestValidateTarballShape_RejectsHardlinkAbsoluteLinkname(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	entries := []tar.Header{{
		Name:     "evil.txt",
		Typeflag: tar.TypeLink,
		Linkname: "/etc/passwd",
	}}
	body := buildTestTarGz(t, entries, nil)
	path := writeTarToSpool(t, dir, body)

	prob := validateTarballShape(path)
	if prob == nil { //nolint:staticcheck // t.Fatal terminates, var proven non-nil below
		t.Fatal("expected reject for hardlink with absolute Linkname, got nil problem")
	}
}

// TestValidateTarballShape_FileCountBoundary pins the maxSourceFiles
// (10k) cap. The check is intentionally AFTER the symlink/hardlink
// check so a 10k-entry tarball that contains one malicious symlink
// is rejected on the symlink, not on the count — defense in depth.
func TestValidateTarballShape_FileCountBoundary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	// 10,000 valid entries: should accept.
	mkEntries := func(n int) []tar.Header {
		out := make([]tar.Header, n)
		for i := 0; i < n; i++ {
			out[i] = tar.Header{Name: fmt.Sprintf("file-%05d.txt", i)}
		}
		return out
	}
	ok := buildTestTarGz(t, mkEntries(10000), nil)
	path := writeTarToSpool(t, dir, ok)
	if prob := validateTarballShape(path); prob != nil {
		t.Fatalf("10000 entries should pass, got %v", prob)
	}

	// 10,001 entries: must reject.
	over := buildTestTarGz(t, mkEntries(10001), nil)
	path = writeTarToSpool(t, dir, over)
	prob := validateTarballShape(path)
	if prob == nil { //nolint:staticcheck // t.Fatal terminates, var proven non-nil below
		t.Fatal("10001 entries should be rejected, got nil problem")
	}

	// Zero entries: must accept (an empty tarball is valid).
	empty := buildTestTarGz(t, nil, nil)
	path = writeTarToSpool(t, dir, empty)
	if prob := validateTarballShape(path); prob != nil {
		t.Fatalf("empty tarball should pass, got %v", prob)
	}
}

// TestValidateTarballShape_EscapeBeforeCountCap pins the ordering
// claim from the PR-A doc-comment: the symlink/hardlink escape check
// runs BEFORE the file-count increment. A future refactor that flips
// the order would surface here as a "too many files" problem instead
// of the symlink-specific one. PR-A review fix.
func TestValidateTarballShape_EscapeBeforeCountCap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	// 10,000 valid entries (just under the cap) + 1 escaping symlink
	// as entry 10,001. The validator must reject with the
	// symlink-specific message, NOT the file-count message.
	entries := make([]tar.Header, 0, 10001)
	for i := 0; i < 10000; i++ {
		entries = append(entries, tar.Header{Name: fmt.Sprintf("file-%05d.txt", i)})
	}
	entries = append(entries, tar.Header{
		Name:     "evil.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	})
	raw := buildTestTarGz(t, entries, nil)
	path := writeTarToSpool(t, dir, raw)

	prob := mustProblem(t, validateTarballShape(path))
	if !strings.Contains(prob.Detail, "symlink/hardlink") {
		t.Errorf("expected symlink-specific detail, got %q (a flipped order would surface as 'too many files')", prob.Detail)
	}
}

// TestValidateAndSpool_ByteCapBoundary drives validateAndSpool with a
// small injected Limits value so we can pin the byte-cap gate at the
// boundary without allocating 250 MB. PR-A review: the previous
// "ByteCapBoundary" test was misnamed — it only exercised
// validateTarballShape, which doesn't enforce the cap. This one
// drives the helper that actually does.
//
// Caveat: validateAndSpool's `n` is the multipart part bytes (the
// gzipped tarball), not the uncompressed tar contents. Gzip overhead
// is content-dependent, so we don't pin the *exact* byte boundary
// here — instead we drive the predicate on both sides with a delta
// large enough to outpace gzip block quantization. The exact-byte
// predicate (`n > cap`) is already obvious from a one-line read;
// what's worth pinning is that the cap path surfaces
// CodeSourceTooLarge rather than CodeValidation or some other code.
func TestValidateAndSpool_ByteCapBoundary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	// Tiny fake limits: cap = 1 MB. Only SourceTarballMaxMB is consulted
	// by validateAndSpool; the rest are zero-fill.
	limits := api.Limits{SourceTarballMaxMB: 1}
	cap := int64(limits.SourceTarballMaxMB) * 1024 * 1024

	// Incompressible random bytes — gzip can't shrink them, so the
	// payload size on the wire is approximately the body size. Null
	// bytes gzip down to a few KB and the cap test would silently
	// pass even with the over-cap body.
	mkBody := func(n int) []byte {
		b := make([]byte, n)
		x := uint64(0x9e3779b97f4a7c15)
		for i := range b {
			x = x*6364136223846793005 + 1442695040888963407
			b[i] = byte(x >> 56)
		}
		return b
	}

	// Well under the cap: must pass.
	bodyUnder := mkBody(int(cap) / 2)
	entries := []tar.Header{{Name: "blob.bin", Size: int64(len(bodyUnder))}}
	raw := buildTestTarGz(t, entries, map[string][]byte{"blob.bin": bodyUnder})
	part, cleanup := newMultipartFilePart(t, "src.tar.gz", raw)
	defer cleanup()
	_, n, prob := validateAndSpool(part, limits)
	if prob != nil {
		t.Fatalf("under-cap payload should pass, got %v", prob)
	}
	if n == 0 {
		t.Errorf("validateAndSpool n = 0; want >0 (bytes were copied)")
	}

	// Way over the cap: must reject with CodeSourceTooLarge.
	bodyOver := mkBody(int(cap) * 2)
	entries2 := []tar.Header{{Name: "blob.bin", Size: int64(len(bodyOver))}}
	raw2 := buildTestTarGz(t, entries2, map[string][]byte{"blob.bin": bodyOver})
	part2, cleanup2 := newMultipartFilePart(t, "src.tar.gz", raw2)
	defer cleanup2()
	_, _, prob = validateAndSpool(part2, limits)
	prob = mustProblem(t, prob)
	if prob.Code != api.CodeSourceTooLarge {
		t.Errorf("expected CodeSourceTooLarge, got %q (detail=%q)", prob.Code, prob.Detail)
	}
}

// newMultipartFilePart wraps raw bytes in a multipart.Part so they can
// be fed straight to validateAndSpool. The part is backed by an
// in-memory bytes.Buffer; cleanup is a no-op.
func newMultipartFilePart(t *testing.T, filename string, body []byte) (*multipart.Part, func()) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("source", filename)
	if err != nil {
		t.Fatalf("multipart.CreateFormFile: %v", err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	// multipart.NewReader takes the bare boundary value, NOT the full
	// Content-Type header (unlike mime.ParseMediaType which would).
	mr := multipart.NewReader(&buf, mw.Boundary())
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("multipart.NextPart: %v", err)
	}
	return part, func() {}
}

// TestValidateTarballShape_HappyPath is the small valid case — guards
// against a future regression that over-tightens the validator and
// rejects real customer tarballs.
func TestValidateTarballShape_HappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	entries := []tar.Header{
		{Name: "src/index.js"},
		{Name: "package.json"},
		{Name: "node_modules/.keep"},
	}
	bodies := map[string][]byte{
		"src/index.js":       []byte("exports.handler = () => 'ok';\n"),
		"package.json":       []byte(`{"name":"ok","version":"0.0.0"}`),
		"node_modules/.keep": []byte(""),
	}
	raw := buildTestTarGz(t, entries, bodies)
	path := writeTarToSpool(t, dir, raw)
	if prob := validateTarballShape(path); prob != nil {
		t.Fatalf("happy path: validateTarballShape returned %v", prob)
	}
}

// multipartUpload builds a multipart writer body with the supplied parts
// and returns the assembled body + content-type header. Mirrors the
// shape cmd/e2e uses for real uploads.
func multipartUpload(t *testing.T, parts map[string]multipartPart) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for fieldName, p := range parts {
		var part io.Writer
		var err error
		if p.filename != "" {
			part, err = mw.CreateFormFile(fieldName, p.filename)
		} else {
			part, err = mw.CreateFormField(fieldName)
		}
		if err != nil {
			t.Fatalf("multipart %s: %v", fieldName, err)
		}
		if _, err := part.Write(p.body); err != nil {
			t.Fatalf("multipart write %s: %v", fieldName, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	return &body, mw.FormDataContentType()
}

type multipartPart struct {
	filename string
	body     []byte
}

// TestCreateDeploymentMultipart_EmptySourceRejected: a multipart body
// without a `source` field must 400 with the "source required" code.
func TestCreateDeploymentMultipart_EmptySourceRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "empty-src"}, nil)

	body, ct := multipartUpload(t, map[string]multipartPart{
		"runtime": {body: []byte("node22")},
		"handler": {body: []byte("index.handler")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/empty-src/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), api.CodeValidation) {
		t.Errorf("body should reference %s, got %s", api.CodeValidation, rec.Body)
	}
}

// TestCreateDeploymentMultipart_MalformedGzipRejected: a `source`
// field whose body is not gzipped must surface as 400.
func TestCreateDeploymentMultipart_MalformedGzipRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "bad-gzip"}, nil)

	body, ct := multipartUpload(t, map[string]multipartPart{
		"source": {filename: "src.tar.gz", body: []byte("not actually gzip")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/bad-gzip/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
}

// TestCreateDeploymentMultipart_WrongFormShape: a `source` field with
// no filename is rejected (apid requires the file form, not the
// string form).
func TestCreateDeploymentMultipart_WrongFormShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "wrong-shape"}, nil)

	body, ct := multipartUpload(t, map[string]multipartPart{
		"source": {body: []byte("anything")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/wrong-shape/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
}

// TestCreateDeploymentMultipart_RuntimeMismatchOnFunctionApp: when
// the app is a function with a pinned runtime, a deploy whose
// runtime field disagrees must 400. (The reverse — runtime == app.runtime
// — is the success path tested by TestCreateDeploymentMultipart_FunctionHappyPath.)
func TestCreateDeploymentMultipart_RuntimeMismatchOnFunctionApp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-mismatch", Type: "function", Runtime: "node22"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	raw := buildTestTarGz(t, entries, map[string][]byte{
		"index.js": []byte("exports.handler = () => 1;\n"),
	})
	body, ct := multipartUpload(t, map[string]multipartPart{
		"source":  {filename: "src.tar.gz", body: raw},
		"runtime": {body: []byte("python312")}, // mismatch
		"handler": {body: []byte("index.handler")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/fn-mismatch/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
}

// TestCreateDeploymentMultipart_HandlerMissingOnFunctionApp: a
// function app deploy without `handler` must 400.
func TestCreateDeploymentMultipart_HandlerMissingOnFunctionApp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-nohandler", Type: "function", Runtime: "node22"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	raw := buildTestTarGz(t, entries, map[string][]byte{
		"index.js": []byte("exports.handler = () => 1;\n"),
	})
	body, ct := multipartUpload(t, map[string]multipartPart{
		"source":  {filename: "src.tar.gz", body: raw},
		"runtime": {body: []byte("node22")},
		// no handler field
	})
	req := httptest.NewRequest("POST", "/v1/apps/fn-nohandler/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
}

// TestCreateDeploymentMultipart_FunctionHappyPath is the success
// mirror of the two failure tests above — guards against a future
// regression that breaks the function rewrite entirely.
func TestCreateDeploymentMultipart_FunctionHappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-ok", Type: "function", Runtime: "node22"}, nil)

	entries := []tar.Header{{Name: "index.js"}}
	raw := buildTestTarGz(t, entries, map[string][]byte{
		"index.js": []byte("exports.handler = () => 1;\n"),
	})
	body, ct := multipartUpload(t, map[string]multipartPart{
		"source":  {filename: "src.tar.gz", body: raw},
		"runtime": {body: []byte("node22")},
		"handler": {body: []byte("index.handler")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/fn-ok/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Kind != string(state.DeploymentKindTarball) {
		t.Errorf("kind = %q, want tarball", out.Kind)
	}
	// Handler isn't exposed on DeploymentResponse (the dashboard reads it
	// out of band via BuildByDeployment). The success criterion here is
	// simply that the row was accepted with kind=tarball; the
	// handler/round-trip lives in the existing CreateDeployment image:
	// test (server_test.go::TestCreateDeploymentImage).
}

func TestCreateDeploymentMultipart_WorkspaceSourceRootRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "workspace-app"}, nil)
	raw := buildTestTarGz(t, []tar.Header{
		{Name: "package.json"},
		{Name: "apps/api/package.json"},
		{Name: "apps/api/index.js"},
		{Name: "packages/worker/data/state.db"},
	}, map[string][]byte{
		"package.json":                  []byte(`{"private":true,"workspaces":["apps/*"]}`),
		"apps/api/package.json":         []byte(`{"name":"api"}`),
		"apps/api/index.js":             []byte("console.log(1)\n"),
		"packages/worker/data/state.db": []byte("db"),
	})
	body, ct := multipartUpload(t, map[string]multipartPart{
		"source":      {filename: "src.tar.gz", body: raw},
		"source_root": {body: []byte("apps/api")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/workspace-app/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.SourceRoot != "apps/api" {
		t.Fatalf("response source_root = %q, want apps/api", out.SourceRoot)
	}
	dep, err := e.store.LatestDeployment(t.Context(), out.AppID)
	if err != nil {
		t.Fatalf("LatestDeployment: %v", err)
	}
	if dep.SourceRoot != "apps/api" {
		t.Fatalf("stored source_root = %q, want apps/api", dep.SourceRoot)
	}
}

// TestCreateDeploymentMultipart_StatelessRejection: end-to-end wire-
// shape test for the Wave 0 stateless contract. A multipart deploy
// whose source tarball has a top-level data/ directory must come
// back as 422 application/problem+json with code=stateless_only_viola
// tion and a detail that mentions both the directory name and the
// docs page. Pinned because the unit-level scan tests above proved
// the predicate but not the wire shape — a future regression in
// api.WriteProblem or the handler glue would silently let a stateful
// deploy through the API surface while the predicate still worked.
func TestCreateDeploymentMultipart_StatelessRejection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "stateful-app"}, nil)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/index.js"},
		{Name: "myproject/data/"},
		{Name: "myproject/data/payments.db"},
	}, map[string][]byte{
		"myproject/index.js":         []byte("exports.handler = () => 1;\n"),
		"myproject/data/payments.db": {},
	})
	body, ct := multipartUpload(t, map[string]multipartPart{
		"source": {filename: "src.tgz", body: raw},
	})
	req := httptest.NewRequest("POST", "/v1/apps/stateful-app/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("body is not problem+json: %v (body=%s)", err, rec.Body)
	}
	if prob.Code != api.CodeStatelessOnlyViolation {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeStatelessOnlyViolation)
	}
	if !strings.Contains(prob.Detail, "data/") {
		t.Errorf("detail %q does not mention data/", prob.Detail)
	}
	if prob.DocsURL == "" {
		t.Errorf("expected DocsURL pointing at storage page, got empty")
	}
}

// ─── Wave 0 stateless-only tests ────────────────────────────────────────
//
// Pinned for PR-A of Wave 0. Every test below targets a single check
// inside scanForStatefulShape / scanDockerfileForStatefulShape so a
// future refactor can't silently regress one branch while keeping the
// others green. The HTTP-level integration (the 422 in the
// problem+json body) is covered by TestCreateDeploymentMultipart_StatelessRejection
// above — these tests pin the unit-level predicates.

// TestScanForStatefulShape_HappyPath: a clean source deploy (no
// Dockerfile, no stateful top-level dirs) is accepted regardless of
// the dockerfile flag.
func TestScanForStatefulShape_HappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/index.js"},
		{Name: "myproject/package.json"},
	}, map[string][]byte{
		"myproject/index.js":     []byte("exports.handler = () => 1;\n"),
		"myproject/package.json": []byte(`{"name":"x"}`),
	})
	path := writeTarToSpool(t, dir, raw)
	if prob := scanForStatefulShape(path, false); prob != nil {
		t.Fatalf("clean deploy rejected: code=%s detail=%s", prob.Code, prob.Detail)
	}
}

// TestScanForStatefulShape_DockerfileFlagWithoutDockerfile: when
// dockerfile=true was sent but no Dockerfile is in the archive root,
// the scan fails FAST with CodeSourceInvalid rather than silently
// punting to a build-time failure later. Pinned because the OLD
// behaviour (accept-then-builderd-fails) wasted a build slot and was
// unclear to the customer.
func TestScanForStatefulShape_DockerfileFlagWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/index.js"},
	}, map[string][]byte{
		"myproject/index.js": []byte("exports.handler = () => 1;\n"),
	})
	path := writeTarToSpool(t, dir, raw)
	prob := mustProblem(t, scanForStatefulShape(path, true))
	if prob.Code != api.CodeSourceInvalid {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeSourceInvalid)
	}
	if !strings.Contains(prob.Detail, "Dockerfile") {
		t.Errorf("detail %q does not mention Dockerfile", prob.Detail)
	}
}

// TestScanForStatefulShape_DockerfileFlagWithCleanDockerfile: when
// dockerfile=true is sent AND a clean Dockerfile is present, the scan
// passes (no violation). Pinned alongside the HappyPath case so the
// flag-and-Dockerfile happy branch is also covered.
func TestScanForStatefulShape_DockerfileFlagWithCleanDockerfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/Dockerfile"},
		{Name: "myproject/index.js"},
	}, map[string][]byte{
		"myproject/Dockerfile": []byte("FROM node:22-slim\nWORKDIR /app\nCOPY index.js .\n"),
		"myproject/index.js":   []byte("exports.handler = () => 1;\n"),
	})
	path := writeTarToSpool(t, dir, raw)
	if prob := scanForStatefulShape(path, true); prob != nil {
		t.Fatalf("clean Dockerfile+flag deploy rejected: code=%s detail=%s", prob.Code, prob.Detail)
	}
}

func TestArchiveHasRootDockerfile(t *testing.T) {
	dir := t.TempDir()
	withDockerfile := writeTarToSpool(t, dir, buildTestTarGz(t, []tar.Header{
		{Name: "myproject/Dockerfile"},
	}, map[string][]byte{
		"myproject/Dockerfile": []byte("FROM node:22-slim\n"),
	}))
	if got, err := archiveHasRootDockerfile(withDockerfile); err != nil || !got {
		t.Fatalf("archiveHasRootDockerfile(clean): got %v, err %v; want true", got, err)
	}
	withoutDockerfile := writeTarToSpool(t, dir, buildTestTarGz(t, []tar.Header{
		{Name: "myproject/index.js"},
	}, map[string][]byte{
		"myproject/index.js": []byte("exports.handler = () => 1;\n"),
	}))
	if got, err := archiveHasRootDockerfile(withoutDockerfile); err != nil || got {
		t.Fatalf("archiveHasRootDockerfile(no Dockerfile): got %v, err %v; want false", got, err)
	}
}

func TestWorkspaceSourceRootScopesArchiveChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)
	raw := buildTestTarGz(t, []tar.Header{
		{Name: "apps/api/Dockerfile"},
		{Name: "apps/api/package.json"},
		{Name: "apps/api/index.js"},
		{Name: "packages/worker/data/records.db"},
	}, map[string][]byte{
		"apps/api/Dockerfile":             []byte("FROM node:22-slim\n"),
		"apps/api/package.json":           []byte(`{"name":"api"}`),
		"apps/api/index.js":               []byte("console.log(1)\n"),
		"packages/worker/data/records.db": []byte("db"),
	})
	path := writeTarToSpool(t, dir, raw)

	if present, err := archiveHasSourceRoot(path, "apps/api"); err != nil || !present {
		t.Fatalf("archiveHasSourceRoot(apps/api) = %v, %v; want true", present, err)
	}
	if present, err := archiveHasSourceRoot(path, "apps/missing"); err != nil || present {
		t.Fatalf("archiveHasSourceRoot(apps/missing) = %v, %v; want false", present, err)
	}
	if prob := scanForStatefulShapeAtRoot(path, false, "apps/api"); prob != nil {
		t.Fatalf("selected workspace rejected because sibling contains data/: %v", prob)
	}
	if prob := scanForStatefulShapeAtRoot(path, false, "packages/worker"); prob == nil || prob.Code != api.CodeStatelessOnlyViolation {
		t.Fatalf("stateful selected workspace problem = %v, want stateless violation", prob)
	}
	if got, err := archiveHasRootDockerfileAtRoot(path, "apps/api"); err != nil || !got {
		t.Fatalf("archiveHasRootDockerfileAtRoot(apps/api) = %v, %v; want true", got, err)
	}
}

// TestScanForStatefulShape_TopLevelDataDir: a tarball with a top-level
// data/ directory is rejected as stateless_only_violation with kind=tarball.
// Fixtures wrap in a project root (matches validateTarballShape's
// single-root invariant), so "data/" is parts[1] of the tar path.
func TestScanForStatefulShape_TopLevelDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/index.js"},
		{Name: "myproject/data/"},
		{Name: "myproject/data/payments.db"},
	}, map[string][]byte{
		"myproject/index.js":         []byte("exports.handler = () => 1;\n"),
		"myproject/data/payments.db": {},
	})
	path := writeTarToSpool(t, dir, raw)
	prob := mustProblem(t, scanForStatefulShape(path, false))
	if prob.Code != api.CodeStatelessOnlyViolation {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeStatelessOnlyViolation)
	}
	if !strings.Contains(prob.Detail, "data/") {
		t.Errorf("detail %q does not mention data/", prob.Detail)
	}
}

// TestScanForStatefulShape_TopLevelDBDir: same posture as data/, different name.
func TestScanForStatefulShape_TopLevelDBDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/db/"},
		{Name: "myproject/db/main.sqlite"},
	}, map[string][]byte{"myproject/db/main.sqlite": {}})
	path := writeTarToSpool(t, dir, raw)
	prob := mustProblem(t, scanForStatefulShape(path, false))
	if prob.Code != api.CodeStatelessOnlyViolation {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeStatelessOnlyViolation)
	}
	if !strings.Contains(prob.Detail, "db/") {
		t.Errorf("detail %q does not mention db/", prob.Detail)
	}
}

// TestScanForStatefulShape_DockerfileVolume: a Dockerfile with VOLUME
// is rejected with kind=dockerfile and the offending path in detail.
func TestScanForStatefulShape_DockerfileVolume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/Dockerfile"},
		{Name: "myproject/index.js"},
	}, map[string][]byte{
		"myproject/Dockerfile": []byte("FROM node:22-slim\nVOLUME /var/lib/myapp\nCMD [\"node\",\"index.js\"]\n"),
		"myproject/index.js":   []byte(""),
	})
	path := writeTarToSpool(t, dir, raw)
	prob := mustProblem(t, scanForStatefulShape(path, false))
	if prob.Code != api.CodeStatelessOnlyViolation {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeStatelessOnlyViolation)
	}
	if !strings.Contains(prob.Detail, "VOLUME") || !strings.Contains(prob.Detail, "/var/lib/myapp") {
		t.Errorf("detail %q does not mention VOLUME /var/lib/myapp", prob.Detail)
	}
}

// TestScanForStatefulShape_DockerfileMkfs: a Dockerfile with mkfs.ext4
// inside RUN is rejected (mkfs/mount of a block device).
func TestScanForStatefulShape_DockerfileMkfs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/Dockerfile"},
	}, map[string][]byte{
		"myproject/Dockerfile": []byte("FROM alpine\nRUN mkfs.ext4 /dev/sdb1\n"),
	})
	path := writeTarToSpool(t, dir, raw)
	prob := mustProblem(t, scanForStatefulShape(path, false))
	if !strings.Contains(prob.Detail, "mkfs") {
		t.Errorf("detail %q does not mention mkfs", prob.Detail)
	}
}

// TestScanForStatefulShape_DockerfileMountExt4: same posture as mkfs,
// different trigger substring (mount -t ext4).
func TestScanForStatefulShape_DockerfileMountExt4(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/Dockerfile"},
	}, map[string][]byte{
		"myproject/Dockerfile": []byte("FROM alpine\nRUN mount -t ext4 /dev/sdb1 /data\n"),
	})
	path := writeTarToSpool(t, dir, raw)
	prob := mustProblem(t, scanForStatefulShape(path, false))
	if !strings.Contains(prob.Detail, "mkfs") && !strings.Contains(prob.Detail, "mount") {
		t.Errorf("detail %q does not mention mkfs/mount", prob.Detail)
	}
}

// TestScanForStatefulShape_CleanDockerfile: a Dockerfile without any
// VOLUME / mkfs / mount -t ext4|xfs directive is accepted.
func TestScanForStatefulShape_CleanDockerfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/Dockerfile"},
	}, map[string][]byte{
		"myproject/Dockerfile": []byte("FROM node:22-slim\nWORKDIR /app\nCOPY index.js .\nCMD [\"node\",\"index.js\"]\n"),
	})
	path := writeTarToSpool(t, dir, raw)
	if prob := scanForStatefulShape(path, false); prob != nil {
		t.Fatalf("clean Dockerfile rejected: code=%s detail=%s", prob.Code, prob.Detail)
	}
}

// TestScanDockerfileForStatefulShape_Continuation: a RUN that ends with
// `\` continues onto the next line; the mkfs check must catch a
// mkfs.ext4 on the continuation line. Pinned because the line-continuation
// logic is the easiest thing to break in a future refactor.
func TestScanDockerfileForStatefulShape_Continuation(t *testing.T) {
	dockerfile := []byte("FROM alpine\nRUN echo hello \\\n && mkfs.ext4 /dev/sdb1\n")
	if reason := scanDockerfileForStatefulShape(dockerfile); reason == "" {
		t.Fatal("expected violation on continuation, got clean")
	}
}

// TestScanForStatefulShape_LowercaseCommentIsNotAVolume: lowercase
// 'volume' inside a `#` comment is fine; the upper-cased VOLUME on its
// own line trips. Pinned so a future "lowercase the prefix check"
// refactor doesn't regress the false-positive path.
func TestScanForStatefulShape_LowercaseCommentIsNotAVolume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t, []tar.Header{
		{Name: "myproject/Dockerfile"},
	}, map[string][]byte{
		"myproject/Dockerfile": []byte("FROM node:22-slim\n# volume paths live in /app\nWORKDIR /app\n"),
	})
	path := writeTarToSpool(t, dir, raw)
	if prob := scanForStatefulShape(path, false); prob != nil {
		t.Fatalf("comment tripped the VOLUME check: detail=%s", prob.Detail)
	}
}
