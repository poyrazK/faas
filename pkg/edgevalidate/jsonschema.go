package edgevalidate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/onebox-faas/faas/pkg/jsonschemautil"
)

// CompiledSchema is the result of Compile: a ready-to-Validate
// in-memory schema + its SHA-256 digest (for cache lookups). The
// digest is computed over the raw schema bytes (post-strip), so the
// cache key is stable across calls.
type CompiledSchema struct {
	// Schema is the compiled schema. It is safe to call Validate
	// concurrently across many goroutines — jsonschema/v6
	// documents this as the only safe concurrency mode.
	Schema *jsonschema.Schema

	// Digest is sha256(schema) of the bytes that produced this
	// CompiledSchema. The same digest is stashed on the resolved
	// edge rule at load time so the hot path can look up the
	// CompiledSchema without re-hashing.
	Digest [32]byte
}

// schemaRefURLPattern matches any "$ref" or "$id" value that is a
// URL (has a scheme). JSON Pointer refs (`#/definitions/...`) do
// not match because they have no scheme. The apid-Validate path
// uses a tighter regex; this is defence-in-depth on the compiler
// side.
var schemaRefURLPattern = regexp.MustCompile(`(?i)"\$(?:ref|id)"\s*:\s*"[a-z][a-z0-9+.-]*://`)

// jsonschemaDraft2020 is the singleton Draft 2020-12 value.
// The library uses Draft2020 (the spec alias for 2020-12); pinning
// here so the dependency upgrade surface is one line. The
// schemaMempool factory below takes the address (`*Draft`) so the
// pointer is stable across Compiler instances.
var jsonschemaDraft2020 = *jsonschema.Draft2020

// defaultPrinter aliases the shared jsonschemautil.DefaultPrinter
// so the existing call sites stay unchanged. LocalizedString
// panics on nil printer (per jsonschema/v6 output.go:13); the
// shared instance is an English printer so customers get a stable
// wire shape regardless of the daemon's locale env. Factored
// into pkg/jsonschemautil so pkg/openapiimport can share it.
var defaultPrinter = jsonschemautil.DefaultPrinter

// schemaMempool is a sync.Pool of *jsonschema.Compiler. We keep a
// per-call compiler (the library has no safe Compile-on-existing-
// compiler API), so pooling is the cheapest path. Capped to a
// goroutine-local pool: a high-concurrency gateway will see one
// pool hit per request.
//
// NOTE: jsonschema.Compiler is not safe for concurrent Compile
// calls (it builds internal maps during AddResource). Pooling
// defeats that risk because Get/ Put bracketing means at most
// one goroutine owns a compiler at a time.
//
// IMPORTANT: AddResource rejects duplicate URLs with
// "resource for ... already exists". To make pool reuse safe,
// every Compile call uses a unique URL of the form
// "mem://schema-<monotonic-id>" so successive Compile calls on
// the same pooled compiler don't collide.
var schemaMempool = sync.Pool{
	New: func() any {
		c := jsonschema.NewCompiler()
		// Pin to Draft 2020-12. The library will reject any
		// schema whose $schema is a different draft.
		c.DefaultDraft(&jsonschemaDraft2020)
		return c
	},
}

// schemaIDCounter is an atomic counter paired with the pool. It
// guarantees that every Compile call uses a unique URL even when
// the underlying Compiler is recycled from the pool.
var schemaIDCounter atomic.Uint64

// Compile compiles a Draft 2020-12 JSON Schema into a
// CompiledSchema. It performs two safety checks before handing off
// to the library:
//
//  1. Schema must be non-empty and ≤ MaxSchemaBytes (64 KiB).
//  2. Schema must not contain external `$ref` or `$id` URLs —
//     the §11 egress posture forbids the gateway from reaching
//     out from inside a tenant request path. Strip attempts
//     are rejected (the apid-Validate path catches this
//     earlier; we duplicate here for defence-in-depth in case
//     the SQL hotfix path bypasses apid).
//
// rejectUnknownFields applies the rule's reject_on_unknown_fields knob
// (closeUnknownFields) and is folded into the digest, so a closed and an
// open compile of the same schema never share a cache entry.
func Compile(schema []byte, rejectUnknownFields bool) (*CompiledSchema, error) {
	if len(schema) == 0 {
		return nil, ErrSchemaEmpty
	}
	if len(schema) > MaxSchemaBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrSchemaTooLarge, len(schema))
	}
	if schemaRefURLPattern.Match(schema) {
		// Stash the URL that matched for the audit log.
		return nil, fmt.Errorf("%w: %s", ErrSchemaExternalRef, extractFirstExternalRef(schema))
	}

	// SHA-256 the post-strip bytes so the digest matches what the
	// applier cached on the resolved rule.
	digest := sha256.Sum256(schema)
	if rejectUnknownFields {
		digest = sha256.Sum256(append(append([]byte(nil), schema...), rejectUnknownFieldsDigestSuffix...))
	}

	compiler := schemaMempool.Get().(*jsonschema.Compiler)
	defer schemaMempool.Put(compiler)

	// Per-call unique URL — AddResource rejects duplicates, and
	// the Compiler is recycled from the pool so successive calls
	// would collide on a fixed URL.
	loc := fmt.Sprintf("mem://schema-%d", schemaIDCounter.Add(1))

	var doc any
	if err := json.Unmarshal(schema, &doc); err != nil {
		return nil, errors.Join(ErrSchemaInvalid, err)
	}
	if rejectUnknownFields {
		closeUnknownFields(doc, true, legacyDraft(doc))
	}
	if err := compiler.AddResource(loc, doc); err != nil {
		return nil, errors.Join(ErrSchemaInvalid, err)
	}
	compiled, err := compiler.Compile(loc)
	if err != nil {
		return nil, errors.Join(ErrSchemaInvalid, err)
	}

	return &CompiledSchema{Schema: compiled, Digest: digest}, nil
}

// rejectUnknownFieldsDigestSuffix separates the digest of a closed
// compile from the open compile of the same schema bytes.
const rejectUnknownFieldsDigestSuffix = "\x00reject_on_unknown_fields"

// closeUnknownFields implements reject_on_unknown_fields: each object
// schema that declares fields gets unevaluatedProperties=false, so a body
// field the schema does not declare fails with a 422. Unlike
// additionalProperties, Draft 2020-12 unevaluatedProperties counts fields
// declared through $ref, allOf, anyOf, oneOf and if/then/else, so composed
// schemas keep working. An explicit additionalProperties or
// unevaluatedProperties is the author's decision and is kept.
//
// Subschemas that apply in place to the same instance (allOf, anyOf,
// oneOf, not, if/then/else, dependentSchemas, $defs) are not closed
// themselves, since closing one branch would reject the fields its
// siblings declare; their nested field schemas are still closed. A
// free-form object ({"type":"object"} with no declared fields) stays open.
//
// Drafts 04-07 have no unevaluatedProperties, so for them a schema that
// lists its fields directly gets additionalProperties=false and a composed
// one ($ref, allOf, anyOf, oneOf, if, dependencies) stays open.
func closeUnknownFields(node any, closeSelf, legacy bool) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"properties", "patternProperties"} {
		if children, ok := m[key].(map[string]any); ok {
			for _, child := range children {
				closeUnknownFields(child, true, legacy)
			}
		}
	}
	for _, key := range []string{"additionalProperties", "items", "additionalItems", "contains", "unevaluatedItems"} {
		closeUnknownFields(m[key], true, legacy)
	}
	for _, key := range []string{"prefixItems", "items"} {
		if items, ok := m[key].([]any); ok {
			for _, child := range items {
				closeUnknownFields(child, true, legacy)
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if branches, ok := m[key].([]any); ok {
			for _, child := range branches {
				closeUnknownFields(child, false, legacy)
			}
		}
	}
	for _, key := range []string{"not", "if", "then", "else"} {
		closeUnknownFields(m[key], false, legacy)
	}
	for _, key := range []string{"dependentSchemas", "dependencies", "$defs", "definitions"} {
		if children, ok := m[key].(map[string]any); ok {
			for _, child := range children {
				closeUnknownFields(child, false, legacy)
			}
		}
	}
	if !closeSelf || hasAnyKey(m, "additionalProperties", "unevaluatedProperties") {
		return
	}
	switch {
	case legacy:
		if hasAnyKey(m, "properties", "patternProperties") && !hasAnyKey(m, "$ref", "allOf", "anyOf", "oneOf", "if", "dependencies") {
			m["additionalProperties"] = false
		}
	case hasAnyKey(m, "properties", "patternProperties", "$ref", "$dynamicRef", "allOf", "anyOf", "oneOf", "if", "dependentSchemas"):
		m["unevaluatedProperties"] = false
	}
}

func hasAnyKey(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

// legacyDraft reports whether the schema declares a draft without
// unevaluatedProperties (04, 06 or 07).
func legacyDraft(doc any) bool {
	m, ok := doc.(map[string]any)
	if !ok {
		return false
	}
	id, _ := m["$schema"].(string)
	for _, draft := range []string{"draft-04", "draft-06", "draft-07"} {
		if strings.Contains(id, draft) {
			return true
		}
	}
	return false
}

// Validate runs the compiled schema against a parsed-JSON body. It
// expects the body to already be a json.RawMessage-shaped buffer;
// pass-through to jsonschema/v6 returns nil on success and a
// typed *jsonschema.ValidationError on failure. We translate the
// first *ValidationError into a FieldError so the handler can
// emit a stable wire shape.
func (c *CompiledSchema) Validate(body []byte) (*FieldError, error) {
	if c == nil || c.Schema == nil {
		return nil, fmt.Errorf("%w: compiled schema is nil", ErrSchemaInvalid)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		// Body is not JSON. Treat as a single-field failure
		// with the path "" (root) and the expected "json"
		// rather than bubbling the unmarshal error — the
		// customer-facing shape is a 422 with `errors[]`,
		// not a 500. The audit log records the matching
		// schema_digest + sample first-byte for forensics.
		return &FieldError{Field: "", Expected: "json", Got: "non-json"}, nil //nolint:nilerr
	}
	if err := c.Schema.Validate(v); err != nil {
		// jsonschema/v6 returns a *ValidationError. errors.As
		// walks the wrap chain (the library nests via
		// Causes) so a wrapped ValidationError still resolves.
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return translateValidationError(ve), nil
		}
		// Some other error: the schema is structurally
		// broken (e.g. keyword violation at the schema
		// itself). Treat as schema-invalid; caller maps to
		// 500.
		return nil, errors.Join(ErrSchemaInvalid, err)
	}
	return nil, nil
}

// translateValidationError maps a jsonschema/v6 ValidationError into
// the pkg/edgevalidate FieldError shape. The library returns a tree
// of errors with InstanceLocation []string, ErrorKind (an
// interface exposing KeywordPath() []string), and Causes []. We
// join InstanceLocation with "/" to form a JSON Pointer-style
// field path, take the last element of KeywordPath() as the
// expected keyword, and use ErrorKind's LocalizedString as the
// human-readable got.
//
// NOTE: the library nests sub-errors via Causes; we walk down to
// the first leaf so the customer sees the most-specific failure
// rather than a high-level umbrella. (A high-level "type"
// mismatch is rarely actionable; the field-specific "required" /
// "type" check is.)
func translateValidationError(ve *jsonschema.ValidationError) *FieldError {
	if ve == nil {
		return &FieldError{Field: "", Expected: "schema", Got: "unknown"}
	}
	for len(ve.Causes) > 0 {
		ve = ve.Causes[0]
	}
	field := jsonschemautil.JoinInstanceLocation(ve.InstanceLocation)
	// unevaluatedProperties (reject_on_unknown_fields) reports each
	// undeclared field as a false-schema leaf; only its schema location
	// names the keyword.
	if _, falseLeaf := ve.ErrorKind.(*kind.FalseSchema); falseLeaf {
		for _, keyword := range []string{"unevaluatedProperties", "additionalProperties"} {
			if strings.HasSuffix(ve.SchemaURL, "/"+keyword) {
				return &FieldError{Field: field, Expected: keyword, Got: "field is not declared by the schema"}
			}
		}
	}
	expected := ""
	if ve.ErrorKind != nil {
		kp := ve.ErrorKind.KeywordPath()
		if n := len(kp); n > 0 {
			expected = kp[n-1]
		}
	}
	got := ""
	if ve.ErrorKind != nil {
		got = ve.ErrorKind.LocalizedString(defaultPrinter)
	}
	return &FieldError{Field: field, Expected: expected, Got: got}
}

// joinInstanceLocation was previously inlined here. It now lives
// in pkg/jsonschemautil so pkg/openapiimport and this package can
// share the canonical JSON Pointer shape. The local copy was
// identical to the openapiimport copy; the dedup lands here as
// part of the #975-item-2 review-fix cluster (Fix #8).

// extractFirstExternalRef pulls the first $ref or $id URL value
// out of the schema, for the audit log. It does a quick pass; not
// a real JSON parser (the regex already validated that there IS a
// match, so a partial extraction is enough).
//
// The regex match spans from the opening quote of "$ref"/"$id"
// through the opening quote of the URL value. We walk forward
// from idx[1] (end of regex match) backwards one byte to land
// on the opening quote of the URL value, then walk to its
// closing quote.
func extractFirstExternalRef(schema []byte) string {
	idx := schemaRefURLPattern.FindIndex(schema)
	if idx == nil {
		return ""
	}
	// idx[1] is one byte past the last matched char (the opening
	// quote of the URL value). So the URL value starts at
	// idx[1]-1 and ends at the next unescaped quote.
	start := idx[1] - 1
	if start < 0 || start >= len(schema) || schema[start] != '"' {
		return ""
	}
	end := start + 1
	for end < len(schema) && schema[end] != '"' {
		// Skip escaped backslashes: \"
		if schema[end] == '\\' && end+1 < len(schema) {
			end += 2
			continue
		}
		end++
	}
	if end > len(schema) {
		end = len(schema)
	}
	if end <= start+1 {
		return ""
	}
	raw := string(schema[start+1 : end])
	if _, err := url.Parse(raw); err != nil {
		return raw
	}
	return raw
}
