package apidserver

// Operator support visibility (ADR-20261112170000):
//
//	GET /v1/admin/obs/lookup?q=  — resolve a customer-supplied identifier
//	                               to the account (and app) it belongs to
//
// and the diagnosis section of GET /v1/admin/obs/tenants/{id}/360, which
// says why an account may not be working. Both are session-only operator
// reads (requireOperatorSessionRead): they return account identifiers.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/attention"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	obsLookupSlugPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	obsLookupHostPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	obsLookupTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// obsLookup handles GET /v1/admin/obs/lookup?q=. It tries every
// interpretation of q that fits its shape (UUID, email, hostname or URL,
// app slug, request or trace id) and returns the deduplicated matches.
func (s *server) obsLookup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > api.ObsLookupQueryMaxBytes {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad lookup query", "q must be 1 to 256 bytes: an account, app, deployment, request or trace id, an email, an app slug, a hostname or a URL"))
		return
	}
	if strings.Contains(query, "@") && !strings.Contains(query, "/") {
		emitPIIAccessed(r, s, acct, "lookup")
	}
	matches, err := s.lookupCustomerIdentifier(r.Context(), query)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not complete the lookup"))
		return
	}
	writeJSON(w, http.StatusOK, api.ObsLookupResponse{Query: query, Matches: matches})
}

type obsLookupResult struct {
	seen    map[string]bool
	matches []api.ObsLookupMatch
}

func (l *obsLookupResult) add(m api.ObsLookupMatch) {
	key := m.Kind + "|" + m.AccountID + "|" + m.AppID + "|" + m.AppSlug + "|" + m.Detail
	if l.seen[key] || len(l.matches) >= api.ObsLookupMaxMatches {
		return
	}
	l.seen[key] = true
	l.matches = append(l.matches, m)
}

func (l *obsLookupResult) addApp(app state.App, kind, detail string) {
	if app.Status == state.AppDeleted {
		detail += " (deleted)"
	}
	l.add(api.ObsLookupMatch{Kind: kind, AccountID: app.AccountID, AppID: app.ID, AppSlug: app.Slug, Detail: detail})
}

// lookupCustomerIdentifier returns the matches for query. A store miss is
// not an error; any other store failure is.
func (s *server) lookupCustomerIdentifier(ctx context.Context, query string) ([]api.ObsLookupMatch, error) {
	out := &obsLookupResult{seen: map[string]bool{}, matches: []api.ObsLookupMatch{}}
	miss := func(err error) error {
		if err == nil || errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	lower := strings.ToLower(query)

	if id, err := uuid.Parse(query); err == nil {
		// Try the identifier as given and in canonical form: ids are
		// stored canonical in PostgreSQL, but a customer may paste the
		// 32-hex form without dashes.
		ids := []string{lower}
		if canonical := id.String(); canonical != lower {
			ids = append(ids, canonical)
		}
		for _, idText := range ids {
			if account, err := s.store.AccountByID(ctx, idText); err == nil {
				out.add(api.ObsLookupMatch{Kind: api.ObsLookupAccount, AccountID: account.ID, Detail: "account id"})
			} else if err := miss(err); err != nil {
				return nil, err
			}
			if app, err := s.store.AppByID(ctx, idText); err == nil {
				out.addApp(app, api.ObsLookupApp, "app id")
			} else if err := miss(err); err != nil {
				return nil, err
			}
			if deployment, err := s.store.DeploymentByID(ctx, idText); err == nil {
				if app, err := s.store.AppByID(ctx, deployment.AppID); err == nil {
					out.add(api.ObsLookupMatch{Kind: api.ObsLookupDeployment, AccountID: app.AccountID, AppID: app.ID,
						AppSlug: app.Slug, Detail: "deployment " + string(deployment.Status)})
				} else if err := miss(err); err != nil {
					return nil, err
				}
			} else if err := miss(err); err != nil {
				return nil, err
			}
		}
	}

	if strings.Contains(query, "@") && !strings.Contains(query, "/") {
		// Sign-up stores emails lowercased; older rows may keep their case.
		emails := []string{lower}
		if query != lower {
			emails = append(emails, query)
		}
		for _, email := range emails {
			if account, err := s.store.AccountByEmail(ctx, email); err == nil {
				out.add(api.ObsLookupMatch{Kind: api.ObsLookupAccount, AccountID: account.ID, Detail: "email"})
			} else if err := miss(err); err != nil {
				return nil, err
			}
		}
	}

	if host := obsLookupHostname(lower); host != "" {
		if domain, err := s.store.DomainByName(ctx, host); err == nil {
			if app, err := s.store.AppByID(ctx, domain.AppID); err == nil {
				out.addApp(app, api.ObsLookupDomain, "custom domain")
			} else if err := miss(err); err != nil {
				return nil, err
			}
		} else if err := miss(err); err != nil {
			return nil, err
		}
		if base := strings.ToLower(strings.TrimSpace(s.domain)); base != "" && strings.HasSuffix(host, "."+base) {
			label := strings.TrimSuffix(host, "."+base)
			if !strings.Contains(label, ".") {
				for _, slug := range obsLookupSlugCandidates(label) {
					if app, err := s.store.AppBySlugIncludingDeleted(ctx, slug); err == nil {
						out.addApp(app, api.ObsLookupApp, "app hostname")
						break
					} else if err := miss(err); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	if obsLookupSlugPattern.MatchString(lower) {
		if app, err := s.store.AppBySlugIncludingDeleted(ctx, lower); err == nil {
			out.addApp(app, api.ObsLookupApp, "app slug")
		} else if err := miss(err); err != nil {
			return nil, err
		}
	}

	if obsLookupTokenPattern.MatchString(query) {
		recorded, err := s.customerErrors.Lookup(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, m := range recorded {
			out.add(m)
		}
	}
	return out.matches, nil
}

// obsLookupHostname extracts a hostname from a bare host or a URL, or
// returns "" when query is neither.
func obsLookupHostname(query string) string {
	candidate := query
	if strings.Contains(candidate, "://") {
		parsed, err := url.Parse(candidate)
		if err != nil {
			return ""
		}
		candidate = parsed.Hostname()
	} else {
		if i := strings.IndexAny(candidate, "/?#"); i >= 0 {
			candidate = candidate[:i]
		}
		if i := strings.LastIndex(candidate, ":"); i >= 0 {
			candidate = candidate[:i]
		}
	}
	candidate = strings.TrimSuffix(candidate, ".")
	if len(candidate) > 253 || !obsLookupHostPattern.MatchString(candidate) {
		return ""
	}
	return candidate
}

// obsLookupSlugCandidates returns the app slugs an app hostname label may
// name: the label itself, then the part before a "--" preview suffix.
func obsLookupSlugCandidates(label string) []string {
	out := []string{}
	if obsLookupSlugPattern.MatchString(label) {
		out = append(out, label)
	}
	if i := strings.Index(label, "--"); i > 0 && obsLookupSlugPattern.MatchString(label[:i]) {
		out = append(out, label[:i])
	}
	return out
}

// buildTenantDiagnosis composes the diagnosis section of the tenant 360.
// A section that cannot be read is named in Unavailable and left empty;
// the rest of the diagnosis is still returned.
func (s *server) buildTenantDiagnosis(ctx context.Context, target state.Account, billing api.ObsTenantBilling) api.ObsTenantDiagnosis {
	now := time.Now().UTC()
	d := api.ObsTenantDiagnosis{
		GeneratedAt:       now,
		Restrictions:      accountRestrictions(target, billing),
		FailedDeployments: []api.ObsTenantFailedDeploy{},
		DomainIssues:      []api.ObsTenantDomainIssue{},
		EnabledFlags:      []string{},
	}
	if s.platformReleasePaused() {
		d.Restrictions = append(d.Restrictions, api.ObsTenantRestriction{Kind: api.RestrictionPlatformReleasePause,
			Reason: "platform-wide release pause: new deployments and builds are held"})
	}
	unavailable := func(section string) { d.Unavailable = append(d.Unavailable, section) }

	if orgs, err := s.store.ListOrgsForAccount(ctx, target.ID); err != nil {
		unavailable("org_restrictions")
	} else {
		for i, org := range orgs {
			if i == 20 {
				break
			}
			if org.AbuseHoldAt != nil {
				d.Restrictions = append(d.Restrictions, api.ObsTenantRestriction{Kind: api.RestrictionAbuseHold,
					Reason: strings.TrimSpace("organization " + org.Slug + ": " + org.AbuseHoldReason), Since: org.AbuseHoldAt})
			}
		}
	}

	apps, err := s.store.ListApps(ctx, target.ID)
	slugs := make(map[string]string, len(apps))
	if err != nil {
		unavailable("apps")
	}
	for _, app := range apps {
		slugs[app.ID] = app.Slug
	}

	failed, err := s.store.ListDeploymentsForOperator(ctx, state.OperatorDeploymentFilter{
		AccountID: target.ID, Statuses: []state.DeploymentStatus{state.DeployFailed}, Limit: api.ObsDiagnosisDeploymentLimit})
	if err != nil {
		unavailable("failed_deployments")
	}
	since := now.AddDate(0, 0, -api.ObsDiagnosisDeploymentDays)
	for _, dep := range failed {
		if dep.CreatedAt.Before(since) {
			continue
		}
		d.FailedDeployments = append(d.FailedDeployments, api.ObsTenantFailedDeploy{
			DeploymentID: dep.ID, AppID: dep.AppID, AppSlug: slugs[dep.AppID], Status: string(dep.Status),
			ErrorCode: dep.ErrorCode, Error: truncateDiagnosis(dep.Error), Hint: truncateDiagnosis(dep.ErrorHint),
			CreatedAt: dep.CreatedAt.UTC()})
	}

	domains, err := s.store.ListDomainsForAccount(ctx, target.ID)
	if err != nil {
		unavailable("domains")
	}
	for _, domain := range domains {
		issue, ok := domainIssue(domain, now)
		if !ok {
			d.HealthyDomains++
			continue
		}
		issue.AppSlug = slugs[domain.AppID]
		d.DomainIssues = append(d.DomainIssues, issue)
	}
	sort.Slice(d.DomainIssues, func(i, j int) bool { return d.DomainIssues[i].Domain < d.DomainIssues[j].Domain })

	d.EnabledFlags = s.platformFlagAdmin.EnabledFlags(target)

	errorsSummary, err := s.customerErrors.Summary(ctx, target.ID, now.Add(-api.CustomerErrorWindow))
	if err != nil {
		unavailable("customer_errors")
	}
	d.CustomerErrors = errorsSummary

	d.AttentionSignals = []api.ObsAttentionSignal{}
	if signals, err := s.attention.List(ctx, attention.Filter{AccountID: target.ID}); err != nil {
		unavailable("attention_signals")
	} else {
		d.AttentionSignals = signals
	}
	return d
}

// accountRestrictions lists what the account row itself says is stopping
// the account: status, holds, pending deletion, verification and the
// overage cap.
func accountRestrictions(a state.Account, billing api.ObsTenantBilling) []api.ObsTenantRestriction {
	out := []api.ObsTenantRestriction{}
	switch a.Status {
	case state.AccountSuspended:
		out = append(out, api.ObsTenantRestriction{Kind: api.RestrictionAccountSuspended,
			Reason: "account suspended: apps are stopped and API writes are refused"})
	case state.AccountPastDue:
		out = append(out, api.ObsTenantRestriction{Kind: api.RestrictionAccountPastDue,
			Reason: "payment failed; the account is in its dunning grace period", Since: a.PastDueAt})
	case state.AccountDeletedPending:
		out = append(out, api.ObsTenantRestriction{Kind: api.RestrictionDeletionPending,
			Reason: "account deletion pending", Since: a.DeletionRequestedAt})
	}
	if a.Status != state.AccountDeletedPending && a.DeletionRequestedAt != nil {
		out = append(out, api.ObsTenantRestriction{Kind: api.RestrictionDeletionPending,
			Reason: "account deletion requested", Since: a.DeletionRequestedAt})
	}
	if a.AbuseHoldAt != nil {
		out = append(out, api.ObsTenantRestriction{Kind: api.RestrictionAbuseHold,
			Reason: strings.TrimSpace(a.AbuseHoldReason), Since: a.AbuseHoldAt})
	}
	if a.EmailVerifiedAt == nil {
		out = append(out, api.ObsTenantRestriction{Kind: api.RestrictionEmailUnverified,
			Reason: "email not verified: actions that need a verified account are refused"})
	}
	if billing.OverageCapCents != nil && billing.CurrentMonthOverageCents >= *billing.OverageCapCents {
		out = append(out, api.ObsTenantRestriction{Kind: api.RestrictionOverageCapReached,
			Reason: "this month's overage reached the account's overage cap"})
	}
	return out
}

// domainIssue reports whether a custom domain is not serving correctly.
func domainIssue(domain state.CustomDomain, now time.Time) (api.ObsTenantDomainIssue, bool) {
	issue := api.ObsTenantDomainIssue{Domain: domain.Domain, CertStatus: string(domain.CertStatus),
		CertLastError: truncateDiagnosis(domain.CertLastError)}
	if !domain.CertExpiresAt.IsZero() {
		expires := domain.CertExpiresAt.UTC()
		issue.CertExpiresAt = &expires
	}
	switch {
	case domain.VerifiedAt.IsZero():
		issue.Problem = "domain not verified"
	case domain.CertStatus == state.CustomDomainCertFailed:
		issue.Problem = "certificate issuance failed"
	case domain.CertStatus == state.CustomDomainCertDNSDrifted:
		issue.Problem = "DNS no longer points at Gregale"
	case domain.CertStatus == state.CustomDomainCertPending:
		issue.Problem = "certificate pending"
	case !domain.CertExpiresAt.IsZero() && domain.CertExpiresAt.Before(now):
		issue.Problem = "certificate expired"
	default:
		return api.ObsTenantDomainIssue{}, false
	}
	return issue, true
}

// truncateDiagnosis bounds a customer-visible error string for the 360.
func truncateDiagnosis(text string) string {
	const max = 500
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + "…"
}
