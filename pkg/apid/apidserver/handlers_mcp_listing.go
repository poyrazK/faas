package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcplisting"
	"github.com/onebox-faas/faas/pkg/state"
)

// A sold MCP server's public listing (ADR-20261126175800): the owner
// publishes, reads and unpublishes it here; pkg/apid/mcplisting validates
// and renders, pkg/apid/consumersignup serves the public side.

func (s *server) mcpListingStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.MCPListingStore, state.App, bool) {
	store, ok := s.store.(state.MCPListingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("MCP listings"))
		return nil, state.App{}, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	return store, app, ok
}

func (s *server) getMCPListing(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.mcpListingStore(w, r, acct)
	if !ok {
		return
	}
	listing, err := store.GetMCPListing(r.Context(), app.AccountID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not published",
			"this app has no published MCP listing; publish one with gregale mcp server listing publish").WithDocs(mcplisting.DocsURL))
		return
	}
	if err != nil {
		s.log.Error("read MCP listing", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not read the MCP listing"))
		return
	}
	writeJSON(w, http.StatusOK, mcplisting.Response(listing, app.Slug, consumerSignupHost{s: s}.PublicOrigin()))
}

func (s *server) putMCPListing(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.mcpListingStore(w, r, acct)
	if !ok {
		return
	}
	var req api.PutMCPListingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MCPListingRequestMaxBytes)).Decode(&req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body: "+err.Error()))
		return
	}
	listing, problem := mcplisting.Validate(req, app.AccountID, app.ID, s.appHosts(r, acct, app))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	stored, err := store.PutMCPListing(r.Context(), listing)
	if err != nil {
		s.log.Error("publish MCP listing", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not publish the MCP listing"))
		return
	}
	s.audit.Emit(r.Context(), "mcp_listing.published", &acct.ID, map[string]any{"app_id": app.ID, "version": stored.Version, "tools": len(stored.Tools)})
	writeJSON(w, http.StatusOK, mcplisting.Response(stored, app.Slug, consumerSignupHost{s: s}.PublicOrigin()))
}

func (s *server) deleteMCPListing(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.mcpListingStore(w, r, acct)
	if !ok {
		return
	}
	if err := store.DeleteMCPListing(r.Context(), app.AccountID, app.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
		s.log.Error("unpublish MCP listing", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not unpublish the MCP listing"))
		return
	}
	s.audit.Emit(r.Context(), "mcp_listing.unpublished", &acct.ID, map[string]any{"app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

// appHosts are the hosts an app answers on: its platform URL and its
// default custom domain.
func (s *server) appHosts(r *http.Request, acct state.Account, app state.App) []string {
	resp := s.appResponseWithContext(r.Context(), app, acct.Plan)
	hosts := []string{}
	for _, raw := range []string{resp.URL, resp.CanonicalURL} {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	return hosts
}
