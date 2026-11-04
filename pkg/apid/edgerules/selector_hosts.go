package edgerules

import (
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SelectorHost is a hostname the public edge routes to one of an app's
// companions or public listeners instead of its main workload. Rules that
// match only the app's platform hostname do not apply to it; app-wide rules
// (match host "@app", ADR-1191) do.
type SelectorHost struct {
	Host string
	// Name is the companion name, or the listener key for a public port.
	Name       string
	PublicPort bool
}

// SelectorHosts lists the selector hostnames the gateway routes for an app.
//   - <label>--<companion>.<domain> for each companion that declares a port,
//     limited, as the gateway's route set is, to companions every
//     traffic-bearing live deployment declares with the same port.
//   - <label>--port-<key>.<domain> for each public TCP listener (ADR-176).
//
// platformHost is the app's platform hostname, <label>.<domain>.
func SelectorHosts(platformHost string, live []state.Deployment, ports []api.WorkloadPort) []SelectorHost {
	var out []SelectorHost
	for _, name := range routedCompanions(live) {
		if host := api.SelectorHostname(platformHost, name); host != "" {
			out = append(out, SelectorHost{Host: host, Name: name})
		}
	}
	seen := map[string]bool{}
	for _, port := range ports {
		key, ok := api.PublicPortSelectorKey(port)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		if host := api.SelectorHostname(platformHost, api.PublicPortSelectorPrefix+key); host != "" {
			out = append(out, SelectorHost{Host: host, Name: key, PublicPort: true})
		}
	}
	return out
}

type companionRoute struct {
	Name string          `json:"name"`
	Type api.SidecarType `json:"type"`
	Port int             `json:"port"`
}

// routedCompanions mirrors the gateway's companion route set: companions of
// type sidecar with a port, present with the same port in every live
// deployment that carries traffic.
func routedCompanions(live []state.Deployment) []string {
	var shared map[string]int
	for _, deployment := range live {
		if deployment.TrafficPercent <= 0 {
			continue
		}
		current := map[string]int{}
		var specs []companionRoute
		if len(deployment.Sidecars) > 0 && json.Unmarshal(deployment.Sidecars, &specs) != nil {
			return nil
		}
		for _, spec := range specs {
			if spec.Type == api.SidecarTypeSidecar && spec.Port > 0 && spec.Name != "" {
				current[spec.Name] = spec.Port
			}
		}
		if shared == nil {
			shared = current
			continue
		}
		for name, port := range shared {
			if current[name] != port {
				delete(shared, name)
			}
		}
	}
	names := make([]string, 0, len(shared))
	for name := range shared {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
