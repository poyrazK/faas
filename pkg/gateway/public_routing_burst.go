// adr: 570
package gateway

import "context"

type publicDeploymentBurstAdmitter interface {
	AdmitDeploymentBurst(context.Context, string, string, string, string, int, int) (int, error)
}

type publicBurstAdmitter struct {
	backend    publicDeploymentBurstAdmitter
	deployment string
	healthy    func(string) int
}

func (b publicBurstAdmitter) HealthyCount(app string) int { return b.healthy(app) }

func (b publicBurstAdmitter) AdmitBurst(ctx context.Context, app, scope, trigger string, maximum, count int) (int, error) {
	return b.backend.AdmitDeploymentBurst(ctx, app, b.deployment, scope, trigger, maximum, count)
}

// Reuse the app's single capacity worker and pressure signal, while every
// admission in this generation carries the immutable selected deployment.
func (h *Handler) maybePublicRoutingBurst(ctx context.Context, app App, maximum, perVM int, routing PublicRoutingSnapshot) (bool, error) {
	if h == nil || h.burstPressure == nil || maximum <= 0 || perVM <= 0 || routing.SelectedDeploymentID == "" {
		return false, nil
	}
	backend, ok := h.backend.(publicDeploymentBurstAdmitter)
	if !ok || routing.SelectionReason != "weighted" && routing.SelectionReason != "version" && routing.SelectionReason != "session" {
		return false, nil
	}
	if app.MaxConcurrency > 0 && app.MaxConcurrency < maximum {
		maximum = app.MaxConcurrency
	}
	app.Scope = routing.Scope
	healthy := h.backend.HealthyCount
	if counter, ok := h.backend.(interface {
		HealthyCountForDeployments(string, []string) int
	}); ok {
		// Fresh host policy reads deliberately bypass the old weighted cache.
		// Count only the positive cohorts captured for this request; refreshing
		// that cache here would mix a later traffic policy into admission.
		deployments := make([]string, 0, len(routing.Weights))
		for _, weight := range routing.Weights {
			if weight.TrafficPercent > 0 {
				deployments = append(deployments, weight.ID)
			}
		}
		healthy = func(app string) int { return counter.HealthyCountForDeployments(app, deployments) }
	}
	return h.maybeBurstCapacityWithAdmitter(ctx, app, maximum, perVM, publicBurstAdmitter{backend: backend, deployment: routing.SelectedDeploymentID, healthy: healthy})
}
