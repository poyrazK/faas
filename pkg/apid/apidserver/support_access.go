package apidserver

// Customer-granted support access (ADR-20261116163508). The service and its
// handlers live in pkg/apid/supportaccess; this file wires them and holds
// the gate that admits an operator session to a customer app route under a
// grant.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/supportaccess"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// supportActionMailKind is the throttled "support changed something" mail.
const supportActionMailKind = "support_access_action"

// supportStepUpTTL is how recent an operator's step-up must be for a support
// write, as for every other provider mutation.
const supportStepUpTTL = 5 * time.Minute

func (s *server) supportGrantsEnabled(orgID string) bool {
	return s.platformFlagFor(platformflags.SupportGrants, platformflags.Subject{OrgID: orgID})
}

func (s *server) supportAccessService() *supportaccess.Service {
	store, ok := s.store.(supportaccess.Store)
	if !ok {
		return nil
	}
	return supportaccess.New(supportaccess.Deps{
		Store:      store,
		Enabled:    s.supportGrantsEnabled,
		IsOperator: s.isOperator,
		Notify:     s.notifySupportAccess,
		Activity:   s.recordOrgActivity,
		Audit: func(ctx context.Context, kind string, subject *string, data map[string]any) {
			s.audit.Emit(ctx, kind, subject, data)
		},
	})
}

func (s *server) supportAccessHTTP() supportaccess.HTTP {
	return supportaccess.HTTP{
		Service: s.supportAccessService(),
		Org: func(w http.ResponseWriter, r *http.Request, action authz.OrgAction) (state.Org, bool) {
			if !s.requireOrgAction(w, r, action) {
				return state.Org{}, false
			}
			mem, ok := s.requireMembership(w, r)
			if !ok {
				return state.Org{}, false
			}
			org, err := s.store.OrgByID(r.Context(), mem.OrgID)
			if err != nil {
				api.WriteProblem(w, api.ErrCapacity("could not read the organization"))
				return state.Org{}, false
			}
			return org, true
		},
		BrowserSession: requireBrowserSession,
		Actor:          actorAccount,
	}
}

// supportAccessRoute serves a support-access handler, or 503 when the store
// has none.
func (s *server) supportAccessRoute(handler func(supportaccess.HTTP) accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		h := s.supportAccessHTTP()
		if h.Service == nil {
			api.WriteProblem(w, api.ErrCapacity("support access is unavailable"))
			return
		}
		handler(h)(w, r, acct)
	}
}

// notifySupportAccess mails the organization's owners and admins, or a
// personal account's owner.
func (s *server) notifySupportAccess(ctx context.Context, kind, orgID, change string) {
	also := s.orgAdminIDs(ctx, orgID)
	if org, err := s.store.OrgByID(state.WithoutOrgAccessFence(ctx), orgID); err == nil && org.Personal && org.PersonalOwnerAccountID != nil {
		also = append(also, *org.PersonalOwnerAccountID)
	}
	s.mailOrgSecurityEvent(ctx, kind, orgID, change, also...)
}

// supportAccessGate admits an operator session to a customer app route
// under the grant its Support-Grant header names. Requests without the
// header pass unchanged: an operator has no ambient access to customer apps.
func (s *server) supportAccessGate(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		grantID := strings.TrimSpace(r.Header.Get(api.SupportGrantHeader))
		if grantID == "" {
			next(w, r, acct)
			return
		}
		operator, grant, prob := s.verifySupportRequest(r, grantID)
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		*r = *r.WithContext(appaccess.WithSupport(r.Context(), appaccess.Support{Grant: grant, OperatorID: operator.ID}))
		rec := &statusRecorder{ResponseWriter: w}
		next(rec, r, acct)
		s.recordSupportAction(r, operator, grant, rec.status)
	}
}

func (s *server) verifySupportRequest(r *http.Request, grantID string) (state.Account, state.SupportGrant, *api.Problem) {
	forbidden := func(title, detail string) *api.Problem {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, title, detail)
	}
	operator, key, ok := middleware.AccountFromContext(r)
	if !ok {
		return state.Account{}, state.SupportGrant{}, api.ErrCapacity("authenticated principal missing")
	}
	if _, session := sessionFrom(r); key != nil || !session {
		return state.Account{}, state.SupportGrant{}, forbidden("Operator session required",
			"support access is used from a signed-in operator session (gregalectl auth login), never an API key")
	}
	if allowed, prob := s.adminAllows(operator); !allowed {
		return state.Account{}, state.SupportGrant{}, prob
	}
	if !operator.MFAEnrolled() {
		return state.Account{}, state.SupportGrant{}, api.NewProblem(http.StatusForbidden, api.CodeMFARequired, "MFA required",
			"platform operators must enroll an authenticator before using support access")
	}
	if !appaccess.SupportRoute(r.Pattern) {
		return state.Account{}, state.SupportGrant{}, forbidden("Not covered by support access",
			"a support grant covers deploying, retrying, cancelling, rolling back and restarting an app, and the reads those need")
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if at, has := middleware.StepUpFrom(r); !has || at.IsZero() || time.Since(at) > supportStepUpTTL {
			return state.Account{}, state.SupportGrant{}, api.ErrStepUpRequired()
		}
	}
	store, ok := s.store.(state.SupportGrantStore)
	if !ok {
		return state.Account{}, state.SupportGrant{}, api.ErrCapacity("support access is unavailable")
	}
	grant, err := store.UsableSupportGrant(r.Context(), grantID)
	if errors.Is(err, state.ErrNotFound) {
		return state.Account{}, state.SupportGrant{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Support grant not found",
			"the grant does not exist, was revoked, expired, or its owner or admin lost that role")
	}
	if err != nil {
		return state.Account{}, state.SupportGrant{}, api.ErrCapacity("could not check the support grant")
	}
	if !s.supportGrantsEnabled(grant.OrgID) {
		return state.Account{}, state.SupportGrant{}, forbidden("Support access is not available",
			"support access grants are turned off for this organization")
	}
	if prob := s.checkSupportRequestBody(r, operator); prob != nil {
		return state.Account{}, state.SupportGrant{}, prob
	}
	return operator, grant, nil
}

// supportBodyLimit bounds the bodies the gate reads before the handler.
const supportBodyLimit = 1 << 20

// supportBodyFields is the closed set of JSON fields each support write may
// send. Anything else, including a field added to the request later, is
// refused until it is reviewed: a support grant never turns off a customer
// control (health checks, rollback on 5xx, canaries, freezes, triggers),
// picks another environment, notifies other addresses, restores databases or
// attributes the change to someone else.
var supportBodyFields = map[string]map[string]bool{
	"POST /v1/apps/{slug}/deployments/source-ref":  {"repo": true, "ref": true, "reason": true},
	"POST /v1/apps/{slug}/rollback":                {"target_deployment_id": true, "expected_current_deployment_id": true, "reason": true},
	"POST /v1/deployments/{id}/retry":              {"from_stage": true},
	"POST /v1/apps/{slug}/deployments/{id}/cancel": {},
	"POST /v1/apps/{slug}/restart":                 {},
}

// checkSupportRequestBody applies supportBodyFields, and limits a source
// deploy to the app's connected repository at its production branch.
func (s *server) checkSupportRequestBody(r *http.Request, _ state.Account) *api.Problem {
	allowed, ok := supportBodyFields[r.Pattern]
	if !ok {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return nil
		}
		return supportRefusal(r.Pattern)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, supportBodyLimit+1))
	if err != nil || len(body) > supportBodyLimit {
		return api.ErrValidation("request body unreadable or too large")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &fields); err != nil {
			return api.ErrValidation("invalid JSON body")
		}
	}
	for name := range fields {
		if !allowed[name] {
			return supportRefusal(name)
		}
	}
	if r.Pattern == "POST /v1/apps/{slug}/deployments/source-ref" {
		return s.checkSupportSourceRef(r, body)
	}
	return nil
}

func supportRefusal(field string) *api.Problem {
	return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Not allowed under support access",
		field+" cannot be used under a support grant; ask the customer to do it")
}

// checkSupportSourceRef admits a source deploy only of the app's connected
// repository at its production branch. A commit SHA or another ref could
// name code support wrote itself: GitHub serves a fork's pull-request
// commits through the base repository.
func (s *server) checkSupportSourceRef(r *http.Request, body []byte) *api.Problem {
	var req api.SourceRefDeployRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return api.ErrValidation("invalid JSON body")
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		return nil // the app lookup answers not-found next
	}
	binding, err := s.store.GitHubBindingForApp(r.Context(), app.ID)
	if err != nil || binding.RepoFullName == "" || binding.ProductionBranch == "" {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "No connected repository",
			"under a support grant, source deploys build the app's connected GitHub repository at its production branch; this app has none")
	}
	if !strings.EqualFold(binding.RepoFullName, strings.TrimSpace(req.Repo)) {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Only the connected repository",
			"under a support grant, source deploys build the app's connected GitHub repository only")
	}
	if ref := strings.TrimPrefix(strings.TrimSpace(req.Ref), "refs/heads/"); ref != binding.ProductionBranch {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Only the production branch",
			"under a support grant, source deploys build the production branch "+binding.ProductionBranch+" only; a commit or another ref needs the customer")
	}
	return nil
}

// recordSupportAction leaves the customer-visible trail of one support
// request and mails owners and admins, throttled, when it changed something.
func (s *server) recordSupportAction(r *http.Request, operator state.Account, grant state.SupportGrant, status int) {
	svc := s.supportAccessService()
	if svc == nil {
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	var app state.App
	if g, ok := appaccess.From(r); ok {
		app = g.App
	}
	deploymentID := ""
	if strings.Contains(r.Pattern, "/deployments/{id}") {
		deploymentID = r.PathValue("id")
	}
	ctx := state.WithoutOrgAccessFence(context.WithoutCancel(r.Context()))
	if !svc.RecordAction(ctx, operator, grant, app, r.Method, r.Pattern, deploymentID, status) {
		return
	}
	what := strings.TrimPrefix(r.Pattern, r.Method+" ")
	s.notifySupportAccess(ctx, supportActionMailKind, grant.OrgID,
		"Gregale support ("+operator.Email+") used its access: "+r.Method+" "+what+" on "+app.Slug+
			". Every action is in the organization's activity log; revoke the grant on the dashboard's Support access page.")
}

// Support-access handlers for the route table (server.go).
type supportAccessMethod func(supportaccess.HTTP, http.ResponseWriter, *http.Request, state.Account)

func (s *server) supportAccess(method supportAccessMethod) accountHandler {
	return s.supportAccessRoute(func(h supportaccess.HTTP) accountHandler {
		return func(w http.ResponseWriter, r *http.Request, acct state.Account) { method(h, w, r, acct) }
	})
}
