package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flagnotify"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Flag notification settings under
// /v1/projects/{slug}/environments/{environment}/flags/notifications
// (ADR-20261125085913): the channels an environment's experiments and flag
// lifecycle notify.

func writeFlagNotificationsUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flag notifications unavailable", "flag notifications are not enabled for this account").
		WithHint("An operator releases flag notifications account by account. Your credentials are not the cause."))
}

func (s *server) flagNotificationScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.FeatureFlagScope, state.FlagNotificationStore, bool) {
	if !s.platformFlagFor(platformflags.FlagNotifications, flagSubject(r, acct)) {
		writeFlagNotificationsUnavailable(w)
		return state.FeatureFlagScope{}, nil, false
	}
	scope, _, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return scope, nil, false
	}
	store, ok := s.store.(state.FlagNotificationStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("flag notifications unavailable"))
		return scope, nil, false
	}
	return scope, store, true
}

func flagNotificationSettingsResponse(st state.FlagNotificationSettings) api.FlagNotificationSettings {
	out := api.FlagNotificationSettings{ChannelIDs: st.ChannelIDs, UpdatedBy: st.UpdatedBy}
	if out.ChannelIDs == nil {
		out.ChannelIDs = []string{}
	}
	if !st.UpdatedAt.IsZero() {
		t := st.UpdatedAt
		out.UpdatedAt = &t
	}
	return out
}

func (s *server) getFlagNotifications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.flagNotificationScope(w, r, acct)
	if !ok {
		return
	}
	st, err := store.FlagNotificationSettings(r.Context(), scope.EnvironmentID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrInternal("flag notifications unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, flagNotificationSettingsResponse(st))
}

func (s *server) setFlagNotifications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.flagNotificationScope(w, r, acct)
	if !ok {
		return
	}
	var req api.FlagNotificationSettingsRequest
	if !decodeJSONLimit(w, r, &req, 4<<10) {
		return
	}
	ids, p := flagNotifyChannels(r.Context(), store, scope.AccountID, req.ChannelIDs)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	st, err := store.SetFlagNotificationSettings(r.Context(), state.FlagNotificationSettings{
		EnvironmentID: scope.EnvironmentID, AccountID: scope.AccountID, ChannelIDs: ids, UpdatedBy: acct.ID})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("flag notifications unavailable"))
		return
	}
	s.audit.Emit(r.Context(), "flags.notifications_set", &acct.ID, map[string]any{"environment_id": scope.EnvironmentID, "channel_ids": ids})
	writeJSON(w, http.StatusOK, flagNotificationSettingsResponse(st))
}

// flagNotifyChannels validates channel ids against the project owner's
// notification channels.
func flagNotifyChannels(ctx context.Context, store state.FlagNotificationStore, accountID string, ids []string) ([]string, *api.Problem) {
	invalid := func(err error) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid notification channels", err.Error())
	}
	if err := flagnotify.CheckChannelIDs(ids); err != nil {
		return nil, invalid(err)
	}
	found, err := store.FlagNotificationChannels(ctx, accountID, ids)
	if err != nil {
		return nil, api.ErrInternal("notification channels unavailable")
	}
	out, err := flagnotify.ValidChannels(ids, found)
	if err != nil {
		return nil, invalid(err)
	}
	return out, nil
}

// experimentNotifyChannels validates an experiment's own channels: none,
// or flag notifications released and channels of the project's owner.
func (s *server) experimentNotifyChannels(r *http.Request, acct state.Account, scope state.FeatureFlagScope, ids []string) ([]string, *api.Problem) {
	if len(ids) == 0 {
		return nil, nil
	}
	store, ok := s.store.(state.FlagNotificationStore)
	if !ok || !s.platformFlagFor(platformflags.FlagNotifications, flagSubject(r, acct)) {
		return nil, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flag notifications unavailable", "flag notifications are not enabled for this account").
			WithHint("Create the experiment without notify_channel_ids, or ask an operator to release flag notifications.")
	}
	return flagNotifyChannels(r.Context(), store, scope.AccountID, ids)
}
