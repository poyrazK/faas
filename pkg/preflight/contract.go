package preflight

import (
	"io/fs"
	"regexp"
	"sort"
)

// maxContractFileBytes bounds each file the contract scanner reads, matching
// the budget frameworkprofile uses for source reads.
const maxContractFileBytes = 1 << 20

// dockerfileNames and composeNames are the only files the scanner opens. The
// check is static and cheap on purpose: it must stay affordable for anonymous
// callers.
var (
	dockerfileNames = []string{"Dockerfile"}
	composeNames    = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
)

// contractRule is one hard disqualifier from docs/container-compatibility.md.
// Every rule here means "this app cannot run", never "this app needs a tweak"
// — amber lives in the frameworkprofile warning table instead.
type contractRule struct {
	code    string
	files   []string
	pattern *regexp.Regexp
	title   string
	detail  string
	remedy  string
}

// Go's regexp is RE2 and has no negative lookahead, so the unsupported
// architectures are enumerated rather than expressed as "not amd64".
var contractRules = []contractRule{
	{
		code:    "durable_local_disk",
		files:   dockerfileNames,
		pattern: regexp.MustCompile(`(?im)^\s*VOLUME\s+\S`),
		title:   "Expects durable local disk",
		detail:  "The Dockerfile declares a VOLUME. The Gregale root filesystem is ephemeral and host mounts are not supported, so anything written there is lost when the instance is parked or replaced.",
		remedy:  "Move persistent state to object storage or a managed database, then remove the VOLUME.",
	},
	{
		code:    "unsupported_architecture",
		files:   dockerfileNames,
		pattern: regexp.MustCompile(`(?im)--platform=linux/(arm64|arm/v[0-9]+|arm|386|s390x|ppc64le|riscv64)`),
		title:   "Pinned to a non-amd64 architecture",
		detail:  "The Dockerfile pins a platform other than linux/amd64. Gregale runs Linux/amd64 microVMs only.",
		remedy:  "Build a linux/amd64 image, or remove the platform pin if the base image is multi-arch.",
	},
	{
		code:    "privileged_mode",
		files:   composeNames,
		pattern: regexp.MustCompile(`(?im)^\s*privileged:\s*true\b`),
		title:   "Requires privileged mode",
		detail:  "A compose service requests privileged mode. Every Gregale workload runs unprivileged inside a jailed microVM, which is the isolation boundary and is not negotiable.",
		remedy:  "Remove the privileged requirement. If it exists for a sidecar concern, check whether a managed equivalent covers it.",
	},
	{
		code:    "host_networking",
		files:   composeNames,
		pattern: regexp.MustCompile(`(?im)^\s*network_mode:\s*["']?host\b`),
		title:   "Requires host networking",
		detail:  "A compose service requests host networking. Each guest gets its own network namespace, which is what lets one snapshot restore as many instances.",
		remedy:  "Bind to 0.0.0.0 on $PORT and let the platform route to it.",
	},
	{
		code:    "docker_socket",
		files:   append(append([]string{}, dockerfileNames...), composeNames...),
		pattern: regexp.MustCompile(`/var/run/docker\.sock`),
		title:   "Needs the Docker socket",
		detail:  "The app expects access to the host Docker socket. There is no Docker daemon inside a Gregale microVM, and exposing the host's would defeat the isolation boundary.",
		remedy:  "Drop the Docker dependency, or run the work as a Gregale job instead of spawning containers.",
	},
}

// ScanContract looks for hard disqualifiers from the container compatibility
// contract. It reports only findings that would actually stop the app from
// running, so an empty result means the scanner found no reason to say no —
// not that the app is guaranteed to work.
func ScanContract(fsys fs.FS) []Finding {
	bodies := make(map[string]string)
	read := func(name string) string {
		if body, seen := bodies[name]; seen {
			return body
		}
		data, err := fs.ReadFile(fsys, name)
		body := ""
		if err == nil && len(data) <= maxContractFileBytes {
			body = string(data)
		}
		bodies[name] = body
		return body
	}

	var findings []Finding
	for _, rule := range contractRules {
		var sources []string
		for _, name := range rule.files {
			if body := read(name); body != "" && rule.pattern.MatchString(body) {
				sources = append(sources, name)
			}
		}
		if len(sources) == 0 {
			continue
		}
		sort.Strings(sources)
		findings = append(findings, Finding{
			Code:    rule.code,
			Level:   LevelRed,
			Title:   rule.title,
			Detail:  rule.detail,
			Remedy:  rule.remedy,
			Sources: sources,
		})
	}
	return findings
}
