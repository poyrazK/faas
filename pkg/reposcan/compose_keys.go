package reposcan

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Compose keys the scan cannot honour are reported instead of dropped
// silently (ADR-1337). composeAppliedServiceKeys are read by detectCompose;
// composeHarmlessServiceKeys have no meaning for a microVM service and are
// ignored quietly; composeServiceKeyReasons explain why a key has no effect
// and what to use instead. Any other key gets a generic warning. extends and
// top-level include are not resolved; their warnings name exactly what was
// not loaded so the user can inline it.
var composeAppliedServiceKeys = map[string]bool{
	"build": true, "command": true, "cpus": true, "depends_on": true, "deploy": true, "entrypoint": true,
	"env_file": true, "environment": true, "expose": true, "healthcheck": true, "image": true,
	"mem_limit": true, "ports": true, "profiles": true, "scale": true, "secrets": true,
	"stop_grace_period": true, "stop_signal": true, "user": true, "working_dir": true,
}

var composeHarmlessServiceKeys = map[string]bool{
	"container_name": true, "init": true, "stdin_open": true, "tty": true,
}

const (
	composeVolumesReason   = "persistent volumes and bind mounts are not supported; each start gets a fresh filesystem (docs/container-qualification.md)"
	composeNetworkReason   = "services reach each other through depends_on service bindings, not Compose networks"
	composeResourcesReason = "only deploy.resources.limits (or mem_limit and cpus) and deploy.replicas set an app's memory, CPU and replicas"
	composeKernelReason    = "each service runs in its own microVM; kernel, device and namespace controls are not configurable"
	composeGuestNetReason  = "guest networking and DNS are managed by the platform"
)

var composeServiceKeyReasons = map[string]string{
	"volumes":         composeVolumesReason,
	"tmpfs":           "tmpfs mounts are not supported; write scratch data under /tmp",
	"cpu_shares":      composeResourcesReason,
	"cpu_quota":       composeResourcesReason,
	"cpuset":          composeResourcesReason,
	"mem_reservation": composeResourcesReason,
	"memswap_limit":   composeResourcesReason,
	"restart":         "the platform starts, parks and restarts services itself",
	"configs":         "Compose configs are not mounted; build the file into the image or use environment variables",
	"networks":        composeNetworkReason,
	"network_mode":    composeNetworkReason,
	"links":           composeNetworkReason,
	"external_links":  composeNetworkReason,
	"labels":          "container labels are not used",
	"logging":         "the platform collects stdout and stderr; logging drivers are not used",
	"cap_add":         composeKernelReason,
	"cap_drop":        composeKernelReason,
	"privileged":      composeKernelReason,
	"security_opt":    composeKernelReason,
	"devices":         composeKernelReason,
	"sysctls":         composeKernelReason,
	"ulimits":         composeKernelReason,
	"pid":             composeKernelReason,
	"ipc":             composeKernelReason,
	"userns_mode":     composeKernelReason,
	"cgroup_parent":   composeKernelReason,
	"shm_size":        composeKernelReason,
	"dns":             composeGuestNetReason,
	"dns_search":      composeGuestNetReason,
	"dns_opt":         composeGuestNetReason,
	"extra_hosts":     composeGuestNetReason,
	"hostname":        composeGuestNetReason,
	"domainname":      composeGuestNetReason,
	"mac_address":     composeGuestNetReason,
	"pull_policy":     "images are pulled when a deployment starts; see `gregale deploy --image`",
	"platform":        "services run on linux/amd64",
}

// composeBuildKeyReasons covers build: keys beyond context, dockerfile, args,
// target and secrets.
var composeBuildKeyReasons = map[string]string{
	"ssh":               "SSH forwarding is not available to the builder",
	"dockerfile_inline": "inline Dockerfiles are not supported; commit the Dockerfile and point build.dockerfile at it",
}

// composeServiceKeyWarnings reports the keys of one service that the scan
// does not apply. raw is the service's decoded mapping after include and
// extends are resolved, so inherited keys are reported like the service's own.
func composeServiceKeyWarnings(raw map[string]any) []string {
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		switch {
		case composeAppliedServiceKeys[key], composeHarmlessServiceKeys[key], strings.HasPrefix(key, "x-"):
			continue
		case key == "platform" && fmt.Sprint(raw[key]) == "linux/amd64":
			continue
		case composeServiceKeyReasons[key] != "":
			warnings = append(warnings, fmt.Sprintf("%s ignored: %s", key, composeServiceKeyReasons[key]))
		default:
			warnings = append(warnings, fmt.Sprintf("%s ignored: not supported by Gregale", key))
		}
	}
	if build, ok := raw["build"].(map[string]any); ok {
		for _, key := range slices.Sorted(maps.Keys(build)) {
			switch {
			case key == "context" || key == "dockerfile" || key == "args" || key == "target" || key == "secrets" || strings.HasPrefix(key, "x-"):
				continue
			case composeBuildKeyReasons[key] != "":
				warnings = append(warnings, fmt.Sprintf("build.%s ignored: %s", key, composeBuildKeyReasons[key]))
			default:
				warnings = append(warnings, fmt.Sprintf("build.%s ignored: the builder uses only build.context, build.dockerfile, build.args, build.target and build.secrets", key))
			}
		}
	}
	return warnings
}
