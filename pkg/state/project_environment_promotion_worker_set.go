package state

import "github.com/onebox-faas/faas/pkg/api"

func copyPromotionWorkers(workers []*api.ProjectReleaseWorker) []*api.ProjectReleaseWorker {
	if workers == nil {
		return nil
	}
	out := make([]*api.ProjectReleaseWorker, len(workers))
	for i, w := range workers {
		out[i] = copyPromotionWorker(w)
	}
	return out
}
func (c *promotionReleaseConfiguration) workers() []*api.ProjectReleaseWorker {
	return api.ProjectReleaseWorkerPolicies(c.Worker, c.Workers)
}
func (c *promotionReleaseConfiguration) setWorkers(workers []*api.ProjectReleaseWorker) {
	c.Worker, c.Workers = nil, nil
	if len(workers) == 1 {
		c.Worker = workers[0]
	} else {
		c.Workers = workers
	}
}

// Project a single frozen worker through the existing owned-ledger reader. Every
// projection shares the promotion clock/window, never another worker's samples.
func promotionWorkerHealthViews(h *api.ProjectPromotionHealth) []*api.ProjectPromotionHealth {
	if h == nil {
		return nil
	}
	if h.Policy.Worker != nil {
		return []*api.ProjectPromotionHealth{h}
	}
	var views []*api.ProjectPromotionHealth
	for _, policy := range h.Policy.Workers {
		one := *h
		one.Policy = h.Policy
		one.Policy.Worker, one.Policy.Workers = policy, nil
		one.Worker, one.WorkerHealth = nil, nil
		one.Workers = nil
		for _, progress := range h.Workers {
			if progress.Worker != nil && progress.Worker.AppID == policy.AppID {
				one.Worker, one.WorkerHealth = progress.Worker, progress.Health
				break
			}
		}
		views = append(views, &one)
	}
	return views
}
func visitPromotionWorkers(h *api.ProjectPromotionHealth, visit func(*api.ProjectPromotionHealth) error) error {
	if h == nil {
		return nil
	}
	views := promotionWorkerHealthViews(h)
	for _, one := range views {
		if err := visit(one); err != nil {
			return err
		}
	}
	if len(h.Policy.Workers) > 0 {
		h.Workers = nil
		for _, one := range views {
			h.Workers = append(h.Workers, api.ProjectPromotionWorkerHealth{Worker: one.Worker, Health: one.WorkerHealth})
		}
	}
	return nil
}
func promotionWorkersReady(h *api.ProjectPromotionHealth) bool {
	for _, one := range promotionWorkerHealthViews(h) {
		if one.Worker == nil || one.Worker.InstanceID == "" {
			return false
		}
	}
	return true
}
