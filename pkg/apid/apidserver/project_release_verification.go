package apidserver

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) probeProjectRelease(ctx context.Context, receipt api.ProjectSubmissionReceipt, baseline state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error) {
	sender, ok := s.routeProbes.(deploystaging.ProjectCriticalProbeSender)
	if !ok || s.notif == nil {
		return nil, fmt.Errorf("critical gateway probes unavailable")
	}
	return deploystaging.ProbeProjectRelease(ctx, receipt, baseline, sender, func(ctx context.Context, payload string) error {
		return s.notif.Notify(ctx, db.NotifyRouteProbeChallenge, payload)
	}, routeProbeChallengeDelay)
}
