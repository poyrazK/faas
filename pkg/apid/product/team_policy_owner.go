package product

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) OwnerTeamPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	_, err := ownerQuery(r)
	if err == nil {
		err = s.loadTeamPolicy(w, r, c)
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) loadTeamPolicy(w http.ResponseWriter, r *http.Request, c *ownerContext) error {
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil {
		return err
	}
	form := &dashboard.ProductControlTeamPolicy{Revision: catalog.Revision}
	policy := productcontrol.TeamPolicyConfiguration(catalog).Policy
	for source, targets := range policy.Delegations {
		for _, target := range targets {
			form.Delegations = append(form.Delegations, dashboard.ProductControlTeamDelegation{Actor: source, Target: target})
		}
	}
	sort.Slice(form.Delegations, func(i, j int) bool {
		a, b := form.Delegations[i], form.Delegations[j]
		if a.Actor == b.Actor {
			return a.Target < b.Target
		}
		return a.Actor < b.Actor
	})
	for name := range productcontrol.AccessConfiguration(catalog).Policy.Roles {
		form.Roles = append(form.Roles, name)
	}
	sort.Strings(form.Roles)
	form.ReviewToken, err = s.ownerToken(w, c, "team_policy_review", c.account.ID+"/"+c.app.ID)
	c.data.TeamPolicy = form
	return err
}

func (s *Service) OwnerTeamPolicyReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "head", "actor", "role", "action"})
	if err == nil {
		err = s.ownerVerify(r, c, "team_policy_review", acct.ID+"/"+c.app.ID)
	}
	var preview api.ProductTeamPolicyPreview
	if err == nil {
		preview, err = ownerTeamPolicyPreview(r, c, f)
	}
	if err == nil {
		err = s.renderTeamPolicyReview(w, c, preview)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func ownerTeamPolicyPreview(r *http.Request, c *ownerContext, f url.Values) (api.ProductTeamPolicyPreview, error) {
	if !accessRevision(f.Get("head")) {
		return api.ProductTeamPolicyPreview{}, state.ErrInvalidArgument
	}
	base, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, f.Get("head"))
	if err != nil {
		return api.ProductTeamPolicyPreview{}, err
	}
	head, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil {
		return api.ProductTeamPolicyPreview{}, err
	}
	if head.Revision != base.Revision {
		return api.ProductTeamPolicyPreview{}, state.ErrProductCatalogStale
	}
	policy := productcontrol.TeamPolicyConfiguration(base).Policy
	source, target := f.Get("actor"), f.Get("role")
	if f.Get("action") == "close" {
		policy.Delegations = map[string][]string{}
	} else {
		if !productcontrol.ValidAccessRole(source) || !productcontrol.ValidAccessRole(target) {
			return api.ProductTeamPolicyPreview{}, state.ErrInvalidArgument
		}
		targets := []string{}
		for _, name := range policy.Delegations[source] {
			if name != target {
				targets = append(targets, name)
			}
		}
		switch f.Get("action") {
		case "allow":
			targets = append(targets, target)
		case "remove":
		default:
			return api.ProductTeamPolicyPreview{}, state.ErrInvalidArgument
		}
		sort.Strings(targets)
		if len(targets) == 0 {
			delete(policy.Delegations, source)
		} else {
			policy.Delegations[source] = targets
		}
	}
	return previewTeamPolicy(base, policy)
}

func teamPolicyPayload(c api.ProductTeamPolicyChange) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *Service) renderTeamPolicyReview(w http.ResponseWriter, c *ownerContext, p api.ProductTeamPolicyPreview) error {
	payload := teamPolicyPayload(p.Change)
	token, err := s.ownerToken(w, c, "team_policy_confirm", ownerConfirmationSubject(c, payload))
	if err != nil {
		return err
	}
	roles := map[string]bool{}
	for source := range p.Before.Policy.Delegations {
		roles[source] = true
	}
	for source := range p.After.Policy.Delegations {
		roles[source] = true
	}
	names := []string{}
	for source := range roles {
		names = append(names, source)
	}
	sort.Strings(names)
	details := []dashboard.ProductControlComparison{{Feature: "Catalog revision", Before: p.Before.Revision, After: p.After.Revision}}
	for _, source := range names {
		details = append(details, dashboard.ProductControlComparison{Feature: source, Before: teamRolesDescription(p.Before.Policy.Delegations[source]), After: teamRolesDescription(p.After.Policy.Delegations[source])})
	}
	c.data.Confirmation = &dashboard.ProductControlConfirmation{Title: "Review team role delegation", Description: "Only customers with the separate Manage team permission and workspace authority may manage these roles. Both the existing and proposed member role must be allowed. Roles affect the whole workspace. This policy does not change memberships or product prices.", Payload: payload, Token: token, ActionURL: c.data.BaseURL + "/team-policy/confirm", Details: details}
	return nil
}

func teamRolesDescription(targets []string) string {
	if len(targets) == 0 {
		return "No roles delegated"
	}
	return strings.Join(targets, ", ")
}

func decodeTeamPolicyPayload(payload string) (api.ProductTeamPolicyChange, error) {
	var c api.ProductTeamPolicyChange
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return c, state.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || teamPolicyPayload(c) != payload {
		return c, state.ErrInvalidArgument
	}
	return c, nil
}

func (s *Service) OwnerTeamPolicyConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormLimit(w, r, []string{"csrf_token", "payload"}, api.ProductControlSetupFormMaxBytes)
	var change api.ProductTeamPolicyChange
	if err == nil {
		change, err = decodeTeamPolicyPayload(f.Get("payload"))
	}
	if err == nil {
		err = s.ownerVerify(r, c, "team_policy_confirm", ownerConfirmationSubject(c, f.Get("payload")))
	}
	if err == nil {
		_, err = s.applyTeamPolicy(r, acct, c.app, c.products, change)
	}
	if err == nil {
		err = s.loadTeamPolicy(w, r, c)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	c.data.Notice = "Team role delegation published. Customers also need Manage team permission and workspace authority."
	s.OwnerRender(w, r, acct, c.data)
}
