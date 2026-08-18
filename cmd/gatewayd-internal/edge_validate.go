// Edge-rule validate wiring for gatewayd-internal (PR-B). Constructs
// the production pkg/edgevalidate.Manager (sha256-keyed LRU + Draft
// 2020-12 compile + JSON-Schema validate) and adapts it to the
// narrow pkg/gateway.Validator interface that
// handler.go::applyEdgeRuleValidate consumes. The adapter is what
// keeps the dep direction one-way: pkg/gateway never imports
// pkg/edgevalidate; this file is the only place that does, on the
// cmd-side.
//
// Two distinct surfaces are wired through one struct:
//
//  1. validateCompiler (cmd-side loader) — pre-compiles every
//     kind=validate rule at loadHost time so the hot path never
//     sees a cold cache. Returns the SHA-256 digest for stashing
//     on EdgeRuleValidateResolved.
//
//  2. gateway.Validator (cmd-side applier) — buffers r.Body,
//     consults the cached *CompiledSchema, returns FieldError on
//     mismatch. The handler lifts this into api.FieldError on the
//     422 problem+json.
//
// Both surfaces share the same Manager / Cache so a Reset() on
// one wipes both.
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// edgeValidateAdapter is the cmd-side seam for kind=validate. It
// holds the pkg/edgevalidate.Manager (a Cache + a Validator
// projection) and adapts them to the two interfaces
// gatewaydEdgeRules and the gateway.Handler consult.
//
// Compile-time guarantees:
//
//   - edgeValidateAdapter satisfies validateCompiler (declared in
//     cmd/gatewayd-internal/edge_rules.go) — used by the loader.
//   - edgeValidateAdapter satisfies gateway.Validator (declared in
//     pkg/gateway/handler.go) — used by the applier.
type edgeValidateAdapter struct {
	mgr *edgevalidate.Manager
	log *slog.Logger
}

// newEdgeValidateAdapter constructs the production validate adapter
// with a fresh Manager (1024-entry LRU). log is currently
// best-effort: the loader surfaces compile errors via the
// PathGlobError slice, not via this logger; a future hook
// (e.g. "validate_failed" telemetry emitted from the adapter
// rather than the applier) can use it without changing the
// constructor signature. The cache uses the default capacity
// (pkg/edgevalidate.MaxCompiledSchemas = 1024) — large enough for
// 1024 distinct customer schemas per daemon, with LRU eviction
// beyond that.
func newEdgeValidateAdapter(log *slog.Logger) *edgeValidateAdapter {
	return &edgeValidateAdapter{
		mgr: edgevalidate.NewManager(nil),
		log: log,
	}
}

// CompileSchema implements validateCompiler. Called by the cmd-side
// loader for each kind=validate rule at loadHost time. Returns the
// SHA-256 digest the matcher stashes on EdgeRuleValidateResolved
// so the hot path can look up the compiled *CompiledSchema.
//
// A compile error is surfaced as-is so the loader can route it
// into the PathGlobError slice and drop the rule from the
// compiled slice (the customer sees the existing pass-through
// path; an operator sees the WARN slog).
func (a *edgeValidateAdapter) CompileSchema(schema []byte, rejectUnknownFields bool) ([32]byte, error) {
	compiled, err := a.mgr.CompileSchema(schema, rejectUnknownFields)
	if err != nil {
		return [32]byte{}, err
	}
	return compiled.Digest, nil
}

// Validate implements gateway.Validator. Called from
// handler.go::applyEdgeRuleValidate after the body has been
// buffered and r.Body restored. Returns the gateway-applicable
// shape: a *EdgeValidateResult with OK=false + FirstError set on
// mismatch, OK=true on success, or a typed error for the
// alarm-worthy paths (ErrValidateSchemaExternalRef → 502,
// ErrValidateSchemaInvalid → 500).
//
// ctx is honored via the Manager's first-line ctx.Err() check.
//
// Errors are wrapped: pkg/edgevalidate.Err* are mapped to the
// parallel pkg/gateway.ErrValidate* sentinels so the handler's
// errors.Is matches without pkg/gateway importing pkg/edgevalidate.
func (a *edgeValidateAdapter) Validate(ctx context.Context, req *gateway.EdgeValidateIn, rule *gateway.EdgeRuleValidateResolved) (*gateway.EdgeValidateResult, error) {
	if rule == nil {
		return nil, gateway.ErrValidateSchemaInvalid
	}
	res, err := a.mgr.Validate(ctx, &edgevalidate.In{
		Body:        req.Body,
		ContentType: req.ContentType,
	}, &edgevalidate.Rule{
		SchemaDigest:        rule.SchemaDigest,
		ApplyWhileStreaming: rule.ApplyWhileStreaming,
		RejectUnknownFields: rule.RejectUnknownFields,
		// Mode is plumbed for the metric tag path — the validator
		// itself is mode-agnostic and the handler reads Mode via
		// the resolved rule directly. Passing it through here
		// keeps the audit + metric sites in lock-step with the
		// load-time value.
		Mode: rule.ValidateMode,
	})
	if err != nil {
		return nil, translateValidateErr(err)
	}
	if !res.OK {
		var fe *gateway.EdgeValidateFieldError
		if res.FirstError != nil {
			fe = &gateway.EdgeValidateFieldError{
				Field:    res.FirstError.Field,
				Expected: res.FirstError.Expected,
				Got:      res.FirstError.Got,
			}
		}
		return &gateway.EdgeValidateResult{
			OK:           false,
			SchemaDigest: res.SchemaDigest,
			FirstError:   fe,
		}, nil
	}
	return &gateway.EdgeValidateResult{
		OK:           true,
		SchemaDigest: res.SchemaDigest,
	}, nil
}

// Reset drops every compiled-schema entry. Mirrors
// pkg/gateway.EdgeRuleCache.Reset() wholesale-invalidation
// semantics — the edge_rule_changed pg_notify channel triggers
// both. Called from cmd/gatewayd-internal/backend.go (same as the
// route cache + the JWKS cache).
func (a *edgeValidateAdapter) Reset() {
	a.mgr.Cache().Reset()
}

// Compile-time checks (mirror edgeJWKSAdapter):
//
//   - validateCompiler is declared in edge_rules.go and asserts
//     this struct's CompileSchema method matches.
//   - gateway.Validator is declared in pkg/gateway/handler.go
//     and asserts this struct's Validate method matches.
//
// Both checks fail to compile if either interface widens and this
// file forgets to add the new method.
var (
	_ validateCompiler  = (*edgeValidateAdapter)(nil)
	_ gateway.Validator = (*edgeValidateAdapter)(nil)
)

// translateValidateErr maps the pkg/edgevalidate sentinel set to
// the parallel pkg/gateway.ErrValidate* sentinels the handler
// consults via errors.Is. The pkg/edgevalidate package is the
// canonical source of truth for the underlying library errors;
// pkg/gateway owns the applier-side vocabulary.
//
// Pass-through: any non-sentinel error is returned as-is so the
// handler's "default" branch (500 with err.Error()) keeps the
// original prose.
func translateValidateErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, edgevalidate.ErrSchemaExternalRef):
		return errors.Join(gateway.ErrValidateSchemaExternalRef, err)
	case errors.Is(err, edgevalidate.ErrSchemaInvalid):
		return errors.Join(gateway.ErrValidateSchemaInvalid, err)
	case errors.Is(err, edgevalidate.ErrSchemaEmpty):
		return errors.Join(gateway.ErrValidateSchemaEmpty, err)
	case errors.Is(err, edgevalidate.ErrSchemaTooLarge):
		return errors.Join(gateway.ErrValidateSchemaTooLarge, err)
	}
	return err
}
