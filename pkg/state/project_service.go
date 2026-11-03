package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrProjectServiceDeleted reports that a project's service name belongs only
// to deleted production members, whose rows remain until purge. It is an
// ErrNotFound, yet the name must not fall back to an app slug elsewhere: a
// sibling's binding to it was written for the deleted member, which got a
// project-prefixed slug when another app already held the bare name
// (ADR-20261022120205).
var ErrProjectServiceDeleted = fmt.Errorf("project service is deleted: %w", ErrNotFound)

type ProjectServiceReader interface {
	AppsForProject(context.Context, string, string) ([]App, error)
	AppBySlug(context.Context, string) (App, error)
}

type projectServiceLookup interface {
	ProjectAppByWorkload(context.Context, string, string, string) (App, error)
}

// ProjectServiceLookupReader resolves a project alias with one indexed read.
type ProjectServiceLookupReader interface {
	ProjectAppByWorkload(context.Context, string, string, string) (App, error)
	AppBySlug(context.Context, string) (App, error)
}

// AppByServiceName resolves a production project's repository aliases before
// the established global app-slug fallback. A name a deleted member still
// holds does not fall back (ErrProjectServiceDeleted), and a name the caller
// binds falls back only to an app of its own project: a project workload's
// bindings name its own project's services (ADR-20261030131528). Preview and
// scenario routing keep their separate environment-aware owners.
func AppByServiceName(ctx context.Context, reader ProjectServiceReader, caller App, service string) (App, error) {
	if caller.ProjectID != "" && caller.PreviewOfSlug == "" {
		app, err := ProjectMemberByWorkload(ctx, reader, caller.AccountID, caller.ProjectID, service)
		if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrProjectServiceDeleted) {
			return app, err
		}
		if caller.BindsService(service) {
			return boundProjectAppBySlug(ctx, reader, caller, service)
		}
	}
	return reader.AppBySlug(ctx, service)
}

// AppByServiceLookup is AppByServiceName for readers that never load a whole
// project inventory, such as the gateway's pinned service-policy view.
func AppByServiceLookup(ctx context.Context, reader ProjectServiceLookupReader, caller App, service string) (App, error) {
	if caller.ProjectID != "" && caller.PreviewOfSlug == "" {
		app, err := reader.ProjectAppByWorkload(ctx, caller.AccountID, caller.ProjectID, service)
		if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrProjectServiceDeleted) {
			return app, err
		}
		if caller.BindsService(service) {
			return boundProjectAppBySlug(ctx, reader, caller, service)
		}
	}
	return reader.AppBySlug(ctx, service)
}

// boundProjectAppBySlug resolves a bound name no member holds as its service
// name. An older binding may name a member of the caller's project by its
// slug; any other app holding the slug is outside the project, as is the
// app now holding a purged member's name, so the name resolves to nothing.
func boundProjectAppBySlug(ctx context.Context, reader interface {
	AppBySlug(context.Context, string) (App, error)
}, caller App, service string) (App, error) {
	app, err := reader.AppBySlug(ctx, service)
	if err != nil {
		return App{}, err
	}
	if app.AccountID != caller.AccountID || app.ProjectID != caller.ProjectID || app.PreviewOfSlug != "" {
		return App{}, ErrNotFound
	}
	return app, nil
}

// BindsService reports whether the app's manifest declares a binding to the
// named service.
func (a App) BindsService(service string) bool {
	for _, binding := range a.Manifest.ServiceBindings {
		if api.SameServiceName(binding.Service, service) {
			return true
		}
	}
	return false
}

// ProjectMemberByWorkload returns the live production member of a project
// named by its repository service name. ErrConflict reports an ambiguous name,
// and ErrProjectServiceDeleted a name only a deleted member holds.
func ProjectMemberByWorkload(ctx context.Context, store any, accountID, projectID, name string) (App, error) {
	if lookup, ok := store.(projectServiceLookup); ok {
		return lookup.ProjectAppByWorkload(ctx, accountID, projectID, name)
	}
	reader, ok := store.(interface {
		AppsForProject(context.Context, string, string) ([]App, error)
	})
	if !ok {
		return App{}, errors.New("project service lookup is unavailable")
	}
	apps, err := reader.AppsForProject(ctx, accountID, projectID)
	if err != nil {
		return App{}, fmt.Errorf("load project service aliases: %w", err)
	}
	var target App
	deleted := false
	for _, app := range apps {
		if app.AccountID != accountID || app.ProjectID != projectID || app.PreviewOfSlug != "" || !api.SameServiceName(app.WorkloadName, name) {
			continue
		}
		if app.Status == AppDeleted {
			deleted = true
			continue
		}
		if target.ID != "" {
			return App{}, fmt.Errorf("ambiguous project service: %w", ErrConflict)
		}
		target = app
	}
	if target.ID == "" && deleted {
		return App{}, ErrProjectServiceDeleted
	}
	if target.ID == "" {
		return App{}, ErrNotFound
	}
	return target, nil
}

// ProjectServiceDeploymentTargets keeps graph evidence keyed by repository
// service names while selecting deployments by their actual app allocations.
// A nil result rejects ambiguous project aliases rather than dropping a pin.
func ProjectServiceDeploymentTargets(caller App, apps []App, deployments map[string]string) map[string]string {
	targets := map[string]string{}
	for _, binding := range caller.Manifest.ServiceBindings {
		slug := binding.Service
		matched := false
		for _, app := range apps {
			if caller.ProjectID != "" && app.AccountID == caller.AccountID && app.ProjectID == caller.ProjectID && app.PreviewOfSlug == "" && app.Status != AppDeleted && api.SameServiceName(app.WorkloadName, binding.Service) {
				if matched {
					return nil
				}
				slug, matched = app.Slug, true
			}
		}
		if id := deployments[slug]; id != "" {
			targets[binding.Service] = id
		}
	}
	return targets
}
