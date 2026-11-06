package reconcile

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// syncAppProtocols sets each project app's wire protocol to the one its
// Compose service declares with x-gregale-app-protocol (ADR-20261105095300).
// The protocol is an app column the project reconcile does not otherwise
// write, so a gRPC service deployed from Compose stayed http1: its siblings'
// gRPC calls and its readiness took the HTTP/1 bridge and failed. A service
// that declares nothing keeps the protocol its owner set. The plan gate
// (WorkloadPlanAdmissionReasons) has already refused a protocol the plan
// does not include.
func (s *Service) syncAppProtocols(ctx context.Context, project state.Project, workloads []reposcan.Workload) error {
	declared := make(map[string]string, len(workloads))
	for _, w := range workloads {
		if w.AppProtocol != "" {
			declared[strings.ToLower(w.Name)] = w.AppProtocol
		}
	}
	if len(declared) == 0 {
		return nil
	}
	apps, err := s.Store.AppsForProject(ctx, project.AccountID, project.ID)
	if err != nil {
		return fmt.Errorf("reconcile: list project apps for protocols: %w", err)
	}
	for _, app := range apps {
		if app.Status == state.AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		protocol, ok := declared[strings.ToLower(app.WorkloadName)]
		if !ok || protocol == app.AppProtocol {
			continue
		}
		if _, err := s.Store.UpdateApp(ctx, app.ID, state.UpdateAppParams{AppProtocol: &protocol, SetAppProtocol: true}); err != nil {
			return fmt.Errorf("reconcile: set %s app protocol to %s: %w", app.Slug, protocol, err)
		}
	}
	return nil
}
