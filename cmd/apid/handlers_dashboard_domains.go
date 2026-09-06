package main

// Dashboard surface for per-app custom domains (issue #1397 / G5).
// Durable certificate state comes from custom_domains; the latest cached
// domain-doctor observation is shown inline, with the existing doctor page
// linked for a bounded refresh.

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// parseAppDomainsPath recognizes the per-app domains page. The trailing slash
// is accepted to match the other dashboard app subpages.
func parseAppDomainsPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/domains"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// renderAppDomains renders the app's custom-domain bindings, durable TLS
// lifecycle, and the most recent cached domain-doctor checks.
func (s *server) renderAppDomains(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppDomains: count deployed apps", "account_id", acct.ID, "err", err)
	}
	data := dashboard.AppDomainsData{App: dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)}}
	domains, err := s.store.ListDomainsForApp(ctx, app.ID)
	if err != nil {
		data.ErrorMessage = "Domain data is temporarily unavailable. Please try again shortly."
		log.Warn("dashboard renderAppDomains: list domains", "account_id", acct.ID, "app_id", app.ID, "err", err)
	} else {
		data.Domains = s.projectDashboardDomains(ctx, log, app.Slug, domains)
		data.WWWApexHint = dashboardWWWApexHint(data.Domains)
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{
		Title:   "Domains — " + app.Slug,
		Body:    "domains",
		Account: dashboardAccountView(view, appCount),
		Data:    data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func (s *server) projectDashboardDomains(ctx stdctx, log *slog.Logger, slug string, domains []state.CustomDomain) []dashboard.DomainPageItem {
	items := make([]dashboard.DomainPageItem, 0, len(domains))
	for _, domain := range domains {
		resp := domainResponse(domain)
		item := dashboard.DomainPageItem{
			Domain:           resp.Domain,
			Verified:         resp.Verified,
			VerifiedAt:       resp.VerifiedAt,
			TXTRecord:        resp.TXTRecord,
			CertStatus:       resp.CertStatus,
			CertExpiresAt:    resp.CertExpiresAt,
			CertLastError:    resp.CertLastError,
			DNSLastCheckedAt: resp.DNSLastCheckedAt,
			DoctorURL:        dashboardDomainDoctorURL(slug, domain.Domain),
		}
		if obs, err := s.store.GetDoctorObservation(ctx, domain.Domain); err == nil {
			stale := !obs.ObservedAt.IsZero() && time.Since(obs.ObservedAt) >= s.doctorTTL()
			report := doctorReportFromObs(domain, obs, stale)
			summary := &dashboard.DomainDoctorSummary{Healthy: report.Healthy, Stale: report.Stale, ObservedAt: report.ObservedAt}
			for _, check := range report.Checks {
				summary.Checks = append(summary.Checks, dashboard.DashboardDoctorCheck{
					Name: check.Name, Status: check.Status, Detail: check.Detail,
					Observed: check.Observed, Remediation: check.Remediation, CheckedAt: check.CheckedAt,
				})
			}
			item.Doctor = summary
		} else if !errors.Is(err, state.ErrNotFound) {
			log.Warn("dashboard renderAppDomains: get doctor observation", "domain", domain.Domain, "err", err)
		}
		items = append(items, item)
	}
	return items
}

func dashboardDomainDoctorURL(slug, domain string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "/domains/" + url.PathEscape(domain) + "/doctor"
}

// dashboardWWWApexHint calls out the common half-attached www/apex setup. It
// deliberately remains advisory until the D7 redirect writer is available.
func dashboardWWWApexHint(items []dashboard.DomainPageItem) string {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[strings.ToLower(strings.TrimSuffix(item.Domain, "."))] = struct{}{}
	}
	for _, item := range items {
		domain := strings.ToLower(strings.TrimSuffix(item.Domain, "."))
		if strings.HasPrefix(domain, "www.") {
			apex := strings.TrimPrefix(domain, "www.")
			if _, ok := seen[apex]; !ok {
				return fmt.Sprintf("%s is attached without its apex partner %s. Attach both hosts to keep one canonical URL.", item.Domain, apex)
			}
			continue
		}
		// Only suggest www for a conventional two-label apex; suggesting
		// www.api.example.com for an arbitrary subdomain is misleading.
		if strings.Count(domain, ".") == 1 {
			www := "www." + domain
			if _, ok := seen[www]; !ok {
				return fmt.Sprintf("%s is attached without %s. Attach both hosts to keep one canonical URL.", item.Domain, www)
			}
		}
	}
	return ""
}
