package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploydiff"
)

// Existing placement is immutable and survives a pause in new admission.
// Only a fresh app needs current operator policy; this preview reserves nothing.
func (s *server) addComputeRegionAdmissionToDiff(ctx context.Context, baseline deploydiff.Baseline, pending deploydiff.Pending, diff *deploydiff.Diff) {
	if baseline.App != nil || pending.ComputeRegion == nil || *pending.ComputeRegion == "" || !api.ValidComputeRegion(*pending.ComputeRegion) {
		return
	}
	if problem := s.validateComputeRegionAvailable(ctx, *pending.ComputeRegion); problem != nil {
		diff.Breaks = append(diff.Breaks, deploydiff.Break{Code: problem.Code, Severity: deploydiff.SeverityError,
			Field: "compute_region", Reason: problem.Detail})
	}
}
