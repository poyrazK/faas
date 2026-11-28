package mcplisting

// adr: 20261126175800 — the owner side of a sold MCP server's listing:
// publish, read and unpublish. pkg/apid/consumersignup serves the public
// side; pkg/apid/apidserver only wires the routes.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what the owner routes need from apid.
type Host interface {
	Store() state.Store
	// AppRoute resolves the route's app; on false it has written the problem.
	AppRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool)
	// AppHosts are the hosts the app answers on now: its platform URL and
	// its default custom domain.
	AppHosts(ctx context.Context, app state.App) []string
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
	// PublicOrigin is the https origin the public listing is served from.
	PublicOrigin() string
}

// Owner serves GET, PUT and DELETE /v1/apps/{slug}/mcp-listing.
type Owner struct {
	Host Host
	Log  *slog.Logger
}

func (o Owner) route(w http.ResponseWriter, r *http.Request, acct state.Account) (state.MCPListingStore, state.App, bool) {
	store, ok := o.Host.Store().(state.MCPListingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("MCP listings"))
		return nil, state.App{}, false
	}
	app, ok := o.Host.AppRoute(w, r, acct)
	return store, app, ok
}

func notPublished(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not published",
		"this app has no published MCP listing; publish one with gregale mcp server listing publish").WithDocs(DocsURL))
}

// Get reads the published listing.
func (o Owner) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := o.route(w, r, acct)
	if !ok {
		return
	}
	listing, err := store.GetMCPListing(r.Context(), app.AccountID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		notPublished(w)
		return
	}
	if err != nil {
		o.Log.Error("read MCP listing", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not read the MCP listing"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, Response(listing, app.Slug, o.Host.PublicOrigin()))
}

// Put publishes or republishes the listing.
func (o Owner) Put(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := o.route(w, r, acct)
	if !ok {
		return
	}
	var req api.PutMCPListingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MCPListingRequestMaxBytes)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			observed := r.ContentLength
			if observed <= api.MCPListingRequestMaxBytes {
				observed = api.MCPListingRequestMaxBytes + 1
			}
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.MCPListingRequestMaxBytes, observed).WithDocs(DocsURL))
			return
		}
		api.WriteProblem(w, api.ErrValidation("invalid JSON body: "+err.Error()))
		return
	}
	listing, problem := o.validate(r.Context(), req, app, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	stored, err := store.PutMCPListing(r.Context(), listing)
	if err != nil {
		o.Log.Error("publish MCP listing", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not publish the MCP listing"))
		return
	}
	o.Host.Audit(r.Context(), "mcp_listing.published", acct.ID, map[string]any{"app_id": app.ID, "version": stored.Version, "tools": len(stored.Tools),
		"contract_sha256": stored.ContractSHA256, "tool_scan_high": stored.ToolScanHigh})
	if stored.ToolScanReview != "" {
		o.Host.Audit(r.Context(), "mcp_listing.tool_scan_reviewed", acct.ID, map[string]any{"app_id": app.ID, "version": stored.Version,
			"high": stored.ToolScanHigh, "reason": stored.ToolScanReview})
	}
	httpkit.WriteJSON(w, http.StatusOK, Response(stored, app.Slug, o.Host.PublicOrigin()))
}

// validate checks the request, keeps the current contract on a republish
// without one, and runs the tool safety scan (ADR-20261128135329).
func (o Owner) validate(ctx context.Context, req api.PutMCPListingRequest, app state.App, acct state.Account) (state.MCPListing, *api.Problem) {
	listing, problem := o.keepOrValidate(ctx, req, app)
	if problem != nil {
		return listing, problem
	}
	return ApplyToolScan(req, listing, "user:"+acct.ID)
}

// keepOrValidate checks the request and, on a republish without a contract,
// keeps the one production deploys are checked against now.
func (o Owner) keepOrValidate(ctx context.Context, req api.PutMCPListingRequest, app state.App) (state.MCPListing, *api.Problem) {
	listing, problem := Validate(req, app.AccountID, app.ID, o.Host.AppHosts(ctx, app))
	if problem != nil || len(req.Contract) > 0 || req.DropContract {
		return listing, problem
	}
	var current *state.MCPContractBaseline
	if gate, ok := o.Host.Store().(state.MCPContractStore); ok {
		baseline, err := gate.MCPContractBaseline(ctx, app.ID)
		switch {
		case err == nil && baseline.AccountID == app.AccountID:
			current = &baseline
		case err != nil && !errors.Is(err, state.ErrNotFound):
			o.Log.Error("read MCP contract baseline", "app_id", app.ID, "err", err)
			return state.MCPListing{}, api.ErrInternal("could not read the MCP listing's tool contract")
		}
	}
	return KeepContract(req, listing, current)
}

// Delete unpublishes the listing; 404 when none is published.
func (o Owner) Delete(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := o.route(w, r, acct)
	if !ok {
		return
	}
	err := store.DeleteMCPListing(r.Context(), app.AccountID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		notPublished(w)
		return
	}
	if err != nil {
		o.Log.Error("unpublish MCP listing", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not unpublish the MCP listing"))
		return
	}
	o.Host.Audit(r.Context(), "mcp_listing.unpublished", acct.ID, map[string]any{"app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

// OnAppHost reports whether a stored endpoint is still on one of the app's
// hosts; a listing whose app stopped answering there must not serve it.
func OnAppHost(endpoint string, hosts []string) bool {
	return checkEndpoint(endpoint, hosts) == nil
}
