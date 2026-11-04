package main

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// Diagnostic scheduling bounds, not application quotas. Each job owns its
// client and result slot; waiting for a worker does not consume its probe budget.
const (
	mcpVerificationConcurrency = 4
	mcpVerificationJobTimeout  = 30 * time.Second
)

func runMCPVerificationJobs(ctx context.Context, count int, check func(context.Context, int)) {
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(count, mcpVerificationConcurrency) {
		workers.Go(func() {
			for index := range jobs {
				jobCtx, cancel := context.WithTimeout(ctx, mcpVerificationJobTimeout)
				check(jobCtx, index)
				cancel()
			}
		})
	}
	for index := range count {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
}

func mcpReleaseDoctor(ctx context.Context, probe *mcphosting.Client, legacy bool) mcphosting.Report {
	ctx, cancel := context.WithTimeout(ctx, mcpVerificationJobTimeout)
	defer cancel()
	return mcphosting.Doctor(ctx, probe, legacy, "", nil)
}

func checkMCPReleaseReadiness(ctx context.Context, candidate, deployment string, roles []mcpReleaseRole) ([]map[string]any, bool) {
	reports := make([]map[string]any, len(roles))
	ready := make([]bool, len(roles))
	runMCPVerificationJobs(ctx, len(roles), func(ctx context.Context, index int) {
		role := roles[index]
		version := mcphosting.ProtocolVersion
		var baseline *mcphosting.Contract
		if role.contract.Version != 0 {
			baseline = &role.contract
			version = role.contract.ProtocolVersion
		}
		probe, err := mcphosting.NewClient(candidate, role.token, version)
		var result mcphosting.ReadinessReport
		if err == nil {
			result = mcphosting.CheckReadiness(ctx, probe, baseline)
		}
		reports[index] = map[string]any{"role": role.Name, "deployment_id": deployment, "report": result}
		ready[index] = result.OK
	})
	for _, passed := range ready {
		if !passed {
			return reports, false
		}
	}
	return reports, true
}
