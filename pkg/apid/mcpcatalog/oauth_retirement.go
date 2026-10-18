package mcpcatalog

import (
	"context"
	"errors"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
	"strings"
)

func RetirementHandler(store state.Store) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		source, ok := store.(state.MCPOAuthRetirementStore)
		if !ok {
			respond(w, nil, errors.New("retirement store unavailable"))
			return
		}
		if r.Method == http.MethodGet {
			offset := 0
			var err error
			if raw := r.URL.Query().Get("offset"); raw != "" {
				offset, err = strconv.Atoi(raw)
			}
			if err != nil || offset < 0 || offset > api.MCPOAuthRetirementMaxOffset {
				api.WriteProblem(w, api.ErrValidation("invalid retirement offset"))
				return
			}
			out, err := source.ListMCPOAuthRetirements(r.Context(), account.ID, offset)
			respond(w, out, err)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid retirement ID"))
			return
		}
		var action api.MCPOAuthRetirementAction
		if httpkit.DecodeJSONSized(r, &action, api.MCPCatalogOAuthMaxResponseBytes) != nil {
			api.WriteProblem(w, api.ErrValidation("invalid retirement action"))
			return
		}
		if action.Action != "retry" && action.Action != "acknowledge_manual" {
			api.WriteProblem(w, api.ErrValidation("use retry or acknowledge_manual"))
			return
		}
		out, err := source.RecoverMCPOAuthRetirement(r.Context(), account.ID, id.String(), action)
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Grant retirement cannot be changed", "The record is leased, terminal, or past retention; reload the retirement ledger"))
			return
		}
		respond(w, out, err)
	}
}
func (r *ObserverRunner) prepareExchangeRetirement(ctx context.Context, job state.MCPCatalogObserverJob, ids []*age.X25519Identity, clientSecret string) (api.MCPOAuthRetirement, error) {
	source, ok := r.Store.(state.MCPOAuthRetirementStore)
	if !ok {
		return api.MCPOAuthRetirement{}, errors.New("retirement store unavailable")
	}
	var ciphertext []byte
	var err error
	cfg := job.Snapshot.Config.Observer.OAuth
	if clientSecret != "" {
		ciphertext, err = secretbox.Seal(ids[0].Recipient(), secretbox.Envelope{strings.TrimPrefix(cfg.ClientSecretRef, api.SecretRefPrefix): clientSecret})
		if err != nil {
			return api.MCPOAuthRetirement{}, errors.New("credential storage unavailable")
		}
	}
	return source.PrepareMCPOAuthRetirement(ctx, job, ciphertext)
}

// Store every observed grant before validating scopes or attempting installation.
// A lost exchange reply remains a durable unknown-issuance record for recovery.
func (r *ObserverRunner) recordExchangeRetirement(ctx context.Context, account string, record api.MCPOAuthRetirement, ids []*age.X25519Identity, data oauthRefreshResponse) ([]byte, error) {
	if !oauthTokenValue(data.AccessToken) && !oauthTokenValue(data.RefreshToken) {
		return nil, nil
	}
	envelope := secretbox.Envelope{}
	if oauthTokenValue(data.AccessToken) {
		envelope["ACCESS_TOKEN"] = data.AccessToken
	}
	if oauthTokenValue(data.RefreshToken) {
		envelope["REFRESH_TOKEN"] = data.RefreshToken
	}
	sealed, err := secretbox.Seal(ids[0].Recipient(), envelope)
	if err != nil {
		return nil, errors.New("credential storage unavailable")
	}
	source := r.Store.(state.MCPOAuthRetirementStore)
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MCPPolicyConvergenceReadTimeout)
	defer cancel()
	err = source.RecordMCPOAuthIssuedGrant(save, account, record.ID, sealed, "authorization_exchange")
	if err != nil {
		return sealed, errors.New("grant retirement persistence unavailable")
	}
	return sealed, nil
}
