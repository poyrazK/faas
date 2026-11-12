package sales

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) activityStore(w http.ResponseWriter, r *http.Request, acct state.Account) (referrals.ActivityStore, referrals.Scope, bool) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return nil, scope, false
	}
	store, ok := s.Store.(referrals.ActivityStore)
	if !ok {
		unavailable(w)
	}
	return store, scope, ok
}
func (s *Service) Activities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.activityStore(w, r, acct)
	if !ok {
		return
	}
	lead := r.PathValue("lead")
	if !validID(lead) {
		invalid(w, "Choose a valid lead.")
		return
	}
	audience := pipelineAudience("activity", scope, lead)
	f, err := s.activityFilter(r, audience)
	if err != nil {
		invalid(w, "invalid activity cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	prospect, rows, err := store.LeadActivity(ctx, scope, lead, f)
	if err != nil {
		pipelineError(w, err)
		return
	}
	next := ""
	if len(rows) > api.SalesPageSize {
		rows = rows[:api.SalesPageSize]
		last := rows[len(rows)-1]
		next, err = s.encodeCursor(last.OccurredAt.UTC().Format(time.RFC3339Nano)+"|"+last.ID, audience)
	}
	if err != nil {
		unavailable(w)
		return
	}
	s.emit(ctx, "sales.activity_read", acct.ID, map[string]any{"lead_id": lead, "team_scope": scope.All})
	writeJSON(w, map[string]any{"lead": map[string]any{"id": prospect.ID, "name": prospect.Name, "email": prospect.Email, "company": prospect.Company, "stage": prospect.Stage, "owner_id": prospect.OwnerID, "owner_email": prospect.OwnerEmail, "version": prospect.Version}, "activities": rows, "next_cursor": next, "observed_at": time.Now().UTC()})
}
func (s *Service) activityFilter(r *http.Request, audience string) (referrals.ActivityFilter, error) {
	f := referrals.ActivityFilter{Limit: api.SalesPageSize + 1}
	after, err := s.openCursor(r.URL.Query().Get("after"), audience)
	if err != nil || after == "" {
		return f, err
	}
	at, id, ok := strings.Cut(after, "|")
	f.AfterAt, err = time.Parse(time.RFC3339Nano, at)
	if !ok || !validID(id) || err != nil {
		return f, referrals.ErrNotFound
	}
	f.AfterID = id
	return f, nil
}
func normalizeActivity(in *referrals.ActivityInput) {
	in.Summary = strings.TrimSpace(in.Summary)
	in.Outcome = strings.TrimSpace(in.Outcome)
	in.NextStep = strings.TrimSpace(in.NextStep)
	in.FollowUpTitle = strings.TrimSpace(in.FollowUpTitle)
	in.OccurredAt = in.OccurredAt.UTC().Truncate(time.Microsecond)
}
func validActivity(in referrals.ActivityInput, lead string, now time.Time) bool {
	if !validID(in.ID) || in.LeadVersion <= 0 || (in.Kind != "call" && in.Kind != "email" && in.Kind != "demo" && in.Kind != "note") || in.OccurredAt.Year() < api.SalesTaskMinYear || in.OccurredAt.After(now) {
		return false
	}
	if !boundedText(in.Summary, api.SalesActivitySummaryMaxBytes, false) || !boundedText(in.Outcome, api.SalesActivityDetailMaxBytes, true) || !boundedText(in.NextStep, api.SalesActivityDetailMaxBytes, true) {
		return false
	}
	if in.FollowUpTitle == "" {
		return in.FollowUpOn == ""
	}
	return validTask(referrals.TaskInput{LeadID: lead, Title: in.FollowUpTitle, DueOn: in.FollowUpOn}, "")
}
func (s *Service) RecordActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.activityStore(w, r, acct)
	if !ok {
		return
	}
	lead := r.PathValue("lead")
	var in referrals.ActivityInput
	if !validID(lead) || !decode(w, r, &in) {
		invalid(w, "Choose a lead and provide an activity entry.")
		return
	}
	normalizeActivity(&in)
	if !validActivity(in, lead, time.Now().UTC()) {
		invalid(w, "Provide a unique activity ID, current lead version, call/email/demo/note, past or current occurrence time, summary up to 1000 bytes, outcome and next step up to 500 bytes, and a paired follow-up title/date when needed.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	id, err := store.RecordActivity(ctx, scope, lead, in)
	if err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.activity_recorded", acct.ID, map[string]any{"lead_id": lead, "activity_id": id, "kind": in.Kind})
	writeJSON(w, map[string]string{"id": id})
}
func (s *Service) staleDays() int32 {
	if s.Config.StaleDays < 1 {
		return api.SalesStaleDefaultDays
	}
	return s.Config.StaleDays
}
func (s *Service) Alerts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.activityStore(w, r, acct)
	if !ok {
		return
	}
	f, ok := s.alertFilter(w, r, scope)
	if !ok {
		return
	}
	audience := pipelineAudience("alerts", scope, f.Owner, f.Kind, f.On, strconv.Itoa(int(f.StaleDays)))
	var err error
	f.After, err = s.decodeCursor(r.URL.Query().Get("after"), audience)
	if err != nil {
		invalid(w, "invalid alert cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, counts, err := store.LeadAlerts(ctx, scope, f)
	if err != nil {
		pipelineError(w, err)
		return
	}
	next := ""
	if len(rows) > api.SalesPageSize {
		rows = rows[:api.SalesPageSize]
		next, err = s.encodeCursor(rows[len(rows)-1].ID, audience)
	}
	if err != nil {
		unavailable(w)
		return
	}
	s.emit(ctx, "sales.alerts_read", acct.ID, map[string]any{"team_scope": scope.All})
	writeJSON(w, map[string]any{"leads": rows, "summary": counts, "next_cursor": next, "on": f.On, "stale_days": f.StaleDays, "observed_at": f.AsOf})
}
func (s *Service) alertFilter(w http.ResponseWriter, r *http.Request, scope referrals.Scope) (referrals.AlertFilter, bool) {
	f := referrals.AlertFilter{Kind: r.URL.Query().Get("kind"), On: r.URL.Query().Get("on"), AsOf: time.Now().UTC(), StaleDays: s.staleDays(), Limit: api.SalesPageSize + 1}
	var ok bool
	f.Owner, ok = ownerFilter(w, r, scope)
	if !ok {
		return f, false
	}
	if f.Kind == "" {
		f.Kind = "all"
	}
	if f.On == "" {
		f.On = f.AsOf.Format(time.DateOnly)
	}
	day, err := time.Parse(time.DateOnly, f.On)
	if err != nil || day.Year() < api.SalesTaskMinYear || day.Year() > api.SalesTaskMaxYear || (f.Kind != "all" && f.Kind != "stale" && f.Kind != "overdue") {
		invalid(w, "Choose all, stale or overdue and a YYYY-MM-DD day between 2000 and 2100.")
		return f, false
	}
	return f, true
}
