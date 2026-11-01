package apidserver

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/domainhosts"
	"github.com/onebox-faas/faas/pkg/state"
)

// getDomainHosts, setDomainHosts and clearDomainHosts serve a wildcard custom
// domain's host allowlist, /v1/domains/{domain}/hosts (ADR-520 amendment 2,
// pkg/apid/domainhosts). loadDomain applies the same ownership check as the
// domain's other routes; appaccess maps their organization actions.
func (s *server) getDomainHosts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, ok := s.loadDomain(w, r, acct, strings.ToLower(r.PathValue("domain")))
	if !ok {
		return
	}
	out, problem := domainhosts.Read(r.Context(), s.store, d)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) setDomainHosts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, ok := s.loadDomain(w, r, acct, strings.ToLower(r.PathValue("domain")))
	if !ok {
		return
	}
	var req api.SetCustomDomainHostsRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	out, problem := domainhosts.Replace(r.Context(), s.store, d, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "domain.hosts_set", &acct.ID, map[string]any{
		"app_id": d.AppID,
		"domain": d.Domain,
		"names":  out.Names,
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *server) clearDomainHosts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, ok := s.loadDomain(w, r, acct, strings.ToLower(r.PathValue("domain")))
	if !ok {
		return
	}
	if problem := domainhosts.Clear(r.Context(), s.store, d); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "domain.hosts_cleared", &acct.ID, map[string]any{
		"app_id": d.AppID,
		"domain": d.Domain,
	})
	w.WriteHeader(http.StatusNoContent)
}
