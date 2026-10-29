package reconcile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// AppSlug resolves an allocation name without changing repository identity.
func AppSlug(w reposcan.Workload) string {
	if w.AppSlug != "" {
		return w.AppSlug
	}
	return w.Name
}

// PreserveAppSlugs makes later scans and GitHub reconciliation retain the
// project's existing allocations. An explicit different slug is left in place
// so admission can reject a rename before any mutations.
func PreserveAppSlugs(workloads []reposcan.Workload, apps []state.App, projectID string) []reposcan.Workload {
	out := slices.Clone(workloads)
	var projectApps []state.App
	for _, app := range apps {
		if app.ProjectID == projectID && app.Status != state.AppDeleted && app.PreviewOfSlug == "" {
			projectApps = append(projectApps, app)
		}
	}
	// A renamed service keeps its previous name's app (ADR-20261029090000).
	renamed := RenamedWorkloads(workloads, projectApps)
	for i := range out {
		if out[i].AppSlug != "" || projectID == "" {
			continue
		}
		name := out[i].Name
		if previous := out[i].RenamedFrom; renamed[previous] == name {
			name = previous
		}
		for _, app := range projectApps {
			if app.WorkloadName == name {
				out[i].AppSlug = app.Slug
				break
			}
		}
	}
	return out
}

// SuggestedAppSlug is stable across scans. A digest preserves distinctions
// lost by normalization or truncation; availability remains authoritative.
func SuggestedAppSlug(project, name string) string {
	raw := project + "-" + name
	normalized := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(raw))
	normalized = strings.Trim(normalized, "-")
	if raw == normalized && len(normalized) <= api.AppSlugMaxLength && api.ValidAppSlug(normalized) && !api.IsReservedAppSlug(normalized) {
		return normalized
	}
	digest := sha256.Sum256([]byte(raw))
	suffix := fmt.Sprintf("-%x", digest[:4])
	if strings.HasPrefix(normalized, "tag-") || api.IsReservedAppSlug(normalized) || normalized == "" {
		normalized = "app-" + normalized
	}
	if len(normalized) > api.AppSlugMaxLength-len(suffix) {
		normalized = normalized[:api.AppSlugMaxLength-len(suffix)]
	}
	return strings.TrimRight(normalized, "-") + suffix
}

// AllocateAppSlugs preserves existing members' slugs and gives a new workload
// a project-prefixed slug when its bare name cannot be one: reserved, not a
// valid slug, or held by another app (ADR-20261022120205). A workload with an
// explicit slug is left alone. A name held by one of the account's own apps in
// its delete grace window is not stepped around, so admission keeps offering
// the restore.
func AllocateAppSlugs(ctx context.Context, lookup SlugLookup, workloads []reposcan.Workload, accountApps []state.App, accountID, projectID, projectSlug string) ([]reposcan.Workload, error) {
	out := PreserveAppSlugs(workloads, accountApps, projectID)
	if projectSlug == "" {
		return out, nil
	}
	accountSlugs := make(map[string]state.App, len(accountApps))
	for _, app := range accountApps {
		accountSlugs[app.Slug] = app
	}
	for i := range out {
		if out[i].AppSlug != "" {
			continue
		}
		free, err := bareAppSlugFree(ctx, lookup, out[i].Name, accountSlugs, accountID)
		if err != nil {
			return nil, err
		}
		if !free {
			out[i].AppSlug = SuggestedAppSlug(projectSlug, out[i].Name)
		}
	}
	return out, nil
}

func bareAppSlugFree(ctx context.Context, lookup SlugLookup, name string, accountSlugs map[string]state.App, accountID string) (bool, error) {
	if !api.ValidAppSlug(name) || api.IsReservedAppSlug(name) {
		return false, nil
	}
	holder, held := accountSlugs[name]
	if !held && lookup != nil {
		app, err := lookup(ctx, name)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return false, fmt.Errorf("reconcile: look up app slug %q: %w", name, err)
		}
		holder, held = app, err == nil
	}
	return !held || holder.AccountID == accountID && holder.Status == state.AppDeleted, nil
}
