package projectsetup

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

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

// AppSlugs validates exact repository keys, preserves existing members, and
// suggests project-prefixed names only for migration-mode scans.
func AppSlugs(workloads []reposcan.Workload, apps []state.App, projectID, project string, overrides map[string]string, migration bool) (map[string]string, *api.Problem) {
	known := map[string]bool{}
	for _, w := range workloads {
		known[w.Name] = true
	}
	for name, slug := range overrides {
		if slug == "" {
			return nil, api.ErrValidation("choose a nonempty Gregale app name for each mapped service")
		}
		if !known[name] {
			return nil, api.ErrValidation("app_slugs must target exact repository service names")
		}
	}
	resolved := map[string]string{}
	for _, w := range reconcile.PreserveAppSlugs(workloads, apps, projectID) {
		if slug, ok := overrides[w.Name]; ok {
			resolved[w.Name] = slug
		} else if w.AppSlug != "" {
			resolved[w.Name] = w.AppSlug
		} else if migration {
			resolved[w.Name] = SuggestedAppSlug(project, w.Name)
		}
	}
	return resolved, nil
}

func SetAppSlugs(workloads []reposcan.Workload, slugs map[string]string) {
	for i := range workloads {
		if slug, ok := slugs[workloads[i].Name]; ok {
			workloads[i].AppSlug = slug
		}
	}
}

// PreserveLegacyMigrationService recognizes the single generic service alias
// emitted before migration scans retained repository identities. Existing
// members and explicit old selectors must remain resumable after an upgrade.
func PreserveLegacyMigrationService(workloads []reposcan.Workload, apps []state.App, projectID, project string, only map[string]bool) {
	if len(workloads) != 1 {
		return
	}
	w := &workloads[0]
	generic := w.Source == "root-floor" && w.Name == "app" || w.DetectedBy.Detector == "procfile" && w.Name == "web"
	if !generic || w.Name == project {
		return
	}
	for _, app := range apps {
		if projectID != "" && app.ProjectID == projectID && app.WorkloadName == project && app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			w.Name, w.AppSlug = project, app.Slug
			return
		}
	}
	if only[strings.ToLower(project)] && !only[strings.ToLower(w.Name)] && (w.RootDir == "" || !only[strings.ToLower(w.RootDir)]) {
		w.Name, w.AppSlug = project, project
	}
}
