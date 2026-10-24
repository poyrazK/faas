package reconcile

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// composeResourcesAdmissionReasons applies the plan to a service's Compose
// deploy.replicas and deploy.resources.limits (ADR-20261023120000), as the
// apps API applies it to the same settings.
func composeResourcesAdmissionReasons(plan api.Plan, w reposcan.Workload) []string {
	r := w.Resources
	if r.IsZero() {
		return nil
	}
	if err := r.Validate(); err != nil {
		return []string{fmt.Sprintf("workload %q: %s", w.Name, err)}
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return []string{fmt.Sprintf("workload %q: unknown plan %q", w.Name, plan)}
	}
	var reasons []string
	if r.MemoryMB > limits.RAMMB {
		reasons = append(reasons, fmt.Sprintf("workload %q: memory %d MB exceeds the %s plan's %d MB per app", w.Name, r.MemoryMB, plan, limits.RAMMB))
	}
	if r.Replicas != nil {
		switch replicas := *r.Replicas; scanExecutionMode(w) {
		case "":
			if replicas > limits.MaxConcurrency {
				reasons = append(reasons, fmt.Sprintf("workload %q: deploy.replicas %d exceeds the %s plan's %d concurrent instances", w.Name, replicas, plan, limits.MaxConcurrency))
			}
		case api.ExecutionModeWorker:
			if replicas > limits.WorkerReplicasMax {
				reasons = append(reasons, fmt.Sprintf("workload %q: deploy.replicas %d exceeds the %s plan's %d worker replicas", w.Name, replicas, plan, limits.WorkerReplicasMax))
			}
		default:
			reasons = append(reasons, fmt.Sprintf("workload %q: deploy.replicas applies to services and workers, not jobs", w.Name))
		}
	}
	return reasons
}

// applyComposeResources writes a service's declared RAM, CPU share and
// replicas onto its app and reports whether it declared any. Undeclared
// settings keep the app's own values. Replicas follow the app's execution
// mode: they cap a request-mode app's instances, fix a worker's or service's
// replica count, and do not apply to jobs.
func applyComposeResources(app *state.App, w reposcan.Workload) bool {
	r := w.Resources
	if r.IsZero() {
		return false
	}
	if r.MemoryMB > 0 {
		app.RAMMB = r.MemoryMB
	}
	if r.CPUMillicores > 0 {
		app.CPUMillicores = r.CPUMillicores
	}
	if r.Replicas != nil {
		n := *r.Replicas
		switch app.Manifest.ExecutionMode {
		case "", api.ExecutionModeRequest:
			app.MaxConcurrency = n
		case api.ExecutionModeWorker:
			app.Manifest.WorkerReplicas = &state.WorkerScaling{Min: n, Max: n}
		case api.ExecutionModeService:
			app.MaxConcurrency = n
			app.Manifest.ServiceReplicas = &state.ServiceReplicas{Min: n, Max: n, Desired: n}
		}
	}
	return true
}

// composeResourcesChanged names the declared settings the app does not
// already have.
func composeResourcesChanged(a state.App, w reposcan.Workload) []string {
	desired := a
	if !applyComposeResources(&desired, w) {
		return nil
	}
	var changed []string
	if desired.RAMMB != a.RAMMB {
		changed = append(changed, "ram_mb")
	}
	if desired.CPUMillicores != a.CPUMillicores {
		changed = append(changed, "cpu_millicores")
	}
	if desired.MaxConcurrency != a.MaxConcurrency {
		changed = append(changed, "max_concurrency")
	}
	if !samePointee(desired.Manifest.WorkerReplicas, a.Manifest.WorkerReplicas) {
		changed = append(changed, "worker_replicas")
	}
	if !samePointee(desired.Manifest.ServiceReplicas, a.Manifest.ServiceReplicas) {
		changed = append(changed, "service_replicas")
	}
	return changed
}

func samePointee[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
