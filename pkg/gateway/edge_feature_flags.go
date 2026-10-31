package gateway

import "github.com/onebox-faas/faas/pkg/platformflags"

// WithEdgeFeatureFlags gates challenge rules, WAF warn and block, and the
// penalty box on their platform flags (ADR-20261023113721): for an account
// the flag leaves out, challenge rules are inert, warn and block rules are
// inspected as observe, and penalties neither box nor refuse anyone.
// Without it every edge feature is enforced; gatewayd-internal always wires
// the set its runtime-config watcher keeps current.
func (h *Handler) WithEdgeFeatureFlags(flags *platformflags.Set) *Handler {
	h.edgeFeatureFlags = flags
	return h
}

// edgeFeatureReleased reports whether app's account may use the edge
// feature a platform flag releases.
func (h *Handler) edgeFeatureReleased(flag string, app App) bool {
	if h.edgeFeatureFlags == nil {
		return true
	}
	return h.edgeFeatureFlags.Enabled(flag, platformflags.Subject{AccountID: app.AccountID, Plan: string(app.Plan)})
}
