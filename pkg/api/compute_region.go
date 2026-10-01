package api

import "strings"

// ValidComputeRegion accepts canonical operator-defined locality identifiers.
// The empty value preserves unconstrained placement for existing applications.
func ValidComputeRegion(region string) bool {
	if region == "" {
		return true
	}
	if len(region) > 63 || strings.HasPrefix(region, "-") || strings.HasSuffix(region, "-") {
		return false
	}
	for _, c := range region {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
