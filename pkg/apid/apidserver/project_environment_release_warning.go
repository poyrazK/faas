package apidserver

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// environmentReleaseSetWarning warns an environment deploy that its
// environment routes service calls through an active release set: the new
// deployments are outside that graph, so calls from workloads that declare
// service bindings are refused until a release set includes them or the
// environment's release sets are deactivated (ADR-20261019120024 amendment
// 2026-11-13). A missing project or release set, or a read failure, adds no
// warning; the deploy itself is unaffected.
func (s *server) environmentReleaseSetWarning(ctx context.Context, accountID, projectSlug, environment string) string {
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok || projectSlug == "" || environment == "" {
		return ""
	}
	project, err := s.store.ProjectBySlug(ctx, accountID, projectSlug)
	if err != nil {
		return ""
	}
	if _, err := reader.ActiveProjectReleaseSet(ctx, accountID, project.ID, environment); err != nil {
		return ""
	}
	return fmt.Sprintf("environment %s has an active release set: workloads deployed here stay outside it, and their calls to other services are refused (403) until a release set includes them or the environment's release sets are deactivated (gregale environments release-sets)", environment)
}
