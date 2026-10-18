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
// and what to use instead. Any other key gets a generic warning. extends is
// rejected because the effective service definition cannot be computed.
var composeAppliedServiceKeys = map[string]bool{
	"build": true, "command": true, "depends_on": true, "entrypoint": true, "env_file": true,
	"environment": true, "expose": true, "healthcheck": true, "image": true, "ports": true,
	"profiles": true, "stop_grace_period": true, "stop_signal": true, "user": true, "working_dir": true,
}

var composeHarmlessServiceKeys = map[string]bool{
	"container_name": true, "init": true, "stdin_open": true, "tty": true,
}

const (
	composeVolumesReason   = "persistent volumes and bind mounts are not supported; each start gets a fresh filesystem (docs/container-qualification.md)"
	composeNetworkReason   = "services reach each other through depends_on service bindings, not Compose networks"
	composeResourcesReason = "CPU, memory and replicas come from the plan and app scaling settings"
	composeKernelReason    = "each service runs in its own microVM; kernel, device and namespace controls are not configurable"
	composeGuestNetReason  = "guest networking and DNS are managed by the platform"
)

var composeServiceKeyReasons = map[string]string{
	"volumes":         composeVolumesReason,
	"tmpfs":           "tmpfs mounts are not supported; write scratch data under /tmp",
	"deploy":          composeResourcesReason,
	"scale":           composeResourcesReason,
	"cpus":            composeResourcesReason,
	"cpu_shares":      composeResourcesReason,
	"cpu_quota":       composeResourcesReason,
	"cpuset":          composeResourcesReason,
	"mem_limit":       composeResourcesReason,
	"mem_reservation": composeResourcesReason,
	"memswap_limit":   composeResourcesReason,
	"restart":         "the platform starts, parks and restarts services itself",
	"secrets":         "Compose secrets are not mounted; set them with `gregale secrets set`",
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

// composeBuildKeyReasons covers build: keys beyond context and dockerfile.
var composeBuildKeyReasons = map[string]string{
	"args":              "build args are not passed to the builder; give each ARG a default in the Dockerfile",
	"target":            "the builder builds the Dockerfile's final stage; build target is not selectable",
	"secrets":           "build secrets are not available to the builder",
	"ssh":               "SSH forwarding is not available to the builder",
	"dockerfile_inline": "inline Dockerfiles are not supported; commit the Dockerfile and point build.dockerfile at it",
}

// composeServiceKeyWarnings reports the keys of one service that the scan
// does not apply. raw is the service's decoded mapping.
func composeServiceKeyWarnings(raw map[string]any) ([]string, error) {
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		switch {
		case key == "extends":
			return nil, fmt.Errorf("extends is not supported; inline the extended service's settings")
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
			case key == "context" || key == "dockerfile" || strings.HasPrefix(key, "x-"):
				continue
			case composeBuildKeyReasons[key] != "":
				warnings = append(warnings, fmt.Sprintf("build.%s ignored: %s", key, composeBuildKeyReasons[key]))
			default:
				warnings = append(warnings, fmt.Sprintf("build.%s ignored: the builder uses only build.context and build.dockerfile", key))
			}
		}
	}
	return warnings, nil
}

// rejectComposeInclude refuses top-level include: the included services
// would otherwise be missing from the deployment without any signal.
func rejectComposeInclude(doc map[string]any, source string) error {
	if _, present := doc["include"]; present {
		return fmt.Errorf("reposcan: %s uses include, which is not supported; merge the included services into one Compose file", source)
	}
	return nil
}
