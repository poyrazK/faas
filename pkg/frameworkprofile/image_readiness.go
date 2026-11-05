// adr:683, 20261024100000
package frameworkprofile

import "encoding/json"

// RequiresImageHealthcheck reads imaged's effective immutable-manifest receipt.
// A malformed receipt cannot turn a required command check into TCP readiness.
// Missing receipts retain compatibility with previously assembled deployments.
func RequiresImageHealthcheck(profile []byte) bool {
	var fields map[string]json.RawMessage
	if len(profile) == 0 {
		return false
	}
	if err := json.Unmarshal(profile, &fields); err != nil {
		return true
	}
	raw, present := fields["image_healthcheck_required"]
	if !present {
		return false
	}
	var version string
	if json.Unmarshal(fields["version"], &version) != nil || version != Version {
		return true
	}
	var required *bool
	if json.Unmarshal(raw, &required) != nil || required == nil {
		return true
	}
	return *required
}

// RequiresSourceImageHealthcheck applies the receipt to a source-built
// deployment. imaged records it for every build whose image declares a
// command HEALTHCHECK, from the Dockerfile or a Compose override
// (ADR-20261019195000, ADR-20261024100000). Unlike an image deployment's, a
// source profile that is missing or malformed never requires a check, so
// builds assembled before the receipt existed keep their readiness.
func RequiresSourceImageHealthcheck(profile []byte) bool {
	var fields map[string]json.RawMessage
	if len(profile) == 0 || json.Unmarshal(profile, &fields) != nil {
		return false
	}
	return RequiresImageHealthcheck(profile)
}
