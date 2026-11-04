package reposcan

import (
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"sort"
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
//     or wss URL in its Compose `environment:`, or
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
				if ok && workloads[i].Class == ClassWorker && (len(workloads[i].InternalPorts) > 0 ||
					listensByItsOwnCommand(fsys, workloads[i]) || imageListensByItsOwnCommand(&workloads[i], listens)) {
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
