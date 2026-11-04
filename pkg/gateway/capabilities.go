package gateway

// Capability flags (ADR-20261023115741) the request path decides for the
// account that owns the app. An off capability stops the behavior it adds;
// rules that restrict traffic (WAF, IP, JWT, limits, maintenance) keep
// enforcing, so a flag change never opens an app its owner locked down.
const (
	// CapabilitySessionAffinity gates cookie-pinned instance routing.
	CapabilitySessionAffinity = "session-affinity"
	// CapabilityWakePage gates the browser wake page; requests without it
	// wait for the wake like an API client.
	CapabilityWakePage = "wake-page"
	// CapabilityTrafficMirroring gates shadow traffic to a second deployment.
	CapabilityTrafficMirroring = "traffic-mirroring"
)

// WithCapabilities installs the per-account capability decision. nil (the
// default and every unit test) releases every capability.
func (h *Handler) WithCapabilities(allowed func(capability, accountID string) bool) *Handler {
	h.capabilityAllowed = allowed
	return h
}

func (h *Handler) capabilityOn(capability string, app App) bool {
	return h.capabilityAllowed == nil || h.capabilityAllowed(capability, app.AccountID)
}

// sessionAffinity reports whether app's requests stick to one instance.
func (h *Handler) sessionAffinity(app App) bool {
	return app.SessionAffinity && h.capabilityOn(CapabilitySessionAffinity, app)
}
