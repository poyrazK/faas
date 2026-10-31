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
	change, ok := decodeLabelChange(w, r)
	if !ok {
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
	s.writeLabelsResult(w, r, acct, "app", app.ID, change, next, err)
}

// writeLabelsResult answers a label write of any kind and audits a
// successful one.
func (s *server) writeLabelsResult(w http.ResponseWriter, r *http.Request, acct state.Account, kind, id string, change labels.Change, next map[string]string, err error) {
	switch {
	case resourcelabels.Problem(err) != nil:
		api.WriteProblem(w, resourcelabels.Problem(err))
		return
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such "+kind)
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "labels_changed", "Labels changed",
			"another request changed this "+kind+"'s labels; retry"))
		return
	case err != nil:
		s.log.ErrorContext(r.Context(), "update labels", "kind", kind, "id", id, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not update labels"))
		return
	}
	s.audit.Emit(r.Context(), "resource.labels.changed", &acct.ID, map[string]any{
		"kind": kind, kind + "_id": id, "set": labelKeys(change.Set), "removed": change.Remove,
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

// resourceLabelStore is the store's label methods for databases, crons and
// jobs; false when the store has none.
func (s *server) resourceLabelStore() (state.ResourceLabelStore, bool) {
	store, ok := s.store.(state.ResourceLabelStore)
	return store, ok
}

// decodeLabelChange reads and checks a PATCH .../labels body, answering the
// request itself when the body is refused.
func decodeLabelChange(w http.ResponseWriter, r *http.Request) (labels.Change, bool) {
	var body api.LabelChange
	if err := decodeJSON(r, &body); err != nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"set\": {key: value}, \"remove\": [key]}"))
		return labels.Change{}, false
	}
	change := labels.Change(body)
	if err := change.Validate(); err != nil {
		api.WriteProblem(w, resourcelabels.Problem(err))
		return labels.Change{}, false
	}
	return change, true
}

// updateResourceLabels writes the labels of a database, cron or job the
// caller has been shown to own.
func (s *server) updateResourceLabels(w http.ResponseWriter, r *http.Request, acct state.Account, kind state.LabelKind, id string, change labels.Change) {
	store, ok := s.resourceLabelStore()
	if !ok {
		api.WriteProblem(w, errLabelsUnavailable())
		return
	}
	next, err := store.UpdateResourceLabels(r.Context(), kind, id, change)
	s.writeLabelsResult(w, r, acct, string(kind), id, change, next, err)
}

// updateDatabaseLabels is PATCH /v1/postgres/databases/{id}/labels.
func (s *server) updateDatabaseLabels(w http.ResponseWriter, r *http.Request, acct state.Account) {
	change, ok := decodeLabelChange(w, r)
	if !ok {
		return
	}
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	database, err := s.managedPostgres.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.updateResourceLabels(w, r, acct, state.LabelKindDatabase, database.ID, change)
}

// updateCronLabels is PATCH /v1/crons/{id}/labels. Ownership is checked as
// updateCron checks it, with one 404 for a missing and a foreign cron.
func (s *server) updateCronLabels(w http.ResponseWriter, r *http.Request, acct state.Account) {
	change, ok := decodeLabelChange(w, r)
	if !ok {
		return
	}
	c, err := s.store.CronByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	if app, err := s.store.AppByID(r.Context(), c.AppID); err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	s.updateResourceLabels(w, r, acct, state.LabelKindCron, c.ID, change)
}

// updateJobLabels is PATCH /v1/jobs/{name}/labels.
func (s *server) updateJobLabels(w http.ResponseWriter, r *http.Request, acct state.Account) {
	change, ok := decodeLabelChange(w, r)
	if !ok {
		return
	}
	job, ok, err := s.resolveJob(r.Context(), r.PathValue("name"), acct)
	if err != nil {
		s.log.ErrorContext(r.Context(), "update job labels: resolve job", "account", acct.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not update labels"))
		return
	}
	if !ok {
		s.notFound(w, "no such job")
		return
	}
	s.updateResourceLabels(w, r, acct, state.LabelKindJob, job.ID, change)
}

// labelFilter is a list's labels by ID and the selector it was asked for.
// The zero value keeps every item and carries no labels.
type labelFilter struct {
	sel  labels.Selector
	byID map[string]map[string]string
}

func (f labelFilter) keep(id string) bool { return f.sel.Matches(f.byID[id]) }

// labelsOf returns an item's labels; nil when it has none or labels are off.
func (f labelFilter) labelsOf(id string) map[string]string { return f.byID[id] }

// listLabels reads the labels of a list of databases, crons or jobs and
// applies ?selector=, as appListLabels does for apps: while the capability is
// off for the caller the list keeps every item with no labels and a selector
// is refused, and a failed read drops labels from a plain list and refuses a
// selected one.
func (s *server) listLabels(r *http.Request, acct state.Account, kind state.LabelKind, ids []string) (labelFilter, *api.Problem) {
	selected := resourcelabels.Requested(r.URL.Query())
	if !s.labelsReleased(r, acct) {
		if selected {
			return labelFilter{}, resourcelabels.NotReleased(s.capGate.Name(resourcelabels.Capability))
		}
		return labelFilter{}, nil
	}
	sel, problem := resourcelabels.ParseSelector(r.URL.Query())
	if problem != nil {
		return labelFilter{}, problem
	}
	store, ok := s.resourceLabelStore()
	switch {
	case !ok && selected:
		return labelFilter{}, errLabelsUnavailable()
	case !ok:
		return labelFilter{}, nil
	}
	byID, err := store.ResourceLabelsByID(r.Context(), kind, ids)
	if err != nil {
		s.log.WarnContext(r.Context(), "apid: resource labels", "kind", kind, "account", acct.ID, "err", err)
		if selected {
			return labelFilter{}, api.ErrCapacity("could not read labels")
		}
		return labelFilter{}, nil
	}
	return labelFilter{sel: sel, byID: byID}, nil
}

// resourceLabels reads one database's, cron's or job's labels for its GET
// response; nil while the capability is off or the read fails.
func (s *server) resourceLabels(r *http.Request, acct state.Account, kind state.LabelKind, id string) map[string]string {
	if !s.labelsReleased(r, acct) {
		return nil
	}
	store, ok := s.resourceLabelStore()
	if !ok {
		return nil
	}
	l, err := store.ResourceLabels(r.Context(), kind, id)
	if err != nil {
		s.log.WarnContext(r.Context(), "apid: resource labels", "kind", kind, "id", id, "err", err)
		return nil
	}
	return l
}
