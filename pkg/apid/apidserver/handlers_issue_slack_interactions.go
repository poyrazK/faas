package apidserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueSlackInteractionHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackInteractionTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	st, ok := s.store.(state.IssueSlackInteractionStore)
	if !ok {
		http.Error(w, "Slack interaction unavailable", http.StatusServiceUnavailable)
		return
	}
	in, err := readIssueSlackInteraction(r, st)
	if err != nil {
		status := http.StatusUnauthorized
		if !errors.Is(err, state.ErrInvalidArgument) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, "Invalid Slack interaction", status)
		return
	}
	result, err := st.ApplyIssueSlackInteraction(ctx, r.PathValue("app_id"), in)
	if err != nil && !expectedSlackActionError(err) {
		http.Error(w, "Slack interaction unavailable", http.StatusServiceUnavailable)
		return
	}
	text := "This action is no longer available. Open Gregale to check the current response and your access."
	if err == nil {
		text, err = issueslack.InteractionText(result, s.cliAuthURLBase)
		if err != nil {
			http.Error(w, "Slack interaction unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	client := s.issueSlackResponses
	if client == nil {
		client = issueslack.NewClient(nil)
	}
	if err = client.RespondToInteraction(ctx, in.ResponseURL, in.WorkspaceID, text); err != nil {
		http.Error(w, "Slack response unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
func readIssueSlackInteraction(r *http.Request, st state.IssueSlackInteractionStore) (state.IssueSlackInteractionRequest, error) {
	out := state.IssueSlackInteractionRequest{}
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/x-www-form-urlencoded" {
		return out, state.ErrInvalidArgument
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, api.IssueSlackInteractionMaxBytes+1))
	if err != nil || len(raw) > api.IssueSlackInteractionMaxBytes {
		return out, state.ErrInvalidArgument
	}
	policy, err := st.IssueSlackInteractionVerifier(r.Context(), r.PathValue("app_id"))
	if err != nil {
		return out, err
	}
	if !policy.Enabled || mfaIdentities == nil {
		return out, state.ErrInvalidArgument
	}
	secret, err := issueslack.OpenSigningSecret(mfaIdentities(), policy.SecretSealed)
	if err != nil || !issueslack.VerifyInteraction(secret, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), raw, time.Now()) {
		return out, state.ErrInvalidArgument
	}
	return issueslack.ParseInteraction(raw, policy)
}

func expectedSlackActionError(err error) bool {
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) {
		return true
	}
	p := api.AsProblem(err)
	return p != nil && p.Status < 500
}
