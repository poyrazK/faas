package sandboxes

// Web-mode domain policies (ADR-20261117091026 §1–§2). An account keeps a
// default policy; a session can narrow it at create, never widen it, and is
// admitted with its effective policy fixed on its row.

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// EgressPolicyStore keeps accounts' default policies; *agentstore.Store
// implements it.
type EgressPolicyStore interface {
	SandboxEgressPolicy(ctx context.Context, accountID string) (agentstore.SandboxEgressPolicy, error)
	PutSandboxEgressPolicy(ctx context.Context, accountID string, allow, deny []string) (agentstore.SandboxEgressPolicy, error)
}

// validRules checks one list of a policy and returns it sorted and without
// duplicates.
func validRules(field string, rules []string) ([]string, *api.Problem) {
	if len(rules) > api.SandboxEgressRulesMax {
		return nil, api.ErrValidation(fmt.Sprintf("%s holds at most %d rules (SandboxEgressRulesMax); got %d", field,
			api.SandboxEgressRulesMax, len(rules)))
	}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if !api.ValidSandboxEgressRule(r) {
			return nil, api.ErrValidation(fmt.Sprintf("%s rule %q must be a lowercase public domain name such as example.com; "+
				"it matches that name and every name below it", field, r))
		}
		out = append(out, r)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// egressPolicy reads the account's default policy, or the empty one when
// this host keeps none.
func (h *Handler) egressPolicy(ctx context.Context, acct state.Account) (agentstore.SandboxEgressPolicy, *api.Problem) {
	store, ok := h.Store.(EgressPolicyStore)
	if !ok {
		return agentstore.SandboxEgressPolicy{}, nil
	}
	p, err := store.SandboxEgressPolicy(ctx, acct.ID)
	if err != nil {
		return p, h.problem(acct, "sandbox.egress_policy.get", err)
	}
	return p, nil
}

// applyEgress sets a web-mode session's effective policy: the account's
// deny plus the session's, and the session's allow, which must fall under
// the account's, or else the account's (ADR-20261117091026 §2).
func (h *Handler) applyEgress(ctx context.Context, acct state.Account, a *agentstore.SandboxAdmission, n *api.SandboxNetwork) *api.Problem {
	var allow, deny []string
	if n != nil {
		allow, deny = n.Allow, n.Deny
	}
	if a.NetworkMode != api.SandboxNetworkWeb {
		if len(allow)+len(deny) > 0 {
			return api.ErrValidation("network.allow and network.deny apply to network.mode web only")
		}
		return nil
	}
	allow, prob := validRules("network.allow", allow)
	if prob != nil {
		return prob
	}
	if deny, prob = validRules("network.deny", deny); prob != nil {
		return prob
	}
	account, prob := h.egressPolicy(ctx, acct)
	if prob != nil {
		return prob
	}
	if len(allow) > 0 && len(account.Allow) > 0 {
		for _, r := range allow {
			if !api.SandboxEgressRuleCovered(r, account.Allow) {
				return api.ErrValidation(fmt.Sprintf("network.allow rule %q is outside the account's egress policy, which a session "+
					"can narrow but not widen", r))
			}
		}
	}
	if len(allow) == 0 {
		allow = account.Allow
	}
	deny = append(slices.Clone(account.Deny), deny...)
	slices.Sort(deny)
	deny = slices.Compact(deny)
	if len(allow)+len(deny) > 0 && !h.EgressPolicy {
		return api.ErrSandboxUnsupported("network.allow", "egress policies are not on for this control plane yet")
	}
	a.EgressAllow, a.EgressDeny = allow, deny
	return nil
}

// GetEgressPolicy handles GET /v1/sandbox-egress-policy: the account's
// default web-mode domain policy.
func (h *Handler) GetEgressPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	p, prob := h.egressPolicy(r.Context(), acct)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, policyView(p))
}

// PutEgressPolicy handles PUT /v1/sandbox-egress-policy. It applies to
// sessions admitted afterwards; live ones keep the policy they started with.
func (h *Handler) PutEgressPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	store, ok := h.Store.(EgressPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("egress policies are unavailable on this control-plane host"))
		return
	}
	if !h.EgressPolicy {
		api.WriteProblem(w, api.ErrSandboxUnsupported("egress policy", "egress policies are not on for this control plane yet"))
		return
	}
	var body api.SandboxEgressPolicy
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	allow, prob := validRules("allow", body.Allow)
	if prob == nil {
		body.Deny, prob = validRules("deny", body.Deny)
	}
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	p, err := store.PutSandboxEgressPolicy(r.Context(), acct.ID, allow, body.Deny)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.egress_policy.put", err))
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "sandbox.egress_policy.updated", acct.ID, map[string]any{"allow": len(p.Allow), "deny": len(p.Deny)})
	}
	httpkit.WriteJSON(w, http.StatusOK, policyView(p))
}

func policyView(p agentstore.SandboxEgressPolicy) api.SandboxEgressPolicy {
	out := api.SandboxEgressPolicy{Allow: p.Allow, Deny: p.Deny}
	if out.Allow == nil {
		out.Allow = []string{}
	}
	if out.Deny == nil {
		out.Deny = []string{}
	}
	return out
}

// EgressLogStore reads sessions' domain logs; *agentstore.Store implements
// it.
type EgressLogStore interface {
	SandboxDomains(ctx context.Context, accountID, id string) ([]agentstore.SandboxDomain, error)
}

// GetEgress handles GET /v1/sandboxes/{id}/egress: the policy the session
// was admitted under and its domain log (ADR-20261117091026 §4).
func (h *Handler) GetEgress(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	sb, err := h.Store.Sandbox(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.egress", err))
		return
	}
	out := api.SandboxEgress{SandboxID: sb.ID, Mode: sb.NetworkMode,
		SandboxEgressPolicy: policyView(agentstore.SandboxEgressPolicy{Allow: sb.EgressAllow, Deny: sb.EgressDeny}),
		Domains:             []api.SandboxEgressDomain{}}
	if store, ok := h.Store.(EgressLogStore); ok {
		log, err := store.SandboxDomains(r.Context(), acct.ID, sb.ID)
		if err != nil {
			api.WriteProblem(w, h.problem(acct, "sandbox.egress", err))
			return
		}
		for _, d := range log {
			out.Domains = append(out.Domains, api.SandboxEgressDomain{Domain: d.Domain, Refused: d.Refused, FirstSeen: d.FirstSeen.UTC()})
		}
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
