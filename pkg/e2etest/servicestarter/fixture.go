package servicestarter

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

const Scope = "acceptance"
const ManifestPath = "environments/acceptance.yaml"

// Definition uses the ordinary reviewed image/runtime contract. The custom
// environment key deliberately differs from standalone SERVICE_* discovery.
func Definition(project, caller, target, callerImage, targetImage string) api.EnvironmentDefinition {
	return api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project, Environment: Scope, Workloads: map[string]api.EnvironmentWorkload{
		"caller": {App: caller, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: callerImage, FullRootfs: true},
			Runtime:         json.RawMessage(`{"port":8080,"healthz":"/healthz","startup_deadline_s":30,"execution_mode":"request","service_binding_transport":"https"}`),
			ServiceBindings: map[string]api.EnvironmentServiceBinding{"billing": {Workload: "billing", EnvKey: "BILLING_ENDPOINT"}}},
		"billing": {App: target, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: targetImage, FullRootfs: true}, Runtime: json.RawMessage(`{"port":8080,"healthz":"/healthz","startup_deadline_s":30,"execution_mode":"request"}`)},
	}}
}

// WithWorker adds a reviewed worker with one HTTPS dependency and one bounded
// push queue. Qualification sends its smoke directly to the private candidate.
// The reviewed capability permits one bounded canary only after explicit
// publication consent; service-only probes still publish no application work.
func WithWorker(definition api.EnvironmentDefinition, slug, image string) api.EnvironmentDefinition {
	enabled := true
	definition.Workloads["worker"] = api.EnvironmentWorkload{App: slug,
		Variables:       map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`},
		Source:          &api.EnvironmentWorkloadSource{Kind: "image", Image: image, FullRootfs: true},
		Runtime:         json.RawMessage(`{"port":8080,"startup_deadline_s":30,"execution_mode":"worker","service_binding_transport":"https"}`),
		ServiceBindings: map[string]api.EnvironmentServiceBinding{"billing": {Workload: "billing", EnvKey: "WORKER_BILLING_ENDPOINT"}},
		QueueBindings:   map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: 1}},
		QueueSmoke:      map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"worker-service-qualification"}`)}},
	}
	return definition
}

// WithSourceWorker keeps the reviewed worker's dependency and consumer intent
// while selecting the immutable Git archive used by the real source builder.
func WithSourceWorker(definition api.EnvironmentDefinition) api.EnvironmentDefinition {
	worker := definition.Workloads["worker"]
	worker.Source = &api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "services/worker", Dockerfile: "Dockerfile"}
	definition.Workloads["worker"] = worker
	return definition
}
