package dashboard

import "github.com/onebox-faas/faas/pkg/api"

// AppLogSearchData is the /dashboard/apps/{slug}/log-search page
// (ADR-20261018083536): a filter form, facet counts for the window, and one
// page of matching events.
type AppLogSearchData struct {
	Enabled       bool
	AppSlug       string
	Form          LogSearchForm
	SourceOptions []string
	// Error is the validation or read problem to show instead of results.
	Error   string
	Results *api.LogEventsResponse
	Facets  *api.LogEventFacetsResponse
	// NextURL is the dashboard link for the next page; empty on the last.
	NextURL string
}

// LogSearchForm echoes the submitted filters back into the form.
type LogSearchForm struct {
	Q, Since, Level, Status, Route, Source string
}
