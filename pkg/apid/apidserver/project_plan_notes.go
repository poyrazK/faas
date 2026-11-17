package apidserver

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// environmentDependencyNotes warns when a deploy to a named environment
// selects a service whose project dependency has no deployment in that
// environment and is not part of the deploy (ADR-20261108090000). Calls stay
// in their environment, so every call to it is refused with 403 until it is
// deployed there; the plan said nothing.
func (s *server) environmentDependencyNotes(ctx context.Context, environment string, selected []reposcan.Workload, projectApps []state.App) []string {
	if environment == "" {
		return nil
	}
	isSelected := func(name string) bool {
		for _, w := range selected {
			if api.SameServiceName(w.Name, name) {
				return true
			}
		}
		return false
	}
	var notes []string
	warned := map[string]bool{}
	for _, w := range selected {
		for _, dependency := range w.DependsOn {
			if isSelected(dependency) {
				continue
			}
			for _, app := range projectApps {
				if app.Status == state.AppDeleted || !api.SameServiceName(app.WorkloadName, dependency) {
					continue
				}
				if _, err := s.store.LiveDeploymentForScope(ctx, app.ID, environment); err == nil {
					break
				}
				key := w.Name + "\x00" + app.WorkloadName
				if !warned[key] {
					warned[key] = true
					notes = append(notes, fmt.Sprintf("environment: %s calls %s, which has no deployment in %s; those calls get 403 until %s is deployed there (add it to this deploy)",
						w.Name, app.WorkloadName, environment, app.WorkloadName))
				}
				break
			}
		}
	}
	return notes
}

// allowCallersNotes warns when x-gregale-allow-callers names neither a
// service of the project nor an app of the account (ADR-20261108090000). A
// typo, or a caller renamed with x-gregale-renamed-from, left the list naming
// nothing, and the caller's every call was refused with no word in the plan.
func allowCallersNotes(selected, scanned []reposcan.Workload, projectApps, accountApps []state.App) []string {
	known := func(name string) bool {
		for _, w := range scanned {
			if api.SameServiceName(w.Name, name) {
				return true
			}
		}
		for _, app := range projectApps {
			if app.Status != state.AppDeleted && api.SameServiceName(app.WorkloadName, name) {
				return true
			}
		}
		for _, app := range accountApps {
			if app.Status != state.AppDeleted && app.Slug == name {
				return true
			}
		}
		return false
	}
	var notes []string
	for _, w := range selected {
		if w.AllowedServiceCallers == nil {
			continue
		}
		for _, caller := range *w.AllowedServiceCallers {
			if !known(caller) {
				notes = append(notes, fmt.Sprintf("x-gregale-allow-callers on %s names %q, which is no service of this project and no app; nothing can call %s under that name",
					w.Name, caller, w.Name))
			}
		}
	}
	return notes
}
