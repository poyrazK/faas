package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/domainplan"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) planDomains(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var manifest api.DomainManifest
	if err := decodeJSONSized(r, &manifest, api.DomainPlanMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	in, problem := s.domainPlanInput(r, acct, manifest)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.serveDomainPlanFenced(w, r, acct, func(w http.ResponseWriter, r *http.Request, acct state.Account) { s.respondDomainPlan(w, r, in) })
}

func (s *server) respondDomainPlan(w http.ResponseWriter, r *http.Request, in state.DomainPlanInput) {
	store, ok := s.store.(state.DomainPlanStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("domain plans are unavailable"))
		return
	}
	plan, err := store.PlanDomainBindings(s.defaultDomainContext(r.Context()), in)
	if err != nil {
		s.domainPlanProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) applyDomains(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.DomainApplyRequest
	if err := decodeJSONSized(r, &req, api.DomainPlanMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if !req.Confirm || !domainplan.ValidDigest(req.ExpectedPlanSHA256) {
		api.WriteProblem(w, api.ErrValidation("confirm and the reviewed expected_plan_sha256 are required"))
		return
	}
	in, problem := s.domainPlanInput(r, acct, req.Manifest)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	// Reauthorize before even replaying a receipt. Canonicalize the consumed
	// body so the standard request-bound idempotency middleware can hash it.
	body, _ := json.Marshal(req)
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.serveDomainPlanFenced(w, r, acct, s.idempotentWithInEnvironment("", func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		s.commitDomainPlan(w, r, acct, in, req.ExpectedPlanSHA256)
	}, func(_ context.Context, _ state.Account, status int, _ []byte) bool {
		return status < 200 || status >= 300
	}, true))
}

// Manifest routes address several apps through their body. Apply the common
// organization revocation fence after all of them have been authorized.
func (s *server) serveDomainPlanFenced(w http.ResponseWriter, r *http.Request, acct state.Account, next accountHandler) {
	if grant, ok := appaccess.From(r); ok {
		appaccess.ServeFenced(s.store, middleware.AccountHandler(next), w, r, acct, grant.Fence)
		return
	}
	next(w, r, acct)
}

func (s *server) commitDomainPlan(w http.ResponseWriter, r *http.Request, acct state.Account, in state.DomainPlanInput, expected string) {
	store, ok := s.store.(state.DomainPlanStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("domain plans are unavailable"))
		return
	}
	p, domains, err := store.ApplyDomainBindings(s.defaultDomainContext(r.Context()), in, expected)
	if err != nil {
		s.domainPlanProblem(w, err)
		return
	}
	response := api.DomainApplyResponse{Plan: p, Rollback: domainplan.Rollback(p), Domains: make([]api.CustomDomainResponse, 0, len(domains))}
	for i, d := range domains {
		response.Domains = append(response.Domains, s.domainResponseWithDefault(r.Context(), d))
		s.domainPlanCommitted(r, acct, p.Changes[i])
	}
	s.domainChecks.Wake()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) domainPlanInput(r *http.Request, actor state.Account, manifest api.DomainManifest) (state.DomainPlanInput, *api.Problem) {
	m, err := domainplan.Normalize(manifest)
	if err != nil {
		return state.DomainPlanInput{}, api.ErrValidation(err.Error())
	}
	in := state.DomainPlanInput{Manifest: m}
	for _, spec := range m.Domains {
		if err := state.ValidateCustomDomainClaim(spec.Domain); err != nil {
			return in, api.ErrValidation(err.Error())
		}
		app, owner, problem := s.namedAppAs(r, actor, spec.App, true)
		if problem != nil {
			return in, problem
		}
		if in.AccountID == "" {
			in.AccountID, in.OrgID = app.AccountID, app.OrgID
			var ok bool
			in.AppLimit, in.AccountLimit, ok = api.CustomDomainLimitsFor(owner.Plan)
			if !ok {
				return in, api.ErrCapacity("unknown plan")
			}
		}
		if app.AccountID != in.AccountID || app.OrgID != in.OrgID {
			return in, api.ErrValidation("all bindings must share an account namespace and organization")
		}
		if problem := s.domainSettingsProblem(r, owner, spec.Domain); problem != nil {
			return in, problem
		}
		if state.IsWildcardCustomDomain(spec.Domain) {
			limits, _ := api.LimitsFor(owner.Plan)
			if !limits.WildcardDomainsAllowed {
				return in, api.ErrWildcardDomainsNotAllowed(owner.Plan)
			}
		}
		target := api.DomainBindingTarget{App: app.Slug, AppID: app.ID, Environment: spec.Environment}
		// Accept slugs only in a manifest, so it remains portable and readable.
		if spec.App != app.Slug {
			return in, api.ErrValidation("manifest app must be an app slug")
		}
		if spec.Environment != "" {
			e, err := s.store.ProjectEnvironmentBySlug(r.Context(), app.AccountID, app.ProjectID, spec.Environment)
			if err != nil {
				return in, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Environment unavailable", "no such environment in the app's project")
			}
			target.EnvironmentID = e.ID
		}
		claim, err := s.store.DomainByName(r.Context(), spec.Domain)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return in, api.ErrCapacity("could not read domain binding")
		}
		if err == nil {
			source, _, problem := s.namedAppAs(r, actor, claim.AppID, false)
			if problem != nil {
				return in, problem
			}
			if source.AccountID != in.AccountID || source.OrgID != in.OrgID {
				return in, api.NewProblem(http.StatusConflict, api.CodeConflict, "Domain ownership differs", "domain moves cannot transfer ownership between accounts or organizations")
			}
		}
		in.Targets = append(in.Targets, target)
	}
	return in, nil
}

func (s *server) domainPlanProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such domain, app or environment")
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	case errors.Is(err, state.ErrCustomDomainQuotaExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeQuotaExhausted, "Custom domain quota reached", err.Error()))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Domain plan cannot apply", err.Error()))
	case errors.Is(err, state.ErrOrgAccessRevoked):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Access changed", "authorize and review a new domain plan"))
	default:
		api.WriteProblem(w, domainPublicationWriteProblem(err))
	}
}

func (s *server) domainPlanCommitted(r *http.Request, acct state.Account, c api.DomainBindingChange) {
	if c.Action == "noop" {
		return
	}
	payload, _ := json.Marshal(map[string]string{"kind": "binding_changed", "domain": c.Domain})
	_ = s.notif.Notify(r.Context(), db.NotifyDomainChanged, string(payload))
	kind := "domain.moved"
	if c.Action == "create" {
		kind = "domain.added"
	}
	data := map[string]any{"domain": c.Domain, "app_id": c.After.AppID, "environment": c.After.Environment, "before": c.Before, "clears_default": c.ClearsDefault}
	s.audit.Emit(r.Context(), kind, &acct.ID, data)
	if app, err := s.store.AppByID(r.Context(), c.After.AppID); err == nil {
		s.recordAppActivity(r.Context(), r, acct, app, state.OrgActivity{Kind: kind, ResourceType: "domain", ResourceID: c.Domain, ResourceLabel: c.Domain, SourceType: kind, SourceID: activitySourceID(r, c.Domain), Data: activityData(data)})
	}
}
