// Package aiconsole serves the AI gateway's dashboard pages
// (ADR 20261022145950): the account's AI integrations, one integration's
// day, its trace explorer and one trace's timeline. It reads what the API
// serves and writes nothing.
package aiconsole

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prefix is the pages' path.
const Prefix = "/dashboard/ai"

const readTimeout = 5 * time.Second

// errStoreUnavailable reports a store that does not serve a read.
var errStoreUnavailable = errors.New("aiconsole: store does not serve this read")

// Host is what the pages need from apid.
type Host interface {
	// Store is apid's state store.
	Store() state.Store
	// Gate says why the account cannot use the AI gateway, or "".
	Gate(acct state.Account) string
	// Render writes a dashboard page for the signed-in account.
	Render(w http.ResponseWriter, r *http.Request, acct state.Account, page dashboard.Page)
}

// Service serves the pages.
type Service struct {
	Host Host
	Log  *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Serve routes a /dashboard/ai request.
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), Prefix), "/")
	var parts []string
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	for index, part := range parts {
		unescaped, err := url.PathUnescape(part)
		if err != nil || unescaped == "" {
			http.NotFound(w, r)
			return
		}
		parts[index] = unescaped
	}
	if reason := s.Host.Gate(acct); reason != "" {
		s.Host.Render(w, r, acct, dashboard.Page{Title: "AI gateway", Body: "ai_gateway",
			Data: dashboard.AIGatewayData{Unavailable: reason, Day: s.now().Format(dayLayout)}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	switch {
	case len(parts) == 0:
		s.overview(w, r, acct)
	case len(parts) == 1:
		s.integrationPage(w, r, acct, parts[0])
	case len(parts) == 2 && parts[1] == "traces":
		s.tracesPage(w, r, acct, parts[0])
	case len(parts) == 3 && parts[1] == "traces":
		s.tracePage(w, r, acct, parts[0], parts[2])
	default:
		http.NotFound(w, r)
	}
}

// aiIntegrations lists the account's AI integrations.
func (s *Service) aiIntegrations(ctx context.Context, accountID string) ([]state.OutboundIntegrationOffer, error) {
	bindings, ok := s.Host.Store().(state.OutboundBindingStore)
	if !ok {
		return nil, errStoreUnavailable
	}
	offers, err := bindings.ListOutboundIntegrationOffers(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ai := make([]state.OutboundIntegrationOffer, 0, len(offers))
	for _, offer := range offers {
		if offer.AIProvider != "" {
			ai = append(ai, offer)
		}
	}
	return ai, nil
}

// integration finds one of the account's AI integrations.
func (s *Service) integration(ctx context.Context, accountID, integrationID string) (state.OutboundIntegrationOffer, bool, error) {
	offers, err := s.aiIntegrations(ctx, accountID)
	if err != nil {
		return state.OutboundIntegrationOffer{}, false, err
	}
	for _, offer := range offers {
		if offer.ID == integrationID {
			return offer, true, nil
		}
	}
	return state.OutboundIntegrationOffer{}, false, nil
}

// otherApps names, in usage rollups, the account's apps the reader may no
// longer see.
const otherApps = "Other apps"

// appNames maps the account's app IDs the reader may see to their slugs and
// returns the rest, whose traces and calls the pages withhold. Company apps
// share their creator's AccountID, so a creator removed from a company
// keeps the integration but not the company apps' AI calls.
func (s *Service) appNames(ctx context.Context, acct state.Account) (map[string]string, map[string]bool) {
	apps, err := s.Host.Store().ListApps(ctx, acct.ID)
	names, hidden := make(map[string]string, len(apps)), make(map[string]bool)
	if err != nil {
		s.log().Warn("dashboard ai: app list unavailable", "account_id", acct.ID, "err", err)
		return names, hidden
	}
	visible, err := appaccess.VisibleAppsContext(ctx, s.Host.Store(), acct, apps)
	if err != nil {
		s.log().Warn("dashboard ai: app access unavailable", "account_id", acct.ID, "err", err)
		visible = nil
	}
	for _, app := range apps {
		hidden[app.ID] = true
	}
	for _, app := range visible {
		names[app.ID] = app.Slug
		delete(hidden, app.ID)
	}
	return names, hidden
}

func integrationHref(integrationID string) string {
	return Prefix + "/" + url.PathEscape(integrationID)
}

func tracesHref(integrationID string) string {
	return integrationHref(integrationID) + "/traces"
}

func traceHref(integrationID, traceID string) string {
	return tracesHref(integrationID) + "/" + url.PathEscape(traceID)
}

// unavailable is the notice for a read that failed.
func unavailable(what string, err error) string {
	if errors.Is(err, state.ErrInvalidArgument) {
		return "That " + what + " filter is not valid."
	}
	return "The " + what + " is temporarily unavailable."
}

func (s *Service) overview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	today := s.now().Format(dayLayout)
	data := dashboard.AIGatewayData{Day: today, Integrations: make([]dashboard.AIIntegrationRow, 0)}
	offers, err := s.aiIntegrations(r.Context(), acct.ID)
	if err != nil {
		s.log().Warn("dashboard ai: integrations unavailable", "account_id", acct.ID, "err", err)
		data.Unavailable = unavailable("integration list", err)
	}
	usageStore, _ := s.Host.Store().(state.OutboundAIStore)
	for _, offer := range offers {
		row := dashboard.AIIntegrationRow{Name: offer.Name, Provider: offer.AIProvider, Href: integrationHref(offer.ID),
			RequestLog: offer.AIRequestLogEnabled, OnlineEvaluation: offer.AIOnlineEvalPercent != nil, Alerts: offer.AIQualityAlerts,
			Requests: "—", ErrorRate: "—", Cost: "—", Flagged: "—", Score: "—"}
		if usageStore != nil {
			if usage, err := usageStore.GetOutboundAIUsage(r.Context(), acct.ID, offer.ID, today); err == nil {
				totals := sumUsage(usage.Items)
				row.Requests, row.ErrorRate = count(totals.Requests), percent(totals.ErrorRequests, totals.Requests)
				row.Cost, row.Flagged, row.Score = money(totals.CostMillicents), count(flagTotal(totals)), meanScore(totals)
			}
		}
		data.Integrations = append(data.Integrations, row)
	}
	s.Host.Render(w, r, acct, dashboard.Page{Title: "AI gateway", Body: "ai_gateway", Data: data})
}
