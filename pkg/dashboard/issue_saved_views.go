package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
)

type IssueSavedViewLink struct{ Key, Name, URL string }
type IssueSavedViewsView struct {
	AppSlug        string
	CSRF           string
	CurrentKey     string
	CurrentFilters api.IssueSavedViewFilters
	Items          []IssueSavedViewLink
	Updated        bool
	MaxViews       int
}

func IssueSavedViewLinks(slug string, views []api.IssueSavedView) []IssueSavedViewLink {
	out := make([]IssueSavedViewLink, 0, len(views))
	for _, view := range views {
		out = append(out, IssueSavedViewLink{Key: view.Key, Name: view.Name, URL: "/dashboard/apps/" + url.PathEscape(slug) + "/issues?" + url.Values{"view": {view.Key}}.Encode()})
	}
	return out
}
