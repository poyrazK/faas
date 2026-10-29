package hosting

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type RetentionHost interface {
	HostingRetentionEnabled() bool
	HostingRetentionStore() state.HostingRetentionStore
}

func (s Service) retentionHost(w http.ResponseWriter, a state.Account) (state.HostingRetentionStore, bool) {
	if !s.guard(w, a) {
		return nil, false
	}
	h, ok := s.Host.(RetentionHost)
	if !ok || !h.HostingRetentionEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_retention_disabled", "Hosting retention unavailable", "internal hosting retention is disabled"))
		return nil, false
	}
	if h.HostingRetentionStore() == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_retention_unconfigured", "Hosting retention unavailable", "upgrade every API writer before enabling retention"))
		return nil, false
	}
	return h.HostingRetentionStore(), true
}
func (s Service) GetCleanupPlan(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.retentionHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	rules, err := cleanupQueryRules(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide both bounded age values as whole seconds"))
		return
	}
	plan, err := store.GetHostingCleanupPlan(r.Context(), a.ID, site.ID, rules, time.Now().UTC())
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, plan)
}
func cleanupQueryRules(r *http.Request) (*api.HostingRetentionRules, error) {
	q := r.URL.Query()
	if !q.Has("pending_age_seconds") && !q.Has("staged_age_seconds") {
		return nil, nil
	}
	if len(q["pending_age_seconds"]) != 1 || len(q["staged_age_seconds"]) != 1 {
		return nil, state.ErrInvalidArgument
	}
	var rules api.HostingRetentionRules
	var err error
	rules.PendingAgeSeconds, err = strconv.ParseInt(q.Get("pending_age_seconds"), 10, 64)
	if err != nil {
		return nil, err
	}
	rules.StagedAgeSeconds, err = strconv.ParseInt(q.Get("staged_age_seconds"), 10, 64)
	if err != nil || !artifacts.ValidRetentionRules(rules) {
		return nil, state.ErrInvalidArgument
	}
	return &rules, nil
}
func (s Service) SetRetentionPolicy(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.retentionHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	var body struct {
		api.HostingRetentionRules
		ExpectedRevision *int64 `json:"expected_revision"`
		Enabled          *bool  `json:"enabled"`
	}
	if httpkit.DecodeJSON(r, &body) != nil || body.Enabled == nil || body.ExpectedRevision == nil || *body.ExpectedRevision < 0 || !artifacts.ValidRetentionRules(body.HostingRetentionRules) {
		api.WriteProblem(w, api.ErrValidation("a policy revision, enabled value and bounded age rules are required"))
		return
	}
	req := api.SetHostingRetentionRequest{HostingRetentionRules: body.HostingRetentionRules, ExpectedRevision: *body.ExpectedRevision, Enabled: *body.Enabled}
	policy, err := store.SetHostingRetentionPolicy(r.Context(), a.ID, site.ID, req)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), a.ID, "hosting_retention_policy_updated", map[string]any{"site_id": site.ID, "revision": policy.Revision, "enabled": policy.Enabled, "pending_age_seconds": policy.PendingAgeSeconds, "staged_age_seconds": policy.StagedAgeSeconds})
	httpkit.WriteJSON(w, 200, policy)
}

// New native writers record activity even while the retention switch is off,
// so a hot opt-in cannot race an upload already admitted by this process.
func (s Service) touchRelease(w http.ResponseWriter, r *http.Request, a state.Account, release api.HostingRelease) (api.HostingRelease, bool) {
	store, ok := s.Host.HostingStore().(state.HostingRetentionStore)
	if !ok {
		if h, enabled := s.Host.(RetentionHost); enabled && h.HostingRetentionEnabled() {
			api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_retention_unconfigured", "Hosting retention unavailable", "upgrade every API writer before enabling retention"))
			return release, false
		}
		return release, true
	}
	out, err := store.TouchHostingRelease(r.Context(), a.ID, release.SiteID, release.ID)
	if err != nil {
		failure(w, err)
		return release, false
	}
	return out, true
}
