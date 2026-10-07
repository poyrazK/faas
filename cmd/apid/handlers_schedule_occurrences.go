package main

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func scheduleOccurrenceResponse(o state.ScheduleOccurrence) api.ScheduleOccurrenceResponse {
	return api.ScheduleOccurrenceResponse{
		SchedulePolicy: &o.SchedulePolicy, WorkDecision: o.WorkDecision, OutcomeCode: o.OutcomeCode,
		ID: o.ID, ScheduleRevision: o.ScheduleRevision,
		ScheduledFor: o.ScheduledFor.UTC(), StartDeadlineAt: o.StartDeadlineAt,
		Status: o.Status, Reason: o.Reason, BlockingOccurrenceID: o.BlockingOccurrenceID,
		ExclusiveOperationID: o.ExclusiveOperationID,
		JobRunID:             o.JobRunID, InvocationID: o.InvocationID, AppTaskID: o.AppTaskID,
		StartedAt: o.StartedAt, FinishedAt: o.FinishedAt, CreatedAt: o.CreatedAt.UTC(),
	}
}

func parseOccurrencePage(r *http.Request) (int, string, *api.Problem) {
	limit := 50
	if _, ok := r.URL.Query()["limit"]; ok {
		n, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || n < 1 || n > 200 {
			return 0, "", api.ErrValidation("limit must be an integer between 1 and 200")
		}
		limit = n
	}
	before := r.URL.Query().Get("before")
	if before != "" {
		if _, err := uuid.Parse(before); err != nil {
			return 0, "", api.ErrValidation("before must be a schedule occurrence id")
		}
	}
	return limit, before, nil
}

func occurrencePage(limit int, before string, rows []state.ScheduleOccurrence) api.ListScheduleOccurrencesResponse {
	out := api.ListScheduleOccurrencesResponse{Occurrences: make([]api.ScheduleOccurrenceResponse, 0, len(rows)), Limit: limit, Before: before}
	for _, row := range rows {
		out.Occurrences = append(out.Occurrences, scheduleOccurrenceResponse(row))
	}
	if len(rows) == limit {
		out.NextBefore = rows[len(rows)-1].ID
	}
	return out
}

func (s *server) listJobScheduleOccurrences(w http.ResponseWriter, r *http.Request, acct state.Account) {
	job, ok, err := s.resolveJob(r.Context(), r.PathValue("name"), acct)
	if err != nil {
		s.log.Error("list job schedule occurrences: resolve failed", "account", acct.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list schedule occurrences"))
		return
	}
	if !ok {
		s.notFound(w, "no such job")
		return
	}
	limit, before, problem := parseOccurrencePage(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	history, ok := s.store.(state.ScheduleOccurrenceHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("schedule occurrence history is unavailable"))
		return
	}
	rows, err := history.ScheduleOccurrenceListByJob(r.Context(), job.ID, limit, before)
	if err != nil {
		s.log.Error("list job schedule occurrences failed", "job", job.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list schedule occurrences"))
		return
	}
	writeJSON(w, http.StatusOK, occurrencePage(limit, before, rows))
}

func (s *server) listCronScheduleOccurrences(w http.ResponseWriter, r *http.Request, acct state.Account) {
	cron, err := s.store.CronByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(r.Context(), cron.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	limit, before, problem := parseOccurrencePage(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	history, ok := s.store.(state.ScheduleOccurrenceHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("schedule occurrence history is unavailable"))
		return
	}
	rows, err := history.ScheduleOccurrenceListByCron(r.Context(), cron.ID, limit, before)
	if err != nil {
		s.log.Error("list cron schedule occurrences failed", "cron", cron.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list schedule occurrences"))
		return
	}
	writeJSON(w, http.StatusOK, occurrencePage(limit, before, rows))
}
