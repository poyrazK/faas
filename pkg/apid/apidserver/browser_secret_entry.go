package apidserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Secret entry (ADR-20261026174727) follows the browser promotion review
// (ADR-1242): a sealed ticket names the account, app, scope and key, and a
// durable cli_auth_codes row is the one-time human gate. The owner types the
// value on the session-authenticated /cli-auth surface; the requester holds
// only the private exchange code and learns whether the secret was set.
const (
	secretEntryPurpose = "mcp-secret-entry-v1"
	secretEntryTTL     = 15 * time.Minute
)

type secretEntryTicket struct {
	Purpose      string
	ExchangeHash string
	AccountID    string
	AppID        string
	App          string
	Scope        string
	Key          string
	Reason       string
	ExpiresAt    time.Time
}

func secretEntryCodeHash(code string) []byte {
	return api.HashToken([]byte(secretEntryPurpose + ":" + code))
}

func secretEntryUnavailable() *api.Problem {
	return api.NewProblem(http.StatusGone, api.CodeSecretEntryUnavailable, "Secret entry unavailable", "the request expired, was used, or its account or app changed; request a new one")
}

func (s *server) openSecretEntryTicket(value string) (secretEntryTicket, error) {
	var ticket secretEntryTicket
	if len(value) > 32<<10 {
		return ticket, state.ErrNotFound
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return ticket, state.ErrNotFound
	}
	data, err := s.sessions.OpenForCSRF(raw)
	if err != nil || json.Unmarshal(data, &ticket) != nil || ticket.Purpose != secretEntryPurpose || !ticket.ExpiresAt.After(time.Now()) {
		return secretEntryTicket{}, state.ErrNotFound
	}
	return ticket, nil
}

// secretEntryApp re-resolves the ticket's app, so a deleted and recreated slug
// cannot inherit a request.
func (s *server) secretEntryApp(ctx context.Context, acct state.Account, ticket secretEntryTicket) (state.App, bool) {
	app, err := s.store.AppBySlug(ctx, ticket.App)
	return app, err == nil && app.ID == ticket.AppID && app.AccountID == acct.ID && ticket.AccountID == acct.ID
}

func (s *server) requestSecretEntry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var input api.SecretEntryRequest
	if decodeJSON(r, &input) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	scope, prob := secretEntryScope(input)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	response, err := s.mintSecretEntry(r.Context(), acct, app, scope, input)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not start secret entry"))
		return
	}
	s.audit.Emit(r.Context(), "secret.entry_requested", &acct.ID, map[string]any{"app_id": app.ID, "name": input.Key, "scope": scope})
	writeJSON(w, http.StatusCreated, response)
}

func secretEntryScope(input api.SecretEntryRequest) (string, *api.Problem) {
	if prob := api.ValidateSecretKey(input.Key); prob != nil {
		return "", prob
	}
	if strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(input.Reason) > 280 {
		return "", api.NewProblem(400, api.CodeValidation, "Invalid secret entry", "provide a reason of at most 280 characters")
	}
	if input.Scope == "" {
		return defaultEnvScope, nil
	}
	if prob := api.ValidateScope(input.Scope); prob != nil {
		return "", prob
	}
	return input.Scope, nil
}

func (s *server) mintSecretEntry(ctx context.Context, acct state.Account, app state.App, scope string, input api.SecretEntryRequest) (api.SecretEntryResponse, error) {
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return api.SecretEntryResponse{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(entropy)
	hash := secretEntryCodeHash(code)
	ticket := secretEntryTicket{Purpose: secretEntryPurpose, ExchangeHash: hex.EncodeToString(hash), AccountID: acct.ID, AppID: app.ID, App: app.Slug, Scope: scope, Key: input.Key, Reason: input.Reason, ExpiresAt: time.Now().UTC().Add(secretEntryTTL)}
	data, err := json.Marshal(ticket)
	if err != nil {
		return api.SecretEntryResponse{}, err
	}
	sealed, err := s.sessions.SealForCSRF(data)
	if err != nil {
		return api.SecretEntryResponse{}, err
	}
	if err := s.store.IssueCliAuthCode(ctx, hash, ticket.ExpiresAt); err != nil {
		return api.SecretEntryResponse{}, err
	}
	return api.SecretEntryResponse{
		EntryURL:     strings.TrimRight(s.cliAuthURLBase, "/") + cliAuthPath + "?secret=" + url.QueryEscape(sealed),
		Ticket:       sealed,
		ExchangeCode: code,
		ExpiresAt:    ticket.ExpiresAt.Format(time.RFC3339Nano),
	}, nil
}

func (s *server) secretEntryStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var input api.SecretEntryStatusRequest
	if decodeJSON(r, &input) != nil || len(input.ExchangeCode) != 43 {
		api.WriteProblem(w, secretEntryUnavailable())
		return
	}
	ticket, err := s.openSecretEntryTicket(input.Ticket)
	hash := secretEntryCodeHash(input.ExchangeCode)
	if err != nil || ticket.AccountID != acct.ID || ticket.AppID != app.ID || ticket.ExchangeHash != hex.EncodeToString(hash) {
		api.WriteProblem(w, secretEntryUnavailable())
		return
	}
	_, claimedBy, err := s.store.PeekCliAuthCode(r.Context(), hash)
	if err != nil || (claimedBy != "" && claimedBy != acct.ID) {
		api.WriteProblem(w, secretEntryUnavailable())
		return
	}
	status := api.SecretEntryPending
	if claimedBy == acct.ID {
		status = api.SecretEntrySet
	}
	writeJSON(w, http.StatusOK, api.SecretEntryStatus{Status: status, App: ticket.App, Key: ticket.Key, Scope: ticket.Scope, ExpiresAt: ticket.ExpiresAt.Format(time.RFC3339Nano)})
}
