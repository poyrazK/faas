package edgevalidate

import (
	"context"
)

// Rule is the minimal projection of an edge rule's validation
// config that pkg/edgevalidate needs to do its work. It deliberately
// avoids importing pkg/state or pkg/gateway — the same one-way
// dependency direction pkg/edgejwks enforces (pkg/edgejwks does not
// import pkg/gateway; pkg/gateway imports pkg/edgejwks via the
// validator interface). The cmd-side adapter copies fields from
// pkg/state.EdgeRuleValidateAction into Rule on load.
type Rule struct {
	// SchemaDigest is the SHA-256 of the raw schema body, computed
	// once at apid create-time and cached on the resolved rule.
	// It's the cache key for CompiledSchema lookup; the schema body
	// itself is not on the hot path.
	SchemaDigest [32]byte

	// ApplyWhileStreaming mirrors the per-rule
	// apply_while_streaming knob from EdgeRuleValidateAction. When
	// the inbound request is part of a streaming response, the
	// applier short-circuits to pass-through unless this is true.
	// Default false (safer default — body validation requires the
	// full body, which streaming doesn't have).
	ApplyWhileStreaming bool

	// RejectUnknownFields mirrors the per-rule
	// reject_on_unknown_fields knob. When true, the validator
	// returns a 422 on any extra field on the inbound body that is
	// not declared in the schema. Wired through to jsonschema/v6
	// at compile time (additionalProperties=false with a closed
	// set of properties is the schema-side knob; this flag exists
	// for ops who want a hard reject without rewriting the schema).
	RejectUnknownFields bool

	// Mode is the per-rule validate_mode (issue #975 #3 / Mega-
	// Foundation #979-a). The validator itself is mode-agnostic:
	// it always returns the same Result regardless of Mode. The
	// gateway-side handler reads Mode to decide whether to 422
	// (mode='block'), pass-through (mode='observe'), or pass-
	// through and stamp a warning header (mode='warn'). Default
	// empty string is treated as 'block' by the handler to match
	// the schema default.
	Mode string
}

// In carries the inbound request slice that the validator needs. It
// is intentionally minimal: only the bytes that have already been
// buffered (validate runs AFTER the body read cap, never on a
// streaming body).
type In struct {
	// Body is the full inbound request body, buffered by the
	// applier. nil/empty for GET-style requests where the rule
	// has no ContentTypes set; the validator returns ok in that
	// case (no body to fail).
	Body []byte

	// ContentType is r.Header.Get("Content-Type") at the time
	// of the validate call. Used by the validator to gate on
	// the rule's content_types set BEFORE running the schema
	// (no point validating an HTML form post against a JSON
	// schema).
	ContentType string
}

// Result is the per-call outcome. On a successful validation, ok is
// true and SchemaDigest is populated (for audit/metric tags). On a
// failed validation, ok is false and FirstError is populated with the
// first failing field; the wire-shape translation (api.FieldError)
// happens at the handler applier boundary.
type Result struct {
	// OK is true when the body matches the schema, OR when the
	// rule's content_types didn't match (in which case the
	// handler returns 415, not 422, before consulting the
	// validator).
	OK bool

	// SchemaDigest is echoed back so the handler can tag
	// audit/metric events without re-hashing the rule.
	SchemaDigest [32]byte

	// FirstError is non-nil only when OK is false. FieldError
	// lives in sentinels.go; the handler lifts it into
	// api.FieldError verbatim.
	FirstError *FieldError
}

// Validator is the gateway-applicable contract for the kind=validate
// edge rule. The Handler accepts it via WithValidator; nil is the
// dev-mode default (matches pkg/edgejwks' WithEdgeJWKS nil-safe
// pattern). Concrete impl is the cmd-side adapter in
// cmd/gatewayd-internal/edge_validate.go.
type Validator interface {
	// Validate returns nil error and OK=true when the body
	// matches the schema. Returns an error wrapping
	// ErrValidationFailed and OK=false with FirstError set when
	// the body fails. Other sentinel errors propagate up:
	//
	//   - ErrSchemaExternalRef: compile-time defense fired at
	//     runtime — should not happen if apid-Validate was
	//     correct. The handler returns 502.
	//   - ErrSchemaEmpty / ErrSchemaTooLarge /
	//     ErrSchemaInvalid: indicates a broken stored schema;
	//     handler returns 500.
	Validate(ctx context.Context, req *In, rule *Rule) (*Result, error)
}
