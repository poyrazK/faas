//go:build linux

package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const workloadEndpointHost = "127.0.0.1"

// buildWorkloadEndpointEnv turns the workload roster into the endpoint
// contract visible to every process in the task network namespace. Workloads
// share a netns, so loopback is the stable address even when a VM is restored
// or a service replica moves to another compute node.
//
// The main workload always has an endpoint. Sidecars only get one when they
// declare a port; a sidecar with no listener remains a valid worker/init
// workload. Explicit port collisions are rejected because two processes in a
// shared netns cannot bind the same address.
func buildWorkloadEndpointEnv(roster workloadRoster, mainManifest api.AppManifest) (map[string]string, error) {
	endpoints := make(map[string]string, 4*(1+len(roster.Sidecars)))
	ports := make(map[string]string, 1+len(roster.Sidecars))

	mainPorts := append([]api.WorkloadPort(nil), roster.Main.Ports...)
	if len(mainPorts) == 0 && roster.Main.Port != 0 {
		mainPorts = []api.WorkloadPort{{Port: roster.Main.Port, Protocol: api.WorkloadPortTCP}}
	}
	if len(mainPorts) == 0 {
		mainPorts = []api.WorkloadPort{{Port: mainManifest.EffectivePort(), Protocol: api.WorkloadPortTCP}}
		for _, declared := range mainManifest.Ports {
			if declared.EffectiveProtocol() == api.WorkloadPortTCP && declared.Port == mainManifest.EffectivePort() {
				continue
			}
			mainPorts = append(mainPorts, declared)
		}
	}
	if err := addWorkloadEndpoints(endpoints, ports, "main", mainPorts); err != nil {
		return nil, err
	}
	seenNames := map[string]struct{}{"main": {}}
	for _, sidecar := range roster.Sidecars {
		if _, exists := seenNames[sidecar.Name]; exists {
			return nil, fmt.Errorf("workload network: duplicate workload name %q", sidecar.Name)
		}
		seenNames[sidecar.Name] = struct{}{}
		sidecarPorts := append([]api.WorkloadPort(nil), sidecar.Ports...)
		if len(sidecarPorts) == 0 && sidecar.Port != 0 {
			sidecarPorts = []api.WorkloadPort{{Port: sidecar.Port, Protocol: api.WorkloadPortTCP}}
		}
		if len(sidecarPorts) == 0 {
			continue
		}
		if err := addWorkloadEndpoints(endpoints, ports, sidecar.Name, sidecarPorts); err != nil {
			return nil, err
		}
	}
	return endpoints, nil
}

func singleWorkloadEndpointEnv(port int) map[string]string {
	endpoints := make(map[string]string, 4)
	_ = addWorkloadEndpoints(endpoints, make(map[string]string, 1), "main", []api.WorkloadPort{{Port: port, Protocol: api.WorkloadPortTCP}})
	return endpoints
}

func addWorkloadEndpoints(endpoints map[string]string, ports map[string]string, name string, workloadPorts []api.WorkloadPort) error {
	if err := api.ValidateWorkloadPorts(workloadPorts); err != nil {
		return fmt.Errorf("workload network: workload %q: %w", name, err)
	}
	prefix := workloadEndpointPrefix(name)
	for i, port := range workloadPorts {
		protocol := port.EffectiveProtocol()
		key := string(protocol) + "/" + strconv.Itoa(port.Port)
		if previous, exists := ports[key]; exists {
			return fmt.Errorf("workload network: workloads %q and %q both claim %s", previous, name, key)
		}
		ports[key] = name
		// The first endpoint keeps the legacy workload-wide variables so
		// existing images need no changes. Named and multi-port entries also
		// receive a stable, specific suffix for unambiguous discovery.
		if i == 0 {
			setWorkloadEndpoint(endpoints, prefix, port)
		}
		if len(workloadPorts) > 1 || port.Name != "" {
			suffix := port.Name
			if suffix == "" {
				suffix = string(protocol) + "-" + strconv.Itoa(port.Port)
			}
			setWorkloadEndpoint(endpoints, prefix+"_"+workloadEndpointPortSuffix(suffix), port)
		}
	}
	return nil
}

func setWorkloadEndpoint(endpoints map[string]string, prefix string, port api.WorkloadPort) {
	protocol := port.EffectiveProtocol()
	portString := strconv.Itoa(port.Port)
	endpoints[prefix+"_HOST"] = workloadEndpointHost
	endpoints[prefix+"_PORT"] = portString
	endpoints[prefix+"_ADDR"] = workloadEndpointHost + ":" + portString
	endpoints[prefix+"_PROTOCOL"] = string(protocol)
}

func workloadEndpointPrefix(name string) string {
	return "FAAS_WORKLOAD_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func workloadEndpointPortSuffix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// stampWorkloadEndpointEnv appends platform-owned endpoint variables after
// customer and image environment values. Appending preserves the existing
// guest-init precedence rule: platform routing metadata cannot be shadowed by
// a user-supplied variable with the same name.
func stampWorkloadEndpointEnv(env []string, endpointEnv map[string]string) []string {
	if len(endpointEnv) == 0 {
		return env
	}
	keys := make([]string, 0, len(endpointEnv))
	for key := range endpointEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+endpointEnv[key])
	}
	return env
}
