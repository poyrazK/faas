package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) readManagedSlackCallback(r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, api.IssueSlackInteractionMaxBytes+1))
	return raw, err == nil && s.issueSlackOAuthConfig.Enabled() && issueslack.VerifyInteraction(s.issueSlackOAuthConfig.SigningSecret, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), raw, time.Now())
}
func (s *server) issueSlackManagedActions(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.readManagedSlackCallback(r)
	if !ok {
		http.Error(w, "Invalid Slack callback", http.StatusUnauthorized)
		return
	}
	hint, err := issueslack.OAuthActionHint(raw, s.issueSlackOAuthConfig.SlackAppID)
	if err != nil {
		http.Error(w, "Invalid Slack callback", http.StatusUnauthorized)
		return
	}
	service, ok := s.slackOAuthService()
	if !ok {
		http.Error(w, "Slack unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackInteractionTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, err := service.Store.IssueSlackOAuthActionApp(ctx, hint.DeliveryID, s.issueSlackOAuthConfig.SlackAppID, hint.WorkspaceID)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) {
			status = http.StatusUnauthorized
		}
		http.Error(w, "Slack action unavailable", status)
		return
	}
	r.SetPathValue("app_id", app)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	s.issueSlackInteractionHandler(w, r)
}
func (s *server) issueSlackManagedEvents(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.readManagedSlackCallback(r)
	if !ok {
		http.Error(w, "Invalid Slack callback", http.StatusUnauthorized)
		return
	}
	event, err := issueslack.ParseOAuthEvent(raw, s.issueSlackOAuthConfig.SlackAppID)
	if err != nil {
		http.Error(w, "Invalid Slack event", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if event.Challenge != "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"challenge": event.Challenge})
		return
	}
	service, ok := s.slackOAuthService()
	if !ok {
		http.Error(w, "Slack unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackInteractionTimeout)
	defer cancel()
	if event.Uninstalled {
		err = service.Store.RevokeIssueSlackOAuth(ctx, s.issueSlackOAuthConfig.SlackAppID, event.WorkspaceID, "", event.Time)
	} else {
		for _, bot := range event.Bots {
			err = service.Store.RevokeIssueSlackOAuth(ctx, s.issueSlackOAuthConfig.SlackAppID, event.WorkspaceID, bot, event.Time)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		http.Error(w, "Slack unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
