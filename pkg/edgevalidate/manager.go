package edgevalidate

import (
	"context"
	"fmt"
)

// Manager is the production Validator impl: it consults the Cache
// keyed by Rule.SchemaDigest, recompiles the policy's source on miss, and forwards to the
// cached *CompiledSchema. The cmd-side adapter
// (cmd/gatewayd-internal/edge_validate.go) wires one of these per
// daemon process.
//
// Manager is safe for concurrent use across request goroutines; the
// underlying Cache takes its own lock and the CompiledSchema
// validation is documented as the only safe concurrent mode by
// jsonschema/v6.
type Manager struct {
	cache Cache
}

// NewManager returns a Manager wrapping cache. A nil cache creates
// a fresh one (handy for tests).
func NewManager(cache Cache) *Manager {
	if cache == nil {
		cache = NewCache()
	}
	return &Manager{cache: cache}
}

// Cache exposes the underlying cache so the cmd-side can call Reset
// from the db.NotifyEdgeRuleChanged listener. Tests can introspect
// the cache size via this.
func (m *Manager) Cache() Cache { return m.cache }

// CompileSchema is a thin pass-through to Compile + Register. The
// cmd-side load path calls this once per edge rule at startup (or
// after a Reset) so the hot path never sees a cold cache. On
// compile failure, the error is returned and the rule is dropped —
// the cmd-side logs it as a 500 (broken schema is a deploy bug,
// not a customer request failure).
func (m *Manager) CompileSchema(schema []byte, rejectUnknownFields bool) (*CompiledSchema, error) {
	if compiled, ok := m.cache.Get(schemaDigest(schema, rejectUnknownFields)); ok {
		return compiled, nil
	}
	compiled, err := Compile(schema, rejectUnknownFields)
	if err != nil {
		return nil, err
	}
	m.cache.Register(compiled.Digest, compiled)
	return compiled, nil
}

// Validate implements the Validator interface. The expected flow:
//
//  1. Cache hit on Rule.SchemaDigest → validate against the
//     cached *CompiledSchema.
//  2. Cache miss after eviction or Reset → verify and recompile the immutable
//     source retained by the loaded policy. The compiled pointer stays live
//     for this call even if another load immediately evicts its LRU entry.
//
// The body is read once and passed through; we don't re-buffer.
func (m *Manager) Validate(ctx context.Context, req *In, rule *Rule) (*Result, error) {
	if req == nil || rule == nil {
		return nil, fmt.Errorf("%w: nil req or rule", ErrSchemaInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	compiled, ok := m.cache.Get(rule.SchemaDigest)
	if !ok {
		if len(rule.Schema) == 0 || schemaDigest(rule.Schema, rule.RejectUnknownFields) != rule.SchemaDigest {
			return nil, fmt.Errorf("%w: schema digest %x has no matching source", ErrSchemaInvalid, rule.SchemaDigest[:8])
		}
		var err error
		compiled, err = m.CompileSchema(rule.Schema, rule.RejectUnknownFields)
		if err != nil {
			return nil, err
		}
	}

	fe, err := compiled.Validate(req.Body)
	if err != nil {
		return nil, err
	}
	if fe != nil {
		return &Result{
			OK:           false,
			SchemaDigest: compiled.Digest,
			FirstError:   fe,
		}, nil
	}
	return &Result{OK: true, SchemaDigest: compiled.Digest}, nil
}
