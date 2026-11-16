package sales

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) Tasks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.pipeline(w, r, acct)
	if !ok {
		return
	}
	f, ok := taskFilters(w, r, scope)
	if !ok {
		return
	}
	audience := pipelineAudience("tasks", scope, f.Owner, f.Status, f.Due, f.On)
	after, err := s.openCursor(r.URL.Query().Get("after"), audience)
	if err == nil && after != "" {
		f.AfterDate, f.AfterID, ok = strings.Cut(after, "|")
		_, dateErr := time.Parse(time.DateOnly, f.AfterDate)
		if !ok || dateErr != nil || !validID(f.AfterID) {
			err = referrals.ErrNotFound
		}
	}
	if err != nil {
		invalid(w, "invalid task cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, counts, err := store.Tasks(ctx, scope, f)
	if err != nil {
		pipelineError(w, err)
		return
	}
	next := ""
	if len(rows) > api.SalesPageSize {
		rows = rows[:api.SalesPageSize]
		last := rows[len(rows)-1]
		next, err = s.encodeCursor(last.DueOn+"|"+last.ID, audience)
	}
	if err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.tasks_read", acct.ID, map[string]any{"team_scope": scope.All})
	writeJSON(w, map[string]any{"tasks": rows, "summary": counts, "next_cursor": next, "on": f.On})
}
func taskFilters(w http.ResponseWriter, r *http.Request, scope referrals.Scope) (referrals.TaskFilter, bool) {
	q := r.URL.Query()
	f := referrals.TaskFilter{Status: q.Get("status"), Due: q.Get("due"), On: q.Get("on"), Limit: api.SalesPageSize + 1}
	owner, ok := ownerFilter(w, r, scope)
	if !ok {
		return f, false
	}
	f.Owner = owner
	if f.Status == "" {
		f.Status = "open"
	}
	if f.Due == "" {
		f.Due = "all"
	}
	if f.On == "" {
		f.On = time.Now().UTC().Format(time.DateOnly)
	}
	_, err := time.Parse(time.DateOnly, f.On)
	if err != nil || (f.Status != "open" && f.Status != "done" && f.Status != "all") || (f.Due != "all" && f.Due != "today" && f.Due != "overdue" && f.Due != "upcoming") {
		invalid(w, "Use a valid task status, due filter and YYYY-MM-DD day.")
		return f, false
	}
	return f, true
}
func validTask(in referrals.TaskInput, id string) bool {
	day, err := time.Parse(time.DateOnly, in.DueOn)
	return err == nil && day.Year() >= api.SalesTaskMinYear && day.Year() <= api.SalesTaskMaxYear && boundedText(in.Title, api.SalesFieldMaxBytes, false) && validVersion(id, in.Version) &&
		((id == "" && validID(in.LeadID) && !in.Done) || (id != "" && in.LeadID == ""))
}
func (s *Service) SaveTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.pipeline(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("task")
	var in referrals.TaskInput
	if !decode(w, r, &in) {
		invalid(w, "invalid task body")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if !validTask(in, id) {
		invalid(w, "Provide a task title, due date between 2000 and 2100, and a lead for new tasks or the current version for edits.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	saved, err := store.SaveTask(ctx, scope, acct.ID, id, in)
	if err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.task_saved", acct.ID, map[string]any{"task_id": saved, "created": id == "", "done": in.Done})
	writeJSON(w, map[string]string{"id": saved})
}
