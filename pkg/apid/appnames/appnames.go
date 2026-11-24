// Package appnames applies the app_name_blocklist runtime setting: the
// refusal a new app name gets when the list matches it, and the operator
// report of existing apps a list matches. The list binds only allocation
// (create, rename, a new project workload); an app that already has a name
// keeps it.
package appnames

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReportMaxMatches bounds GET /v1/admin/app-names/report.
const ReportMaxMatches = 5000

// Refusal is the problem for a new app name that blocklist matches, or nil
// when the name is free of it. Callers validate the slug first: a malformed
// name is refused as invalid, not as unavailable. The answer is a 409 with
// api.CodeAppNameUnavailable, so a CLI deploy takes a suffixed alternative
// (it suffixes any 409 for a name it does not hold).
func Refusal(log *slog.Logger, blocklist []string, accountID, slug string) *api.Problem {
	entry, blocked := api.MatchAppNameBlocklist(slug, blocklist)
	if !blocked {
		return nil
	}
	if log != nil {
		log.Info("apid: app name refused by app_name_blocklist",
			"slug", logsanitize.Field(slug), "entry", entry, "account_id", accountID)
	}
	return api.NewProblem(http.StatusConflict, api.CodeAppNameUnavailable, "App name unavailable",
		fmt.Sprintf("app name %q is not available; choose another name", slug))
}

// Reporter serves GET /v1/admin/app-names/report.
type Reporter struct {
	// Blocklist is the live app_name_blocklist.
	Blocklist func() []string
	// ListApps and ListDeletedApps read every app and every tombstone.
	ListApps        func(context.Context) ([]state.App, error)
	ListDeletedApps func(context.Context) ([]state.App, error)
	Log             *slog.Logger
	Now             func() time.Time
}

// ServeHTTP lists the existing apps whose names the live list matches, for
// review; it changes nothing. With ?blocklist=<entries> it reports against
// that list instead, so an operator sees a list's effect before setting it.
// Deleted apps that can still be restored are listed too: a restore brings
// the name back.
func (rp Reporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	entries, source := rp.Blocklist(), "live"
	if preview := strings.TrimSpace(r.URL.Query().Get("blocklist")); preview != "" {
		parsed, err := api.ParseAppNameBlocklist(preview)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad blocklist", err.Error()))
			return
		}
		entries, source = parsed, "preview"
	}
	var apps []state.App
	if len(entries) > 0 {
		var err error
		if apps, err = rp.apps(r.Context()); err != nil {
			if rp.Log != nil {
				rp.Log.Warn("apid: app name report: list apps", "err", err)
			}
			api.WriteProblem(w, api.ErrCapacity("could not list apps"))
			return
		}
	}
	httpkit.WriteJSON(w, http.StatusOK, Report(apps, entries, source))
}

// apps is every live app plus the tombstones a restore can bring back.
func (rp Reporter) apps(ctx context.Context) ([]state.App, error) {
	apps, err := rp.ListApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	deleted, err := rp.ListDeletedApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list deleted apps: %w", err)
	}
	now := time.Now()
	if rp.Now != nil {
		now = rp.Now()
	}
	for _, app := range deleted {
		if app.DeleteGraceUntil == nil || app.DeleteGraceUntil.After(now) {
			apps = append(apps, app)
		}
	}
	return apps, nil
}

// Report matches apps against entries. Matches are sorted by slug before the
// list is cut at ReportMaxMatches, so a truncated report is the first rows
// alphabetically.
func Report(apps []state.App, entries []string, source string) api.AppNameReportResponse {
	resp := api.AppNameReportResponse{Source: source, Entries: len(entries), Matches: []api.AppNameReportMatch{}}
	for _, app := range apps {
		entry, ok := api.MatchAppNameBlocklist(app.Slug, entries)
		if !ok {
			continue
		}
		resp.Matches = append(resp.Matches, api.AppNameReportMatch{
			Slug: app.Slug, AppID: app.ID, AccountID: app.AccountID, OrgID: app.OrgID,
			Status: string(app.Status), Entry: entry,
		})
	}
	sort.Slice(resp.Matches, func(i, j int) bool {
		if resp.Matches[i].Slug != resp.Matches[j].Slug {
			return resp.Matches[i].Slug < resp.Matches[j].Slug
		}
		return resp.Matches[i].AppID < resp.Matches[j].AppID
	})
	if len(resp.Matches) > ReportMaxMatches {
		resp.Matches, resp.Truncated = resp.Matches[:ReportMaxMatches], true
	}
	return resp
}
