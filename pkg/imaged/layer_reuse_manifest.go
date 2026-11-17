package imaged

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// An app layer's files depend on only a few manifest fields: the shared
// runner split reads the entrypoint and rewrites the entrypoint, PATH and
// VIRTUAL_ENV; working directory, user and execution mode shape the start.
// Every other field changes only the embedded /etc/faas/app.json, so layer
// reuse keys on these and patches the manifest file when the rest differs.

// layerContentEnv are the environment keys that are layer content.
var layerContentEnv = []string{"PATH", "VIRTUAL_ENV"}

// layerContentFields are the AppManifest fields whose value can change an
// app layer's files. TestLayerManifestFieldsAreClassified fails when a new
// field is in neither list, so it cannot drop out of the key unnoticed.
var layerContentFields = []string{"Entrypoint", "WorkingDir", "User", "ExecutionMode", "Env"}

// layerRuntimeOnlyFields only change the embedded manifest.
var layerRuntimeOnlyFields = []string{
	"EnvSecrets", "Port", "ServiceBindingTransport", "Ports", "Healthz", "Healthcheck",
	"StopSignal", "SecretReloadSignal", "SecretReloadReadiness", "SecretFiles", "StopGracePeriod",
	"RestartPolicy", "AfterRestore", "BeforeCheckpoint", "Warmup", "Pause", "Profiling", "Tracing",
	"ProfilingOnDemand", "StartupDeadlineS", "MaxRetries", "RequestTimeoutS", "FunctionConcurrency",
	"ServiceReplicas", "WorkerReplicas", "Favicon", "RobotsTxt", "HeadWakes", "CrawlerPolicy",
	"PreAuthRateLimit", "HealthPath", "HealthPathWakes", "SessionAffinity", "VersionAffinityCookie",
	"VersionAffinityManagedCookie", "RevisionPinTTLSeconds", "ReleasePolicy", "ReleaseProbePaths",
}

// layerContentManifest keeps the fields of m that are layer content. Env
// keeps only layerContentEnv.
func layerContentManifest(m api.AppManifest) api.AppManifest {
	out := api.AppManifest{Entrypoint: m.Entrypoint, WorkingDir: m.WorkingDir, User: m.User, ExecutionMode: m.ExecutionMode}
	for _, k := range layerContentEnv {
		if v, ok := m.Env[k]; ok {
			if out.Env == nil {
				out.Env = map[string]string{}
			}
			out.Env[k] = v
		}
	}
	return out
}

// cloneManifest deep-copies m: the shared runner split mutates the manifest
// and its Env map in place.
func cloneManifest(m api.AppManifest) (api.AppManifest, bool) {
	raw, err := json.Marshal(m)
	if err != nil {
		return api.AppManifest{}, false
	}
	var out api.AppManifest
	if json.Unmarshal(raw, &out) != nil {
		return api.AppManifest{}, false
	}
	return out, true
}

func manifestsEqual(a, b api.AppManifest) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ra) == string(rb)
}

// splitManifest applies to next what the build of a layer did to its input
// manifest: the shared runner split's entrypoint and environment rewrites,
// read off the recorded input and final manifests. With the same layer
// content key the split would make the same rewrites for next.
func splitManifest(next, input, final api.AppManifest) (api.AppManifest, bool) {
	out, ok := cloneManifest(next)
	if !ok {
		return api.AppManifest{}, false
	}
	if !slices.Equal(input.Entrypoint, final.Entrypoint) {
		out.Entrypoint = slices.Clone(final.Entrypoint)
	}
	for k, v := range final.Env {
		if in, had := input.Env[k]; had && in == v {
			continue
		}
		if out.Env == nil {
			out.Env = map[string]string{}
		}
		out.Env[k] = v
	}
	for k := range input.Env {
		if _, kept := final.Env[k]; !kept {
			// The build removed a key; nothing does today, so refuse
			// rather than guess.
			return api.AppManifest{}, false
		}
	}
	return out, true
}

// appManifestFieldNames lists AppManifest's fields, for the classification
// test.
func appManifestFieldNames() []string {
	t := reflect.TypeOf(api.AppManifest{})
	names := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		names = append(names, t.Field(i).Name)
	}
	return names
}
