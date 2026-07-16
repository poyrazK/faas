package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) whoami(w http.ResponseWriter, _ *http.Request, acct state.Account) {
	writeJSON(w, http.StatusOK, api.AccountResponse{
		ID: acct.ID, Email: acct.Email, Plan: string(acct.Plan), Status: string(acct.Status),
	})
}

func (s *server) listApps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	apps, err := s.store.ListApps(ctx(r), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list apps"))
		return
	}
	out := make([]api.AppResponse, 0, len(apps))
	for _, a := range apps {
		out = append(out, s.appResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateAppRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	app, prob := s.buildApp(acct, req, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Deployed-app count quota (needs the store, so not in ValidateAppConfig).
	if n, _ := s.store.CountDeployedApps(ctx(r), acct.ID); n >= limits.DeployedApps {
		api.WriteProblem(w, api.ErrPlanLimitApps(limits, n))
		return
	}
	created, err := s.store.CreateApp(ctx(r), app)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Slug taken", fmt.Sprintf("app slug %q is already in use", req.Slug)))
		return
	}
	s.log.Info("app created", "app", created.ID, "slug", created.Slug, "account", acct.ID)
	writeJSON(w, http.StatusCreated, s.appResponse(created))
}

// buildApp applies defaults and validates a create request, returning the App to
// persist or a *Problem describing the first violation.
func (s *server) buildApp(acct state.Account, req api.CreateAppRequest, limits api.Limits) (state.App, *api.Problem) {
	if !validSlug(req.Slug) {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid slug", "slug must be 3–40 chars, lowercase letters, digits, and hyphens")
	}
	typ := state.AppType(orDefault(req.Type, string(state.AppTypeApp)))
	if typ != state.AppTypeApp && typ != state.AppTypeFunction {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid type", "type must be app or function")
	}
	if typ == state.AppTypeFunction && req.Runtime != "node22" && req.Runtime != "python312" {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid runtime", "functions require runtime node22 or python312")
	}
	ram := req.RAMMB
	if ram == 0 {
		ram = limits.RAMMB
	}
	mc := req.MaxConcurrency
	if mc == 0 {
		mc = 1
	}
	if prob := api.ValidateAppConfig(limits, ram, mc); prob != nil {
		return state.App{}, prob
	}
	return state.App{
		AccountID: acct.ID, Slug: req.Slug, Type: typ, Runtime: req.Runtime,
		RAMMB: ram, MaxConcurrency: mc, IdleTimeoutS: req.IdleTimeoutS, Status: state.AppActive,
	}, nil
}

func (s *server) createDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, err := s.store.AppBySlug(ctx(r), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	var req api.CreateDeploymentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	digest, ok := parseImageDigest(req.Image)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Image required", "M5 supports digest-pinned image deploys only, e.g. registry.DOMAIN/app@sha256:..."))
		return
	}
	// apid writes the row and notifies; imaged/schedd advance it to PARKED (never
	// a direct call, spec §Component ownership).
	d, err := s.store.CreateDeployment(ctx(r), state.Deployment{
		AppID: app.ID, ImageDigest: digest, Status: state.DeployPending,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
		return
	}
	s.log.Info("deployment created", "deployment", d.ID, "app", app.ID, "digest", digest)
	writeJSON(w, http.StatusAccepted, api.DeploymentResponse{
		ID: d.ID, AppID: d.AppID, ImageDigest: d.ImageDigest, Status: string(d.Status),
	})
}

func (s *server) appResponse(a state.App) api.AppResponse {
	return api.AppResponse{
		ID: a.ID, Slug: a.Slug, Type: string(a.Type), Runtime: a.Runtime,
		RAMMB: a.RAMMB, MaxConcurrency: a.MaxConcurrency, IdleTimeoutS: a.IdleTimeoutS,
		Status: string(a.Status), URL: fmt.Sprintf("https://%s.apps.%s", a.Slug, s.domain),
	}
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

func validSlug(s string) bool { return slugRe.MatchString(s) }

// parseImageDigest requires a digest-pinned reference (spec gap G1: public
// registries, digest-pinned) and returns the sha256 digest.
func parseImageDigest(ref string) (string, bool) {
	i := strings.Index(ref, "@sha256:")
	if i < 0 {
		return "", false
	}
	digest := ref[i+1:]
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(digest) {
		return "", false
	}
	return digest, true
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
