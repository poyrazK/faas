package mcpmanagement

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"sort"
)

func discoverEnvironments(ctx context.Context, client *api.Client) ([]EnvironmentGrant, error) {
	projects, err := client.ListProjects(ctx)
	var apiErr *api.APIError
	// Older APIs and app-only credentials can still connect with default access.
	if errors.As(err, &apiErr) && (apiErr.Problem.Status == 403 || apiErr.Problem.Status == 404) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(projects) > maxOAuthEntries {
		return nil, invalid("response_too_large", "too many projects to review in one connection")
	}
	apps, err := client.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	identities := map[string]string{}
	for _, a := range apps {
		identities[a.Slug] = a.ID
	}
	grants := []EnvironmentGrant{}
	for _, project := range projects {
		if validateSegment(project.Slug, "project") != nil || project.ID == "" {
			return nil, invalid("invalid_response", "invalid project identity")
		}
		p, err := client.GetProject(ctx, project.Slug)
		if err != nil {
			return nil, err
		}
		if p.ID != project.ID {
			return nil, invalid("invalid_response", "project identity changed during review")
		}
		members := map[string]string{}
		for _, w := range p.Workloads {
			id := identities[w.Slug]
			if id == "" {
				return nil, invalid("invalid_response", "project workload is missing from the account")
			}
			members[w.Slug] = id
		}
		environments, err := client.ListProjectEnvironments(ctx, project.Slug)
		if err != nil {
			return nil, err
		}
		for _, e := range environments {
			if !api.ValidProjectEnvironmentSlug(e.Slug) || e.ProjectID != p.ID || e.ID == "" {
				return nil, invalid("invalid_response", "invalid environment registry identity")
			}
			grants = append(grants, EnvironmentGrant{Project: p.Slug, ProjectID: p.ID, Environment: e.Slug, EnvironmentID: e.ID, Apps: members})
			if len(grants) > maxOAuthEntries {
				return nil, invalid("response_too_large", "too many environments to review in one connection")
			}
		}
	}
	sort.Slice(grants, func(i, j int) bool {
		return grants[i].Project+"/"+grants[i].Environment < grants[j].Project+"/"+grants[j].Environment
	})
	return grants, nil
}

func selectedEnvironments(req *http.Request, available []EnvironmentGrant, apps map[string]string, writes bool) (*EnvironmentConsent, error) {
	consent := &EnvironmentConsent{DefaultWrite: writes && req.PostForm.Get("default_write") == "yes", Named: map[string]EnvironmentGrant{}}
	for _, key := range req.PostForm["environments"] {
		found := false
		for _, g := range available {
			if key != g.Project+"/"+g.Environment {
				continue
			}
			g.Apps = cloneGrantedApps(g.Apps, apps)
			g.Write = writes && containsFormValue(req, "environment_writes", key)
			consent.Named[key], found = g, true
			break
		}
		if !found {
			return nil, errors.New("unknown environment")
		}
	}
	for _, key := range req.PostForm["environment_writes"] {
		if _, ok := consent.Named[key]; !ok || !writes {
			return nil, errors.New("environment write requires read consent and connection writes")
		}
	}
	return consent, consent.validate()
}

func cloneGrantedApps(members, allowed map[string]string) map[string]string {
	out := map[string]string{}
	for slug, id := range members {
		if allowed == nil || allowed[slug] == id {
			out[slug] = id
		}
	}
	return out
}

func containsFormValue(req *http.Request, name, value string) bool {
	for _, v := range req.PostForm[name] {
		if v == value {
			return true
		}
	}
	return false
}
