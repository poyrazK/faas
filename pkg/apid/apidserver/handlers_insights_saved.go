package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Saved insights under
// /v1/projects/{slug}/environments/{environment}/insights/saved
// (ADR-20261122190000). Anyone who can read the project can list, run and
// export them; deploy rights create, replace and delete them.

func (s *server) savedInsightScope(w http.ResponseWriter, r *http.Request, acct state.Account) (insightsScope, state.SavedInsightStore, bool) {
	if !s.platformFlagFor(platformflags.SavedInsights, flagSubject(r, acct)) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Saved insights unavailable", "saved insights are not enabled for this account").
			WithHint("An operator releases saved insights account by account. Your credentials are not the cause."))
		return insightsScope{}, nil, false
	}
	sc, ok := s.insightsScope(w, r, acct)
	if !ok {
		return sc, nil, false
	}
	store, ok := s.store.(state.SavedInsightStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("saved insights unavailable"))
		return sc, nil, false
	}
	return sc, store, true
}

func savedInsightResponse(si state.SavedInsight) api.SavedInsight {
	return api.SavedInsight{
		SavedInsightRequest: insights.SavedRequest(si), AlertState: si.AlertState, LastValue: si.LastValue,
		LastRunAt: si.LastRunAt, LastError: si.LastError, NextDigestAt: si.NextDigestAt, CreatedBy: si.CreatedBy,
		CreatedAt: si.CreatedAt, UpdatedAt: si.UpdatedAt,
	}
}

func writeSavedInsightError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Saved insight not found", "no saved insight has that key in this environment"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Saved insight exists", "a saved insight with that key already exists; replace it with PUT"))
	case errors.Is(err, state.ErrSavedInsightLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "saved_insight_limit", "Saved insight limit reached",
			"the environment already holds the maximum number of saved insights; delete one first").
			WithLimit(api.SavedInsightsMaxPerEnvironment, api.SavedInsightsMaxPerEnvironment))
	default:
		writeInsightsError(w, err)
	}
}

// savedInsightInput validates a create or replace request and returns the
// row to store. Channels must be the account's own.
func (s *server) savedInsightInput(ctx context.Context, sc insightsScope, req api.SavedInsightRequest) (state.SavedInsight, error) {
	query, err := insights.NormalizeSaved(&req, api.Plan(sc.acct.Plan).AnalyticsRetentionDays())
	if err != nil {
		return state.SavedInsight{}, err
	}
	if len(req.ChannelIDs) > 0 {
		channels, ok := s.store.(state.NotificationChannelStore)
		if !ok {
			return state.SavedInsight{}, fmt.Errorf("%w: notification channels are unavailable", insights.ErrInvalid)
		}
		for _, id := range req.ChannelIDs {
			if _, err := channels.GetNotificationChannel(ctx, sc.acct.ID, id); err != nil {
				return state.SavedInsight{}, fmt.Errorf("%w: channel %s is not one of this account's notification channels", insights.ErrInvalid, id)
			}
		}
	}
	nextDigest, nextCheck := insights.Schedule(req.Digest, req.Alert != nil, time.Now())
	out := state.SavedInsight{AccountID: sc.acct.ID, EnvironmentID: sc.env.ID, Key: req.Key, Name: req.Name, Kind: req.Kind,
		Query: query, RangeDays: req.RangeDays, Digest: req.Digest, ChannelIDs: req.ChannelIDs,
		NextDigestAt: nextDigest, NextCheckAt: nextCheck, CreatedBy: sc.acct.ID}
	if req.Alert != nil {
		out.AlertComparison, out.AlertThreshold = req.Alert.Comparison, req.Alert.Threshold
	}
	return out, nil
}

func (s *server) listSavedInsights(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.savedInsightScope(w, r, acct)
	if !ok {
		return
	}
	list, err := store.ListSavedInsights(r.Context(), sc.env.ID)
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	out := api.SavedInsightList{Insights: make([]api.SavedInsight, 0, len(list))}
	for _, si := range list {
		out.Insights = append(out.Insights, savedInsightResponse(si))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createSavedInsight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.savedInsightScope(w, r, acct)
	if !ok {
		return
	}
	var req api.SavedInsightRequest
	if !decodeJSONLimit(w, r, &req, api.SavedInsightRequestMaxBytes) {
		return
	}
	in, err := s.savedInsightInput(r.Context(), sc, req)
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	si, err := store.CreateSavedInsight(r.Context(), in, api.SavedInsightsMaxPerEnvironment)
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "insights.saved_created", &acct.ID, map[string]any{"environment_id": sc.env.ID, "insight": si.Key, "kind": si.Kind})
	writeJSON(w, http.StatusCreated, savedInsightResponse(si))
}

func (s *server) replaceSavedInsight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.savedInsightScope(w, r, acct)
	if !ok {
		return
	}
	var req api.SavedInsightRequest
	if !decodeJSONLimit(w, r, &req, api.SavedInsightRequestMaxBytes) {
		return
	}
	if req.Key == "" {
		req.Key = r.PathValue("key")
	}
	if req.Key != r.PathValue("key") {
		writeSavedInsightError(w, fmt.Errorf("%w: the body's key differs from the path's", insights.ErrInvalid))
		return
	}
	in, err := s.savedInsightInput(r.Context(), sc, req)
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	si, err := store.UpdateSavedInsight(r.Context(), in)
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "insights.saved_updated", &acct.ID, map[string]any{"environment_id": sc.env.ID, "insight": si.Key, "kind": si.Kind})
	writeJSON(w, http.StatusOK, savedInsightResponse(si))
}

func (s *server) getSavedInsight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.savedInsightScope(w, r, acct)
	if !ok {
		return
	}
	si, err := store.GetSavedInsight(r.Context(), sc.env.ID, r.PathValue("key"))
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, savedInsightResponse(si))
}

func (s *server) deleteSavedInsight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.savedInsightScope(w, r, acct)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if !flags.ValidKey(key) {
		writeSavedInsightError(w, state.ErrNotFound)
		return
	}
	deleted, err := store.DeleteSavedInsight(r.Context(), sc.env.ID, key)
	if err == nil && !deleted {
		err = state.ErrNotFound
	}
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "insights.saved_deleted", &acct.ID, map[string]any{"environment_id": sc.env.ID, "insight": key})
	w.WriteHeader(http.StatusNoContent)
}

// runSavedInsight runs a saved insight over its range now and answers JSON
// or, with format=csv, a CSV attachment.
func (s *server) runSavedInsight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.savedInsightScope(w, r, acct)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format != "" && format != "json" && format != "csv" {
		writeInsightsError(w, fmt.Errorf("%w: format is json or csv", insights.ErrInvalid))
		return
	}
	si, err := store.GetSavedInsight(r.Context(), sc.env.ID, r.PathValue("key"))
	if err != nil {
		writeSavedInsightError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AnalyticsQueryTimeout)
	defer cancel()
	res, err := insights.RunSaved(ctx, sc.store, si, api.Plan(acct.Plan).AnalyticsRetentionDays(), time.Now())
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if format != "csv" {
		writeJSON(w, http.StatusOK, res)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", si.Key+".csv"))
	w.WriteHeader(http.StatusOK)
	_ = api.WriteSavedInsightCSV(w, res)
}
