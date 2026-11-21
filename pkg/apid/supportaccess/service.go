// Package supportaccess owns customer-granted support access: an
// organization's owners and admins let Gregale support deploy, roll back and
// restart its apps for a limited time, and support may ask first.
// adr: 20261116163508
package supportaccess

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is the lookup and grant surface the service uses.
type Store interface {
	state.SupportGrantStore
	AppBySlug(ctx context.Context, slug string) (state.App, error)
	AppByID(ctx context.Context, id string) (state.App, error)
	AccountByID(ctx context.Context, id string) (state.Account, error)
	OrgBySlug(ctx context.Context, slug string) (state.Org, error)
	OrgByID(ctx context.Context, id string) (state.Org, error)
	ListOrgActivity(ctx context.Context, filter state.OrgActivityFilter) ([]state.OrgActivity, error)
}

// Deps wires the service into apid.
type Deps struct {
	Store Store
	// Enabled decides the support_grants platform flag for an organization.
	Enabled func(orgID, accountID string) bool
	// IsOperator reports a platform operator. Operators never grant or
	// approve access, even to an organization they belong to.
	IsOperator func(state.Account) bool
	// Notify mails the organization's owners and admins about change.
	Notify func(ctx context.Context, kind, orgID, change string)
	// Activity appends an organization activity row.
	Activity func(ctx context.Context, entry state.OrgActivity)
	// Audit emits an audit event.
	Audit func(ctx context.Context, kind string, subject *string, data map[string]any)
	// DashboardURL makes a dashboard path absolute for a mail.
	DashboardURL func(path string) string
	// Mail sends one mail to the organization's owners and admins.
	Mail func(ctx context.Context, kind, orgID, subject, body string)
}

// PagePath is the dashboard's Support access page.
const PagePath = "/dashboard/support-access"

// Service implements grants and requests.
type Service struct{ d Deps }

func New(d Deps) *Service { return &Service{d: d} }

// enabled decides the flag for the organization and the account acting in
// it: platform flags always decide for an account.
func (s *Service) enabled(orgID, accountID string) bool {
	return s.d.Enabled != nil && s.d.Enabled(orgID, accountID)
}

var reasonRule = fmt.Sprintf("reason must be %d to %d characters", api.SupportReasonMinChars, api.SupportReasonMaxChars)

var errDisabled = api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Support access is not available",
	"support access grants are not enabled for this organization")

// Grant gives support access to the organization's apps (in.Apps slugs, or
// all of them).
func (s *Service) Grant(ctx context.Context, actor state.Account, org state.Org, in api.CreateSupportGrantRequest) (api.SupportGrant, *api.Problem) {
	grant, prob := s.newGrant(ctx, actor, org, in, "")
	if prob != nil {
		return api.SupportGrant{}, prob
	}
	created, err := s.d.Store.CreateSupportGrant(ctx, grant)
	if err != nil {
		return api.SupportGrant{}, storeProblem(err, "could not grant support access")
	}
	s.recordGrant(ctx, actor, created, "support_access.granted")
	return s.View(ctx, created), nil
}

// Approve decides an open request and grants its access atomically.
// An empty body approves what the request asked for.
func (s *Service) Approve(ctx context.Context, actor state.Account, org state.Org, requestID string, in api.CreateSupportGrantRequest) (api.SupportGrant, *api.Problem) {
	req, found := s.findRequest(ctx, org.ID, requestID)
	if found && len(in.Apps) == 0 && in.TTLHours == 0 && strings.TrimSpace(in.Reason) == "" {
		in.Apps = s.newNames().slugs(ctx, req.AppIDs)
		in.TTLHours = int(req.TTL / time.Hour)
	}
	if found && strings.TrimSpace(in.Reason) == "" {
		in.Reason = req.Reason
	}
	grant, prob := s.newGrant(ctx, actor, org, in, requestID)
	if prob != nil {
		return api.SupportGrant{}, prob
	}
	created, err := s.d.Store.ApproveSupportGrantRequest(ctx, requestID, grant)
	if err != nil {
		return api.SupportGrant{}, storeProblem(err, "could not approve the support access request")
	}
	s.recordGrant(ctx, actor, created, "support_access.request_approved")
	return s.View(ctx, created), nil
}

// Decline refuses an open request.
func (s *Service) Decline(ctx context.Context, actor state.Account, org state.Org, requestID string) *api.Problem {
	if err := s.d.Store.DeclineSupportGrantRequest(ctx, org.ID, requestID, actor.ID); err != nil {
		return storeProblem(err, "could not decline the support access request")
	}
	s.activity(ctx, org.ID, actor, false, "support_access.request_declined", "support_access_request", requestID, nil)
	s.audit(ctx, "support_access.request_declined", actor.ID, map[string]any{"org_id": org.ID, "request_id": requestID})
	return nil
}

// Revoke ends a grant now. Revoking is never gated by the flag: a customer
// can always take access back.
func (s *Service) Revoke(ctx context.Context, actor state.Account, org state.Org, grantID string) *api.Problem {
	if err := s.d.Store.RevokeSupportGrant(ctx, org.ID, grantID, actor.ID); err != nil {
		return storeProblem(err, "could not revoke support access")
	}
	s.activity(ctx, org.ID, actor, false, "support_access.revoked", "support_grant", grantID, nil)
	s.audit(ctx, "support_access.revoked", actor.ID, map[string]any{"org_id": org.ID, "grant_id": grantID})
	s.notify(ctx, "support_access_revoked", org.ID, accountLabel(actor)+" revoked Gregale support's access")
	return nil
}

// Request is an operator asking the organization for access: for the apps
// and time in.Apps and in.TTLHours, or to extend the grant in.Extends.
func (s *Service) Request(ctx context.Context, operator state.Account, in api.OperatorSupportAccessRequest) (api.SupportAccessRequest, *api.Problem) {
	org, err := s.d.Store.OrgBySlug(ctx, strings.TrimSpace(in.Org))
	if err != nil {
		return api.SupportAccessRequest{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Organization not found", "no organization with that slug")
	}
	if !s.enabled(org.ID, operator.ID) {
		return api.SupportAccessRequest{}, errDisabled
	}
	reason := strings.TrimSpace(in.Reason)
	if !state.ValidSupportReason(reason) {
		return api.SupportAccessRequest{}, api.ErrValidation(reasonRule + "; the customer reads it")
	}
	ask, prob := s.newRequest(ctx, operator, org, in)
	if prob != nil {
		return api.SupportAccessRequest{}, prob
	}
	ask.Reason = reason
	req, err := s.d.Store.CreateSupportGrantRequest(ctx, ask)
	if err != nil {
		return api.SupportAccessRequest{}, storeProblem(err, "could not request support access")
	}
	view := s.newNames().requestView(ctx, org.Slug, operator, req)
	s.activity(ctx, org.ID, operator, true, "support_access.requested", "support_access_request", req.ID,
		map[string]any{"reason": reason, "apps": view.Apps, "ttl_hours": view.TTLHours, "extends": view.Extends})
	s.audit(ctx, "support_access.requested", operator.ID, map[string]any{"org_id": org.ID, "request_id": req.ID, "reason": reason,
		"app_ids": req.AppIDs, "ttl_hours": view.TTLHours, "extends": req.ExtendsGrantID})
	s.notify(ctx, "support_access_requested", org.ID, requestMailText(operator, view, s.dashboardURL(RequestPath(req.ID))))
	return view, nil
}

// RequestPath is the dashboard page that opens one request's approval form.
func RequestPath(requestID string) string { return PagePath + "?request=" + url.QueryEscape(requestID) }

// newRequest resolves what an operator asks for. Extending a grant asks for
// its apps again unless in.Apps names others.
func (s *Service) newRequest(ctx context.Context, operator state.Account, org state.Org, in api.OperatorSupportAccessRequest) (state.NewSupportGrantRequest, *api.Problem) {
	out := state.NewSupportGrantRequest{OrgID: org.ID, OperatorID: operator.ID}
	maxHours := int(api.SupportGrantMaxTTL / time.Hour)
	if in.TTLHours < 0 || in.TTLHours > maxHours {
		return out, api.ErrValidation(fmt.Sprintf("ttl_hours must be 1 to %d, or 0 to leave it to the customer", maxHours))
	}
	out.TTL = time.Duration(in.TTLHours) * time.Hour
	if len(in.Apps) > api.SupportGrantMaxApps {
		return out, api.ErrValidation(fmt.Sprintf("a request names at most %d apps", api.SupportGrantMaxApps))
	}
	for _, slug := range in.Apps {
		app, err := s.d.Store.AppBySlug(ctx, strings.TrimSpace(slug))
		if err != nil || app.OrgID != org.ID {
			return out, api.ErrValidation("app " + slug + " is not an app of this organization")
		}
		out.AppIDs = append(out.AppIDs, app.ID)
	}
	if extends := strings.TrimSpace(in.Extends); extends != "" {
		grant, err := s.d.Store.SupportGrantByID(ctx, extends)
		if err != nil || grant.OrgID != org.ID || !grant.Live(time.Now()) {
			return out, api.ErrValidation("extends must name a live grant of this organization")
		}
		out.ExtendsGrantID = grant.ID
		if len(out.AppIDs) == 0 {
			out.AppIDs = grant.AppIDs
		}
	}
	return out, nil
}

// requestMailText is the change line of the request mail.
func requestMailText(operator state.Account, view api.SupportAccessRequest, link string) string {
	scope := "your apps"
	if len(view.Apps) > 0 {
		scope = strings.Join(view.Apps, ", ")
	}
	ask := "access to deploy, roll back and restart " + scope
	if view.TTLHours > 0 {
		ask += fmt.Sprintf(" for %d hours", view.TTLHours)
	}
	if view.Extends != "" {
		ask = "more time on its current access: " + ask
	}
	return "Gregale support (" + accountLabel(operator) + ") asks for " + ask + ": " + view.Reason +
		". Nothing changes unless an owner or admin approves it: " + link
}

func (s *Service) dashboardURL(path string) string {
	if s.d.DashboardURL == nil {
		return path
	}
	return s.d.DashboardURL(path)
}

// Overview lists the organization's grants and requests for viewer.
func (s *Service) Overview(ctx context.Context, org state.Org, viewer state.Account) (api.SupportAccess, error) {
	out := api.SupportAccess{Enabled: s.enabled(org.ID, viewer.ID), Grants: []api.SupportGrant{}, Requests: []api.SupportAccessRequest{}}
	grants, err := s.d.Store.ListSupportGrants(ctx, org.ID)
	if err != nil {
		return out, err
	}
	names := s.newNames()
	for _, g := range grants {
		out.Grants = append(out.Grants, names.view(ctx, g))
	}
	requests, err := s.d.Store.ListSupportGrantRequests(ctx, org.ID)
	if err != nil {
		return out, err
	}
	for _, req := range requests {
		operator, _ := names.account(ctx, req.OperatorID)
		out.Requests = append(out.Requests, names.requestView(ctx, org.Slug, operator, req))
	}
	return out, nil
}

// LiveGrants lists every usable-looking grant for operators.
func (s *Service) LiveGrants(ctx context.Context) ([]api.SupportGrant, error) {
	grants, err := s.d.Store.ListLiveSupportGrants(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.SupportGrant, 0, len(grants))
	names := s.newNames()
	for _, g := range grants {
		if s.enabled(g.OrgID, g.CreatedBy) {
			out = append(out, names.view(ctx, g))
		}
	}
	return out, nil
}

func (s *Service) newGrant(ctx context.Context, actor state.Account, org state.Org, in api.CreateSupportGrantRequest, requestID string) (state.NewSupportGrant, *api.Problem) {
	if !s.enabled(org.ID, actor.ID) {
		return state.NewSupportGrant{}, errDisabled
	}
	if s.d.IsOperator != nil && s.d.IsOperator(actor) {
		return state.NewSupportGrant{}, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Operators cannot grant support access",
			"a customer owner or admin grants support access; a Gregale operator account never can")
	}
	ttl := api.SupportGrantDefaultTTL
	if in.TTLHours != 0 {
		ttl = time.Duration(in.TTLHours) * time.Hour
	}
	if ttl <= 0 || ttl > api.SupportGrantMaxTTL {
		return state.NewSupportGrant{}, api.ErrValidation(fmt.Sprintf("ttl_hours must be 1 to %d", int(api.SupportGrantMaxTTL/time.Hour)))
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "Granted by " + accountLabel(actor)
	}
	if !state.ValidSupportReason(reason) {
		return state.NewSupportGrant{}, api.ErrValidation(reasonRule)
	}
	if len(in.Apps) > api.SupportGrantMaxApps {
		return state.NewSupportGrant{}, api.ErrValidation(fmt.Sprintf("a grant names at most %d apps; leave apps empty to cover every app", api.SupportGrantMaxApps))
	}
	grant := state.NewSupportGrant{OrgID: org.ID, CreatedBy: actor.ID, RequestID: requestID, Reason: reason, TTL: ttl}
	for _, slug := range in.Apps {
		app, err := s.d.Store.AppBySlug(ctx, strings.TrimSpace(slug))
		if err != nil || app.OrgID != org.ID {
			return state.NewSupportGrant{}, api.ErrValidation("app " + slug + " is not an app of this organization")
		}
		grant.AppIDs = append(grant.AppIDs, app.ID)
	}
	return grant, nil
}

func (s *Service) findRequest(ctx context.Context, orgID, requestID string) (state.SupportGrantRequest, bool) {
	requests, err := s.d.Store.ListSupportGrantRequests(ctx, orgID)
	if err != nil {
		return state.SupportGrantRequest{}, false
	}
	for _, req := range requests {
		if req.ID == requestID {
			return req, true
		}
	}
	return state.SupportGrantRequest{}, false
}

func (s *Service) recordGrant(ctx context.Context, actor state.Account, g state.SupportGrant, kind string) {
	view := s.View(ctx, g)
	scope := "all apps"
	if !view.AllApps {
		scope = strings.Join(view.Apps, ", ")
	}
	s.activity(ctx, g.OrgID, actor, false, kind, "support_grant", g.ID, map[string]any{"apps": view.Apps, "expires_at": view.ExpiresAt})
	s.audit(ctx, kind, actor.ID, map[string]any{"org_id": g.OrgID, "grant_id": g.ID, "app_ids": g.AppIDs, "expires_at": view.ExpiresAt, "request_id": g.RequestID})
	s.notify(ctx, "support_access_granted", g.OrgID, accountLabel(actor)+" gave Gregale support access to deploy, roll back and restart "+scope+
		" until "+g.ExpiresAt.UTC().Format("2006-01-02 15:04 MST")+". Revoke it any time on the dashboard's Support access page.")
}

// View renders a grant for the API.
func (s *Service) View(ctx context.Context, g state.SupportGrant) api.SupportGrant {
	return s.newNames().view(ctx, g)
}

// names memoizes the app and account lookups behind grant views, so a list
// reads each app and account once rather than once per grant.
type names struct {
	store    Store
	apps     map[string]string
	accounts map[string]lookup
}

type lookup struct {
	acct state.Account
	err  error
}

func (s *Service) newNames() *names {
	return &names{store: s.d.Store, apps: map[string]string{}, accounts: map[string]lookup{}}
}

func (n *names) app(ctx context.Context, id string) (string, bool) {
	if slug, ok := n.apps[id]; ok {
		return slug, slug != ""
	}
	slug := ""
	if app, err := n.store.AppByID(ctx, id); err == nil {
		slug = app.Slug
	}
	n.apps[id] = slug
	return slug, slug != ""
}

func (n *names) account(ctx context.Context, id string) (state.Account, error) {
	if l, ok := n.accounts[id]; ok {
		return l.acct, l.err
	}
	acct, err := n.store.AccountByID(ctx, id)
	n.accounts[id] = lookup{acct: acct, err: err}
	return acct, err
}

// slugs names app ids, skipping apps that are gone.
func (n *names) slugs(ctx context.Context, ids []string) []string {
	var out []string
	for _, id := range ids {
		if slug, ok := n.app(ctx, id); ok {
			out = append(out, slug)
		}
	}
	return out
}

func (n *names) view(ctx context.Context, g state.SupportGrant) api.SupportGrant {
	out := api.SupportGrant{
		ID: g.ID, Org: g.OrgSlug, Apps: []string{}, AllApps: len(g.AppIDs) == 0, Reason: g.Reason, RequestID: g.RequestID,
		CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: g.ExpiresAt.UTC().Format(time.RFC3339),
	}
	for _, id := range g.AppIDs {
		if slug, ok := n.app(ctx, id); ok {
			out.Apps = append(out.Apps, slug)
		}
	}
	if acct, err := n.account(ctx, g.CreatedBy); err == nil {
		out.CreatedBy = acct.Email
	}
	switch {
	case g.RevokedAt != nil:
		out.Status, out.RevokedAt = api.SupportGrantRevoked, g.RevokedAt.UTC().Format(time.RFC3339)
		if acct, err := n.account(ctx, g.RevokedBy); err == nil {
			out.RevokedBy = acct.Email
		}
	case !g.ExpiresAt.After(time.Now()):
		out.Status = api.SupportGrantExpired
	default:
		out.Status = api.SupportGrantActive
	}
	return out
}

func (n *names) requestView(ctx context.Context, orgSlug string, operator state.Account, req state.SupportGrantRequest) api.SupportAccessRequest {
	out := api.SupportAccessRequest{
		ID: req.ID, Org: orgSlug, Operator: "Gregale support", Reason: req.Reason, Status: req.Status,
		CreatedAt: req.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: req.ExpiresAt.UTC().Format(time.RFC3339),
		Apps: n.slugs(ctx, req.AppIDs), TTLHours: int(req.TTL / time.Hour), Extends: req.ExtendsGrantID,
	}
	if operator.Email != "" {
		out.Operator = "Gregale support (" + operator.Email + ")"
	}
	if req.Status == state.SupportRequestOpen && !req.ExpiresAt.After(time.Now()) {
		out.Status = "expired"
	}
	if req.DecidedAt != nil {
		out.DecidedAt = req.DecidedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func accountLabel(acct state.Account) string {
	if acct.Email != "" {
		return acct.Email
	}
	return "A member"
}

func storeProblem(err error, what string) *api.Problem {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such open grant or request in this organization")
	case errors.Is(err, state.ErrSupportRequestLimit):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Too many open requests",
			fmt.Sprintf("this organization already has %d open support access requests; wait for a decision", api.SupportGrantMaxOpenRequests))
	case errors.Is(err, state.ErrInvalidArgument):
		return api.ErrValidation(what + ": invalid grant")
	default:
		return api.ErrCapacity(what)
	}
}

func (s *Service) activity(ctx context.Context, orgID string, actor state.Account, operator bool, kind, resourceType, resourceID string, data map[string]any) {
	if s.d.Activity == nil {
		return
	}
	org, err := uuid.Parse(orgID)
	if err != nil {
		return
	}
	// The store refuses an entry without a source, and keeps one row per
	// source: every support event is its own row.
	entry := state.OrgActivity{OrgID: org, OccurredAt: time.Now().UTC(), Kind: kind, ActorType: state.OrgActivityActorUser,
		ActorLabel: accountLabel(actor), ResourceType: resourceType, ResourceID: resourceID, ResourceLabel: "Gregale support access",
		SourceType: kind, SourceID: kind + ":" + uuid.NewString()}
	if operator {
		entry.ActorType, entry.ActorLabel = state.OrgActivityActorOperator, "Gregale support ("+accountLabel(actor)+")"
	}
	if id, err := uuid.Parse(actor.ID); err == nil {
		entry.ActorAccountID = &id
	}
	entry.Data = activityJSON(data)
	s.d.Activity(ctx, entry)
}

func (s *Service) audit(ctx context.Context, kind, subject string, data map[string]any) {
	if s.d.Audit != nil {
		s.d.Audit(ctx, kind, &subject, data)
	}
}

func (s *Service) notify(ctx context.Context, kind, orgID, change string) {
	if s.d.Notify != nil {
		s.d.Notify(ctx, kind, orgID, change)
	}
}
