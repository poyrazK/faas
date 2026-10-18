package outboundai

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const accessTokenInvalidDetail = "an access token needs a name of 1 to 63 lowercase letters, digits or hyphens, " +
	"expires_in_hours from 1 to 2160, and integration_ids, if any, listing at most 32 of the account's enabled AI " +
	"integrations bound to the app"

// accessTokenPrefixLength is how much of a token is kept to name it:
// "ggai_" and seven characters of the secret.
const accessTokenPrefixLength = len(api.OutboundAIAccessTokenPrefix) + 7

func (s *Service) accessTokenStore(w http.ResponseWriter) (state.OutboundAIAccessTokenStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIAccessTokenStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "AI gateway access tokens are unavailable")
	}
	return store, ok
}

// newAccessToken returns a token and the SHA-256 hash outboundd looks it up
// by (ADR-1431).
func newAccessToken() (string, [32]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", [32]byte{}, err
	}
	token := api.OutboundAIAccessTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	return token, sha256.Sum256([]byte(token)), nil
}

// CreateAccessToken issues an AI gateway access token for an app. The
// secret is in this response only.
func (s *Service) CreateAccessToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.CreateOutboundAIAccessTokenRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil || !req.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, accessTokenInvalidDetail)
		return
	}
	req = req.Normalized()
	store, ok := s.accessTokenStore(w)
	if !ok {
		return
	}
	secret, hash, err := newAccessToken()
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The access token could not be issued")
		return
	}
	created, err := store.CreateOutboundAIAccessToken(r.Context(), state.OutboundAIAccessToken{
		ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: req.Name, Prefix: secret[:accessTokenPrefixLength],
		TokenHash: hash[:], IntegrationIDs: req.IntegrationIDs,
		ExpiresAt: time.Now().UTC().Add(time.Duration(*req.ExpiresInHours) * time.Hour).Truncate(time.Second),
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "app not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, accessTokenInvalidDetail)
	case errors.Is(err, state.ErrConflict):
		problem(w, http.StatusConflict, "outbound_ai_access_token_exists", "The app already has a live access token with this name")
	case errors.Is(err, state.ErrOutboundAIAccessTokenLimit):
		api.WriteProblem(w, api.ErrOutboundAIAccessTokenLimit(api.OutboundAIAccessTokensPerApp))
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The access token could not be issued")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusCreated, api.CreatedOutboundAIAccessToken{AccessToken: accessTokenResponse(created), Token: secret})
	}
}

// ListAccessTokens lists an app's access tokens without their secrets.
func (s *Service) ListAccessTokens(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.accessTokenStore(w)
	if !ok {
		return
	}
	tokens, err := store.ListOutboundAIAccessTokens(r.Context(), acct.ID, app.ID)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "AI gateway access tokens are unavailable")
		return
	}
	out := api.OutboundAIAccessTokenList{Items: make([]api.OutboundAIAccessToken, 0, len(tokens))}
	for _, token := range tokens {
		out.Items = append(out.Items, accessTokenResponse(token))
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// RevokeAccessToken revokes one of an app's access tokens. outboundd may
// accept it for up to OutboundAIAccessTokenCacheSeconds more.
func (s *Service) RevokeAccessToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	tokenID := r.PathValue("token")
	if _, err := uuid.Parse(tokenID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Access token ID must be a UUID")
		return
	}
	store, ok := s.accessTokenStore(w)
	if !ok {
		return
	}
	err := store.RevokeOutboundAIAccessToken(r.Context(), acct.ID, app.ID, tokenID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "access token not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The access token could not be revoked")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func accessTokenResponse(token state.OutboundAIAccessToken) api.OutboundAIAccessToken {
	return api.OutboundAIAccessToken{
		ID: token.ID, Name: token.Name, AppID: token.AppID, Prefix: token.Prefix,
		IntegrationIDs: append([]string{}, token.IntegrationIDs...), ExpiresAt: token.ExpiresAt.UTC(),
		CreatedAt: token.CreatedAt.UTC(), LastUsedAt: token.LastUsedAt, RevokedAt: token.RevokedAt,
	}
}
