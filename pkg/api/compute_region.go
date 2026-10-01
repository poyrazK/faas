package api

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	CodeComputeRegionUnknown           = "compute_region_unknown"
	CodeComputeRegionAdmissionPaused   = "compute_region_admission_paused"
	CodeComputeRegionUnavailable       = "compute_region_unavailable"
	CodeComputeRegionCapacityExhausted = "compute_region_capacity_exhausted"
	ComputeRegionDisplayNameMaxRunes   = 128
)

func ValidComputeRegionDisplayName(name string) bool {
	if name == "" || !utf8.ValidString(name) || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > ComputeRegionDisplayNameMaxRunes {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func computeRegionProblem(region, code, title, detail string, status int) *Problem {
	p := NewProblem(status, code, title, detail)
	p.ComputeRegion = region
	return p
}

func ErrComputeRegionUnknown(region string) *Problem {
	return computeRegionProblem(region, CodeComputeRegionUnknown, "Unknown compute region",
		fmt.Sprintf("Compute region %q is not registered; use gregale regions list to choose a region.", region), http.StatusUnprocessableEntity)
}

func ErrComputeRegionAdmissionPaused(region string) *Problem {
	return computeRegionProblem(region, CodeComputeRegionAdmissionPaused, "Compute region admission paused",
		fmt.Sprintf("Compute region %q is not accepting new apps. Existing apps retain their region and can continue to wake and recover.", region), http.StatusServiceUnavailable)
}

func ErrComputeRegionUnavailable(region string) *Problem {
	return computeRegionProblem(region, CodeComputeRegionUnavailable, "Compute region unavailable",
		fmt.Sprintf("Compute region %q has no eligible compute nodes. Retry when the region recovers; execution will stay within the selected region.", region), http.StatusServiceUnavailable)
}

func ErrComputeRegionCapacityExhausted(region string) *Problem {
	return computeRegionProblem(region, CodeComputeRegionCapacityExhausted, "Compute region at capacity",
		fmt.Sprintf("Compute region %q has eligible nodes, but none can admit this request. Retry later or reduce requested resources; execution will stay within the selected region.", region), http.StatusServiceUnavailable)
}

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
