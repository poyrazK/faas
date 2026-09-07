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
	endpoints := make(map[string]string, 3*(1+len(roster.Sidecars)))
	ports := make(map[int]string, 1+len(roster.Sidecars))

	mainPort := roster.Main.Port
	if mainPort == 0 {
		mainPort = mainManifest.EffectivePort()
	}
	if err := addWorkloadEndpoint(endpoints, ports, "main", mainPort); err != nil {
		return nil, err
	}
	seenNames := map[string]struct{}{"main": {}}
	for _, sidecar := range roster.Sidecars {
		if _, exists := seenNames[sidecar.Name]; exists {
			return nil, fmt.Errorf("workload network: duplicate workload name %q", sidecar.Name)
		}
		seenNames[sidecar.Name] = struct{}{}
		if sidecar.Port == 0 {
			continue
		}
		if err := addWorkloadEndpoint(endpoints, ports, sidecar.Name, sidecar.Port); err != nil {
			return nil, err
		}
	}
	return endpoints, nil
}

func singleWorkloadEndpointEnv(port int) map[string]string {
	endpoints := make(map[string]string, 3)
	_ = addWorkloadEndpoint(endpoints, make(map[int]string, 1), "main", port)
	return endpoints
}

func addWorkloadEndpoint(endpoints map[string]string, ports map[int]string, name string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("workload network: workload %q port %d is outside 1..65535", name, port)
	}
	if previous, exists := ports[port]; exists {
		return fmt.Errorf("workload network: workloads %q and %q both claim port %d", previous, name, port)
	}
	ports[port] = name
	prefix := workloadEndpointPrefix(name)
	endpoints[prefix+"_HOST"] = workloadEndpointHost
	endpoints[prefix+"_PORT"] = strconv.Itoa(port)
	endpoints[prefix+"_ADDR"] = workloadEndpointHost + ":" + strconv.Itoa(port)
	return nil
}

func workloadEndpointPrefix(name string) string {
	return "FAAS_WORKLOAD_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
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
