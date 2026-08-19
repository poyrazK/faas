// Package edgevalidate is the gateway's compiled-JSON-Schema cache +
// validator for the kind=validate edge rule. It wraps
// github.com/santhosh-tekuri/jsonschema/v6 behind a narrow
// pkg/gateway-friendly surface, the same way pkg/edgejwks wraps
// go-jose/v4. pkg/edgevalidate is the only package that imports
// jsonschema/v6 outside generated/vendor code, so the “pkg/gateway
// doesn't import jsonschema” + “cmd-side doesn't import jsonschema
// directly” doctrines hold.
//
// One package, three files:
//
//   - validator.go (this file's siblings) — Validator interface,
//     minimal Rule / In / Result projections, the sentinel error set.
//   - cache.go — per-SHA-256 compiled-schema cache with a capacity
//     cap; no fetch / no TTL (the schema is inlined in the
//     edge_rules.action jsonb blob, so the cache is compile-once).
//   - jsonschema.go — thin wrapper around jsonschema/v6 with
//     compile-time external-$ref stripping as defence-in-depth on
//     the §11 egress posture.
//
// Cache invalidation is wholesale via Reset(); cmd-side wires the
// reset through db.NotifyEdgeRuleChanged so the cache + the
// pkg/gateway EdgeRuleCache stay in lock-step.
package edgevalidate

import (
	"errors"
	"fmt"
)

// MaxCompiledSchemas caps the per-process compiled-schema cache.
// 1024 is a conservative upper bound: compiled jsonschema/v6 schemas
// are typically 10–100 KiB each → at most ~100 MiB worst-case. LRU
// eviction kicks in over the cap (mirrors pkg/edgejwks' MaxKeysPerJWKSURL
// reasonOther is the overflow bucket for FieldError.Reason() when
// the schema-side keyword (FieldError.Expected) doesn't map to one
// of the named reasons (required_missing, type_mismatch,
// additional_properties_not_allowed, enum_violation,
// format_violation) AND when the receiver itself is nil. Hoisted
// from a string literal so goconst stops flagging the 3-occurrence
// duplication and so a future taxonomy rename is a one-line edit
// (issue #975 #3 / Mega-Foundation #979-a).
const reasonOther = "other"

// = 1024). If a customer's deploy fan-out balloons past this, the
// 1025th compile evicts the LRU — schemas are deterministic so a
// later miss re-compiles.
const MaxCompiledSchemas = 1024

// MaxSchemaBytes mirrors api.MaxEdgeRuleValidateSchemaBytes (64 KiB).
// The apid-Validate path enforces the cap upstream; the constant is
// duplicated here so pkg/edgevalidate is package-standalone (a test
// or operator-mode tool can call Compile directly without dragging
// in pkg/api).
const MaxSchemaBytes = 64 * 1024

// Sentinel errors. handler.go (and tests) match on errors.Is to pick
// the right audit kind / metric outcome. mapParseError translates
// jsonschema/v6's typed errors into this set so the failure modes
// stay stable at the wire.
var (
	// ErrSchemaEmpty is returned when the schema body is nil or
	// zero-length. apid-Validate catches this upstream; the
	// compiler guard duplicates the check so a direct Compile
	// call (operator tool, test) cannot bypass it.
	ErrSchemaEmpty = errors.New("edgevalidate: schema is empty")

	// ErrSchemaTooLarge is returned when the schema body exceeds
	// MaxSchemaBytes. Caller-side cap; the apid-Validate path
	// rejects earlier with a friendlier detail string.
	ErrSchemaTooLarge = errors.New("edgevalidate: schema exceeds MaxSchemaBytes")

	// ErrSchemaInvalid is returned when the schema body is not
	// well-formed JSON or violates a jsonschema/v6 meta-schema
	// invariant. Wrap with %w and the underlying error inside.
	ErrSchemaInvalid = errors.New("edgevalidate: schema is invalid")

	// ErrSchemaExternalRef is returned when the schema references
	// (or anchors at) a URL outside the document — even though
	// apid-Validate strips URL-shaped $ref/$id at create time,
	// the compiler re-strips at compile time as defence-in-depth.
	// A customer who sneaks a URL through (the SQL hotfix path
	// that bypasses apid-Validate) cannot ship a runtime that
	// resolves external references.
	ErrSchemaExternalRef = errors.New("edgevalidate: schema contains an external $ref or $id")

	// ErrValidationFailed is returned when the inbound body
	// fails the compiled schema. The detail string is the
	// human-readable path + reason; the handler unwraps to
	// populate api.FieldError{Field, Expected, Got} on the
	// 422 response. mapParseError translates jsonschema/v6
	// ValidationError into this sentinel.
	ErrValidationFailed = errors.New("edgevalidate: body does not match schema")
)

// FieldError is one per-field entry of a validation failure. It maps
// 1:1 onto api.FieldError so handler.go can lift the slice verbatim
// into Problem.Errors without translation. The shape mirrors how
// jsonschema/v6 reports multi-field failures (one keyword per
// instance) so a customer can drive form-field UI without parsing
// prose.
type FieldError struct {
	// Field is the dotted JSON Pointer path of the failing field
	// (e.g. "address.zip"). Empty when the failure is
	// document-wide (e.g. an enum mismatch at root).
	Field string `json:"field"`
	// Expected is a short stable identifier of the keyword that
	// failed (e.g. "string", "required", "minimum"). Mirrors
	// the apid-Validate reply format.
	Expected string `json:"expected"`
	// Got is the briefest possible description of the observed
	// value (e.g. "integer", "missing", "0"). Optional; an
	// empty Got is fine.
	Got string `json:"got,omitempty"`
}

// FormatValidationError is a small helper that turns a single
// FieldError into an error wrapping ErrValidationFailed. Used by the
// validator's first-failure path so errors.Is works at the call
// site.
func FormatValidationError(fe FieldError) error {
	return fmt.Errorf("%w: field=%q expected=%q got=%q",
		ErrValidationFailed, fe.Field, fe.Expected, fe.Got)
}

// ValidateReasonClosedSet is the bounded taxonomy of validation
// failure reasons exposed to the gateway metric. The set is closed
// (issue #975 #3 / Mega-Foundation #979-a) — the gateway metric
// label is one of these strings, never a free-form value from the
// schema. Adding a new value requires a coordinated apid-side +
// gateway-side + schema CHECK change so the cardinality stays
// bounded.
var ValidateReasonClosedSet = map[string]struct{}{
	"required_missing":                  {},
	"type_mismatch":                     {},
	"additional_properties_not_allowed": {},
	"enum_violation":                    {},
	"format_violation":                  {},
	reasonOther:                         {},
}

// Reason (issue #975 #3 / Mega-Foundation #979-a) maps a FieldError
// to the bounded metric reason. The wire-side FieldError is built
// by jsonschema/v6's ErrorKind.LocalizedString(), so the only
// pointer we have is the keyword the schema actually rejected
// (FieldError.Expected). We map the keyword to the closed set; any
// keyword we don't recognise collapses to "other".
//
// The mapping is intentionally narrow — the schema keyword is the
// only input we trust, not FieldError.Got (which is the
// localised error string and can vary by library version). Adding
// a new keyword-to-reason mapping is a one-line change here plus
// a test in handler_validate_mode_test.go.
func (fe *FieldError) Reason() string {
	if fe == nil {
		return reasonOther
	}
	switch fe.Expected {
	case "required":
		return "required_missing"
	case "type":
		return "type_mismatch"
	case "additionalProperties":
		return "additional_properties_not_allowed"
	case "enum":
		return "enum_violation"
	case "format":
		return "format_violation"
	}
	return reasonOther
}
