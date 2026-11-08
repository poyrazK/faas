package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// AgentApprovalGate is the optional store seam behind human approval of an
// agent key's risky requests (ADR-20261104140058).
type AgentApprovalGate interface {
	APIKeyApprovalCategories(ctx context.Context, keyID string) ([]string, bool, error)
	ConsumeAgentApproval(ctx context.Context, binding state.AgentApprovalBinding) (string, bool, error)
	RecentAgentApprovalOutcome(ctx context.Context, binding state.AgentApprovalBinding, window time.Duration) (state.AgentApproval, bool, error)
	RequestAgentApproval(ctx context.Context, request state.AgentApproval) (state.AgentApproval, error)
}

// OrgAgentPolicyReader loads the organization's floor of approval categories.
type OrgAgentPolicyReader interface {
	OrgAgentPolicy(ctx context.Context, orgID string) (state.OrgAgentPolicy, error)
}

// ApprovalCategoriesFor returns the categories a person must approve for an
// agent or mcp-login key: its own set (every category when it chose none)
// plus its organization's floor.
func ApprovalCategoriesFor(ctx context.Context, store any, key state.APIKey) (map[string]bool, error) {
	gate, ok := store.(AgentApprovalGate)
	if !ok {
		return agentapproval.Effective(nil, false, nil), nil
	}
	own, chosen, err := gate.APIKeyApprovalCategories(ctx, key.ID)
	if err != nil {
		return nil, err
	}
	var floor []string
	if policies, ok := store.(OrgAgentPolicyReader); ok && key.OrgID != "" {
		policy, err := policies.OrgAgentPolicy(ctx, key.OrgID)
		if err != nil {
			return nil, err
		}
		floor = policy.ApprovalCategories
	}
	return agentapproval.Effective(own, chosen, floor), nil
}

// requireAgentApproval answers 428 approval_required to an agent key's
// request that needs a person's approval and has none; with an approval it
// uses it and lets the request through. It reports whether it has answered.
func (m *Middleware) requireAgentApproval(w http.ResponseWriter, r *http.Request, acct state.Account, key state.APIKey) bool {
	if !ProjectLimitable(key) {
		return false
	}
	rule, ok := agentapproval.Classify(r.Pattern)
	if !ok {
		return false
	}
	return m.gateAgentRequest(w, r, acct, key, rule)
}

// RequireAgentApprovalForApp is the gate for a handler that resolves its app
// itself (an upload commit names its app in a stored session): a production
// app's deploy needs approval like POST /v1/apps/{slug}/deployments.
func (m *Middleware) RequireAgentApprovalForApp(w http.ResponseWriter, r *http.Request, acct state.Account, key *state.APIKey, app state.App) bool {
	if key == nil || !ProjectLimitable(*key) || !productionApp(app) {
		return false
	}
	return m.gateAgentRequest(w, r, acct, *key, agentapproval.Rule{Category: api.AgentApprovalProduction})
}

// productionApp reports whether an app serves production: previews and
// developer environments name their parent app.
func productionApp(app state.App) bool {
	return app.PreviewOfSlug == ""
}

func (m *Middleware) gateAgentRequest(w http.ResponseWriter, r *http.Request, acct state.Account, key state.APIKey, rule agentapproval.Rule) bool {
	ctx := r.Context()
	gate, ok := m.Authn.(AgentApprovalGate)
	if !ok {
		// Without the store seam no request could ever be approved, so
		// refuse rather than let a gated request through.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Approval unavailable",
			"this request needs a person's approval, which this server cannot record"))
		return true
	}
	categories, err := ApprovalCategoriesFor(ctx, m.Authn, key)
	if err != nil {
		m.storeUnavailable(w, r, err, "could not load the agent's approval settings; retry shortly")
		return true
	}
	if !categories[rule.Category] {
		return false
	}
	if rule.Target != agentapproval.TargetNone {
		production, answered := m.productionTarget(w, r, rule.Target)
		if answered {
			return true
		}
		if !production {
			return false
		}
	}
	binding, err := approvalBinding(r, key.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeBadRequest, "Unreadable request body", "the request body could not be read"))
		return true
	}
	if _, used, err := gate.ConsumeAgentApproval(ctx, binding); err != nil {
		m.storeUnavailable(w, r, err, "could not check the request's approval; retry shortly")
		return true
	} else if used {
		return false
	}
	outcome, decided, err := gate.RecentAgentApprovalOutcome(ctx, binding, api.AgentApprovalOutcomeWindow)
	if err != nil {
		m.storeUnavailable(w, r, err, "could not check the request's approval; retry shortly")
		return true
	}
	if decided {
		m.writeApprovalOutcome(w, acct, key, outcome)
		return true
	}
	// The id is chosen here so a new request's webhook event can carry its
	// review link (ADR-20261107140000 §4).
	id := uuid.NewString()
	request, err := gate.RequestAgentApproval(ctx, state.AgentApproval{
		ID: id, KeyID: key.ID, AccountID: key.AccountID, OrgID: key.OrgID, Category: rule.Category,
		Method: binding.Method, Path: binding.Path, BodySHA256: binding.BodySHA256,
		ExpiresAt: time.Now().Add(api.AgentApprovalTTL), ReviewURL: m.reviewURL(id), AgentName: key.Label,
	})
	if errors.Is(err, state.ErrTooManyAgentApprovals) {
		limit, observed := int64(api.MaxPendingAgentApprovals), int64(api.MaxPendingAgentApprovals)
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeApprovalRequired, "Too many requests awaiting approval",
			"this agent has too many requests waiting for a person; ask them to decide those first").WithLimit(limit, observed))
		return true
	}
	if err != nil {
		m.storeUnavailable(w, r, err, "could not record the approval request; retry shortly")
		return true
	}
	reviewURL := m.reviewURL(request.ID)
	problem := api.NewProblem(http.StatusPreconditionRequired, api.CodeApprovalRequired, "Approval required",
		"this "+rule.Category+" request needs a person's approval: ask them to open "+reviewURL+", then repeat the same request").
		WithHint("Show the person the review link and wait; after they approve, repeat exactly the same request once.")
	problem.AgentApproval = &api.AgentApprovalPendingInfo{ID: request.ID, Category: rule.Category, ReviewURL: reviewURL, ExpiresAt: request.ExpiresAt.UTC().Format(time.RFC3339)}
	if m.Log != nil {
		m.Log.Info("agent_approval.required", "key_id", key.ID, "account_id", acct.ID, "category", rule.Category, "approval_id", request.ID)
	}
	api.WriteProblem(w, problem)
	return true
}

// reviewURL is the page where a person decides request id; empty when the
// server has none configured.
func (m *Middleware) reviewURL(id string) string {
	if m.ApprovalReviewURL == nil {
		return ""
	}
	return m.ApprovalReviewURL(id)
}

// writeApprovalOutcome tells an agent that repeats a denied or expired
// request what happened, so it stops asking (ADR-20261107140000 §6).
func (m *Middleware) writeApprovalOutcome(w http.ResponseWriter, acct state.Account, key state.APIKey, outcome state.AgentApproval) {
	reviewURL := m.reviewURL(outcome.ID)
	retryAt := outcome.DecidedAt
	status := api.AgentApprovalDenied
	if outcome.Status != api.AgentApprovalDenied {
		retryAt, status = outcome.ExpiresAt, api.AgentApprovalExpired
	}
	retryAt = retryAt.Add(api.AgentApprovalOutcomeWindow).UTC()
	var problem *api.Problem
	if status == api.AgentApprovalDenied {
		detail := "a person denied this " + outcome.Category + " request"
		if outcome.Note != "" {
			detail += ": " + outcome.Note
		}
		problem = api.NewProblem(http.StatusForbidden, api.CodeApprovalDenied, "Approval denied", detail).
			WithHint("Do not repeat this request. Tell the person, and change what you are doing; repeating it after " + retryAt.Format(time.RFC3339) + " asks again.")
	} else {
		problem = api.NewProblem(http.StatusGone, api.CodeApprovalExpired, "Approval expired",
			"nobody approved this "+outcome.Category+" request in time, or its approval went unused").
			WithHint("Ask the person whether to go ahead; repeating it after " + retryAt.Format(time.RFC3339) + " asks again.")
	}
	problem.AgentApproval = &api.AgentApprovalPendingInfo{
		ID: outcome.ID, Category: outcome.Category, ReviewURL: reviewURL,
		ExpiresAt: outcome.ExpiresAt.UTC().Format(time.RFC3339), Status: status, Note: outcome.Note,
	}
	if m.Log != nil {
		m.Log.Info("agent_approval.outcome", "key_id", key.ID, "account_id", acct.ID, "category", outcome.Category, "approval_id", outcome.ID, "status", status)
	}
	api.WriteProblem(w, problem)
}

// productionTarget reports whether the request's app is a production app. It
// reports answered when it has written an error.
func (m *Middleware) productionTarget(w http.ResponseWriter, r *http.Request, target agentapproval.Target) (production, answered bool) {
	ctx := r.Context()
	var app state.App
	var err error
	switch target {
	case agentapproval.TargetApp:
		app, err = m.Authn.AppBySlug(ctx, r.PathValue(firstWildcard(r.Pattern)))
	case agentapproval.TargetDeployment:
		deployments, okDeployments := m.Authn.(DeploymentAppResolver)
		apps, okApps := m.Authn.(AppByIDResolver)
		if !okDeployments || !okApps {
			// Without the seams the app is unknown: treat it as production.
			return true, false
		}
		id := r.PathValue(firstWildcard(r.Pattern))
		if uuid.Validate(id) != nil {
			// A malformed id names no deployment; the handler answers 404.
			return false, false
		}
		var appID string
		if appID, err = deployments.DeploymentAppID(ctx, id); err == nil {
			app, err = apps.AppByID(ctx, appID)
		}
	case agentapproval.TargetEdgeRule:
		rules, okRules := m.Authn.(EdgeRuleAppResolver)
		apps, okApps := m.Authn.(AppByIDResolver)
		if !okRules || !okApps {
			return true, false
		}
		id := r.PathValue(firstWildcard(r.Pattern))
		if uuid.Validate(id) != nil {
			return false, false
		}
		var rule state.EdgeRule
		if rule, err = rules.GetEdgeRuleByID(ctx, id); err == nil {
			app, err = apps.AppByID(ctx, rule.AppID)
		}
	}
	if errors.Is(err, state.ErrNotFound) {
		// The handler answers 404 for an app or deployment that does not
		// exist; there is nothing to approve.
		return false, false
	}
	if err != nil {
		m.storeUnavailable(w, r, err, "could not load the app; retry shortly")
		return false, true
	}
	return productionApp(app), false
}

// firstWildcard names the pattern's first path wildcard ("slug" in
// "POST /v1/apps/{slug}/rollback").
func firstWildcard(pattern string) string {
	_, rest, ok := strings.Cut(pattern, "{")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "}")
	return strings.TrimSuffix(name, "...")
}

// approvalBinding is what an approval of this request allows: the key, the
// method, the path with its query, and the body's hash when the body is small
// and not multipart. It restores the body for the handler.
func approvalBinding(r *http.Request, keyID string) (state.AgentApprovalBinding, error) {
	path := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	if len(path) > 2048 {
		path = path[:2048]
	}
	binding := state.AgentApprovalBinding{KeyID: keyID, Method: r.Method, Path: path}
	if r.Body == nil || r.Body == http.NoBody || multipartBody(r) || r.ContentLength > api.AgentApprovalBodyBindBytes {
		return binding, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, api.AgentApprovalBodyBindBytes+1))
	if err != nil {
		return binding, err
	}
	if len(body) > api.AgentApprovalBodyBindBytes {
		// Too large to bind: hand the handler the whole body back.
		r.Body = readCloser{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
		return binding, nil
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	sum := sha256.Sum256(body)
	binding.BodySHA256 = sum[:]
	return binding, nil
}

func multipartBody(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && strings.HasPrefix(mediaType, "multipart/")
}

type readCloser struct {
	io.Reader
	io.Closer
}
