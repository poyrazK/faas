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
