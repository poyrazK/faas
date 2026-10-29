package reposcan

import (
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
)

// classifyCalledComposeServices makes a Compose service without `ports:` an
// HTTP service when another service calls it (ADR-20261029120000). Docker
// reaches such a service on the project network without publishing it, so it
// is an internal API: it must park when idle and wake when called, which a
// worker never does. A service is called when
//
//   - another service addresses it by its Docker name in an http, https, ws
//     or wss URL in its Compose `environment:`, or
//   - a service that serves requests lists it in `depends_on` (Compose or
//     .gregale/project.yaml) and it declares `expose:`, so it listens.
//
// `depends_on` alone is often only start order (a gateway listing its queue
// worker), so a worker it names stays a worker, and the returned warnings
// say how to mark it if the caller does call it. A service nothing calls
// stays a worker.
func classifyCalledComposeServices(fsys fs.FS, workloads []Workload) []string {
	candidates := map[string]int{}
	for i, w := range workloads {
		if w.composeDefaultWorker {
			candidates[strings.ToLower(w.Name)] = i
		}
		workloads[i].composeDefaultWorker = false
	}
	if len(candidates) == 0 {
		return nil
	}
	addressed := composeServicesAddressedByURL(fsys)
	for name, i := range candidates {
		if addressed[name] {
			workloads[i].Class = ClassHTTP
		}
	}
	for changed := true; changed; {
		changed = false
		for _, caller := range workloads {
			if !servesRequests(caller) {
				continue
			}
			for _, dependency := range caller.DependsOn {
				i, ok := candidates[strings.ToLower(dependency)]
				if ok && workloads[i].Class == ClassWorker && len(workloads[i].InternalPorts) > 0 {
					workloads[i].Class = ClassHTTP
					changed = true
				}
			}
		}
	}
	var warnings []string
	for _, caller := range workloads {
		if !servesRequests(caller) {
			continue
		}
		for _, dependency := range caller.DependsOn {
			if i, ok := candidates[strings.ToLower(dependency)]; ok && workloads[i].Class == ClassWorker {
				warnings = append(warnings, fmt.Sprintf(
					"reposcan: service %q publishes no ports and runs as a background worker, though %q lists it in depends_on; "+
						"if %s calls it, give %s's environment its URL (http://%s:<port>) or give it expose: [<port>]",
					workloads[i].Name, caller.Name, caller.Name, caller.Name, workloads[i].Name))
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}

func servesRequests(w Workload) bool {
	return w.Class != ClassWorker && w.Class != ClassJob
}

// composeServicesAddressedByURL returns the lower-cased Docker hosts of the
// http, https, ws and wss URLs each Compose service's environment sets,
// leaving out a service's own name. Values are read apart from the scan
// result, as the env import reads them; an unreadable environment only means
// no address counts as a call.
func composeServicesAddressedByURL(fsys fs.FS) map[string]bool {
	called := map[string]bool{}
	env, err := ReadComposeEnv(fsys)
	if err != nil {
		return called
	}
	for caller, entries := range env.Services {
		for _, value := range entries {
			if value == nil {
				continue
			}
			parsed, err := url.Parse(strings.TrimSpace(*value))
			if err != nil || parsed.Opaque != "" {
				continue
			}
			switch strings.ToLower(parsed.Scheme) {
			case "http", "https", "ws", "wss":
			default:
				continue
			}
			if host := parsed.Hostname(); host != "" && !strings.EqualFold(host, caller) {
				called[strings.ToLower(host)] = true
			}
		}
	}
	return called
}
