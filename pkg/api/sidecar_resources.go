package api

// SidecarScratchMBMin and SidecarScratchMBMax bound the customer-selectable
// writable /tmp tmpfs that guest-init mounts for a sidecar. Zero means the
// platform default derived from the sidecar's RAM profile (or 64 MiB when RAM
// is inherited).
const (
	SidecarScratchMBMin = 16
	SidecarScratchMBMax = 512
)

// SidecarDiskIOProfile is the closed set of per-workload disk-I/O scheduling
// policies. The values map to cgroup v2 io.weight inside the guest; the wire
// name deliberately stays policy-oriented so callers do not depend on kernel
// tuning details.
type SidecarDiskIOProfile string

const (
	SidecarDiskIOProfileLow      SidecarDiskIOProfile = "low"
	SidecarDiskIOProfileStandard SidecarDiskIOProfile = "standard"
	SidecarDiskIOProfileHigh     SidecarDiskIOProfile = "high"
)

// SidecarDiskIOProfileWeight returns the cgroup v2 io.weight for a profile.
// The empty value means inherit the guest's parent policy and therefore does
// not create an extra I/O policy leaf.
func SidecarDiskIOProfileWeight(profile string) (int, bool) {
	switch SidecarDiskIOProfile(profile) {
	case SidecarDiskIOProfileLow:
		return 50, true
	case SidecarDiskIOProfileStandard:
		return 100, true
	case SidecarDiskIOProfileHigh:
		return 200, true
	default:
		return 0, false
	}
}

// ValidSidecarDiskIOProfile reports whether profile is empty or one of the
// supported named policies.
func ValidSidecarDiskIOProfile(profile string) bool {
	if profile == "" {
		return true
	}
	_, ok := SidecarDiskIOProfileWeight(profile)
	return ok
}
