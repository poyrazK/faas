package reposcan

import (
	"fmt"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// foldComposeReleaseServices runs each Compose service that another service
// waits for with `condition: service_completed_successfully` as that service's
// release command (ADR-20261102192943). Compose starts such a service before
// its dependents and it exits: a migration or a seed step, usually built from
// the dependent's own source with another command. Gregale runs a release
// command once before each release of an app goes live, in the app's image
// with the app's environment, so the awaited service becomes the waiting
// service's release command and is not deployed as an app of its own.
//
// It refuses what a release command cannot express: a service that builds
// from other source than every service waiting for it, one that publishes
// ports, one without a command, chained completion dependencies, a waiting
// service that does not serve requests (the release fence holds a serving
// release), and two release commands for one service.
func foldComposeReleaseServices(workloads []Workload) ([]Workload, []string, error) {
	index := make(map[string]int, len(workloads))
	for i, w := range workloads {
		index[strings.ToLower(w.Name)] = i
	}
	waiters := map[string][]int{}
	for i, w := range workloads {
		for name, condition := range w.DependsOnConditions {
			if condition == api.ComposeDependencyCompleted {
				waiters[name] = append(waiters[name], i)
			}
		}
	}
	if len(waiters) == 0 {
		return workloads, nil, nil
	}
	for _, target := range slices.Sorted(maps.Keys(waiters)) {
		for _, i := range waiters[target] {
			if _, chained := waiters[strings.ToLower(workloads[i].Name)]; chained {
				return nil, nil, fmt.Errorf("reposcan: %s waits for %s with service_completed_successfully while another service waits for %s the same way; chained completion dependencies are not supported, so merge the steps into one command", workloads[i].Name, target, workloads[i].Name)
			}
		}
	}
	var warnings []string
	folded := map[int]bool{}
	for _, target := range slices.Sorted(maps.Keys(waiters)) {
		waiting := waiters[target]
		slices.SortFunc(waiting, func(a, b int) int {
			return strings.Compare(strings.ToLower(workloads[a].Name), strings.ToLower(workloads[b].Name))
		})
		first := workloads[waiting[0]].Name
		ti, ok := index[target]
		if !ok {
			return nil, nil, fmt.Errorf("reposcan: %s waits for %s with service_completed_successfully, but %s is not a service Gregale builds (a datastore or an unknown name); only a service with build: or image: can run to completion", first, target, target)
		}
		release := workloads[ti]
		if len(release.Ports) > 0 {
			return nil, nil, fmt.Errorf("reposcan: %s publishes ports, but %s waits for it with service_completed_successfully; a service that runs to completion cannot also serve requests", release.Name, first)
		}
		if len(release.Command) == 0 {
			return nil, nil, fmt.Errorf("reposcan: %s has no command:, but %s waits for it with service_completed_successfully; Gregale runs it as the release command of the service that waits for it, so it needs the command to run", release.Name, first)
		}
		// A release task executes argv directly; preserve a prebuilt image's
		// explicit entrypoint just as the built-service path already does.
		release.Command, release.CommandShell, _ = builtServiceProcess(release.ImageProcess, release.Command, release.CommandShell)
		if _, problem := (api.CreateAppTaskRequest{Command: release.Command, CommandShell: release.CommandShell}).Resolve(); problem != nil {
			return nil, nil, fmt.Errorf("reposcan: %s command cannot run as a release command: %s", release.Name, problem.Detail)
		}
		owner := -1
		var runtimeMismatch string
		for _, i := range waiting {
			if sameComposeBuild(release, workloads[i]) && workloadServes(workloads[i]) {
				if mismatch := composeReleaseRuntimeMismatch(release, workloads[i]); mismatch != "" {
					runtimeMismatch = mismatch
					continue
				}
				owner = i
				break
			}
		}
		if owner < 0 {
			if runtimeMismatch != "" {
				return nil, nil, fmt.Errorf("reposcan: %s: %s; a folded release command shares its owner's user and working_dir, so make those settings match", release.Name, runtimeMismatch)
			}
			names := make([]string, len(waiting))
			for n, i := range waiting {
				names[n] = workloads[i].Name
			}
			return nil, nil, fmt.Errorf("reposcan: %s waits for %s with service_completed_successfully; Gregale runs %s as the release command of a waiting service that serves requests, in that service's image, so build %s from the same build: or image: as one of them", strings.Join(names, ", "), release.Name, release.Name, release.Name)
		}
		if len(workloads[owner].ReleaseCommand) > 0 {
			return nil, nil, fmt.Errorf("reposcan: %s already runs %s as its release command and also waits for %s; Gregale runs one release command per service, so merge them into one command", workloads[owner].Name, workloads[owner].ReleaseService, release.Name)
		}
		o := &workloads[owner]
		secretFiles, err := mergeComposeReleaseSecretFiles(o.SecretFiles, release.SecretFiles)
		if err != nil {
			return nil, nil, fmt.Errorf("reposcan: %s release command for %s: %w", release.Name, o.Name, err)
		}
		o.SecretFiles = secretFiles
		o.ReleaseCommand = slices.Clone(release.Command)
		o.ReleaseCommandShell = release.CommandShell
		o.ReleaseService = release.Name
		// The release command runs with the owner's environment, so the
		// owner needs the variables and dependencies the step declared.
		o.EnvKeys = mergeEnvKeys(o.EnvKeys, release.EnvKeys)
		for _, dep := range release.DependsOn {
			key := strings.ToLower(dep)
			if key == strings.ToLower(o.Name) || slices.ContainsFunc(o.DependsOn, func(have string) bool { return strings.EqualFold(have, dep) }) {
				continue
			}
			o.DependsOn = append(o.DependsOn, dep)
			if condition, ok := release.DependsOnConditions[key]; ok {
				if o.DependsOnConditions == nil {
					o.DependsOnConditions = map[string]string{}
				}
				o.DependsOnConditions[key] = condition
			}
		}
		warnings = append(warnings, fmt.Sprintf("reposcan: %s runs as the release command of %s: once before each %s release goes live, in %s's image, not as a service of its own", release.Name, o.Name, o.Name, o.Name))
		folded[ti] = true
		for i := range workloads {
			if i == ti || !dropDependency(&workloads[i], target) || i == owner {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("reposcan: %s depends on %s, which runs as the release command of %s; %s does not wait for it", workloads[i].Name, release.Name, o.Name, workloads[i].Name))
		}
	}
	out := make([]Workload, 0, len(workloads)-len(folded))
	for i, w := range workloads {
		if !folded[i] {
			out = append(out, w)
		}
	}
	return out, warnings, nil
}

func composeReleaseRuntimeMismatch(release, owner Workload) string {
	var step, serving api.ComposeProcess
	if release.ImageProcess != nil {
		step = *release.ImageProcess
	}
	if owner.ImageProcess != nil {
		serving = *owner.ImageProcess
	}
	if step.User != serving.User {
		return fmt.Sprintf("user %q differs from %s's user %q", step.User, owner.Name, serving.User)
	}
	if path.Clean(step.WorkingDir) != path.Clean(serving.WorkingDir) {
		return fmt.Sprintf("working_dir %q differs from %s's working_dir %q", step.WorkingDir, owner.Name, serving.WorkingDir)
	}
	return ""
}

func mergeComposeReleaseSecretFiles(owner, release []api.SecretFile) ([]api.SecretFile, error) {
	out := slices.Clone(owner)
	for _, file := range release {
		if i := slices.IndexFunc(out, func(have api.SecretFile) bool { return have.Name == file.Name }); i >= 0 {
			if out[i].Secret != file.Secret || out[i].EffectiveMode() != file.EffectiveMode() {
				return nil, fmt.Errorf("secret file %q has conflicting declarations", file.Name)
			}
			continue
		}
		out = append(out, file)
	}
	return out, api.ValidateSecretFiles(out)
}

// dropDependency removes name from w's dependencies and reports whether w
// listed it.
func dropDependency(w *Workload, name string) bool {
	listed := false
	w.DependsOn = slices.DeleteFunc(w.DependsOn, func(dep string) bool {
		match := strings.EqualFold(dep, name)
		listed = listed || match
		return match
	})
	if _, ok := w.DependsOnConditions[name]; ok {
		delete(w.DependsOnConditions, name)
		listed = true
	}
	if len(w.DependsOnConditions) == 0 {
		w.DependsOnConditions = nil
	}
	return listed
}

// sameComposeBuild reports whether two Compose services run the same image:
// the same prebuilt image, or the same build context, Dockerfile and build
// options.
func sameComposeBuild(a, b Workload) bool {
	if a.Image != "" || b.Image != "" {
		return a.Image == b.Image
	}
	return a.RootDir == b.RootDir && a.Dockerfile == b.Dockerfile && a.GoCommand == b.GoCommand &&
		reflect.DeepEqual(normalizedBuild(a.DockerfileBuild), normalizedBuild(b.DockerfileBuild))
}

func normalizedBuild(o *api.DockerfileBuildOptions) *api.DockerfileBuildOptions {
	if o.IsZero() {
		return nil
	}
	return o
}

// workloadServes reports whether w serves requests, so its releases pass the
// release fence before they serve.
func workloadServes(w Workload) bool {
	return w.Class != ClassWorker && w.Class != ClassJob
}
