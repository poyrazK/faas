package apidserver

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appNameReportMaxMatches bounds GET /v1/admin/app-names/report.
const appNameReportMaxMatches = 5000

// blockedAppNameProblem refuses slug as a new app name when app_name_blocklist
// matches it. The answer is the slug-taken conflict, so a CLI deploy takes a
// suffixed alternative instead (the CLI suffixes any 409 for a name it does
// not hold); one that matches the same entry is refused too.
func (s *server) blockedAppNameProblem(accountID, slug string) *api.Problem {
	entry, blocked := api.MatchAppNameBlocklist(slug, s.runtimeConfig.appNameBlocklist())
	if !blocked {
		return nil
	}
	if s.log != nil {
		s.log.Info("apid: app name refused by app_name_blocklist", "slug", slug, "entry", entry, "account_id", accountID)
	}
	return api.NewProblem(http.StatusConflict, api.CodeValidation, "Slug taken",
		fmt.Sprintf("app name %q is not available; choose another name", slug))
}

// appNameReport is GET /v1/admin/app-names/report: the existing apps whose
// names app_name_blocklist matches, for review. It changes nothing. With
// ?blocklist=<entries> it reports against that list instead of the live one,
// so an operator can see a list's effect before setting it.
func (s *server) appNameReport(w http.ResponseWriter, r *http.Request, _ state.Account) {
	resp := api.AppNameReportResponse{Source: "live", Matches: []api.AppNameReportMatch{}}
	entries := s.runtimeConfig.appNameBlocklist()
	if preview := strings.TrimSpace(r.URL.Query().Get("blocklist")); preview != "" {
		parsed, err := api.ParseAppNameBlocklist(preview)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad blocklist", err.Error()))
			return
		}
		entries, resp.Source = parsed, "preview"
	}
	resp.Entries = len(entries)
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	apps, err := s.store.ListAllApps(r.Context())
	if err != nil {
		s.log.Warn("apid: app name report: list apps", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list apps"))
		return
	}
	for _, app := range apps {
		entry, ok := api.MatchAppNameBlocklist(app.Slug, entries)
		if !ok {
			continue
		}
		if len(resp.Matches) == appNameReportMaxMatches {
			resp.Truncated = true
			break
		}
		resp.Matches = append(resp.Matches, api.AppNameReportMatch{
			Slug: app.Slug, AppID: app.ID, AccountID: app.AccountID, OrgID: app.OrgID,
			Status: string(app.Status), Entry: entry,
		})
	}
	sort.Slice(resp.Matches, func(i, j int) bool { return resp.Matches[i].Slug < resp.Matches[j].Slug })
	writeJSON(w, http.StatusOK, resp)
}
