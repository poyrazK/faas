package sales

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type reportWindow struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
	Owner    string `json:"owner"`
}

func parseReportWindow(q url.Values, now time.Time) (reportWindow, bool) {
	w := reportWindow{From: q.Get("from"), To: q.Get("to"), Timezone: q.Get("tz"), Owner: q.Get("owner")}
	if w.Timezone == "" {
		w.Timezone = "UTC"
	}
	if len(w.Timezone) > api.SalesFieldMaxBytes || w.Timezone == "Local" || (w.Owner != "" && !validID(w.Owner)) {
		return w, false
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return w, false
	}
	if w.From == "" {
		w.From = now.In(loc).Format("2006-01") + "-01"
	}
	if w.To == "" {
		w.To = now.In(loc).Format(time.DateOnly)
	}
	from, err := time.Parse(time.DateOnly, w.From)
	if err != nil {
		return w, false
	}
	to, err := time.Parse(time.DateOnly, w.To)
	if err != nil || from.Year() < api.SalesTaskMinYear || to.Year() > api.SalesTaskMaxYear || to.Before(from) || int(to.Sub(from)/(24*time.Hour))+1 > api.SalesReportMaxDays {
		return w, false
	}
	return w, true
}

func (s *Service) Reports(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return
	}
	if !scope.All {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Manager access required", "Sales reports are available to sales managers."))
		return
	}
	window, ok := parseReportWindow(r.URL.Query(), time.Now())
	if !ok {
		invalid(w, "Choose valid dates from 2000 through 2100, at most 366 days, a valid reporting time zone and salesperson ID.")
		return
	}
	store, ok := s.Store.(referrals.ReportingStore)
	if !ok {
		pipelineError(w, errors.New("reporting store unavailable"))
		return
	}
	emails := make([]string, 0, len(s.Config.Members))
	for email := range s.Config.Members {
		emails = append(emails, email)
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	report, err := store.ManagerReport(ctx, scope, referrals.ReportFilter{From: window.From, To: window.To, Timezone: window.Timezone, Owner: window.Owner, MemberEmails: emails})
	if err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.report_read", acct.ID, map[string]any{"from": window.From, "to": window.To, "timezone": window.Timezone, "owner_id": window.Owner})
	writeJSON(w, struct {
		Window reportWindow `json:"window"`
		referrals.ManagerReport
	}{Window: window, ManagerReport: report})
}
