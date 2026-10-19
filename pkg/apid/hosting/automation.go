package hosting

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type AutomationHost interface {
	HostingAutomationEnabled() bool
	HostingAutomationStore() state.HostingAutomationStore
}

func (s Service) automationHost(w http.ResponseWriter, a state.Account) (state.HostingAutomationStore, bool) {
	if !s.guard(w, a) {
		return nil, false
	}
	h, ok := s.Host.(AutomationHost)
	if !ok || !h.HostingAutomationEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_automation_disabled", "Site automation unavailable", "internal site automation is disabled"))
		return nil, false
	}
	if h.HostingAutomationStore() == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_automation_unconfigured", "Site automation unavailable", "upgrade all API writers before enabling site automation"))
		return nil, false
	}
	return h.HostingAutomationStore(), true
}
func (s Service) ListAutomationCredentials(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.automationHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	rows, err := store.ListHostingAutomationCredentials(r.Context(), a.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, rows)
}
func (s Service) CreateAutomationCredential(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.automationHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	var req api.CreateHostingAutomationCredentialRequest
	if httpkit.DecodeJSON(r, &req) != nil {
		api.WriteProblem(w, api.ErrValidation("a bounded credential request is required"))
		return
	}
	if len(req.Operations) == 0 {
		req.Operations = []string{"stage", "preview"}
	}
	ttl := req.TTLSeconds
	if ttl == 0 {
		ttl = int64(api.HostingAutomationTTLDefault / time.Second)
	}
	if !artifacts.ValidHostingAutomationLabel(req.Label) || !artifacts.ValidHostingAutomationOperations(req.Operations) || ttl < int64(api.HostingAutomationTTLMin/time.Second) || ttl > int64(api.HostingAutomationTTLMax/time.Second) {
		api.WriteProblem(w, api.ErrValidation("invalid label, operations or credential lifetime"))
		return
	}
	token, hash, err := api.GenerateHostingAutomationToken()
	if err != nil {
		failure(w, err)
		return
	}
	now := time.Now().UTC()
	c, err := store.CreateHostingAutomationCredential(r.Context(), api.HostingAutomationCredential{ID: uuid.NewString(), AccountID: a.ID, SiteID: site.ID, Label: req.Label, Hash: hash, Operations: req.Operations, CreatedAt: now, ExpiresAt: now.Add(time.Duration(ttl) * time.Second)})
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), a.ID, "hosting_automation_credential_created", map[string]any{"site_id": site.ID, "credential_id": c.ID, "operations": c.Operations})
	c.Hash = nil
	httpkit.WriteJSON(w, 201, api.HostingAutomationCredentialResponse{Credential: c, Token: token})
}
func (s Service) RevokeAutomationCredential(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.automationHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	id := r.PathValue("credential")
	if uuid.Validate(id) != nil {
		failure(w, state.ErrNotFound)
		return
	}
	if err := store.RevokeHostingAutomationCredential(r.Context(), a.ID, site.ID, id); err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), a.ID, "hosting_automation_credential_revoked", map[string]any{"site_id": site.ID, "credential_id": id})
	w.WriteHeader(204)
}
func (s Service) CreateCIRun(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.automationHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	id := r.PathValue("run")
	var req api.HostingCIRunRequest
	if !artifacts.ValidCIRunID(id) || httpkit.DecodeJSONSized(r, &req, api.HostingReleaseRequestMaxBytes) != nil || !artifacts.ValidCIRequest(req) {
		api.WriteProblem(w, api.ErrValidation("a stable run UUID, valid manifest/source and bounded preview TTL are required"))
		return
	}
	if req.ReservePreview && !s.allowReservedIntent(w, r, a, site.ID, id, req, store) {
		return
	}
	if !s.requireConfig(w, req.Manifest) {
		return
	}
	if req.Provenance != nil {
		if _, ok := s.historyHost(w, a); !ok {
			return
		}
	}
	run, err := store.CreateHostingCIRun(r.Context(), a.ID, site.ID, id, req)
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, run)
}
func (s Service) GetCIRun(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.automationHost(w, a)
	if !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	run, err := store.GetHostingCIRun(r.Context(), a.ID, site.ID, r.PathValue("run"))
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, run)
}
func (s Service) CreateCIPreview(w http.ResponseWriter, r *http.Request, a state.Account) {
	store, ok := s.automationHost(w, a)
	if !ok {
		return
	}
	if _, ok := s.previewHost(w, a); !ok {
		return
	}
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	preview, err := store.CreateHostingCIPreview(r.Context(), a.ID, site.ID, r.PathValue("run"))
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, 202, preview)
}
