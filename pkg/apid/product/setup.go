package product

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

type setupPublication struct {
	ExpectedHead string                 `json:"expected_head"`
	Spec         api.ProductCatalogSpec `json:"spec"`
}

func (s *Service) OwnerSetup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant_cursor")
	if err == nil {
		err = s.loadSetup(w, r, c, q.Get("tenant_cursor"))
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) loadSetup(w http.ResponseWriter, r *http.Request, c *ownerContext, cursor string) error {
	form := &dashboard.ProductControlSetup{Currency: "EUR", Feature: "export", Label: "Export", Operation: "export", Scope: api.DefaultEnvScope, StarterName: "starter", StarterLabel: "Starter", StarterStarts: "10", StarterPrice: "25000", ProName: "pro", ProLabel: "Pro", ProStarts: "20", ProPrice: "10000", ChooseCustomer: true}
	form.MaxStarts, form.MaxPrice = api.ProductControlMaxMonthlyStarts, api.ProductControlMaxUnitPriceMillicents
	c.data.Setup = form
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	if err == nil {
		c.data.LatestRevision = catalog.Revision
		form.Currency, form.AuthApp = catalog.Spec.Currency, catalog.Spec.AuthApp
		seedSetupForm(form, catalog)
	}
	apps, err := s.Host.Store().ListApps(r.Context(), c.account.ID)
	if err != nil {
		return err
	}
	for _, app := range apps {
		if app.AccountID == c.account.ID && app.Status != state.AppDeleted {
			form.Apps = append(form.Apps, app.Slug)
		}
	}
	sort.Strings(form.Apps)
	if len(form.Apps) > api.ProductControlOwnerPageSize {
		form.Apps = form.Apps[:api.ProductControlOwnerPageSize]
	}
	if err = s.setupTenants(r, c, cursor); err != nil {
		return err
	}
	form.ReviewToken, err = s.ownerToken(w, c, "setup_review", c.account.ID+"/"+c.app.ID)
	return err
}

func (s *Service) setupTenants(r *http.Request, c *ownerContext, cursor string) error {
	rows, next, err := c.tenants.ListPlatformTenantsPage(r.Context(), c.account.ID, api.ProductControlOwnerPageSize, 0, cursor)
	if err != nil {
		return err
	}
	if c.data.Setup == nil {
		c.data.Setup = &dashboard.ProductControlSetup{ChooseCustomer: true}
	}
	for _, tenant := range rows {
		c.data.Setup.Tenants = append(c.data.Setup.Tenants, dashboard.ProductControlCustomer{TenantID: tenant.ID, Name: tenant.Name, Status: tenant.Status, URL: ownerURL(c, tenant.ID, productcontrol.Period(time.Now()), nil)})
	}
	if next != "" {
		c.data.Setup.NextTenantsURL = c.data.BaseURL + "/setup?tenant_cursor=" + url.QueryEscape(next)
	}
	return nil
}

func (s *Service) OwnerSetupReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	fields := []string{"csrf_token", "auth_app", "currency", "feature", "label", "operation", "scope", "starter_name", "starter_label", "starter_starts", "starter_price", "pro_name", "pro_label", "pro_starts", "pro_price"}
	f, err := ownerParseForm(w, r, fields)
	if err == nil {
		err = s.ownerVerify(r, c, "setup_review", acct.ID+"/"+c.app.ID)
	}
	var publication setupPublication
	if err == nil {
		publication, err = setupSpec(r, c, f)
	}
	if err == nil {
		err = s.validateSetupAuth(r, c, publication.Spec)
	}
	if err == nil {
		err = s.renderSetupReview(w, r, c, publication)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func setupSpec(r *http.Request, c *ownerContext, f url.Values) (setupPublication, error) {
	p := setupPublication{Spec: api.ProductCatalogSpec{Features: map[string]api.ProductFeature{}, Plans: map[string]api.ProductPlan{}}}
	current, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return p, err
	}
	if err == nil {
		p.ExpectedHead = current.Revision
		p.Spec = current.Spec
		if current.Spec.Currency != f.Get("currency") {
			return p, state.ErrConflict
		}
	}
	p.Spec.AuthApp, p.Spec.Currency = f.Get("auth_app"), f.Get("currency")
	p.Spec.Features[f.Get("feature")] = api.ProductFeature{Label: f.Get("label"), Operation: &api.ProductOperationBinding{Name: f.Get("operation"), Scope: f.Get("scope")}}
	if f.Get("starter_name") == f.Get("pro_name") {
		return p, state.ErrInvalidArgument
	}
	for _, prefix := range []string{"starter", "pro"} {
		starts, err := strconv.ParseInt(f.Get(prefix+"_starts"), 10, 64)
		if err != nil || strconv.FormatInt(starts, 10) != f.Get(prefix+"_starts") {
			return p, state.ErrInvalidArgument
		}
		price, err := strconv.ParseInt(f.Get(prefix+"_price"), 10, 64)
		if err != nil || strconv.FormatInt(price, 10) != f.Get(prefix+"_price") {
			return p, state.ErrInvalidArgument
		}
		name := f.Get(prefix + "_name")
		plan := p.Spec.Plans[name]
		if plan.Features == nil {
			plan.Features = map[string]api.ProductFeatureGrant{}
		}
		plan.Label = f.Get(prefix + "_label")
		plan.Features[f.Get("feature")] = api.ProductFeatureGrant{MonthlyStarts: starts, UnitPriceMillicents: price}
		p.Spec.Plans[name] = plan
	}
	catalog, err := productcontrol.Compile(p.Spec)
	if err != nil {
		return p, state.ErrInvalidArgument
	}
	raw, _ := json.Marshal(catalog.Spec)
	if len(raw) > api.ProductControlMaxCatalogBytes {
		return p, state.ErrInvalidArgument
	}
	p.Spec = catalog.Spec
	return p, nil
}

func (s *Service) validateSetupAuth(r *http.Request, c *ownerContext, spec api.ProductCatalogSpec) error {
	if spec.AuthApp == "" || s.AuthOrigin == nil {
		return state.ErrInvalidArgument
	}
	_, _, err := s.AuthOrigin(r.Context(), c.account, spec.AuthApp)
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrProductAccess) {
		return state.ErrInvalidArgument
	}
	return err
}

func setupPayload(p setupPublication) string {
	raw, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *Service) renderSetupReview(w http.ResponseWriter, r *http.Request, c *ownerContext, p setupPublication) error {
	catalog, err := productcontrol.Compile(p.Spec)
	if err != nil {
		return state.ErrInvalidArgument
	}
	payload := setupPayload(p)
	token, err := s.ownerToken(w, c, "setup_confirm", ownerConfirmationSubject(c, payload))
	if err != nil {
		return err
	}
	before := p.ExpectedHead
	if before == "" {
		before = "No catalog"
	}
	c.data.Confirmation = &dashboard.ProductControlConfirmation{Title: "Publish product setup", Description: "Review all resulting terms below. Existing customer assignments keep their pinned revisions. The latest Auth app selects the login realm for every customer; changing it can remove access until organization links match that realm. Publication does not enable Operations or qualify a host.", Payload: payload, Token: token, ActionURL: c.data.BaseURL + "/setup/confirm", Details: []dashboard.ProductControlComparison{{Feature: "Catalog head", Before: before, After: catalog.Revision}, {Feature: "Customer login realm", After: p.Spec.AuthApp}, {Feature: "Currency", After: p.Spec.Currency}}}
	var old api.ProductCatalog
	if p.ExpectedHead != "" {
		old, err = c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, p.ExpectedHead)
		if err != nil {
			return err
		}
		c.data.Confirmation.Details[1].Before = old.Spec.AuthApp
	}
	featureNames := []string{}
	for name := range p.Spec.Features {
		featureNames = append(featureNames, name)
	}
	sort.Strings(featureNames)
	for _, name := range featureNames {
		c.data.Confirmation.Details = append(c.data.Confirmation.Details, dashboard.ProductControlComparison{Feature: "Binding / " + name, Before: setupBindingDescription(old.Spec.Features[name]), After: setupBindingDescription(p.Spec.Features[name])})
	}
	planNames := []string{}
	for name := range p.Spec.Plans {
		planNames = append(planNames, name)
	}
	sort.Strings(planNames)
	for _, name := range planNames {
		features := []string{}
		for feature := range p.Spec.Plans[name].Features {
			features = append(features, feature)
		}
		sort.Strings(features)
		for _, feature := range features {
			before := "No access"
			if _, exists := old.Spec.Plans[name].Features[feature]; exists {
				before = ownerGrantDescription(old, api.ProductCustomer{Plan: name}, feature)
			}
			c.data.Confirmation.Details = append(c.data.Confirmation.Details, dashboard.ProductControlComparison{Feature: name + " / " + feature, Before: before, After: ownerGrantDescription(catalog, api.ProductCustomer{Plan: name}, feature)})
		}
	}
	raw, _ := json.MarshalIndent(p.Spec, "", "  ")
	c.data.Setup = &dashboard.ProductControlSetup{CatalogJSON: string(raw)}
	return nil
}

func setupBindingDescription(feature api.ProductFeature) string {
	if feature.Operation != nil {
		return feature.Operation.Name + "@" + feature.Operation.Scope + " (follows fully serving deployment)"
	}
	if feature.DefinitionID != "" {
		return "Pinned definition " + feature.DefinitionID
	}
	return "No binding"
}

func seedSetupForm(form *dashboard.ProductControlSetup, catalog api.ProductCatalog) {
	if feature, ok := catalog.Spec.Features[form.Feature]; ok {
		form.Label = feature.Label
		if feature.Operation != nil {
			form.Operation, form.Scope = feature.Operation.Name, feature.Operation.Scope
		}
	}
	if plan, ok := catalog.Spec.Plans[form.StarterName]; ok {
		form.StarterLabel = plan.Label
		if grant, ok := plan.Features[form.Feature]; ok {
			form.StarterStarts, form.StarterPrice = strconv.FormatInt(grant.MonthlyStarts, 10), strconv.FormatInt(grant.UnitPriceMillicents, 10)
		}
	}
	if plan, ok := catalog.Spec.Plans[form.ProName]; ok {
		form.ProLabel = plan.Label
		if grant, ok := plan.Features[form.Feature]; ok {
			form.ProStarts, form.ProPrice = strconv.FormatInt(grant.MonthlyStarts, 10), strconv.FormatInt(grant.UnitPriceMillicents, 10)
		}
	}
}

func decodeSetup(payload string) (setupPublication, error) {
	var p setupPublication
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return p, state.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || setupPayload(p) != payload {
		return p, state.ErrInvalidArgument
	}
	if _, err := productcontrol.Compile(p.Spec); err != nil {
		return p, state.ErrInvalidArgument
	}
	return p, nil
}

func (s *Service) OwnerSetupConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormLimit(w, r, []string{"csrf_token", "payload"}, api.ProductControlSetupFormMaxBytes)
	var p setupPublication
	if err == nil {
		p, err = decodeSetup(f.Get("payload"))
	}
	if err == nil {
		err = s.ownerVerify(r, c, "setup_confirm", ownerConfirmationSubject(c, f.Get("payload")))
	}
	if err == nil {
		err = s.validateSetupAuth(r, c, p.Spec)
	}
	if err == nil {
		err = s.publishSetup(r, c, p)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) publishSetup(r *http.Request, c *ownerContext, p setupPublication) error {
	store, ok := s.Host.Store().(state.ProductSetupStore)
	if !ok {
		return state.ErrNotFound
	}
	catalog, err := store.PublishProductCatalog(r.Context(), c.account.ID, c.app.ID, p.Spec, p.ExpectedHead)
	if err != nil {
		return err
	}
	c.data.Notice = "Published catalog " + catalog.Revision + ". Choose an existing customer below, preview its plan assignment, then check readiness. Existing assignments were preserved."
	s.Host.EmitAudit(r.Context(), "product.catalog.published", &c.account.ID, map[string]any{"app_id": c.app.ID, "revision": catalog.Revision, "source": "guided_setup"})
	return s.setupTenants(r, c, "")
}
