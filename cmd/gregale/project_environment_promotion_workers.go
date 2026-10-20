package main

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

func renderPromotionWorker(w *api.ProjectReleaseWorker, h *api.ProjectWorkerHealth) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Worker %s: %s (queue binding %s, drain %ds)\n", w.Slug, w.Status, w.BindingID, w.DrainSeconds)
	if h != nil {
		_, _ = fmt.Fprintf(osStdout, "  job health: %s, attempts %d, failures %d, pending %d\n", h.Status, h.Candidate.Attempts, h.Candidate.Failures, h.PendingJobs)
	}
}
