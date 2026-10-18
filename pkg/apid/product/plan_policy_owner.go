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

func (s *Service) OwnerPlanPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	_, err := ownerQuery(r)
	if err == nil {
		err = s.loadPlanPolicy(w, r, c)
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) loadPlanPolicy(w http.ResponseWriter, r *http.Request, c *ownerContext) error {
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil {
		return err
	}
	form := &dashboard.ProductControlPlanPolicy{Revision: catalog.Revision}
	policy := productcontrol.PlanPolicyConfiguration(catalog).Policy
	for source, targets := range policy.Transitions {
		for _, target := range targets {
			form.Transitions = append(form.Transitions, dashboard.ProductControlPlanTransition{Source: source, Target: target})
		}
	}
	sort.Slice(form.Transitions, func(i, j int) bool {
		a, b := form.Transitions[i], form.Transitions[j]
		if a.Source == b.Source {
			return a.Target < b.Target
		}
		return a.Source < b.Source
	})
	for name, plan := range catalog.Spec.Plans {
		form.Plans = append(form.Plans, dashboard.ProductControlPlan{Name: name, Label: plan.Label})
	}
	sort.Slice(form.Plans, func(i, j int) bool { return form.Plans[i].Name < form.Plans[j].Name })
	form.ReviewToken, err = s.ownerToken(w, c, "plan_policy_review", c.account.ID+"/"+c.app.ID)
	c.data.PlanPolicy = form
	return err
}

func (s *Service) OwnerPlanPolicyReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "head", "source", "target", "action"})
	if err == nil {
		err = s.ownerVerify(r, c, "plan_policy_review", acct.ID+"/"+c.app.ID)
	}
	var preview api.ProductPlanPolicyPreview
	if err == nil {
		preview, err = ownerPlanPolicyPreview(r, c, f)
	}
	if err == nil {
		err = s.renderPlanPolicyReview(w, c, preview)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func ownerPlanPolicyPreview(r *http.Request, c *ownerContext, f url.Values) (api.ProductPlanPolicyPreview, error) {
	if !accessRevision(f.Get("head")) {
		return api.ProductPlanPolicyPreview{}, state.ErrInvalidArgument
	}
	base, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, f.Get("head"))
	if err != nil {
		return api.ProductPlanPolicyPreview{}, err
	}
	head, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil {
		return api.ProductPlanPolicyPreview{}, err
	}
	if head.Revision != base.Revision {
		return api.ProductPlanPolicyPreview{}, state.ErrProductCatalogStale
	}
	policy := productcontrol.PlanPolicyConfiguration(base).Policy
	source, target := f.Get("source"), f.Get("target")
	if f.Get("action") == "close" {
		policy.Transitions = map[string][]string{}
	} else {
		if !productcontrol.ValidProductPlanName(source) || !productcontrol.ValidProductPlanName(target) {
			return api.ProductPlanPolicyPreview{}, state.ErrInvalidArgument
		}
		targets := []string{}
		for _, name := range policy.Transitions[source] {
			if name != target {
				targets = append(targets, name)
			}
		}
		switch f.Get("action") {
		case "allow":
			targets = append(targets, target)
		case "remove":
		default:
			return api.ProductPlanPolicyPreview{}, state.ErrInvalidArgument
		}
		sort.Strings(targets)
		if len(targets) == 0 {
			delete(policy.Transitions, source)
		} else {
			policy.Transitions[source] = targets
		}
	}
	return previewPlanPolicy(base, policy)
}

func planPolicyPayload(c api.ProductPlanPolicyChange) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *Service) renderPlanPolicyReview(w http.ResponseWriter, c *ownerContext, p api.ProductPlanPolicyPreview) error {
	payload := planPolicyPayload(p.Change)
	token, err := s.ownerToken(w, c, "plan_policy_confirm", ownerConfirmationSubject(c, payload))
	if err != nil {
		return err
	}
	roles := map[string]bool{}
	for source := range p.Before.Policy.Transitions {
		roles[source] = true
	}
	for source := range p.After.Policy.Transitions {
		roles[source] = true
	}
	names := []string{}
	for source := range roles {
		names = append(names, source)
	}
	sort.Strings(names)
	details := []dashboard.ProductControlComparison{{Feature: "Catalog revision", Before: p.Before.Revision, After: p.After.Revision}}
	for _, source := range names {
		details = append(details, dashboard.ProductControlComparison{Feature: source, Before: planTargetsDescription(p.Before.Policy.Transitions[source]), After: planTargetsDescription(p.After.Policy.Transitions[source])})
	}
	c.data.Confirmation = &dashboard.ProductControlConfirmation{Title: "Review self-service plan choices", Description: "Only customers with the separate Manage plan permission can select these transitions. Targets use the latest reviewed product terms. Changing this policy does not change any customer's plan, prices or consumed starts.", Payload: payload, Token: token, ActionURL: c.data.BaseURL + "/plan-policy/confirm", Details: details}
	return nil
}

func planTargetsDescription(targets []string) string {
	if len(targets) == 0 {
		return "No changes offered"
	}
	return strings.Join(targets, ", ")
}

func decodePlanPolicyPayload(payload string) (api.ProductPlanPolicyChange, error) {
	var c api.ProductPlanPolicyChange
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return c, state.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || planPolicyPayload(c) != payload {
		return c, state.ErrInvalidArgument
	}
	return c, nil
}

func (s *Service) OwnerPlanPolicyConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormLimit(w, r, []string{"csrf_token", "payload"}, api.ProductControlSetupFormMaxBytes)
	var change api.ProductPlanPolicyChange
	if err == nil {
		change, err = decodePlanPolicyPayload(f.Get("payload"))
	}
	if err == nil {
		err = s.ownerVerify(r, c, "plan_policy_confirm", ownerConfirmationSubject(c, f.Get("payload")))
	}
	if err == nil {
		_, err = s.applyPlanPolicy(r, acct, c.app, c.products, change)
	}
	if err == nil {
		err = s.loadPlanPolicy(w, r, c)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	c.data.Notice = "Self-service plan choices published. Customers also need the separate Manage plan permission."
	s.OwnerRender(w, r, acct, c.data)
}
