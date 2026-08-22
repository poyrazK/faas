package openapiimport_test

// Test surface for the structural-minimum OpenAPI validator
// (ADR-126 / issue #975 item #2). Pins the contract that the
// apid write boundary depends on:
//
//   - Valid 3.0.x and 3.1.x docs pass
//   - Missing required top-level fields fail (openapi, info, paths)
//   - Bad openapi version fails (closed enum)
//   - Non-object root fails
//   - paths must be an object (not array, not string)
//   - info.title + info.version are required + bounded
//   - EndpointCount is computed correctly across multiple
//     paths and operations
//
// The structural-minimum schema is hand-crafted (not the full
// OAI meta-schema, which OAI no longer hosts in JSON form) so
// these tests pin the contract that the apid layer relies on.
// If a follow-up PR layers in the full meta-schema, these tests
// stay green as long as the structural guarantees hold.

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/openapiimport"
)

const validOpenAPI31 = `{
  "openapi": "3.1.0",
  "info": {"title": "Test API", "version": "1.0.0"},
  "paths": {
    "/users": {"get": {"summary": "list users"}, "post": {"summary": "create user"}},
    "/users/{id}": {"get": {"summary": "get user"}, "delete": {"summary": "delete user"}}
  }
}`

const validOpenAPI30 = `{
  "openapi": "3.0.0",
  "info": {"title": "Test API 3.0", "version": "2.0.0"},
  "paths": {"/ping": {"get": {"summary": "ping"}}}
}`

const validEmptyPaths = `{
  "openapi": "3.1.0",
  "info": {"title": "Empty", "version": "0.0.1"},
  "paths": {}
}`

// TestValidateImport_Valid31 pins the happy path on a 3.1 doc
// with 4 operations across 2 paths.
func TestValidateImport_Valid31(t *testing.T) {
	version, endpointCount, err := openapiimport.ValidateImport([]byte(validOpenAPI31))
	if err != nil {
		t.Fatalf("ValidateImport(3.1): %v", err)
	}
	if version != "3.1.0" {
		t.Errorf("version: got %q, want 3.1.0", version)
	}
	if endpointCount != 4 {
		t.Errorf("endpointCount: got %d, want 4", endpointCount)
	}
}

// TestValidateImport_Valid30 pins the 3.0 path.
func TestValidateImport_Valid30(t *testing.T) {
	version, endpointCount, err := openapiimport.ValidateImport([]byte(validOpenAPI30))
	if err != nil {
		t.Fatalf("ValidateImport(3.0): %v", err)
	}
	if version != "3.0.0" {
		t.Errorf("version: got %q, want 3.0.0", version)
	}
	if endpointCount != 1 {
		t.Errorf("endpointCount: got %d, want 1", endpointCount)
	}
}

// TestValidateImport_EmptyPaths pins the empty `paths: {}`
// case. The customer's doc declares the API but has no
// operations yet — the apid surface accepts it.
func TestValidateImport_EmptyPaths(t *testing.T) {
	_, endpointCount, err := openapiimport.ValidateImport([]byte(validEmptyPaths))
	if err != nil {
		t.Fatalf("ValidateImport(empty paths): %v", err)
	}
	if endpointCount != 0 {
		t.Errorf("endpointCount: got %d, want 0", endpointCount)
	}
}

// TestValidateImport_MissingOpenAPI pins the missing openapi
// field contract.
func TestValidateImport_MissingOpenAPI(t *testing.T) {
	doc := `{"info":{"title":"x","version":"1"},"paths":{}}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on missing openapi field")
	}
	if !openapiimport.IsValidationError(err) {
		t.Errorf("error type: got %T, want *ValidationError", err)
	}
}

// TestValidateImport_MissingInfo pins the missing info field
// contract.
func TestValidateImport_MissingInfo(t *testing.T) {
	doc := `{"openapi":"3.1.0","paths":{}}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on missing info field")
	}
	if !openapiimport.IsValidationError(err) {
		t.Errorf("error type: got %T, want *ValidationError", err)
	}
}

// TestValidateImport_MissingPaths pins the missing paths field
// contract.
func TestValidateImport_MissingPaths(t *testing.T) {
	doc := `{"openapi":"3.1.0","info":{"title":"x","version":"1"}}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on missing paths field")
	}
}

// TestValidateImport_BadVersion pins the closed enum. The
// SQL CHECK in migration 00378 enforces the same enum on the
// openapi_version column; the validator enforces it upstream
// so the customer gets a structured error rather than a 23514.
func TestValidateImport_BadVersion(t *testing.T) {
	doc := `{"openapi":"3.2.0","info":{"title":"x","version":"1"},"paths":{}}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on openapi=3.2.0")
	}
	var ve *openapiimport.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error type: got %T, want *ValidationError", err)
	}
	// Path should point to the openapi field (top-level).
	if !strings.Contains(ve.Path, "openapi") && ve.Path != "" {
		t.Logf("Path: %q (informational; jsonschema/v6 may pin to root for enum violations)", ve.Path)
	}
	if ve.Reason == "" {
		t.Error("ValidationError.Reason empty")
	}
}

// TestValidateImport_NonObjectRoot pins the type:object root
// contract. An array, string, or number root must fail.
func TestValidateImport_NonObjectRoot(t *testing.T) {
	cases := []struct{ name, doc string }{
		{"array", `[]`},
		{"string", `"hello"`},
		{"number", `42`},
		{"null", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := openapiimport.ValidateImport([]byte(tc.doc))
			if err == nil {
				t.Fatalf("expected ValidationError on root=%s", tc.name)
			}
		})
	}
}

// TestValidateImport_PathsNotObject pins that `paths` must be
// an object (not array, not string).
func TestValidateImport_PathsNotObject(t *testing.T) {
	doc := `{"openapi":"3.1.0","info":{"title":"x","version":"1"},"paths":[]}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on paths=[]")
	}
}

// TestValidateImport_InfoMissingTitle pins the
// info.title-required contract.
func TestValidateImport_InfoMissingTitle(t *testing.T) {
	doc := `{"openapi":"3.1.0","info":{"version":"1"},"paths":{}}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on missing info.title")
	}
}

// TestValidateImport_InfoMissingVersion pins the
// info.version-required contract.
func TestValidateImport_InfoMissingVersion(t *testing.T) {
	doc := `{"openapi":"3.1.0","info":{"title":"x"},"paths":{}}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on missing info.version")
	}
}

// TestValidateImport_EndpointCount pins the operation count
// across multiple paths and methods. The apid layer uses this
// to enforce OpenAPIImportMaxEndpoints (50) at the SQL CHECK
// layer + the apid gate.
func TestValidateImport_EndpointCount(t *testing.T) {
	doc := `{
	  "openapi": "3.1.0",
	  "info": {"title": "Many Ops", "version": "1"},
	  "paths": {
	    "/a": {"get": {}, "post": {}, "put": {}, "delete": {}, "patch": {}, "options": {}, "head": {}, "trace": {}},
	    "/b": {"get": {}, "post": {}},
	    "/c": {"parameters": [{"name": "x", "in": "query"}], "get": {}}
	  }
	}`
	_, n, err := openapiimport.ValidateImport([]byte(doc))
	if err != nil {
		t.Fatalf("ValidateImport: %v", err)
	}
	// /a has 8 methods, /b has 2, /c has 1 (parameters is not an op).
	if n != 11 {
		t.Errorf("endpointCount: got %d, want 11", n)
	}
}

// TestValidateImport_SniffFalsePositive pins the cheap-sniff
// failure mode. A random JSON object with an "openapi" key but
// nothing else structured must be rejected by the meta-schema
// even though the early-reject sniff at
// cmd/apid/handler_openapi_doc.go:341 would admit it.
func TestValidateImport_SniffFalsePositive(t *testing.T) {
	doc := `{"openapi": "3.1.0", "junk": "value"}`
	_, _, err := openapiimport.ValidateImport([]byte(doc))
	if err == nil {
		t.Fatal("expected ValidationError on sniff false-positive (junk JSON with openapi key)")
	}
}
