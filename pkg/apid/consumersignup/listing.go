package consumersignup

// adr: 20261126175800 — the public side of a sold MCP server's listing:
// its JSON, its page and its MCP registry server.json. They answer only
// while the seller has the listing published.

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/mcplisting"
	"github.com/onebox-faas/faas/pkg/state"
)

// publicListing resolves a published listing behind the public read rate
// limit; on false it has written the response.
func (s *Service) publicListing(w http.ResponseWriter, r *http.Request) (state.App, state.APIConsumerSignupSettings, state.MCPListing, bool) {
	if !s.allow(s.Host.ClientIP(r), false) {
		rateLimited(w, api.CodeConsumerSignupRateLimited, "too many requests from this address", api.ConsumerSignupReadsPerIPPerMinute)
		return state.App{}, state.APIConsumerSignupSettings{}, state.MCPListing{}, false
	}
	app, set, ok := s.publicApp(w, r, false)
	if !ok {
		return state.App{}, state.APIConsumerSignupSettings{}, state.MCPListing{}, false
	}
	listings, ok := s.Host.Store().(state.MCPListingStore)
	if !ok {
		httpkit.NotFound(w, "this MCP server is not listed")
		return state.App{}, state.APIConsumerSignupSettings{}, state.MCPListing{}, false
	}
	listing, err := listings.GetMCPListing(r.Context(), app.AccountID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "this MCP server is not listed")
		return state.App{}, state.APIConsumerSignupSettings{}, state.MCPListing{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read the listing"))
		return state.App{}, state.APIConsumerSignupSettings{}, state.MCPListing{}, false
	}
	return app, set, listing, true
}

// Listing serves a published listing's JSON.
func (s *Service) Listing(w http.ResponseWriter, r *http.Request) {
	app, set, listing, ok := s.publicListing(w, r)
	if !ok {
		return
	}
	out := api.PublicMCPListingResponse{App: app.Slug, Title: listing.Title, Summary: listing.Summary, Endpoint: listing.Endpoint,
		Tools: mcplisting.Tools(listing.Tools), Plans: []api.ConsumerSignupPlan{}, TrialDays: set.TrialDays, SignupOpen: set.Enabled,
		Install: mcplisting.Install(app.Slug, listing.Endpoint), PublishedAt: listing.PublishedAt}
	if set.Enabled {
		out.SignupURL = strings.TrimRight(s.Host.PublicOrigin(), "/") + "/v1/consumer-signup/" + url.PathEscape(app.Slug) + "/page"
	}
	if connections, ok := s.Host.Store().(state.StatementInvoiceExportStore); ok {
		if conn, err := connections.GetBillingConnection(r.Context(), app.AccountID, state.BillingProviderStripe); err == nil {
			out.Livemode = conn.Livemode
		}
	}
	plans, err := s.OfferedPlans(r.Context(), app, set)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read the plans"))
		return
	}
	out.Plans = plans
	if listing.ShowUsage {
		consumers, err := s.Host.Store().ListAPIConsumersForApp(r.Context(), app.AccountID, app.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrInternal("could not read the listing"))
			return
		}
		var open int64
		for _, c := range consumers {
			if c.RevokedAt == nil {
				open++
			}
		}
		out.Customers = &open
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// ListingPage serves a published listing's page.
func (s *Service) ListingPage(w http.ResponseWriter, r *http.Request) {
	if _, _, _, ok := s.publicListing(w, r); !ok {
		return
	}
	servePageFile(w, "listing.html", "text/html; charset=utf-8")
}

// ListingScript serves the listing page's script.
func (s *Service) ListingScript(w http.ResponseWriter, _ *http.Request) {
	servePageFile(w, "listing.js", "text/javascript; charset=utf-8")
}

// ListingStyle serves the listing page's stylesheet.
func (s *Service) ListingStyle(w http.ResponseWriter, _ *http.Request) {
	servePageFile(w, "listing.css", "text/css; charset=utf-8")
}

// ServerJSON serves the listing in the MCP registry server.json format
// when the seller set a registry name.
func (s *Service) ServerJSON(w http.ResponseWriter, r *http.Request) {
	app, _, listing, ok := s.publicListing(w, r)
	if !ok {
		return
	}
	if listing.RegistryName == "" {
		httpkit.NotFound(w, "this MCP server has no registry name")
		return
	}
	doc, err := mcplisting.ServerJSON(listing, app.Slug, s.Host.PublicOrigin())
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not render server.json"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(doc)
}
