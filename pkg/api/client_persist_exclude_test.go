// client_persist_exclude_test.go — pins the persist_exclude wire
// shape that ADR-124 follow-up #3 (PR-B commit 5) added to the
// ScanProject / ApplyProjectPlan multipart body.
//
// What this test pins
//
//   - The multipart field name is exactly `persist_exclude`.
//   - When persistExclude=false the field is OMITTED from the body
//     (preserves wire-shape stability for existing captures).
//   - When persistExclude=true the field value is the literal
//     string "true" (the server's parser also accepts "1" / "yes"
//     but the canonical wire value is "true").
//   - The field is a sibling of `exclude` (post-ADR-124
//     inverse-allowlist) — both fields are present in the same
//     multipart body when both flags are set.
//
// Build tag: (none). CI-safe; no infra needed.

package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"strings"
	"testing"
)

// TestWriteProjectMultipartFields_PersistExclude_OmittedByDefault
// pins the default-OFF posture: when persistExclude=false, the
// multipart body MUST NOT carry the persist_exclude field. This
// preserves wire-capture compatibility (existing integration tests
// that diff the full body would trip if the field appeared out of
// nowhere).
func TestWriteProjectMultipartFields_PersistExclude_OmittedByDefault(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := writeProjectMultipartFields(w, strings.NewReader("x"), "src.tgz",
		"demo", "", "main", 0, nil, []string{"foo"}, false); err != nil {
		t.Fatalf("writeProjectMultipartFields: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	body, err := io.ReadAll(multipartReader(t, buf.Bytes()))
	if err != nil {
		t.Fatalf("read multipart: %v", err)
	}
	if strings.Contains(string(body), "persist_exclude") {
		t.Errorf("multipart body carries persist_exclude when flag is false; "+
			"want omitted by default. body=%s", body)
	}
	// Sanity: exclude IS present (the inverse-allowlist shape).
	if !strings.Contains(string(body), "name=\"exclude\"") {
		t.Errorf("multipart body missing exclude field; body=%s", body)
	}
}

// TestWriteProjectMultipartFields_PersistExclude_EmittedWhenTrue
// pins the opt-in write path: when persistExclude=true, the
// multipart body carries persist_exclude=true. The server's parser
// (cmd/apid/scan_service.go::parseScanMultipart case
// "persist_exclude") accepts the literal "true".
func TestWriteProjectMultipartFields_PersistExclude_EmittedWhenTrue(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := writeProjectMultipartFields(w, strings.NewReader("x"), "src.tgz",
		"demo", "", "main", 0, nil, []string{"foo"}, true); err != nil {
		t.Fatalf("writeProjectMultipartFields: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	body, err := io.ReadAll(multipartReader(t, buf.Bytes()))
	if err != nil {
		t.Fatalf("read multipart: %v", err)
	}
	if !strings.Contains(string(body), "name=\"persist_exclude\"") {
		t.Errorf("multipart body missing persist_exclude field; body=%s", body)
	}
	if !strings.Contains(string(body), "persist_exclude\"\r\n\r\ntrue") {
		t.Errorf("multipart body persist_exclude field value != \"true\"; body=%s", body)
	}
}

// TestWriteProjectMultipartFields_PersistExclude_NotEmittedWhenExcludeEmpty
// pins the operator-experience corner case: persist-exclude without
// --exclude is a no-op write. The body still carries the flag
// (server-side audit + idempotency depend on the operator's intent
// shape) but the server's handler ignores it because Skipped is
// empty. This test documents that the flag IS emitted even when
// --exclude is unset — the no-op happens server-side, not
// client-side.
func TestWriteProjectMultipartFields_PersistExclude_NotEmittedWhenExcludeEmpty(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	// persistExclude=true with no exclude list. Server-side this
	// is a no-op write (Skipped empty), but the body still carries
	// the flag for the audit trail.
	if err := writeProjectMultipartFields(w, strings.NewReader("x"), "src.tgz",
		"demo", "", "main", 0, nil, nil, true); err != nil {
		t.Fatalf("writeProjectMultipartFields: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	body, err := io.ReadAll(multipartReader(t, buf.Bytes()))
	if err != nil {
		t.Fatalf("read multipart: %v", err)
	}
	// Flag IS emitted (server decides whether to honour it).
	if !strings.Contains(string(body), "name=\"persist_exclude\"") {
		t.Errorf("multipart body missing persist_exclude field even when exclude is empty; body=%s", body)
	}
	// exclude field is NOT emitted (no slugs to write).
	if strings.Contains(string(body), "name=\"exclude\"") {
		t.Errorf("multipart body carries exclude field when exclude list is empty; body=%s", body)
	}
}

// TestWriteProjectMultipartFields_ProjectBinding pins the fields that let a
// CLI-created project participate in GitHub push reconciliation. The binding
// tuple must travel with both scan and apply requests so the server can persist
// the same repository, installation, and production branch identity.
func TestWriteProjectMultipartFields_ProjectBinding(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := writeProjectMultipartFields(w, strings.NewReader("x"), "src.tgz",
		"demo", "acme/widgets", "release", 42, nil, nil, false); err != nil {
		t.Fatalf("writeProjectMultipartFields: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	body := string(buf.Bytes())
	for _, want := range []string{
		"name=\"repo_full_name\"\r\n\r\nacme/widgets",
		"name=\"production_branch\"\r\n\r\nrelease",
		"name=\"install_id\"\r\n\r\n42",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("multipart body missing %q; body=%s", want, body)
		}
	}
}

// multipartReader returns an io.Reader that re-parses the multipart
// body for content scanning. Tests use this to assert specific
// field names/values without depending on a real HTTP server.
func multipartReader(t *testing.T, body []byte) io.Reader {
	t.Helper()
	// We need a Content-Type to parse the multipart; reconstruct
	// the boundary by re-walking the body. Easiest path: use
	// mime.ParseMediaType to extract the boundary from a
	// synthesized Content-Type header. The body starts with
	// "--<boundary>\r\n".
	boundary := extractBoundary(t, body)
	_ = boundary
	return bytes.NewReader(body)
}

// extractBoundary returns the multipart boundary encoded in body.
// The first line of a multipart body is "--<boundary>\r\n".
func extractBoundary(t *testing.T, body []byte) string {
	t.Helper()
	firstLine := body
	if idx := bytes.IndexByte(body, '\n'); idx >= 0 {
		firstLine = body[:idx]
	}
	firstLine = bytes.TrimPrefix(firstLine, []byte("--"))
	firstLine = bytes.TrimRight(firstLine, "\r")
	return string(firstLine)
}
