package hosting

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/hosting/activity"
	"github.com/onebox-faas/faas/pkg/state"
)

type ActivityHost interface {
	HostingActivityEnabled() bool
	HostingActivityReader() state.HostingActivityReader
}

func (s Service) activityHost(w http.ResponseWriter, account state.Account) (ActivityHost, bool) {
	if !s.guard(w, account) {
		return nil, false
	}
	h, ok := s.Host.(ActivityHost)
	a, automation := s.Host.(AutomationHost)
	if !ok || !h.HostingActivityEnabled() || h.HostingActivityReader() == nil || !automation || !a.HostingAutomationEnabled() || a.HostingAutomationStore() == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_activity_disabled", "Deployment activity unavailable", "internal deployment activity or its automation prerequisite is disabled or unconfigured"))
		return nil, false
	}
	return h, true
}
func (s Service) ListActivity(w http.ResponseWriter, r *http.Request, account state.Account) {
	h, ok := s.activityHost(w, account)
	if !ok {
		return
	}
	before, valid := activityQuery(r)
	if !valid {
		api.WriteProblem(w, api.ErrValidation("only one optional bounded before cursor is accepted"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.HostingActivityTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	site, ok := s.site(w, r, account)
	if !ok {
		return
	}
	result, err := s.collectActivity(r, account, site, h.HostingActivityReader(), before)
	if err != nil {
		failure(w, err)
		return
	}
	if _, ok = s.activityHost(w, account); !ok {
		return
	}
	httpkit.WriteJSON(w, 200, result)
}
func activityQuery(r *http.Request) (string, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 {
		return "", false
	}
	values, exists := query["before"]
	if !exists {
		return "", len(query) == 0
	}
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	_, _, err = activity.ParseCursor(values[0])
	return values[0], err == nil
}
func (s Service) collectActivity(r *http.Request, account state.Account, site api.HostingSite, reader state.HostingActivityReader, before string) (api.HostingActivityPage, error) {
	var caps api.HostingCapabilities
	s.extendCapabilities(r, &caps)
	records, err := reader.ListHostingActivityRecords(r.Context(), account.ID, site.ID, before)
	if err != nil {
		return api.HostingActivityPage{}, err
	}
	if len(records.Records) > api.HostingActivityPageSize {
		return api.HostingActivityPage{}, state.ErrConflict
	}
	if _, _, err = activity.ParseCursor(records.NextCursor); err != nil {
		return api.HostingActivityPage{}, state.ErrConflict
	}
	now := time.Now().UTC()
	result := api.HostingActivityPage{SiteID: site.ID, Entries: []api.HostingActivityEntry{}, NextCursor: records.NextCursor, ObservedAt: now, ServingHealth: "not_checked", SourceLabelsIncluded: caps.HistoryEnabled}
	for _, row := range records.Records {
		if row.Run.SiteID != site.ID {
			return api.HostingActivityPage{}, state.ErrConflict
		}
		entry := activity.Summarize(site, row.Run, row.Release, row.Preview, caps.PreviewsEnabled && site.DurableRouting, caps.HistoryEnabled, now)
		entry.CanCancel = caps.CancellationEnabled && site.Status == "active" && site.DurableRouting && !row.Run.Canceled && site.ActiveReleaseID != row.Run.ReleaseID && !row.OtherPreviews && (row.Release == nil || row.Release.FirstPromotedAt == nil)
		result.Entries = append(result.Entries, entry)
	}
	current, err := s.Host.HostingStore().GetHostingSite(r.Context(), account.ID, site.ID)
	if err != nil {
		return api.HostingActivityPage{}, err
	}
	if current.Revision != site.Revision || current.Status != site.Status || current.Suspended != site.Suspended || current.OriginID != site.OriginID {
		return api.HostingActivityPage{}, state.ErrConflict
	}
	var latest api.HostingCapabilities
	s.extendCapabilities(r, &latest)
	if caps.CancellationEnabled != latest.CancellationEnabled || caps.HistoryEnabled != latest.HistoryEnabled || caps.PreviewsEnabled != latest.PreviewsEnabled {
		return api.HostingActivityPage{}, state.ErrConflict
	}
	return result, nil
}
