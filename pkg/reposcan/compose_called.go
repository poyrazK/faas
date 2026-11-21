package reposcan

import (
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

// classifyCalledComposeServices makes a Compose service without `ports:` an
// HTTP service when another service calls it (ADR-20261029120000). Docker
// reaches such a service on the project network without publishing it, so it
// is an internal API: it must park when idle and wake when called, which a
// worker never does. A service is called when
//
//   - another service addresses it by its Docker name in an http, https, ws
//     or wss URL in its Compose `environment:`, or by a bare host:port (a
//     gRPC target) when it shows it listens, as below, or
//   - a service that serves requests lists it in `depends_on` (Compose or
//     .gregale/project.yaml) and the repository shows that it listens: it
//     declares `expose:`, or it runs its own image's command and that image
//     listens (listensByItsOwnCommand).
//
// `depends_on` alone is often only start order (a gateway listing its queue
// worker), so a worker it names stays a worker, and the returned warnings
// say how to mark it if the caller does call it. A service nothing calls
// stays a worker.
func classifyCalledComposeServices(fsys fs.FS, workloads []Workload, imageListening ImageListening) []string {
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
	listens := imageListeningOnce(imageListening)
	addressed, bare := composeServicesAddressed(fsys)
	for name, i := range candidates {
		if addressed[name] {
			workloads[i].Class = ClassHTTP
			continue
		}
		// gRPC clients take host:port, not a URL (microservices-demo's
		// PRODUCT_CATALOG_SERVICE_ADDR: productcatalogservice:3550). Such an
		// address is a call when the service shows it listens, the evidence
		// depends_on needs below; otherwise it may name a raw TCP service.
		if bare[name] && (len(workloads[i].InternalPorts) > 0 ||
			listensByItsOwnCommand(fsys, workloads[i]) || imageListensByItsOwnCommand(&workloads[i], listens)) {
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
				if ok && workloads[i].Class == ClassWorker && (len(workloads[i].InternalPorts) > 0 ||
					listensByItsOwnCommand(fsys, workloads[i]) || imageListensByItsOwnCommand(&workloads[i], listens)) {
					workloads[i].Class = ClassHTTP
					changed = true
				}
			}
		}
	}
	var warnings []string
	listedAsDependency := map[int]bool{}
	for _, caller := range workloads {
		if !servesRequests(caller) {
			continue
		}
		for _, dependency := range caller.DependsOn {
			if i, ok := candidates[strings.ToLower(dependency)]; ok {
				listedAsDependency[i] = true
			}
		}
	}
	// A service that declares `expose:` listens, and a listening worker
	// fails its first boot with execution_mode_conflict. With no caller and
	// no published port it stays a worker (the ADR keeps a health or metrics
	// listener loud rather than parking it), so say so in the plan instead
	// of only at boot. A depends_on target is warned below.
	for _, i := range candidates {
		w := workloads[i]
		if w.Class != ClassWorker || len(w.InternalPorts) == 0 || listedAsDependency[i] {
			continue
		}
		ports := make([]string, 0, len(w.InternalPorts))
		for _, p := range w.InternalPorts {
			ports = append(ports, strconv.Itoa(p.Port))
		}
		warnings = append(warnings, fmt.Sprintf(
			"reposcan: service %q declares expose: [%s] but publishes no ports and no service calls it, so it is planned as a background worker, "+
				"and a worker that listens fails its first boot (execution_mode_conflict); publish it with ports:, have a service call it (http://%s:%s), "+
				"or drop expose: if it does not serve requests",
			w.Name, strings.Join(ports, ", "), w.Name, ports[0]))
	}
	for _, caller := range workloads {
		if !servesRequests(caller) {
			continue
		}
		for _, dependency := range caller.DependsOn {
			if i, ok := candidates[strings.ToLower(dependency)]; ok && workloads[i].Class == ClassWorker {
				unread := ""
				if workloads[i].ListenerUnknown {
					unread = fmt.Sprintf(" (Gregale could not read whether its image %s exposes a port)", workloads[i].Image)
				}
				warnings = append(warnings, fmt.Sprintf(
					"reposcan: service %q publishes no ports and runs as a background worker, though %q lists it in depends_on%s; "+
						"if %s calls it, give %s's environment its URL (http://%s:<port>) or give it expose: [<port>]",
					workloads[i].Name, caller.Name, unread, caller.Name, caller.Name, workloads[i].Name))
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}

// httpFrameworks are the frameworkprofile frameworks whose default start
// command serves HTTP.
var httpFrameworks = map[string]bool{
	"express": true, "fastify": true, "nestjs": true, "hono": true,
	"fastapi": true, "flask": true, "django": true, "gin": true,
}

var dockerfileExpose = regexp.MustCompile(`(?im)^\s*EXPOSE\s+\S`)

// listensByItsOwnCommand reports that a built Compose service runs its own
// image's command and the repository shows that command listens: its
// Dockerfile EXPOSEs a port, or, built without a Dockerfile, its source is an
// HTTP framework (ADR-20261029120000 amendment 1). A service that overrides
// the command, such as a Celery worker built from the web service's
// Dockerfile, runs something else, so that image says nothing about it. A
// prebuilt image's own metadata is not read at scan time.
func listensByItsOwnCommand(fsys fs.FS, w Workload) bool {
	if w.Image != "" || len(w.Command) > 0 {
		return false
	}
	root := w.RootDir
	if root == "" {
		root = "."
	}
	dockerfile := w.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if body, err := fs.ReadFile(fsys, path.Join(root, dockerfile)); err == nil {
		return dockerfileExpose.Match(body)
	}
	source, err := fs.Sub(fsys, root)
	if err != nil {
		return false
	}
	profile, err := frameworkprofile.Analyze(source)
	return err == nil && httpFrameworks[profile.Framework]
}

// ImageListening reports whether a prebuilt image declares a TCP port it
// listens on (its config's ExposedPorts). known is false when the registry
// could not say: a private image, a registry that is down, a timeout
// (ADR-20261029120000 amendment 2).
type ImageListening func(image string) (listens, known bool)

// imageListeningOnce asks about each image at most once per scan.
func imageListeningOnce(lookup ImageListening) ImageListening {
	if lookup == nil {
		return nil
	}
	type answer struct{ listens, known bool }
	seen := map[string]answer{}
	return func(image string) (bool, bool) {
		if a, ok := seen[image]; ok {
			return a.listens, a.known
		}
		listens, known := lookup(image)
		seen[image] = answer{listens, known}
		return listens, known
	}
}

// imageListensByItsOwnCommand is listensByItsOwnCommand for a prebuilt
// image: the service runs the image's own command (no Compose command: or
// entrypoint:) and the image's config exposes a port. When the registry
// cannot say, the service is marked ListenerUnknown so reconcile keeps an
// existing app's serving class instead of flipping it to a worker.
func imageListensByItsOwnCommand(w *Workload, listens ImageListening) bool {
	if listens == nil || w.Image == "" || len(w.Command) > 0 || w.ImageProcess != nil && w.ImageProcess.Entrypoint != nil {
		return false
	}
	ok, known := listens(w.Image)
	if !known {
		w.ListenerUnknown = true
	}
	return ok
}

func servesRequests(w Workload) bool {
	return w.Class != ClassWorker && w.Class != ClassJob
}

// composeServicesAddressed returns the lower-cased Docker hosts each Compose
// service's environment addresses, leaving out a service's own name: by an
// http, https, ws or wss URL, and separately by a bare host:port such as a
// gRPC target. Values are read apart from the scan result, as the env import
// reads them; an unreadable environment only means no address counts as a call.
func composeServicesAddressed(fsys fs.FS) (called, bare map[string]bool) {
	called, bare = map[string]bool{}, map[string]bool{}
	env, err := ReadComposeEnv(fsys)
	if err != nil {
		return called, bare
	}
	for caller, entries := range env.Services {
		for _, value := range entries {
			if value == nil {
				continue
			}
			if host, ok := BareServiceAddressHost(*value); ok {
				if !strings.EqualFold(host, caller) {
					bare[host] = true
				}
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
	return called, bare
}

// BareServiceAddressHost returns the lower-cased host of a value written as
// host:port with no scheme, path or credentials, the form gRPC and many TCP
// clients take. The host must be a single DNS label, as a Compose service
// name is: an IP address or a dotted name is not a project service.
func BareServiceAddressHost(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "/@ ") {
		return "", false
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || strings.ContainsAny(host, ".:") {
		return "", false
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return strings.ToLower(host), true
}
