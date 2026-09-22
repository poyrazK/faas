package api

// PreviewServiceCallsPolicy controls whether a production app accepts
// internal service calls from preview apps. Existing apps default to allow.
type PreviewServiceCallsPolicy string

const (
	PreviewServiceCallsAllow PreviewServiceCallsPolicy = "allow"
	PreviewServiceCallsDeny  PreviewServiceCallsPolicy = "deny"
)

// Effective fails closed for unknown stored values so an older gateway
// cannot widen a policy written by a newer control plane.
func (p PreviewServiceCallsPolicy) Effective() PreviewServiceCallsPolicy {
	switch p {
	case "", PreviewServiceCallsAllow:
		return PreviewServiceCallsAllow
	default:
		return PreviewServiceCallsDeny
	}
}
