package githubd

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// previewBuildURLHosts maps the public host of each production app in a PR
// environment to the host of its preview in that environment
// (ADR-20261105090226). previews[i] previews production[i].
func previewBuildURLHosts(production, previews []state.App) map[string]string {
	hosts := make(map[string]string, len(production))
	for i := range production {
		if i >= len(previews) || production[i].Slug == "" || previews[i].Slug == "" {
			continue
		}
		hosts[strings.ToLower(previewHostnameForSlug(production[i].Slug))] = previewHostnameForSlug(previews[i].Slug)
	}
	return hosts
}

// rewritePreviewBuildURLs points a frontend's public build URLs at the
// previews of the same pull request. A frontend baked with a production API
// URL sent every browser call from its preview to production: reviewers
// changed production data, and the PR's own API was never exercised. A URL
// on any other host is kept.
func rewritePreviewBuildURLs(urls, hosts map[string]string) map[string]string {
	if len(urls) == 0 {
		return nil
	}
	out := make(map[string]string, len(urls))
	for key, value := range urls {
		out[key] = value
		parsed, err := url.Parse(value)
		if err != nil {
			continue
		}
		if preview, ok := hosts[strings.ToLower(parsed.Host)]; ok {
			parsed.Host = preview
			out[key] = parsed.String()
		}
	}
	return out
}

// syncPreviewBuildURLs gives a preview its production app's public build
// URLs, rewritten to this pull request's previews, before its build is
// queued; a deployment captures them when it is created.
func (s *Service) syncPreviewBuildURLs(ctx context.Context, preview, production state.App, hosts map[string]string) (state.App, error) {
	desired := rewritePreviewBuildURLs(production.Manifest.PublicBuildURLs, hosts)
	if maps.Equal(desired, preview.Manifest.PublicBuildURLs) {
		return preview, nil
	}
	store, ok := s.Reconcile.Store.(state.PublicBuildURLStore)
	if !ok {
		return preview, nil
	}
	if err := store.SetAppPublicBuildURLs(ctx, preview.AccountID, preview.ID, desired); err != nil {
		return preview, fmt.Errorf("githubd: set preview build URLs for %q: %w", preview.Slug, err)
	}
	preview.Manifest.PublicBuildURLs = desired
	return preview, nil
}
