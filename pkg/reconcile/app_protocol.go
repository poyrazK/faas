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

// syncAddedAppProtocols sets the declared protocol on the apps a named
// environment's deploy created. It leaves existing apps alone: the protocol
// is app-wide, so a stage deploy changing it would change production
// (ADR-20261108090000). syncAppProtocols covers production deploys.
func (s *Service) syncAddedAppProtocols(ctx context.Context, added []state.App, workloads []reposcan.Workload) error {
	declared := make(map[string]string, len(workloads))
	for _, w := range workloads {
		if w.AppProtocol != "" {
			declared[strings.ToLower(w.Name)] = w.AppProtocol
		}
	}
	for _, app := range added {
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

// syncAppComposeSettings applies x-gregale-streaming and x-gregale-wake-ahead
// to project apps, the settings `gregale app <slug> --streaming-enabled` and
// `gregale app wake-ahead <slug> on|off` write. Like the protocol, they are
// app-wide, so only production deploys and apps a named environment created
// change them (apps), and a service that declares nothing keeps its owner's
// choice. The plan gate has already refused streaming the plan does not include.
func (s *Service) syncAppComposeSettings(ctx context.Context, apps []state.App, workloads []reposcan.Workload) error {
	declared := make(map[string]reposcan.Workload, len(workloads))
	for _, w := range workloads {
		if w.Streaming != nil || w.WakeAhead != nil {
			declared[strings.ToLower(w.Name)] = w
		}
	}
	if len(declared) == 0 {
		return nil
	}
	wakeAhead, _ := s.Store.(state.ServiceWakeAheadStore)
	for _, app := range apps {
		if app.Status == state.AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		w, ok := declared[strings.ToLower(app.WorkloadName)]
		if !ok {
			continue
		}
		if w.Streaming != nil && *w.Streaming != app.StreamingEnabled {
			if _, err := s.Store.UpdateApp(ctx, app.ID, state.UpdateAppParams{StreamingEnabled: w.Streaming, SetStreamingEnabled: true}); err != nil {
				return fmt.Errorf("reconcile: set %s streaming: %w", app.Slug, err)
			}
		}
		if w.WakeAhead == nil {
			continue
		}
		if wakeAhead == nil {
			return fmt.Errorf("reconcile: set %s wake-ahead: store does not record wake-ahead", app.Slug)
		}
		current, err := wakeAhead.GetServiceWakeAhead(ctx, app.AccountID, app.ID)
		if err != nil {
			return fmt.Errorf("reconcile: read %s wake-ahead: %w", app.Slug, err)
		}
		if !current.UpdatedAt.IsZero() && current.Enabled == *w.WakeAhead {
			continue
		}
		if _, err := wakeAhead.SetServiceWakeAhead(ctx, app.AccountID, app.ID, *w.WakeAhead); err != nil {
			return fmt.Errorf("reconcile: set %s wake-ahead: %w", app.Slug, err)
		}
	}
	return nil
}

func hasAppComposeSettings(workloads []reposcan.Workload) bool {
	for _, w := range workloads {
		if w.Streaming != nil || w.WakeAhead != nil {
			return true
		}
	}
	return false
}
