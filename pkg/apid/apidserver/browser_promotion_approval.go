package apidserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const browserApprovalPurpose = "mcp-promotion-review-v1"

// The sealed browser ticket contains only a hash of the private exchange code.
// Durable cli_auth_codes rows provide the atomic human-claim gate. This domain
// cannot be exchanged through the CLI login endpoints or mint an API key.
type browserApprovalTicket struct {
	Purpose        string
	ExchangeHash   string
	AccountID      string
	Project        string
	ProjectID      string
	FromID         string
	Environment    string
	EnvironmentID  string
	PromotionToken string
	Reason         string
	ExpiresAt      time.Time
}

func browserApprovalCodeHash(code string) []byte {
	return api.HashToken([]byte(browserApprovalPurpose + ":" + code))
}

func (s *server) openBrowserApprovalTicket(value string) (browserApprovalTicket, error) {
	var ticket browserApprovalTicket
	if len(value) > 32<<10 {
		return ticket, state.ErrNotFound
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return ticket, state.ErrNotFound
	}
	data, err := s.sessions.OpenForCSRF(raw)
	if err != nil || json.Unmarshal(data, &ticket) != nil || ticket.Purpose != browserApprovalPurpose || !ticket.ExpiresAt.After(time.Now()) {
		return browserApprovalTicket{}, state.ErrNotFound
	}
	return ticket, nil
}

func (s *server) checkedBrowserApproval(ctx context.Context, acct state.Account, ticket browserApprovalTicket) (projectEnvironmentPromotionPlan, *api.Problem) {
	if ticket.AccountID != acct.ID {
		return projectEnvironmentPromotionPlan{}, browserApprovalUnavailable()
	}
	plan, problem := s.validateProjectEnvironmentPromotionApproval(ctx, acct, ticket.Project, ticket.Environment, ticket.PromotionToken)
	if problem != nil {
		return plan, problem
	}
	source, sourceErr := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, plan.ProjectID, plan.Preview.FromEnvironment)
	target, targetErr := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, plan.ProjectID, ticket.Environment)
	if sourceErr != nil || targetErr != nil || !target.Protected || plan.SyncConfig || plan.ProjectID != ticket.ProjectID || source.ID != ticket.FromID || target.ID != ticket.EnvironmentID {
		return projectEnvironmentPromotionPlan{}, browserApprovalUnavailable()
	}
	return plan, nil
}

func browserApprovalUnavailable() *api.Problem {
	return api.NewProblem(http.StatusGone, api.CodeBrowserApprovalUnavailable, "Review unavailable", "the review expired, was used, or its account or environment changed; create a new plan")
}

func (s *server) requestBrowserPromotionApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var input api.BrowserPromotionApprovalRequest
	if decodeJSON(r, &input) != nil || strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(input.Reason) > 280 || len(input.PromotionToken) > 16<<10 {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid review", "provide an exact promotion token and a reason of at most 280 characters"))
		return
	}
	plan, problem := s.validateProjectEnvironmentPromotionApproval(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"), input.PromotionToken)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if !plan.Preview.ToEnvironmentProtected || plan.SyncConfig {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid, "Protected promotion required", "browser review is available only for protected promotions that preserve target configuration"))
		return
	}
	result, err := s.mintBrowserApproval(r.Context(), acct, plan, input)
	if err != nil {
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, browserApprovalUnavailable())
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not start human promotion review"))
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *server) mintBrowserApproval(ctx context.Context, acct state.Account, plan projectEnvironmentPromotionPlan, input api.BrowserPromotionApprovalRequest) (api.BrowserPromotionApprovalResponse, error) {
	var result api.BrowserPromotionApprovalResponse
	source, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, plan.ProjectID, plan.Preview.FromEnvironment)
	if err != nil {
		return result, err
	}
	target, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, plan.ProjectID, plan.Preview.ToEnvironment)
	if err != nil || !target.Protected || plan.SyncConfig {
		return result, state.ErrConflict
	}
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return result, err
	}
	code := base64.RawURLEncoding.EncodeToString(entropy)
	hash := browserApprovalCodeHash(code)
	ticket := browserApprovalTicket{Purpose: browserApprovalPurpose, ExchangeHash: hex.EncodeToString(hash), AccountID: acct.ID, Project: plan.Preview.ProjectSlug, ProjectID: plan.ProjectID, FromID: source.ID, Environment: target.Slug, EnvironmentID: target.ID, PromotionToken: input.PromotionToken, Reason: input.Reason, ExpiresAt: time.Now().UTC().Add(cliAuthCodeTTL)}
	data, err := json.Marshal(ticket)
	if err != nil {
		return result, err
	}
	sealed, err := s.sessions.SealForCSRF(data)
	if err != nil {
		return result, err
	}
	if err := s.store.IssueCliAuthCode(ctx, hash, ticket.ExpiresAt); err != nil {
		return result, err
	}
	return api.BrowserPromotionApprovalResponse{ReviewURL: strings.TrimRight(s.cliAuthURLBase, "/") + cliAuthPath + "?approval=" + url.QueryEscape(sealed), Ticket: sealed, ExchangeCode: code, ExpiresAt: ticket.ExpiresAt.Format(time.RFC3339Nano)}, nil
}

func (s *server) exchangeBrowserPromotionApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var input api.BrowserPromotionApprovalExchange
	if decodeJSON(r, &input) != nil || len(input.ExchangeCode) != 43 {
		api.WriteProblem(w, browserApprovalUnavailable())
		return
	}
	ticket, err := s.openBrowserApprovalTicket(input.Ticket)
	if err != nil || ticket.AccountID != acct.ID || ticket.Project != r.PathValue("slug") || ticket.Environment != r.PathValue("environment") || ticket.ExchangeHash != hex.EncodeToString(browserApprovalCodeHash(input.ExchangeCode)) {
		api.WriteProblem(w, browserApprovalUnavailable())
		return
	}
	_, claimedAccount, err := s.store.PeekCliAuthCode(r.Context(), browserApprovalCodeHash(input.ExchangeCode))
	if err != nil {
		api.WriteProblem(w, browserApprovalUnavailable())
		return
	}
	if claimedAccount == "" {
		api.WriteProblem(w, api.NewProblem(404, api.CodeBrowserApprovalPending, "Awaiting human review", "open the review link in Gregale and approve the exact promotion"))
		return
	}
	if claimedAccount != acct.ID {
		api.WriteProblem(w, browserApprovalUnavailable())
		return
	}
	if _, problem := s.checkedBrowserApproval(r.Context(), acct, ticket); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.returnBrowserApproval(w, r, acct, ticket, input.ExchangeCode)
}

// Deterministic private credentials make a lost exchange response retryable
// without renewing approval or reviving a consumed credential. Only hashes
// reach durable state. The unique approval hash fences concurrent exchanges.
func (s *server) returnBrowserApproval(w http.ResponseWriter, r *http.Request, acct state.Account, ticket browserApprovalTicket, code string) {
	mac := hmac.New(sha256.New, s.sessions.BindingKey())
	_, _ = mac.Write([]byte(browserApprovalPurpose + ":credential:" + code))
	credential := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	approval, err := s.store.CreateProjectEnvironmentApproval(r.Context(), state.ProjectEnvironmentApproval{AccountID: acct.ID, ProjectSlug: ticket.Project, EnvironmentSlug: ticket.Environment, TokenKind: "promotion", PlanTokenHash: hashProjectEnvironmentApprovalMaterial(ticket.PromotionToken), ApprovalTokenHash: hashProjectEnvironmentApprovalMaterial(credential), ExpiresAt: ticket.ExpiresAt})
	if errors.Is(err, state.ErrConflict) {
		approval, err = s.store.ProjectEnvironmentApprovalByToken(r.Context(), acct.ID, ticket.Project, ticket.Environment, hashProjectEnvironmentApprovalMaterial(ticket.PromotionToken), hashProjectEnvironmentApprovalMaterial(credential))
	} else if err == nil {
		s.audit.Emit(r.Context(), "project.environment.approved", &acct.ID, map[string]any{"approval_id": approval.ID, "project_slug": ticket.Project, "environment": ticket.Environment, "method": "mcp-browser"})
	}
	if err != nil {
		api.WriteProblem(w, browserApprovalUnavailable())
		return
	}
	writeJSON(w, 200, projectEnvironmentApprovalResponse(credential, approval))
}
