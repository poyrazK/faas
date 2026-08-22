// Package openapiimport validates customer-imported OpenAPI
// documents at the apid write boundary (ADR-126 / issue #975
// item #2).
//
// The validator compiles a structural-minimum JSON Schema
// (schemas/openapi-import-check.json) against the Draft 2020-12
// jsonschema/v6 compiler. It is NOT the full OpenAPI 3.1
// meta-schema — the canonical OAI repo no longer hosts schema.json
// (see OAI/OpenAPI-Specification issue #4149 from 2024-10). The
// structural schema pins the load-bearing shape requirements the
// apid layer needs for abuse prevention:
//
//   - Top-level is a JSON object
//   - Required fields: openapi, info, paths (paths can be empty {})
//   - openapi version is one of the seven accepted (3.0.0-3.0.4,
//     3.1.0-3.1.1)
//   - info.title + info.version are non-empty bounded strings
//   - paths is an object
//
// Customers get a structural guarantee; the full meta-schema can
// be layered in later (jsonschema/v6 resource stacking supports
// it without API changes).
//
// The compiled meta-schema is a package-level singleton
// (metaSchemaSingleton, computed in init). Compiling the
// structural schema is ~5ms; doing it once at package init
// amortises that across every ValidateImport call. The
// *jsonschema.Schema is safe for concurrent Validate calls
// (jsonschema/v6 documents this as the safe concurrency mode).
//
// The schemaRefURLPattern guard from pkg/edgevalidate is NOT
// applied here — that guard rejects customer-supplied schemas
// compiled into the runtime validator (the edge-validate path).
// This package is the inverse: customer docs validated against
// the meta-schema. The two flows are not symmetric.
package openapiimport

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/text/language"
	"golang.org/x/text/message"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// openapiImportSchema is the structural-minimum schema embedded
// at build time. //go:embed pins the file at
// pkg/openapiimport/schemas/openapi-import-check.json — a build
// error if the file is missing or renamed.
//
//go:embed schemas/openapi-import-check.json
var openapiImportSchema []byte

// defaultPrinter mirrors pkg/edgevalidate/jsonschema.go's
// defaultPrinter. LocalizedString panics on nil printer (per
// jsonschema/v6 output.go); we keep a private English printer
// so the wire shape is stable across locale env.
var defaultPrinter = message.NewPrinter(language.English)

// metaSchemaURL is the canonical resource URL the meta-schema
// is loaded at. The URL is referenced by the schema's $id and
// is the load-bearing key for jsonschema/v6's resource loader.
const metaSchemaURL = "https://gregale.dev/schemas/openapi-import-check-1"

// metaSchemaSingleton is the compiled structural-minimum schema,
// computed once at package init. The *jsonschema.Schema is safe
// for concurrent Validate calls (per jsonschema/v6 docs); no
// pooling is necessary.
//
// A pool was the wrong pattern here — pkg/edgevalidate pools the
// *Compiler (because each customer schema is a new resource that
// needs its own Compile call). Here the meta-schema is a
// compile-once singleton; the customer doc is the VALUE being
// validated, not a resource being added to the compiler.
var metaSchemaSingleton *jsonschema.Schema

func init() {
	doc, err := loadSchema(openapiImportSchema)
	if err != nil {
		panic(fmt.Errorf("openapiimport: load embedded schema: %w", err))
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(metaSchemaURL, doc); err != nil {
		panic(fmt.Errorf("openapiimport: add meta-schema resource: %w", err))
	}
	compiled, err := c.Compile(metaSchemaURL)
	if err != nil {
		panic(fmt.Errorf("openapiimport: compile meta-schema: %w", err))
	}
	metaSchemaSingleton = compiled
}

// loadSchema parses the embedded schema bytes into a generic
// JSON value. jsonschema/v6's AddResource takes a parsed value
// (not raw bytes); this is the same pattern as
// pkg/edgevalidate/jsonschema.go:127-133.
func loadSchema(schema []byte) (any, error) {
	var doc any
	if err := json.Unmarshal(schema, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// ValidationError is the typed error returned by ValidateImport
// when the customer doc fails the structural check. It carries
// the JSON Pointer path to the offending field plus the
// jsonschema/v6 reason string. The apid handler surfaces the
// detail in the RFC 7807 problem details extension.
type ValidationError struct {
	// Path is the JSON Pointer to the offending field (e.g.,
	// "/info/title" or "/paths/~1users/get"). Empty for the
	// top-level case.
	Path string
	// Reason is the human-readable description from the
	// jsonschema/v6 leaf-cause walk. Surfaced to the customer
	// in the API response.
	Reason string
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("openapi import invalid: %s", e.Reason)
	}
	return fmt.Sprintf("openapi import invalid at %s: %s", e.Path, e.Reason)
}

// IsValidationError reports whether err is a *ValidationError.
// Convenience for the apid handler's errors.As check.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// ValidateImport runs the structural-minimum check against body.
// Returns nil when the doc is valid; returns *ValidationError
// when the doc fails; returns a wrapped error on internal
// failures (the embedded schema is missing or the compiler is
// in an unexpected state).
//
// The body must be the raw JSON bytes the customer uploaded
// (utf-8, no whitespace guarantees). The apid handler applies
// the size cap before calling this function; this function does
// the JSON parse + meta-schema validate + post-walk for
// version + endpoint count.
//
// EndpointCount is the number of HTTP operations in the doc's
// paths.*. It is computed here (post-validation, against the
// parsed JSON) so the apid handler doesn't re-walk the spec.
// The count is bounded 0..50 by the apid layer (sql CHECK
// + OpenAPIImportMaxEndpoints in pkg/api/limits.go).
//
// Version is the openapi version string from the doc. The
// validator's closed enum guarantees it's one of
// ValidOpenAPIVersions.
func ValidateImport(body []byte) (version string, endpointCount int, err error) {
	// Parse the customer doc first — meta-schema Validate
	// operates on parsed values, not raw bytes.
	var doc any
	if jsonErr := json.Unmarshal(body, &doc); jsonErr != nil {
		return "", 0, &ValidationError{
			Path:   "",
			Reason: fmt.Sprintf("invalid JSON: %s", jsonErr.Error()),
		}
	}
	parsed, ok := doc.(map[string]any)
	if !ok {
		return "", 0, &ValidationError{
			Path:   "",
			Reason: "top-level value is not an object",
		}
	}

	// Validate the parsed doc against the meta-schema.
	if validateErr := metaSchemaSingleton.Validate(parsed); validateErr != nil {
		ve := &ValidationError{}
		var verr *jsonschema.ValidationError
		if errors.As(validateErr, &verr) {
			ve.Path, ve.Reason = firstLeafCause(verr)
		} else {
			ve.Reason = validateErr.Error()
		}
		return "", 0, ve
	}

	// Extract version + endpoint count from the parsed doc.
	if v, ok := parsed["openapi"].(string); ok {
		version = v
	}
	endpointCount = countOperations(parsed)
	return version, endpointCount, nil
}

// firstLeafCause walks a *jsonschema.ValidationError tree and
// returns the JSON Pointer + reason of the deepest leaf — the
// single most-specific failure. If the error has no leaf (e.g.,
// a compile-time meta-schema violation with no deeper cause),
// the path is empty and the reason is the top-level message.
//
// jsonschema/v6's *ValidationError carries three load-bearing
// fields:
//
//   - InstanceLocation []string — JSON Pointer-style token list
//     of where the failure occurred
//   - ErrorKind interface with KeywordPath() []string and
//     LocalizedString(p *message.Printer) string — the schema
//     keyword that failed and the human-readable reason
//   - Causes []*ValidationError — nested sub-failures; we drill
//     to the deepest first-leaf
//
// We mirror pkg/edgevalidate.translateValidationError's drill
// pattern: the deepest cause is the most-specific actionable
// failure (a high-level "type mismatch" is rarely actionable;
// the field-specific "required" / "type" check is).
func firstLeafCause(verr *jsonschema.ValidationError) (path, reason string) {
	if verr == nil {
		return "", "validation failed"
	}
	// Drill to the deepest leaf.
	current := verr
	for len(current.Causes) > 0 {
		current = current.Causes[0]
	}
	// Build the JSON Pointer-style path.
	path = joinInstanceLocation(current.InstanceLocation)
	if path == "" {
		path = joinInstanceLocation(verr.InstanceLocation)
	}
	// Get the human-readable reason via ErrorKind.
	if current.ErrorKind != nil {
		reason = current.ErrorKind.LocalizedString(defaultPrinter)
	}
	if reason == "" && verr.ErrorKind != nil {
		reason = verr.ErrorKind.LocalizedString(defaultPrinter)
	}
	if reason == "" {
		reason = "validation failed"
	}
	return path, reason
}

// joinInstanceLocation joins an InstanceLocation []string into
// a slash-separated JSON Pointer-style path. Mirrors the
// helper in pkg/edgevalidate/jsonschema.go; inlined here so
// this package has no dependency on pkg/edgevalidate (the
// validator runs at the apid boundary, edgevalidate runs at
// the gatewayd-internal boundary — different layers).
func joinInstanceLocation(loc []string) string {
	if len(loc) == 0 {
		return ""
	}
	var sz int
	for _, s := range loc {
		sz += 1 + len(s)
	}
	out := make([]byte, 0, sz)
	for _, s := range loc {
		out = append(out, '/')
		out = append(out, s...)
	}
	return string(out)
}

// countOperations walks the parsed doc and counts HTTP
// operations under paths.*. Recognised methods are the 8 in the
// OpenAPI 3.x spec: get, put, post, delete, options, head, patch,
// trace. Path-level parameters are not counted (they are shared
// across operations, not operations themselves).
func countOperations(parsed map[string]any) int {
	paths, ok := parsed["paths"].(map[string]any)
	if !ok {
		return 0
	}
	n := 0
	for _, pathItem := range paths {
		pi, ok := pathItem.(map[string]any)
		if !ok {
			continue
		}
		for k := range pi {
			switch k {
			case "get", "put", "post", "delete", "options", "head", "patch", "trace":
				n++
			}
		}
	}
	return n
}
