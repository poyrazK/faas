package edgevalidate

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

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

	// Digest identifies the source and closed-object policy that produced this
	// CompiledSchema. The same digest is stashed on the resolved
	// edge rule at load time so the hot path can look up the
	// CompiledSchema without re-hashing.
	Digest [32]byte
}

// defaultPrinter aliases the shared jsonschemautil.DefaultPrinter
// so the existing call sites stay unchanged. LocalizedString
// panics on nil printer (per jsonschema/v6 output.go:13); the
// shared instance is an English printer so customers get a stable
// wire shape regardless of the daemon's locale env. Factored
// into pkg/jsonschemautil so pkg/openapiimport can share it.
var defaultPrinter = jsonschemautil.DefaultPrinter

// inlineSchemaLoader prevents the compiler (including meta-schema resolution)
// from consulting the filesystem or network. Standard metaschemas are embedded
// by jsonschema and are resolved before this loader is consulted.
type inlineSchemaLoader struct{}

func (inlineSchemaLoader) Load(string) (any, error) {
	return nil, ErrSchemaExternalRef
}

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

	// SHA-256 the source bytes so the digest matches what the
	// applier cached on the resolved rule.
	digest := schemaDigest(schema, rejectUnknownFields)

	// Each compile owns its resource maps. Reusing a compiler retains every
	// document and compiled node independently of the bounded schema LRU.
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(inlineSchemaLoader{})
	const loc = "mem://schema"

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, errors.Join(ErrSchemaInvalid, err)
	}
	if err := checkNumberWork(doc, MaxSchemaBytes); err != nil {
		return nil, errors.Join(ErrSchemaInvalid, err)
	}
	if err := checkInlineSchema(doc); err != nil {
		return nil, err
	}
	if err := CheckSchemaComplexity(doc); err != nil {
		return nil, err
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

func schemaDigest(schema []byte, rejectUnknownFields bool) [32]byte {
	if rejectUnknownFields {
		h := sha256.New()
		_, _ = h.Write(schema)
		_, _ = h.Write([]byte(rejectUnknownFieldsDigestSuffix))
		var digest [32]byte
		copy(digest[:], h.Sum(nil))
		return digest
	}
	return sha256.Sum256(schema)
}

// CheckSchemaComplexity refuses a decoded schema document whose combinators
// nest deeper than MaxSchemaCombinatorDepth (ErrSchemaTooComplex). Every
// place that compiles a customer's schema runs it first.
func CheckSchemaComplexity(doc any) error {
	walk := &combinatorWalk{root: doc, memo: map[string]int{}, open: map[string]bool{}, anchors: map[string]any{}}
	walkSchemaNodes(doc, func(node map[string]any) {
		for _, keyword := range []string{"$anchor", "$dynamicAnchor", "$id", "id"} {
			if name, ok := node[keyword].(string); ok && name != "" {
				if keyword == "$id" || keyword == "id" {
					if !strings.HasPrefix(name, "#") {
						continue
					}
					name = strings.TrimPrefix(name, "#")
				}
				walk.anchors[name] = node
			}
		}
	})
	if depth := walk.depth(doc); depth > MaxSchemaCombinatorDepth {
		return fmt.Errorf("%w: %d levels, at most %d", ErrSchemaTooComplex, depth, MaxSchemaCombinatorDepth)
	}
	return nil
}

// combinatorWalk measures how deeply combinators nest in a schema document,
// following local "#..." $refs once per path (a recursive $ref stops there)
// and memoizing each ref's depth so the walk stays linear.
type combinatorWalk struct {
	root    any
	anchors map[string]any
	memo    map[string]int
	open    map[string]bool
}

func (c *combinatorWalk) depth(node any) int {
	switch v := node.(type) {
	case []any:
		deepest := 0
		for _, item := range v {
			deepest = max(deepest, c.depth(item))
		}
		return deepest
	case map[string]any:
		deepest := 0
		for key, val := range v {
			switch key {
			case "anyOf", "oneOf", "allOf", "not", "if", "then", "else":
				deepest = max(deepest, 1+c.depth(val))
			case "$ref", "$dynamicRef", "$recursiveRef":
				if ref, ok := val.(string); ok {
					deepest = max(deepest, c.refDepth(ref))
				}
			default:
				deepest = max(deepest, c.depth(val))
			}
		}
		return deepest
	}
	return 0
}

func (c *combinatorWalk) refDepth(ref string) int {
	if !strings.HasPrefix(ref, "#") || c.open[ref] {
		return 0
	}
	if d, ok := c.memo[ref]; ok {
		return d
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(ref, "#"))
	if err != nil {
		return 0
	}
	target, ok := resolveLocalPointer(c.root, fragment)
	if !ok {
		target, ok = c.anchors[fragment]
	}
	if !ok {
		return 0
	}
	c.open[ref] = true
	d := c.depth(target)
	delete(c.open, ref)
	c.memo[ref] = d
	return d
}

// resolveLocalPointer follows an RFC 6901 JSON pointer within doc.
func resolveLocalPointer(doc any, pointer string) (any, bool) {
	if pointer == "" {
		return doc, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	node := doc
	for _, token := range strings.Split(pointer[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch v := node.(type) {
		case map[string]any:
			next, ok := v[token]
			if !ok {
				return nil, false
			}
			node = next
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			node = v[i]
		default:
			return nil, false
		}
	}
	return node, true
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
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		// Body is not JSON. Treat as a single-field failure
		// with the path "" (root) and the expected "json"
		// rather than bubbling the unmarshal error — the
		// customer-facing shape is a 422 with `errors[]`,
		// not a 500. The audit log records the matching
		// schema_digest + sample first-byte for forensics.
		return &FieldError{Field: "", Expected: "json", Got: "non-json"}, nil //nolint:nilerr
	}
	if fe := duplicateKeyProblem(body); fe != nil {
		return fe, nil
	}
	if err := checkNumberWork(v, MaxNumberWorkBytes); err != nil {
		return &FieldError{Field: "", Expected: "number", Got: err.Error()}, nil
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
	// A required failure is located at the object that lacks the property,
	// so a missing root field printed an empty field (Hunt 32 H32-60). The
	// field names the missing property instead.
	if required, ok := ve.ErrorKind.(*kind.Required); ok && len(required.Missing) > 0 {
		field = jsonschemautil.JoinInstanceLocation(append(slices.Clone(ve.InstanceLocation), required.Missing[0]))
	}
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
