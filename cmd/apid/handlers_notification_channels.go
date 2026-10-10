package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// channelTargetMaxBytes bounds a sealed destination (a Slack URL is ~80).
const channelTargetMaxBytes = 512

// channelStore applies the shared ADR-749 gates: channels follow alert
// rules, so a plan without alert rules has no channels either.
func (s *server) channelStore(w http.ResponseWriter, acct state.Account) (state.NotificationChannelStore, bool) {
	if limits, ok := api.LimitsFor(acct.Plan); !ok || limits.AlertRuleLimitPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanAlertRulesNotAllowed(acct.Plan))
		return nil, false
	}
	store, ok := s.store.(state.NotificationChannelStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("notification channels are unavailable on this deployment"))
	}
	return store, ok
}

// listNotificationChannels serves GET /v1/notification-channels.
func (s *server) listNotificationChannels(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.channelStore(w, acct)
	if !ok {
		return
	}
	rows, err := store.ListNotificationChannels(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list notification channels"))
		return
	}
	out := make([]api.NotificationChannelResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// createNotificationChannel serves POST /v1/notification-channels: validate
// the destination, seal it, store it.
func (s *server) createNotificationChannel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateNotificationChannelRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req, prob := api.NormalizeNotificationChannelRequest(req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	store, ok := s.channelStore(w, acct)
	if !ok {
		return
	}
	in, prob := sealChannel(acct, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	row, err := store.CreateNotificationChannel(r.Context(), in, api.MaxNotificationChannelsPerAccount)
	switch {
	case errors.Is(err, state.ErrNotificationChannelLimit):
		api.WriteProblem(w, api.ErrNotificationChannelLimitReached(api.MaxNotificationChannelsPerAccount))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeNotificationChannelInvalid, "Channel name already exists", "this account already has a notification channel with that name"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not create notification channel"))
		return
	}
	s.audit.Emit(r.Context(), "notification_channel.created", &acct.ID, map[string]any{"channel_id": row.ID, "name": row.Name, "kind": row.Kind, "target": row.TargetHint})
	writeJSON(w, http.StatusCreated, channelResponse(row))
}

// sealChannel validates the destination for its kind and seals the secret
// part. Email may only address the account's own email (ADR-749).
func sealChannel(acct state.Account, req api.CreateNotificationChannelRequest) (state.NotificationChannel, *api.Problem) {
	in := state.NotificationChannel{AccountID: acct.ID, Name: req.Name, Kind: req.Kind}
	var secret string
	switch req.Kind {
	case alertchannels.KindSlack:
		if err := alertchannels.ValidateSlackURL(req.SlackWebhookURL); err != nil {
			return in, api.ErrNotificationChannelInvalid(err.Error())
		}
		secret = req.SlackWebhookURL
		in.TargetHint = alertchannels.Hint(alertchannels.Target{Kind: req.Kind, SlackURL: secret})
	case alertchannels.KindPagerDuty:
		if err := alertchannels.ValidatePagerDuty(req.PagerDutyRoutingKey, req.PagerDutyRegion); err != nil {
			return in, api.ErrNotificationChannelInvalid(err.Error())
		}
		secret, in.PagerDutyRegion = req.PagerDutyRoutingKey, req.PagerDutyRegion
		in.TargetHint = alertchannels.Hint(alertchannels.Target{Kind: req.Kind, RoutingKey: secret})
	case alertchannels.KindEmail:
		if acct.Email == "" || !strings.EqualFold(req.Email, acct.Email) {
			return in, api.ErrNotificationChannelInvalid("email channels can only send to this account's own email address; other recipients need a confirmation flow that is not available yet")
		}
		in.Email, in.TargetHint = acct.Email, acct.Email
		return in, nil
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return in, api.ErrCapacity("host age recipient not loaded — refusing to seal the channel destination")
	}
	sealed, err := secretbox.SealBytes(recipient, alertchannels.SealNamespace, []byte(secret), channelTargetMaxBytes)
	if err != nil {
		return in, api.ErrCapacity("could not seal the channel destination")
	}
	in.TargetSealed = sealed
	return in, nil
}

// getNotificationChannel serves GET /v1/notification-channels/{id}.
func (s *server) getNotificationChannel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.channelStore(w, acct)
	if !ok {
		return
	}
	row, err := store.GetNotificationChannel(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such notification channel")
		return
	}
	writeJSON(w, http.StatusOK, channelResponse(row))
}

// deleteNotificationChannel serves DELETE /v1/notification-channels/{id}.
func (s *server) deleteNotificationChannel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.channelStore(w, acct)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := store.DeleteNotificationChannel(r.Context(), acct.ID, id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such notification channel")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete notification channel"))
		return
	}
	s.audit.Emit(r.Context(), "notification_channel.deleted", &acct.ID, map[string]any{"channel_id": id})
	w.WriteHeader(http.StatusNoContent)
}

func channelResponse(row state.NotificationChannel) api.NotificationChannelResponse {
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	return api.NotificationChannelResponse{
		ID: row.ID, Name: row.Name, Kind: row.Kind, Target: row.TargetHint, PagerDutyRegion: row.PagerDutyRegion,
		LastDeliveredAt: stamp(row.LastDeliveredAt), LastError: row.LastError, LastErrorAt: stamp(row.LastErrorAt),
		CreatedAt: stamp(row.CreatedAt),
	}
}
