package apidserver

import (
	"errors"
	"maps"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/resourcelabels"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/state"
)

// labelsReleased decides the resource-labels capability for a request that
// names no app (ADR-20261031100025). Without a gate every capability is on.
func (s *server) labelsReleased(r *http.Request, acct state.Account) bool {
	return s.capGate == nil || s.capGate.Enabled(resourcelabels.Capability, flagSubject(r, acct))
}

// appLabelStore is the store's label methods; false when the store has none.
func (s *server) appLabelStore() (state.AppLabelStore, bool) {
	store, ok := s.store.(state.AppLabelStore)
	return store, ok
}

// errLabelsUnavailable answers a label read or write the store cannot serve.
func errLabelsUnavailable() *api.Problem {
	return api.ErrCapacity("labels are not available")
}

// updateAppLabels is PATCH /v1/apps/{slug}/labels: set and remove labels in
// one atomic write and return every label the app carries after it.
func (s *server) updateAppLabels(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var body api.LabelChange
	if err := decodeJSON(r, &body); err != nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"set\": {key: value}, \"remove\": [key]}"))
		return
	}
	change := labels.Change(body)
	if err := change.Validate(); err != nil {
		api.WriteProblem(w, resourcelabels.Problem(err))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.appLabelStore()
	if !ok {
		api.WriteProblem(w, errLabelsUnavailable())
		return
	}
	next, err := store.UpdateAppLabels(r.Context(), app.ID, change)
	switch {
	case resourcelabels.Problem(err) != nil:
		api.WriteProblem(w, resourcelabels.Problem(err))
		return
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such app")
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "labels_changed", "Labels changed",
			"another request changed this app's labels; retry"))
		return
	case err != nil:
		s.log.ErrorContext(r.Context(), "update app labels", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not update labels"))
		return
	}
	s.audit.Emit(r.Context(), "resource.labels.changed", &acct.ID, map[string]any{
		"kind": "app", "app_id": app.ID, "set": labelKeys(change.Set), "removed": change.Remove,
	})
	writeJSON(w, http.StatusOK, api.LabelsResponse{Labels: next})
}

// appListLabels reads the labels of a list of apps and applies ?selector=.
// While the capability is off for the caller the list is returned as it is
// with no labels, and a selector is refused. A failed read drops labels from
// a plain list and refuses a selected one.
func (s *server) appListLabels(r *http.Request, acct state.Account, apps []state.App) ([]state.App, map[string]map[string]string, *api.Problem) {
	selected := resourcelabels.Requested(r.URL.Query())
	if !s.labelsReleased(r, acct) {
		if selected {
			return nil, nil, resourcelabels.NotReleased(s.capGate.Name(resourcelabels.Capability))
		}
		return apps, nil, nil
	}
	sel, problem := resourcelabels.ParseSelector(r.URL.Query())
	if problem != nil {
		return nil, nil, problem
	}
	store, ok := s.appLabelStore()
	switch {
	case !ok && selected:
		return nil, nil, errLabelsUnavailable()
	case !ok:
		return apps, nil, nil
	}
	byApp, err := resourcelabels.AppLabels(r.Context(), store, apps)
	if err != nil {
		s.log.WarnContext(r.Context(), "apid: app labels", "account", acct.ID, "err", err)
		if selected {
			return nil, nil, api.ErrCapacity("could not read labels")
		}
		return apps, nil, nil
	}
	return resourcelabels.SelectApps(apps, byApp, sel), byApp, nil
}

// selectDomainsByLabels keeps the domains whose app matches ?selector=; a
// request without one keeps every domain.
func (s *server) selectDomainsByLabels(r *http.Request, acct state.Account, domains []state.CustomDomain) ([]state.CustomDomain, *api.Problem) {
	if !resourcelabels.Requested(r.URL.Query()) {
		return domains, nil
	}
	apps := make([]state.App, 0, len(domains))
	for _, d := range domains {
		apps = append(apps, state.App{ID: d.AppID})
	}
	matched, _, problem := s.appListLabels(r, acct, apps)
	if problem != nil {
		return nil, problem
	}
	keep := make(map[string]bool, len(matched))
	for _, a := range matched {
		keep[a.ID] = true
	}
	out := domains[:0:0]
	for _, d := range domains {
		if keep[d.AppID] {
			out = append(out, d)
		}
	}
	return out, nil
}

// singleAppLabels reads one app's labels for its GET response; nil while
// the capability is off or the read fails.
func (s *server) singleAppLabels(r *http.Request, acct state.Account, app state.App) map[string]string {
	if !s.capabilityEnabledFor(resourcelabels.Capability, acct, app) {
		return nil
	}
	store, ok := s.appLabelStore()
	if !ok {
		return nil
	}
	l, err := store.AppLabels(r.Context(), app.ID)
	if err != nil {
		s.log.WarnContext(r.Context(), "apid: app labels", "app_id", app.ID, "err", err)
		return nil
	}
	return l
}

func labelKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
