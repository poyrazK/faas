package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type ProjectServiceReader interface {
	AppsForProject(context.Context, string, string) ([]App, error)
	AppBySlug(context.Context, string) (App, error)
}

type projectServiceLookup interface {
	ProjectAppByWorkload(context.Context, string, string, string) (App, error)
}

// AppByServiceName resolves a production project's repository aliases before
// the established global app-slug fallback. Preview and scenario routing keep
// their separate environment-aware owners.
func AppByServiceName(ctx context.Context, reader ProjectServiceReader, caller App, service string) (App, error) {
	if caller.ProjectID != "" && caller.PreviewOfSlug == "" {
		app, err := ProjectMemberByWorkload(ctx, reader, caller.AccountID, caller.ProjectID, service)
		if !errors.Is(err, ErrNotFound) {
			return app, err
		}
	}
	return reader.AppBySlug(ctx, service)
}

// ProjectMemberByWorkload returns the live production member of a project
// named by its repository service name. ErrConflict reports an ambiguous name.
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
	for _, app := range apps {
		if app.AccountID == accountID && app.ProjectID == projectID &&
			app.Status != AppDeleted && app.PreviewOfSlug == "" && strings.EqualFold(app.WorkloadName, name) {
			if target.ID != "" {
				return App{}, fmt.Errorf("ambiguous project service: %w", ErrConflict)
			}
			target = app
		}
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
			if caller.ProjectID != "" && app.AccountID == caller.AccountID && app.ProjectID == caller.ProjectID && app.PreviewOfSlug == "" && app.Status != AppDeleted && strings.EqualFold(app.WorkloadName, binding.Service) {
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
