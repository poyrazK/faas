package reposcan

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// composeDeployKeyReasons explain the deploy: keys the scan does not apply.
var composeDeployKeyReasons = map[string]string{
	"restart_policy":  "the platform starts, parks and restarts services itself",
	"update_config":   "deployments roll out under the app's release policy",
	"rollback_config": "deployments roll out under the app's release policy",
	"placement":       "the scheduler places instances",
	"endpoint_mode":   "services reach each other through depends_on service bindings",
	"labels":          "container labels are not used",
}

// composeResources reads deploy.replicas and deploy.resources.limits, and
// the service-level scale, mem_limit and cpus where deploy does not set
// them (ADR-20261023120000). Nil means the service declares none.
func composeResources(s composeCandidate) (*api.ComposeResources, []string, error) {
	deploy, err := composeMapping(s.Deploy, "deploy")
	if err != nil {
		return nil, nil, err
	}
	resources, err := composeMapping(deploy["resources"], "deploy.resources")
	if err != nil {
		return nil, nil, err
	}
	limits, err := composeMapping(resources["limits"], "deploy.resources.limits")
	if err != nil {
		return nil, nil, err
	}
	warnings := composeDeployWarnings(deploy, resources, limits)
	out := &api.ComposeResources{}

	replicas, replicasKey := deploy["replicas"], "deploy.replicas"
	if replicas == nil {
		replicas, replicasKey = s.Scale, "scale"
	}
	if replicas != nil {
		n, err := composeCount(replicas)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", replicasKey, err)
		}
		if n == 0 {
			warnings = append(warnings, replicasKey+" 0 ignored: instances start on demand; remove the service to stop deploying it")
		} else {
			out.Replicas = &n
		}
	}

	memory, memoryKey := limits["memory"], "deploy.resources.limits.memory"
	if memory == nil {
		memory, memoryKey = s.MemLimit, "mem_limit"
	}
	if memory != nil {
		mb, err := composeMemoryMB(memory)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", memoryKey, err)
		}
		out.MemoryMB = mb
	}

	cpus, cpusKey := limits["cpus"], "deploy.resources.limits.cpus"
	if cpus == nil {
		cpus, cpusKey = s.CPUs, "cpus"
	}
	if cpus != nil {
		millicores, warning, err := composeCPUMillicores(cpus)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", cpusKey, err)
		}
		if warning != "" {
			warnings = append(warnings, cpusKey+" "+warning)
		}
		out.CPUMillicores = millicores
	}
	if out.IsZero() {
		return nil, warnings, nil
	}
	return out, warnings, nil
}

func composeMapping(raw any, key string) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping", key)
	}
	return mapping, nil
}

func composeDeployWarnings(deploy, resources, limits map[string]any) []string {
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(deploy)) {
		switch {
		case key == "replicas" || key == "resources" || strings.HasPrefix(key, "x-"):
		case key == "mode":
			if mode := fmt.Sprint(deploy[key]); mode != "replicated" {
				warnings = append(warnings, fmt.Sprintf("deploy.mode %s ignored: services run as replicated apps", mode))
			}
		case composeDeployKeyReasons[key] != "":
			warnings = append(warnings, fmt.Sprintf("deploy.%s ignored: %s", key, composeDeployKeyReasons[key]))
		default:
			warnings = append(warnings, fmt.Sprintf("deploy.%s ignored: not supported by Gregale", key))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(resources)) {
		switch {
		case key == "limits" || strings.HasPrefix(key, "x-"):
		case key == "reservations":
			warnings = append(warnings, "deploy.resources.reservations ignored: an app is given the memory and CPU it is limited to")
		default:
			warnings = append(warnings, fmt.Sprintf("deploy.resources.%s ignored: not supported by Gregale", key))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(limits)) {
		if key != "memory" && key != "cpus" && !strings.HasPrefix(key, "x-") {
			warnings = append(warnings, fmt.Sprintf("deploy.resources.limits.%s ignored: only memory and cpus are applied", key))
		}
	}
	return warnings
}

// composeCount reads a non-negative whole number, as YAML decodes it or as
// interpolation leaves it.
func composeCount(raw any) (int, error) {
	switch typed := raw.(type) {
	case int:
		if typed >= 0 {
			return typed, nil
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && n >= 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%v must be a whole number", raw)
}

// Compose sizes are bytes with an optional k, m, g or t suffix in powers of
// 1024; b, ib and iB endings are accepted, as Docker accepts them.
var composeByteSize = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([kmgt]?)(?:i?b)?$`)

// composeMemoryMB reads a Compose size and rounds it up to whole MiB.
func composeMemoryMB(raw any) (int, error) {
	var bytes float64
	switch typed := raw.(type) {
	case int:
		bytes = float64(typed)
	case string:
		match := composeByteSize.FindStringSubmatch(strings.ToLower(strings.TrimSpace(typed)))
		if match == nil {
			return 0, fmt.Errorf("%q must be a size such as 512m or 1g", typed)
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return 0, fmt.Errorf("%q must be a size such as 512m or 1g", typed)
		}
		bytes = value
		if match[2] != "" {
			bytes *= math.Pow(1024, float64(strings.Index("kmgt", match[2])+1))
		}
	default:
		return 0, fmt.Errorf("%v must be a size such as 512m or 1g", raw)
	}
	mb := math.Ceil(bytes / (1 << 20))
	switch {
	case mb < 1:
		return 0, fmt.Errorf("%v must be more than zero", raw)
	case mb > math.MaxInt32:
		return 0, fmt.Errorf("%v is too large", raw)
	}
	return int(mb), nil
}

// composeCPUMillicores maps a CPU count onto the nearest app CPU share at or
// above it, at most one CPU. The warning, when not empty, says how it moved.
func composeCPUMillicores(raw any) (int, string, error) {
	var cpus float64
	switch typed := raw.(type) {
	case int:
		cpus = float64(typed)
	case float64:
		cpus = typed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, "", fmt.Errorf("%q must be a number of CPUs such as 0.5", typed)
		}
		cpus = parsed
	default:
		return 0, "", fmt.Errorf("%v must be a number of CPUs such as 0.5", raw)
	}
	if cpus <= 0 || math.IsNaN(cpus) || math.IsInf(cpus, 0) {
		return 0, "", fmt.Errorf("%v must be a positive number of CPUs", raw)
	}
	requested := int(math.Round(cpus * 1000))
	for _, share := range []int{250, 500, api.DefaultAppCPUMillicores} {
		if requested <= share {
			if requested == share {
				return share, "", nil
			}
			return share, fmt.Sprintf("%v rounded up to %s: an app's CPU share is 0.25, 0.5 or 1", raw, composeCPUs(share)), nil
		}
	}
	return api.DefaultAppCPUMillicores, fmt.Sprintf("%v lowered to 1: an app's CPU share is at most 1", raw), nil
}

func composeCPUs(millicores int) string {
	return strconv.FormatFloat(float64(millicores)/1000, 'f', -1, 64)
}
