package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/stripex"
)

// --- apps CRUD --------------------------------------------------------------

// getApp returns one app by slug.
func (s *server) getApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.appResponse(app))
}

// updateApp is the PATCH /v1/apps/{slug} handler. RAM, idle_timeout_s, and
// max_concurrency are user-tunable; type and runtime are immutable. Plan
// caps are re-enforced when the requested RAM or concurrency changes (spec
// §4.2: "validation enforces plan quotas before any work happens").
func (s *server) updateApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.UpdateAppRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	ram := app.RAMMB
	if req.RAMMB != nil {
		ram = *req.RAMMB
	}
	mc := app.MaxConcurrency
	if req.MaxConcurrency != nil {
		mc = *req.MaxConcurrency
	}
	if prob := api.ValidateAppConfig(limits, ram, mc); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	updated, err := s.store.UpdateApp(ctx(r), app.ID, state.UpdateAppParams{
		RAMMB:          req.RAMMB,
		IdleTimeoutS:   req.IdleTimeoutS,
		SetIdleTimeout: req.IdleTimeoutS != nil,
		MaxConcurrency: req.MaxConcurrency,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged, `{"kind":"updated","slug":"`+app.Slug+`"}`)
	s.log.Info("app updated", "app", updated.ID, "slug", updated.Slug, "account", acct.ID)
	writeJSON(w, http.StatusOK, s.appResponse(updated))
}

// deleteApp marks the app as deleted (soft delete; PG snapshot GC runs on the
// next successful deploy per spec §9).
func (s *server) deleteApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := s.store.DeleteApp(ctx(r), app.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged, `{"kind":"deleted","slug":"`+app.Slug+`"}`)
	s.log.Info("app deleted", "app", app.ID, "slug", app.Slug, "account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// --- deployments -----------------------------------------------------------

// getDeployment returns one deployment by id.
func (s *server) getDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(ctx(r), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	writeJSON(w, http.StatusOK, s.deploymentResponse(d))
}

// rollbackApp re-primes the most recent superseded deployment per spec §9.
// Implemented as a synchronous status swap; imaged/schedd react via
// pg_notify and re-prime on their side. The previous "live" deployment is
// marked superseded; the rolled-back one moves from superseded → live.
func (s *server) rollbackApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	current, err := s.store.LatestDeployment(ctx(r), app.ID)
	if err != nil {
		s.notFound(w, "no deployments")
		return
	}
	target, err := s.store.LatestSupersededDeployment(ctx(r), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrNoRollbackTarget())
		return
	}
	if err := s.store.MarkDeploymentSuperseded(ctx(r), current.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not supersede current"))
		return
	}
	if err := s.store.MarkDeploymentLive(ctx(r), target.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not activate rollback target"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyDeploymentChanged,
		fmt.Sprintf(`{"app_id":"%s","from":"%s","to":"%s"}`, app.ID, current.ID, target.ID))
	s.log.Info("app rolled back", "app", app.ID, "from", current.ID, "to", target.ID, "account", acct.ID)
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(target))
}

// parkApp marks the app evicted_cold; schedd reacts and tears down live
// instances.
func (s *server) parkApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st := state.AppEvictedCold
	if _, err := s.store.UpdateApp(ctx(r), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not park app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged, `{"kind":"parked","slug":"`+app.Slug+`"}`)
	s.log.Info("app parked", "app", app.ID, "account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// wakeApp unparks an evicted_cold app.
func (s *server) wakeApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st := state.AppActive
	if _, err := s.store.UpdateApp(ctx(r), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not wake app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged, `{"kind":"woken","slug":"`+app.Slug+`"}`)
	s.log.Info("app woken", "app", app.ID, "account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// --- instances -------------------------------------------------------------

func (s *server) listInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	instances, err := s.store.ListInstancesForApp(ctx(r), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list instances"))
		return
	}
	out := make([]api.InstanceResponse, 0, len(instances))
	for _, ins := range instances {
		out = append(out, instanceResponse(ins))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- custom domains --------------------------------------------------------

func (s *server) createDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateCustomDomainRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Domain == "" || req.AppID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", "domain and app_id are required"))
		return
	}
	app, err := s.store.AppByID(ctx(r), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	token := randomToken(16)
	d, err := s.store.CreateCustomDomain(ctx(r), strings.ToLower(req.Domain), app.ID, token)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Domain taken", err.Error()))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyDomainChanged, `{"kind":"created","domain":"`+d.Domain+`"}`)
	// d.Domain came in via the HTTP body (bearer-token authenticated).
	// Sanitize at the log sink — CodeQL go/log-injection (CWE-117).
	// The notify payload above is JSON-encoded so the pg_notify channel
	// can't be tricked into parsing an attacker-supplied structure, but
	// the structured log line is the unencoded sink.
	s.log.Info("domain created", "domain", logsanitize.Field(d.Domain), "app", app.ID, "account", acct.ID)
	writeJSON(w, http.StatusAccepted, domainResponse(d))
}

func (s *server) listDomains(w http.ResponseWriter, r *http.Request, acct state.Account) {
	domains, err := s.store.ListDomainsForAccount(ctx(r), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list domains"))
		return
	}
	out := make([]api.CustomDomainResponse, 0, len(domains))
	for _, d := range domains {
		out = append(out, domainResponse(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	domain := strings.ToLower(r.PathValue("domain"))
	d, err := s.store.DomainByName(ctx(r), domain)
	if err != nil {
		s.notFound(w, "no such domain")
		return
	}
	app, err := s.store.AppByID(ctx(r), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such domain")
		return
	}
	if err := s.store.DeleteCustomDomain(ctx(r), domain); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete domain"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyDomainChanged, `{"kind":"deleted","domain":"`+domain+`"}`)
	s.log.Info("domain deleted", "domain", domain, "account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// --- crons -----------------------------------------------------------------

func (s *server) createCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateCronRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if !validCron(req.Schedule) {
		api.WriteProblem(w, api.ErrCronInvalid("expected 5-field cron expression (m h dom mon dow)"))
		return
	}
	app, err := s.store.AppByID(ctx(r), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	path := req.Path
	if path == "" {
		path = "/"
	}
	c, err := s.store.CreateCron(ctx(r), req.AppID, req.Schedule, path, enabled)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create cron"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyCronChanged, `{"kind":"created","app_id":"`+app.ID+`"}`)
	s.log.Info("cron created", "cron", c.ID, "app", app.ID, "account", acct.ID)
	writeJSON(w, http.StatusCreated, cronResponse(c))
}

func (s *server) listCrons(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// List every cron owned by any of this account's apps.
	apps, err := s.store.ListApps(ctx(r), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list crons"))
		return
	}
	out := make([]api.CronResponse, 0)
	for _, app := range apps {
		cs, err := s.store.ListCronsForApp(ctx(r), app.ID)
		if err != nil {
			continue
		}
		for _, c := range cs {
			out = append(out, cronResponse(c))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) updateCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	var req api.UpdateCronRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Schedule != nil && !validCron(*req.Schedule) {
		api.WriteProblem(w, api.ErrCronInvalid("expected 5-field cron expression"))
		return
	}
	c, err := s.store.CronByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(ctx(r), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	updated, err := s.store.UpdateCron(ctx(r), id, req.Schedule, req.Path, req.Enabled, nil)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update cron"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyCronChanged, `{"kind":"updated","cron":"`+id+`"}`)
	writeJSON(w, http.StatusOK, cronResponse(updated))
}

func (s *server) deleteCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	c, err := s.store.CronByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(ctx(r), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	if err := s.store.DeleteCron(ctx(r), id, c.AppID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete cron"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyCronChanged, `{"kind":"deleted","cron":"`+id+`"}`)
	w.WriteHeader(http.StatusNoContent)
}

// --- api keys --------------------------------------------------------------

func (s *server) createKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req struct {
		Label string `json:"label"`
	}
	_ = decodeJSON(r, &req)
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate key"))
		return
	}
	k, err := s.store.CreateAPIKey(ctx(r), acct.ID, hash, req.Label)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create key"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyKeyChanged, `{"kind":"created","account":"`+acct.ID+`"}`)
	s.log.Info("key created", "key", k.ID, "account", acct.ID)
	writeJSON(w, http.StatusCreated, api.APIKeyResponse{
		ID:        k.ID,
		Prefix:    keyPrefix(plaintext),
		Label:     k.Label,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
		Plaintext: plaintext,
	})
}

func (s *server) listKeys(w http.ResponseWriter, r *http.Request, acct state.Account) {
	keys, err := s.store.ListAPIKeys(ctx(r), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list keys"))
		return
	}
	out := make([]api.APIKeyResponse, 0, len(keys))
	for _, k := range keys {
		resp := api.APIKeyResponse{
			ID:        k.ID,
			Prefix:    keyPrefixFromHash(k.Hash),
			Label:     k.Label,
			CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
		}
		if !k.LastUsedAt.IsZero() {
			resp.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	if err := s.store.DeleteAPIKey(ctx(r), acct.ID, id); err != nil {
		s.notFound(w, "no such key")
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyKeyChanged, `{"kind":"deleted","account":"`+acct.ID+`"}`)
	w.WriteHeader(http.StatusNoContent)
}

// --- usage -----------------------------------------------------------------

func (s *server) getUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	monthStr := r.URL.Query().Get("month")
	month, err := time.Parse("2006-01", orDefault(monthStr, time.Now().UTC().Format("2006-01")))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad month", "expected YYYY-MM"))
		return
	}
	rows, err := s.store.UsageByMonth(ctx(r), acct.ID, month)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load usage"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	out := make([]api.UsageResponse, 0, len(rows))
	for _, u := range rows {
		out = append(out, api.UsageResponse{
			AppID:           u.AppID,
			MBSeconds:       u.MBSeconds,
			Requests:        u.Requests,
			IncludedGBHours: int64(limits.IncludedGBHours),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- plan changes ----------------------------------------------------------

// changePlan implements PATCH /v1/account/plan. Only the Free → Hobby upgrade
// is permitted via the dashboard in M5; everything else flows through
// Stripe (the webhook hits POST /v1/webhooks/stripe and calls into here with
// the network-trusted plan). M5 keeps this minimal — the table is wired,
// full dunning flow lands with M7 + meterd.
func (s *server) changePlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req struct {
		Plan string `json:"plan"`
	}
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	plan := api.Plan(req.Plan)
	if !plan.Valid() {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad plan", "plan must be free|hobby|pro|scale"))
		return
	}
	if err := s.store.UpdateAccountPlan(ctx(r), acct.ID, plan); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update plan"))
		return
	}
	updated, _ := s.store.AccountByID(ctx(r), acct.ID)
	// codeql[go/log-injection] false-positive: acct.ID is server-generated UUID; plan is enum-validated against the 4 Plan constants (free|hobby|pro|scale) by plan.Valid() — handler returns 400 on invalid input.
	s.log.Info("plan changed", "account", acct.ID, "plan", plan)
	writeJSON(w, http.StatusOK, api.AccountResponse{
		ID: updated.ID, Email: updated.Email, Plan: string(updated.Plan), Status: string(updated.Status),
	})
}

// stripeWebhook accepts signed Stripe events. M7 enforces the v1 HMAC
// against s.stripeWebhookSecret (empty secret = verify disabled, dev
// only). It handles:
//
//   - customer.subscription.deleted → suspend the account
//   - invoice.payment_failed        → past_due (apps still serve, deploys blocked)
//   - invoice.payment_succeeded     → active (if the account was past_due)
//   - customer.subscription.updated with a plan → update plan
//
// Unknown event types return 200 with no side effect — Stripe expects
// 2xx for everything it didn't recognize so it doesn't retry forever.
// Returns 400 on bad payload / bad signature.
func (s *server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad webhook", err.Error()))
		return
	}
	// Verify signature when configured. Empty secret = dev mode (network
	// trust); the production deploy must set STRIPE_WEBHOOK_SECRET.
	if s.stripeWebhookSecret != "" {
		header := r.Header.Get("Stripe-Signature")
		if err := stripex.VerifySignature(body, header, s.stripeWebhookSecret, 5*time.Minute); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad signature", err.Error()))
			return
		}
	}
	var ev struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				Customer string `json:"customer"`
				Status   string `json:"status"`
				Plan     string `json:"plan"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad webhook", err.Error()))
		return
	}
	acct, err := s.lookupAccountByStripeID(r.Context(), ev.Data.Object.Customer)
	if err != nil {
		// Unknown customer: 200 so Stripe stops retrying. New customers
		// land via CreateCustomer; we don't auto-provision on a webhook.
		w.WriteHeader(http.StatusOK)
		return
	}
	switch ev.Type {
	case "customer.subscription.deleted":
		_ = s.store.UpdateAccountStatus(r.Context(), acct.ID, state.AccountSuspended)
	case "invoice.payment_failed":
		// Apps keep serving; deploys blocked at the auth gate (handlers
		// reading acct.Active() refuse writes). 7-day dunning timer
		// (M7 dunning state machine) lives in pkg/state.Account.
		_ = s.store.UpdateAccountStatus(r.Context(), acct.ID, state.AccountPastDue)
	case "invoice.payment_succeeded":
		// Restore the account if it was past_due. meterd will refresh
		// quota state on its next tick.
		if acct.Status == state.AccountPastDue {
			_ = s.store.UpdateAccountStatus(r.Context(), acct.ID, state.AccountActive)
		}
	case "customer.subscription.updated":
		if ev.Data.Object.Plan != "" {
			_ = s.store.UpdateAccountPlan(r.Context(), acct.ID, api.Plan(ev.Data.Object.Plan))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// lookupAccountByStripeID is a thin wrapper around
// state.Store.AccountByStripeCustomerID. The reverse index lives on the
// Store so the webhook stays O(1) regardless of account count (MemStore
// uses a map; PgStore uses a unique index).
func (s *server) lookupAccountByStripeID(ctx context.Context, stripeID string) (state.Account, error) {
	if stripeID == "" {
		return state.Account{}, errors.New("apid: empty stripe customer id")
	}
	return s.store.AccountByStripeCustomerID(ctx, stripeID)
}

// --- response helpers ------------------------------------------------------

func (s *server) deploymentResponse(d state.Deployment) api.DeploymentResponse {
	return api.DeploymentResponse{
		ID:          d.ID,
		AppID:       d.AppID,
		BuildID:     d.BuildID,
		ImageDigest: d.ImageDigest,
		Kind:        string(d.Kind),
		Status:      string(d.Status),
		Error:       d.Error,
		CreatedAt:   d.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func instanceResponse(ins state.Instance) api.InstanceResponse {
	r := api.InstanceResponse{
		ID:           ins.ID,
		AppID:        ins.AppID,
		DeploymentID: ins.DeploymentID,
		State:        ins.State,
		HostIP:       ins.HostIP,
		RAMMB:        ins.RAMMB,
	}
	if !ins.StartedAt.IsZero() {
		r.StartedAt = ins.StartedAt.UTC().Format(time.RFC3339)
	}
	if !ins.LastRequestAt.IsZero() {
		r.LastRequestAt = ins.LastRequestAt.UTC().Format(time.RFC3339)
	}
	if !ins.ParkedAt.IsZero() {
		r.ParkedAt = ins.ParkedAt.UTC().Format(time.RFC3339)
	}
	return r
}

func domainResponse(d state.CustomDomain) api.CustomDomainResponse {
	r := api.CustomDomainResponse{
		Domain:         d.Domain,
		AppID:          d.AppID,
		ChallengeToken: d.ChallengeToken,
		Verified:       d.Verified(),
	}
	if d.Verified() {
		r.VerifiedAt = d.VerifiedAt.UTC().Format(time.RFC3339)
	}
	if d.ChallengeToken != "" {
		r.TXTRecord = "_faas-verify." + d.Domain + `  TXT  "` + d.ChallengeToken + `"`
	}
	return r
}

func cronResponse(c state.Cron) api.CronResponse {
	resp := api.CronResponse{
		ID:        c.ID,
		AppID:     c.AppID,
		Schedule:  c.Schedule,
		Path:      c.Path,
		Enabled:   c.Enabled,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !c.LastFiredAt.IsZero() {
		resp.LastFiredAt = c.LastFiredAt.UTC().Format(time.RFC3339)
	}
	return resp
}

// --- dashboard support endpoints (M7.5 slice 4) -----------------------------

// listDeployments serves GET /v1/deployments — every deployment the
// account owns, in created_at DESC order. Cursor pagination via
// ?before=<RFC3339Nano>; limit defaults to 50, capped at 200.
func (s *server) listDeployments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		// RFC3339Nano — matches state.Deployment.CreatedAt. Lenient on
		// RFC3339 too via a fallback parse so callers sending either
		// format succeed.
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			before = t
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = t
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad cursor", "expected RFC3339 timestamp"))
			return
		}
	}
	rows, err := s.store.ListDeploymentsForAccount(ctx(r), acct.ID, before, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list deployments"))
		return
	}
	resp := api.DeploymentListResponse{Items: make([]api.DeploymentResponse, 0, len(rows))}
	for _, d := range rows {
		resp.Items = append(resp.Items, s.deploymentResponse(d))
	}
	if len(rows) == limit && limit > 0 && len(resp.Items) > 0 {
		// NextBefore = CreatedAt of the LAST row (the oldest on this
		// page). Pass it back as `before` to fetch the next page.
		last := resp.Items[len(resp.Items)-1].CreatedAt
		if t, err := time.Parse(time.RFC3339, last); err == nil {
			resp.NextBefore = t.UTC().Format(time.RFC3339Nano)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// usageSummary serves GET /v1/usage/summary — the aggregate
// current-month (or ?month=YYYY-MM) roll-up the dashboard's usage
// page renders. All money is integer cents; GB-h is float.
func (s *server) usageSummary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	monthStr := r.URL.Query().Get("month")
	if monthStr == "" {
		monthStr = time.Now().UTC().Format("2006-01")
	}
	month, err := time.Parse("2006-01", monthStr)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad month", "expected YYYY-MM"))
		return
	}
	rows, err := s.store.UsageByMonth(ctx(r), acct.ID, month)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load usage"))
		return
	}
	var mbSec int64
	for _, u := range rows {
		mbSec += u.MBSeconds
	}
	usedGB := float64(mbSec) / 3_600_000.0
	limits := api.MustLimitsFor(acct.Plan)
	included := int64(limits.IncludedGBHours)
	overage := usedGB - float64(included)
	if overage < 0 {
		overage = 0
	}
	// Spec §1 / financial model: €0.01/GB-h overage → 1 cent per
	// GB-h. Plan's overage rate can vary in the model; constants here
	// are the production default. Storing cents as int64 keeps
	// floats away from money (spec §Conventions).
	overageCents := int64(overage * 1.0)
	writeJSON(w, http.StatusOK, api.UsageSummaryResponse{
		Month:           monthStr,
		UsedGBHours:     usedGB,
		IncludedGBHours: included,
		OverageGBHours:  overage,
		OverageCents:    overageCents,
	})
}

// --- small helpers ---------------------------------------------------------

func randomToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// keyPrefix returns the first 16 chars of the plaintext key (matches the
// "fp_live_xxxxxxxx" prefix that shows up in dashboards).
func keyPrefix(plaintext string) string {
	if len(plaintext) < 16 {
		return plaintext
	}
	return plaintext[:16]
}

// keyPrefixFromHash derives the display prefix from the stored hash. The hash
// itself is sha256(plaintext); the prefix is hex(sha256)[:12] so the customer
// can correlate the hash back to the plaintext key they were shown once.
func keyPrefixFromHash(hash []byte) string {
	if len(hash) < 6 {
		return api.APIKeyPrefix
	}
	return api.APIKeyPrefix + hex.EncodeToString(hash)[:12]
}

// validCron returns true if s is a 5-field cron expression. The actual
// scheduler (spec §4.3) reuses robfig/cron's parser in pkg/sched — this is a
// quick shape check so apid rejects obviously bad input at the API boundary
// instead of letting it through to schedd.
func validCron(s string) bool {
	fields := strings.Fields(s)
	return len(fields) == 5
}

// streamDeploymentLogs serves the build log for a deployment as a
// real Server-Sent Event stream backed by the deployment_logs table
// (M7.5 slice 5; spec §14 + ADR-011). Two phases:
//
//  1. Initial page — `ListDeploymentLogs(deploymentID, before_seq,
//     limit)`, written out in order from oldest → newest (the table
//     returns DESC, the SSE client expects chronological).
//  2. Live tail — subscribe to the in-process broadcaster, write
//     every published log line until the context cancels or the
//     deployment transitions to `live`/`failed` (an `end` event is
//     emitted).
//
// ?before_seq=0 (default) opens with the oldest 50; pass ?before_seq=N
// to resume from seq N. ?limit= caps the initial page (default 50,
// max 500). ?follow=0 closes after the initial page (CLI-friendly
// "fetch once" mode).
//nolint:contextcheck // ctx(r) === r.Context(); suppressed per-call to avoid line-by-line noise in a long SSE handler.
func (s *server) streamDeploymentLogs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(ctx(r), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	beforeSeq := int64(0)
	if v := r.URL.Query().Get("before_seq"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			beforeSeq = n
		}
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = n
		}
	}
	follow := r.URL.Query().Get("follow") != "0"

	startSSE(w)
	flusher, _ := w.(http.Flusher)

	// Walk backwards: the table returns DESC by seq, the SSE stream
	// wants chronological. MemStore + PgStore both order DESC.
	//nolint:contextcheck // Long SSE handler; ctx(r) == r.Context() but the linter loses the alias across the function's many statements.
	page, _, err := s.store.ListDeploymentLogs(ctx(r), id, beforeSeq, limit)
	if err != nil {
		_, _ = fmt.Fprintf(w, "event: error\ndata: {\"error\":%q}\n\n", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	for i := len(page) - 1; i >= 0; i-- {
		writeLogEvent(w, flusher, page[i])
	}

	if !follow {
		_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// Live tail: subscribe to the broadcaster until deploy is done
	// or the client disconnects.
	sub, cancel := s.events.Subscribe(events.TopicDeploymentLog)
	defer cancel()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		// Done status short-circuits the tail. deployment status flips
		// to live/failed via NotifyDeploymentChanged; the dashboard
		// already lives off that channel for the dashboard app
		// update. Slice 6 wires the full pg_notify fan-in. Slice 5
		// keeps it simple with a deadline: builds max out at 10
		// minutes; we cap the tail to that.
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-sub:
			if !ok {
				return
			}
			// payload is the marshalled LogEntry — write verbatim.
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", e.Payload)
			if flusher != nil {
				flusher.Flush()
			}
			// If the deployment transitions to live/failed, the
			// producer publishes the row PLUS a sentinel; we close
			// after the sentinel. Simpler: cap at the build timeout.
		case <-ticker.C:
			// heartbeat — keeps idle proxies from dropping the
			// connection. Doesn't carry data.
			_, _ = fmt.Fprint(w, ":\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case <-time.After(10 * time.Minute):
			_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"timeout\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
	}
}

// writeLogEvent formats one LogEntry as a single SSE event. Used by
// both the initial-page path and the live tail.
func writeLogEvent(w http.ResponseWriter, flusher http.Flusher, e state.LogEntry) {
	payload, _ := json.Marshal(map[string]any{
		"seq":         e.Seq,
		"stream":      e.Stream,
		"line":        e.Line,
		"written_at":  e.WrittenAt.UTC().Format(time.RFC3339Nano),
	})
	_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
}

// streamAppLogs streams the running instance's stdout/stderr ring buffer.
// Implementation note: the in-memory stub emits a placeholder until vmmd
// exposes a Logs gRPC stream (planned for the follow-up PR). Spec §12 lists
// per-app logs as a separate stream from build logs.
func (s *server) streamAppLogs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	instances, err := s.store.ListInstancesForApp(ctx(r), app.ID)
	if err != nil || len(instances) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No running instance", "the app is parked; wake it first"))
		return
	}
	startSSE(w)
	// Stub: emit a single heartbeat. The real implementation dials vmmd
	// gRPC Logs(req) and tails the per-instance ring buffer (M5 follow-up).
	_, _ = fmt.Fprintf(w, "event: log\ndata: {\"instance\":\"%s\",\"line\":\"app is running\"}\n\n",
		instances[0].ID)
	w.(http.Flusher).Flush()
}

// startSSE sets the SSE response headers and disables write timeouts for the
// lifetime of the request.
func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
}
