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

// composeBuildKeyReasons covers build: keys beyond context, dockerfile, args
// and target.
var composeBuildKeyReasons = map[string]string{
	"secrets":           "build secrets are not available to the builder",
	"ssh":               "SSH forwarding is not available to the builder",
	"dockerfile_inline": "inline Dockerfiles are not supported; commit the Dockerfile and point build.dockerfile at it",
}

// composeServiceKeyWarnings reports the keys of one service that the scan
// does not apply. raw is the service's decoded mapping; services holds every
// decoded service so an extends warning can name the inherited keys.
func composeServiceKeyWarnings(raw, services map[string]any) []string {
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		switch {
		case key == "extends":
			warnings = append(warnings, composeExtendsWarning(raw, services))
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
			case key == "context" || key == "dockerfile" || key == "args" || key == "target" || strings.HasPrefix(key, "x-"):
				continue
			case composeBuildKeyReasons[key] != "":
				warnings = append(warnings, fmt.Sprintf("build.%s ignored: %s", key, composeBuildKeyReasons[key]))
			default:
				warnings = append(warnings, fmt.Sprintf("build.%s ignored: the builder uses only build.context, build.dockerfile, build.args and build.target", key))
			}
		}
	}
	return warnings
}

// composeExtendsWarning names what extends would have inherited. For a base
// service in the same file it lists the base's keys this service does not set
// itself; for another file it names the file and service.
func composeExtendsWarning(raw, services map[string]any) string {
	base, file := "", ""
	switch extends := raw["extends"].(type) {
	case string:
		base = extends
	case map[string]any:
		base, _ = extends["service"].(string)
		file, _ = extends["file"].(string)
	}
	const fix = "; inline them into this service"
	if file != "" {
		return fmt.Sprintf("WARNING extends not applied: settings inherited from service %q in %s were not loaded%s", base, file, fix)
	}
	parent, ok := services[base].(map[string]any)
	if !ok {
		return fmt.Sprintf("WARNING extends not applied: settings inherited from service %q were not loaded%s", base, fix)
	}
	var inherited []string
	for _, key := range slices.Sorted(maps.Keys(parent)) {
		if _, own := raw[key]; !own && key != "extends" {
			inherited = append(inherited, key)
		}
	}
	if len(inherited) == 0 {
		return fmt.Sprintf("WARNING extends not applied: service %q contributes no keys this service lacks; remove extends", base)
	}
	return fmt.Sprintf("WARNING extends not applied: %s inherited from service %q were not loaded%s", strings.Join(inherited, ", "), base, fix)
}

// composeIncludeWarning reports a top-level include: the included files'
// services are not part of the deployment.
func composeIncludeWarning(doc map[string]any, source string) string {
	raw, present := doc["include"]
	if !present {
		return ""
	}
	var files []string
	entries, _ := raw.([]any)
	for _, entry := range entries {
		switch typed := entry.(type) {
		case string:
			files = append(files, typed)
		case map[string]any:
			switch paths := typed["path"].(type) {
			case string:
				files = append(files, paths)
			case []any:
				for _, item := range paths {
					if path, ok := item.(string); ok {
						files = append(files, path)
					}
				}
			}
		}
	}
	if len(files) == 0 {
		return fmt.Sprintf("reposcan: %s: WARNING include not applied: the included services were not loaded; merge them into %s", source, source)
	}
	return fmt.Sprintf("reposcan: %s: WARNING include not applied: services in %s were not loaded; merge them into %s", source, strings.Join(files, ", "), source)
}
