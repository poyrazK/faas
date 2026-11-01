package reconcile

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// labelAddedApps gives each app this reconcile created the labels its source
// declares (ADR-20261031100025 §7): an imported Kubernetes object's
// metadata.labels. Only new apps are labelled; an existing app keeps the
// labels its owner set. The scan carries labels only while the account has the
// resource-labels capability. A failed write is logged and the app stays: the
// labels can be set again with `gregale app label set`.
func (s *Service) labelAddedApps(ctx context.Context, added []state.App, workloads []reposcan.Workload) {
	store, ok := s.Store.(state.AppLabelStore)
	if !ok || len(added) == 0 {
		return
	}
	byName := make(map[string]map[string]string, len(workloads))
	for _, w := range workloads {
		if len(w.Labels) > 0 {
			byName[w.Name] = w.Labels
		}
	}
	for _, app := range added {
		declared := byName[app.WorkloadName]
		if len(declared) == 0 {
			continue
		}
		if _, err := store.UpdateAppLabels(ctx, app.ID, labels.Change{Set: declared}); err != nil {
			log := s.Log
			if log == nil {
				log = slog.Default()
			}
			log.WarnContext(ctx, "reconcile: label new app", "app_id", app.ID, "err", err)
		}
	}
}
