package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// BuildRegistryMirrorsKey is the secrets.env entry that tells a builder VM
// which private registries it pulls through the control plane
// (ADR-20261110210000 slice 3j-3). vmmd writes it in place of
// BuildRegistryAuthKey when it serves the build a registry mirror: the
// guest then holds no registry password. guest-init reads it before it
// starts BuildKit, forwards 127.0.0.1:GuestPort to the host's vsock port
// VsockPort, and lists 127.0.0.1:GuestPort as the registry's mirror.
const BuildRegistryMirrorsKey = "GREGALE_BUILD_REGISTRY_MIRRORS"

const (
	// BuildRegistryMirrorMax bounds the private registries one build pulls
	// through mirrors; the rest pull anonymously.
	BuildRegistryMirrorMax = 8
	// BuildRegistryMirrorGuestPortBase is the first guest loopback port a
	// mirror forwarder listens on.
	BuildRegistryMirrorGuestPortBase = 15000
	// BuildRegistryMirrorVsockPortBase is the first host vsock port vmmd
	// serves a build's mirror on (Firecracker's <uds>_<port> endpoint).
	BuildRegistryMirrorVsockPortBase uint32 = 1100
)

// BuildRegistryMirror is one private registry's mirror for a build.
type BuildRegistryMirror struct {
	Registry  string `json:"registry"`
	GuestPort int    `json:"guest_port"`
	VsockPort uint32 `json:"vsock_port"`
}

var buildMirrorRegistry = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// PlanBuildRegistryMirrors assigns ports to a build's private registries:
// distinct valid hosts in sorted order, at most BuildRegistryMirrorMax, so
// vmmd and the guest agree on the mapping without negotiating.
func PlanBuildRegistryMirrors(registries []string) []BuildRegistryMirror {
	seen := map[string]bool{}
	hosts := make([]string, 0, len(registries))
	for _, r := range registries {
		if len(r) > 253 || !buildMirrorRegistry.MatchString(r) || seen[r] {
			continue
		}
		seen[r] = true
		hosts = append(hosts, r)
	}
	sort.Strings(hosts)
	if len(hosts) > BuildRegistryMirrorMax {
		hosts = hosts[:BuildRegistryMirrorMax]
	}
	out := make([]BuildRegistryMirror, len(hosts))
	for i, h := range hosts {
		out[i] = BuildRegistryMirror{Registry: h, GuestPort: BuildRegistryMirrorGuestPortBase + i, VsockPort: BuildRegistryMirrorVsockPortBase + uint32(i)}
	}
	return out
}

// ParseBuildRegistryMirrors reads and checks the BuildRegistryMirrorsKey
// value. The guest writes these hosts into buildkitd.toml, so each must be a
// plain registry host, and each port must be the one the plan assigns.
func ParseBuildRegistryMirrors(raw string) ([]BuildRegistryMirror, error) {
	var mirrors []BuildRegistryMirror
	if err := json.Unmarshal([]byte(raw), &mirrors); err != nil {
		return nil, fmt.Errorf("build registry mirrors: %w", err)
	}
	if err := ValidBuildRegistryMirrors(mirrors); err != nil {
		return nil, err
	}
	return mirrors, nil
}

// ValidBuildRegistryMirrors checks a mirror plan: plain, distinct registry
// hosts on the ports PlanBuildRegistryMirrors assigns.
func ValidBuildRegistryMirrors(mirrors []BuildRegistryMirror) error {
	if len(mirrors) > BuildRegistryMirrorMax {
		return fmt.Errorf("build registry mirrors: %d, at most %d", len(mirrors), BuildRegistryMirrorMax)
	}
	seen := map[string]bool{}
	for i, m := range mirrors {
		if len(m.Registry) > 253 || !buildMirrorRegistry.MatchString(m.Registry) || seen[m.Registry] {
			return fmt.Errorf("build registry mirror %d: registry %q is not a distinct registry host", i, m.Registry)
		}
		seen[m.Registry] = true
		if m.GuestPort != BuildRegistryMirrorGuestPortBase+i || m.VsockPort != BuildRegistryMirrorVsockPortBase+uint32(i) {
			return fmt.Errorf("build registry mirror %d: ports %d/%d are not the planned ones", i, m.GuestPort, m.VsockPort)
		}
	}
	return nil
}
