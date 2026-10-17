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

func (s *Service) OwnerAccess(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	_, err := ownerQuery(r)
	if err == nil {
		err = s.loadAccess(w, r, c)
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) loadAccess(w http.ResponseWriter, r *http.Request, c *ownerContext) error {
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil {
		return err
	}
	config := productcontrol.AccessConfiguration(catalog)
	form := &dashboard.ProductControlAccess{Revision: config.Revision, AuthApp: config.AuthApp, Default: config.Default}
	for role := range config.Policy.Roles {
		form.Roles = append(form.Roles, dashboard.ProductControlAccessRole{Name: role, ProductPermissions: productcontrol.EffectiveAccess(&config.Policy, role)})
	}
	sort.Slice(form.Roles, func(i, j int) bool { return form.Roles[i].Name < form.Roles[j].Name })
	form.ReviewToken, err = s.ownerToken(w, c, "access_review", c.account.ID+"/"+c.app.ID)
	c.data.Access = form
	return err
}

func (s *Service) OwnerAccessReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "head", "role", "view_work", "run_work", "view_billing", "manage_plan", "manage_team", "action"})
	if err == nil {
		err = s.ownerVerify(r, c, "access_review", acct.ID+"/"+c.app.ID)
	}
	var preview api.ProductAccessChangePreview
	if err == nil {
		preview, err = ownerAccessPreview(r, c, f)
	}
	if err == nil {
		err = s.renderAccessReview(w, c, preview)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func ownerAccessPreview(r *http.Request, c *ownerContext, f url.Values) (api.ProductAccessChangePreview, error) {
	if !accessRevision(f.Get("head")) || !productcontrol.ValidAccessRole(f.Get("role")) || f.Get("action") != "set" && f.Get("action") != "remove" {
		return api.ProductAccessChangePreview{}, state.ErrInvalidArgument
	}
	for _, field := range []string{"view_work", "run_work", "view_billing", "manage_plan", "manage_team"} {
		if f.Has(field) && f.Get(field) != "true" {
			return api.ProductAccessChangePreview{}, state.ErrInvalidArgument
		}
	}
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, f.Get("head"))
	if err != nil {
		return api.ProductAccessChangePreview{}, err
	}
	current, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil {
		return api.ProductAccessChangePreview{}, err
	}
	if current.Revision != catalog.Revision {
		return api.ProductAccessChangePreview{}, state.ErrProductCatalogStale
	}
	policy := productcontrol.AccessConfiguration(catalog).Policy
	if f.Get("action") == "remove" {
		delete(policy.Roles, f.Get("role"))
	} else {
		permissions := []api.ProductPermission{}
		for _, entry := range []struct {
			field      string
			permission api.ProductPermission
		}{{"view_work", api.ProductPermissionViewWork}, {"run_work", api.ProductPermissionRunWork}, {"view_billing", api.ProductPermissionViewBilling}, {"manage_plan", api.ProductPermissionManagePlan}, {"manage_team", api.ProductPermissionManageTeam}} {
			if f.Get(entry.field) == "true" {
				permissions = append(permissions, entry.permission)
			}
		}
		policy.Roles[f.Get("role")] = permissions
	}
	return previewAccess(catalog, policy)
}

func accessPayload(change api.ProductAccessChangeRequest) string {
	raw, _ := json.Marshal(change)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *Service) renderAccessReview(w http.ResponseWriter, c *ownerContext, p api.ProductAccessChangePreview) error {
	payload := accessPayload(p.Change)
	token, err := s.ownerToken(w, c, "access_confirm", ownerConfirmationSubject(c, payload))
	if err != nil {
		return err
	}
	details := []dashboard.ProductControlComparison{{Feature: "Policy revision", Before: p.Before.Revision, After: p.After.Revision}, {Feature: "Application Auth", Before: p.Before.AuthApp, After: p.After.AuthApp}}
	roles := map[string]bool{}
	for role := range p.Before.Policy.Roles {
		roles[role] = true
	}
	for role := range p.After.Policy.Roles {
		roles[role] = true
	}
	names := []string{}
	for role := range roles {
		names = append(names, role)
	}
	sort.Strings(names)
	for _, role := range names {
		details = append(details, dashboard.ProductControlComparison{Feature: role, Before: accessDescription(productcontrol.EffectiveAccess(&p.Before.Policy, role)), After: accessDescription(productcontrol.EffectiveAccess(&p.After.Policy, role))})
	}
	c.data.Confirmation = &dashboard.ProductControlConfirmation{Title: "Review customer permissions", Description: "These permissions apply to every linked customer workspace on the next request, including customers on older plan revisions. Auth roles and membership are checked live. Run work includes viewing work. Customer prices, plans and accepted work retain their existing terms.", Payload: payload, Token: token, ActionURL: c.data.BaseURL + "/access/confirm", Details: details}
	return nil
}

func accessDescription(permissions api.ProductPermissions) string {
	labels := []string{}
	if permissions.ViewWork {
		labels = append(labels, "View work")
	}
	if permissions.RunWork {
		labels = append(labels, "Run work")
	}
	if permissions.ViewBilling {
		labels = append(labels, "View billing")
	}
	if permissions.ManagePlan {
		labels = append(labels, "Manage plan")
	}
	if permissions.ManageTeam {
		labels = append(labels, "Manage team")
	}
	if len(labels) == 0 {
		return "No product access"
	}
	return strings.Join(labels, ", ")
}

func decodeAccessPayload(payload string) (api.ProductAccessChangeRequest, error) {
	var change api.ProductAccessChangeRequest
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return change, state.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&change) != nil || d.Decode(new(any)) != io.EOF || accessPayload(change) != payload {
		return change, state.ErrInvalidArgument
	}
	return change, nil
}

func (s *Service) OwnerAccessConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormLimit(w, r, []string{"csrf_token", "payload"}, api.ProductControlSetupFormMaxBytes)
	var change api.ProductAccessChangeRequest
	if err == nil {
		change, err = decodeAccessPayload(f.Get("payload"))
	}
	if err == nil {
		err = s.ownerVerify(r, c, "access_confirm", ownerConfirmationSubject(c, f.Get("payload")))
	}
	if err == nil {
		_, err = s.applyAccess(r, acct, c.app, c.products, change)
	}
	if err == nil {
		err = s.loadAccess(w, r, c)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	c.data.Notice = "Customer permissions published. Each customer's next request checks their current role against this policy."
	s.OwnerRender(w, r, acct, c.data)
}
