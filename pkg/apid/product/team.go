package product

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/state"
)

type teamAuthContext struct {
	ProductID       string `json:"product_id"`
	TenantID        string `json:"tenant_id"`
	AuthAppID       string `json:"auth_app_id"`
	CatalogRevision string `json:"catalog_revision"`
	ReviewHash      string `json:"review_hash"`
}
type teamAuthRequest struct {
	api.ProductTeamActionRequest
	ExpectedSnapshot string          `json:"expected_snapshot"`
	Context          teamAuthContext `json:"context"`
}
type teamAuthResult struct {
	ID           string     `json:"id,omitempty"`
	InvitationID string     `json:"invitation_id,omitempty"`
	UserID       string     `json:"user_id,omitempty"`
	Email        string     `json:"email,omitempty"`
	Role         string     `json:"role,omitempty"`
	BeforeRole   string     `json:"before_role"`
	AfterRole    string     `json:"after_role"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Delivered    bool       `json:"delivered"`
}

func (c customerTeam) actionContext(hash string) teamAuthContext {
	return teamAuthContext{ProductID: teamCanonicalID(c.realm.app.ID), TenantID: c.identity.TenantID, AuthAppID: teamCanonicalID(c.realm.authApp.ID), CatalogRevision: c.realm.catalog.Revision, ReviewHash: hash}
}
func (c customerTeam) matchesContext(value teamAuthContext) bool {
	return value.ProductID == teamCanonicalID(c.realm.app.ID) && value.TenantID == c.identity.TenantID && value.AuthAppID == teamCanonicalID(c.realm.authApp.ID)
}

func teamReceiptProjection(c customerTeam, r api.ProductTeamActionReceipt) (api.ProductTeamActionReceipt, error) {
	var request teamAuthRequest
	var result teamAuthResult
	if json.Unmarshal(r.Request, &request) != nil || json.Unmarshal(r.Result, &result) != nil || !teamUUID(r.ID) || r.OrganizationID != c.identity.OrganizationID || !teamUUID(r.ActorID) || !productValidTeamRole(r.ActorRole) || r.CreatedAt.IsZero() {
		return r, teamUnavailable()
	}
	normalized, err := normalizeTeamRequest(request.ProductTeamActionRequest)
	if err != nil || !reflect.DeepEqual(normalized, request.ProductTeamActionRequest) || r.ID != request.ID || r.Action != request.Action || !accessRevision(request.ExpectedSnapshot) {
		return r, teamUnavailable()
	}
	switch r.Action {
	case "invite":
		if !teamUUID(result.ID) || result.Role != request.Role || result.AfterRole != request.Role || result.BeforeRole != "" || result.Email != request.Email || result.ExpiresAt == nil || result.ExpiresAt.IsZero() {
			return r, teamUnavailable()
		}
	case "set_role":
		if result.UserID != request.TargetID || result.AfterRole != request.Role || !productValidTeamRole(result.BeforeRole) {
			return r, teamUnavailable()
		}
	case "remove_member":
		if result.UserID != request.TargetID || result.AfterRole != "" || !productValidTeamRole(result.BeforeRole) {
			return r, teamUnavailable()
		}
	case "revoke_invitation":
		if result.InvitationID != request.TargetID || result.AfterRole != "" || !productValidTeamRole(result.BeforeRole) {
			return r, teamUnavailable()
		}
	}
	r.ReviewHash = ""
	if c.matchesContext(request.Context) && accessRevision(request.Context.ReviewHash) {
		r.ReviewHash = request.Context.ReviewHash
	}
	r.Request, _ = json.Marshal(request.ProductTeamActionRequest)
	r.Result, _ = json.Marshal(result)
	return r, nil
}

func (s *Service) teamRecorded(r *http.Request, c customerTeam, id string) (api.ProductTeamActionReceipt, error) {
	var receipt api.ProductTeamActionReceipt
	if !teamUUID(id) {
		return receipt, state.ErrInvalidArgument
	}
	err := s.teamAuthCall(r.Context(), c, http.MethodGet, c.path("/team-actions/")+id, nil, &receipt)
	return receipt, err
}

func (s *Service) CustomerTeam(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, err := s.loadCustomerTeam(r, acct)
	if err != nil {
		teamRespond(w, nil, err)
		return
	}
	teamRespond(w, productTeamView(c), nil)
}
func (s *Service) CustomerTeamPreview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.ProductTeamActionRequest
	if !operation.DecodeOperationBody(w, r, &request, api.ProductControlAuthBodyBytes) {
		return
	}
	c, err := s.loadCustomerTeam(r, acct)
	var preview api.ProductTeamActionPreview
	if err == nil {
		preview, err = productTeamPreview(c, request)
	}
	teamRespond(w, preview, err)
}
func (s *Service) CustomerTeamConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var confirmation api.ProductTeamActionConfirmation
	if !operation.DecodeOperationBody(w, r, &confirmation, api.ProductControlAuthBodyBytes) {
		return
	}
	c, err := s.loadCustomerTeam(r, acct)
	var value api.ProductTeamActionResult
	if err == nil {
		value, err = s.applyTeam(r, c, confirmation)
	}
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.team.action_confirmed", &acct.ID, map[string]any{"app_id": c.realm.app.ID, "tenant_id": c.identity.TenantID, "organization_id": c.identity.OrganizationID, "action_id": value.Receipt.ID, "action": value.Receipt.Action, "actor_id": value.Receipt.ActorID, "replayed": value.Replayed})
	}
	teamRespond(w, value, err)
}

func (s *Service) applyTeam(r *http.Request, c customerTeam, confirmation api.ProductTeamActionConfirmation) (api.ProductTeamActionResult, error) {
	var reply api.ProductTeamActionResult
	request, err := normalizeTeamRequest(confirmation.Request)
	if err != nil || !reflect.DeepEqual(request, confirmation.Request) || !accessRevision(confirmation.AuthSnapshot) || !accessRevision(confirmation.ExpectedHash) {
		return reply, state.ErrInvalidArgument
	}
	old, err := s.teamRecorded(r, c, request.ID)
	if err == nil {
		var original teamAuthRequest
		if json.Unmarshal(old.Request, &original) != nil {
			return reply, teamUnavailable()
		}
		if old.ActorID != c.live.ActorID || !c.matchesContext(original.Context) || original.Context.ReviewHash != confirmation.ExpectedHash || original.ExpectedSnapshot != confirmation.AuthSnapshot || !reflect.DeepEqual(original.ProductTeamActionRequest, request) {
			return reply, productTeamError{http.StatusConflict, "product_team_action_conflict", "This reference already belongs to another confirmed action. Check its status."}
		}
		receipt, e := teamReceiptProjection(c, old)
		return api.ProductTeamActionResult{Receipt: receipt, Replayed: true}, e
	}
	var failure productTeamError
	if !errors.As(err, &failure) || failure.status != http.StatusNotFound {
		return reply, err
	}
	preview, err := productTeamPreview(c, request)
	if err != nil {
		return reply, err
	}
	if preview.Hash != confirmation.ExpectedHash || preview.AuthSnapshot != confirmation.AuthSnapshot {
		return reply, teamStale()
	}
	return s.dispatchTeam(r, c, preview)
}

var teamInvitationToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func (s *Service) dispatchTeam(r *http.Request, c customerTeam, p api.ProductTeamActionPreview) (api.ProductTeamActionResult, error) {
	var result struct {
		Receipt  api.ProductTeamActionReceipt `json:"receipt"`
		Replayed bool                         `json:"replayed"`
		Token    string                       `json:"token"`
	}
	body := teamAuthRequest{ProductTeamActionRequest: p.Request, ExpectedSnapshot: p.AuthSnapshot, Context: c.actionContext(p.Hash)}
	err := s.teamCurrentReview(r, c)
	if err != nil {
		return api.ProductTeamActionResult{}, err
	}
	err = s.teamAuthCall(r.Context(), c, http.MethodPost, c.path("/team-actions"), body, &result)
	if err != nil {
		return api.ProductTeamActionResult{}, err
	}
	var original teamAuthRequest
	if json.Unmarshal(result.Receipt.Request, &original) != nil || !reflect.DeepEqual(original, body) || result.Receipt.ActorID != c.live.ActorID {
		return api.ProductTeamActionResult{}, teamUnavailable()
	}
	receipt, err := teamReceiptProjection(c, result.Receipt)
	if err != nil {
		return api.ProductTeamActionResult{}, err
	}
	out := api.ProductTeamActionResult{Receipt: receipt, Replayed: result.Replayed}
	var evidence teamAuthResult
	_ = json.Unmarshal(receipt.Result, &evidence)
	if result.Token != "" {
		if result.Replayed || p.Request.Action != "invite" || evidence.Delivered || !teamInvitationToken.MatchString(result.Token) {
			return out, teamUnavailable()
		}
		link, err := url.Parse(c.realm.origin + "/ui/invitation")
		if err != nil || link.Scheme != "https" || link.Host == "" || link.User != nil {
			return out, teamUnavailable()
		}
		link.Fragment = "token=" + result.Token
		out.InvitationURL = link.String()
	} else if p.Request.Action == "invite" && !result.Replayed && !evidence.Delivered {
		return out, teamUnavailable()
	}
	return out, nil
}

func (s *Service) CustomerTeamAction(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, err := s.loadCustomerTeam(r, acct)
	var receipt api.ProductTeamActionReceipt
	if err == nil {
		receipt, err = s.teamRecorded(r, c, r.PathValue("change"))
	}
	if err == nil {
		receipt, err = teamReceiptProjection(c, receipt)
	}
	teamRespond(w, receipt, err)
}
func (s *Service) CustomerTeamHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	query, err := ownerQuery(r, "cursor")
	var c customerTeam
	var page api.ProductTeamActionHistory
	if err == nil {
		c, err = s.loadCustomerTeam(r, acct)
	}
	if err == nil {
		err = s.teamAuthCall(r.Context(), c, http.MethodGet, c.path("/team-actions")+"?cursor="+url.QueryEscape(query.Get("cursor")), nil, &page)
	}
	if err == nil {
		if page.Actions == nil || len(page.Actions) > api.ProductControlTeamHistoryPageSize || len(page.NextCursor) > api.ProductControlOwnerCursorMaxBytes {
			err = teamUnavailable()
		}
	}
	if err == nil {
		for i, receipt := range page.Actions {
			page.Actions[i], err = teamReceiptProjection(c, receipt)
			if err != nil {
				break
			}
		}
	}
	teamRespond(w, page, err)
}

func teamRespond(w http.ResponseWriter, value any, err error) {
	var failure productTeamError
	if errors.As(err, &failure) {
		api.WriteProblem(w, api.NewProblem(failure.status, failure.code, "Team action unavailable", failure.detail))
		return
	}
	var authFailure customerAuthFailure
	if errors.As(err, &authFailure) {
		customerError(w, err)
		return
	}
	respond(w, value, err)
}
