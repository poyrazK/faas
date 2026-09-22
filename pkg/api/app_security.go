package api

// AppSecurityPolicy controls whether an app's security posture is advisory or
// enforced before a deployment is accepted.
type AppSecurityPolicy string

const (
	AppSecurityPolicyOff     AppSecurityPolicy = "off"
	AppSecurityPolicyWarn    AppSecurityPolicy = "warn"
	AppSecurityPolicyEnforce AppSecurityPolicy = "enforce"
)

// Valid reports whether p is one of the supported app security policies.
func (p AppSecurityPolicy) Valid() bool {
	return p == AppSecurityPolicyOff || p == AppSecurityPolicyWarn || p == AppSecurityPolicyEnforce
}

// RequiresSignedImage reports whether the policy's supply-chain gate requires
// an OCI image to carry a trusted signature. Enforce mode treats the signature
// as part of the deployment's provenance chain; the legacy require_signed flag
// remains available for apps that want signature enforcement without the full
// security posture policy.
func (p AppSecurityPolicy) RequiresSignedImage() bool {
	return p == AppSecurityPolicyEnforce
}
