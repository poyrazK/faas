package apidserver

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// A hosted MCP sign-in acts in one organization, chosen on the approval page
// (ADR-20261101225122).

// mcpOrgChoice is a shared organization an agent may be connected to.
type mcpOrgChoice struct {
	ID, Name, Role string
}

// mcpOrgChoices lists the shared organizations the account may connect an
// agent to: those whose agent policy allows agents and where its role can see
// apps. It is empty for a CLI code or a store without agent policies.
func (h *cliAuthHandlers) mcpOrgChoices(r *http.Request, accountID, raw string) []mcpOrgChoice {
	if _, ok := h.srv.store.(state.OrgAgentStore); !ok || !isMCPAuthCode(raw) {
		return nil
	}
	orgs, err := h.srv.store.ListOrgsForAccount(r.Context(), accountID)
	if err != nil {
		h.log.Warn("cli_auth.list_orgs", "err", err)
		return nil
	}
	var out []mcpOrgChoice
	for _, org := range orgs {
		if org.Personal {
			continue
		}
		if _, err := agentconnections.PolicyFor(r.Context(), h.srv.store, org.ID, accountID); err != nil {
			continue
		}
		member, err := h.srv.store.OrgAccessSnapshot(r.Context(), org.ID, accountID)
		if err != nil {
			continue
		}
		out = append(out, mcpOrgChoice{ID: org.ID, Name: org.Name, Role: string(member.Role)})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// bindMCPOrg binds a hosted MCP code to the organization chosen on the
// approval page, after checking the account may connect an agent there. It
// renders the refusal and returns false when it may not.
func (h *cliAuthHandlers) bindMCPOrg(w http.ResponseWriter, r *http.Request, accountID, rawCode string, hash []byte) bool {
	orgID := strings.TrimSpace(r.PostForm.Get("org"))
	if orgID == "" || !isMCPAuthCode(rawCode) {
		return true
	}
	store, ok := h.srv.store.(state.OrgAgentStore)
	if !ok {
		h.renderCliAuthRefused(w, r, "Organizations unavailable", "Gregale can't connect agents to organizations right now. Connect to your personal account, or try again later.")
		return false
	}
	_, err := agentconnections.PolicyFor(r.Context(), h.srv.store, orgID, accountID)
	switch {
	case errors.Is(err, agentconnections.ErrNotAgentMember):
		h.renderCliAuthRefused(w, r, "Organization unavailable", "You can't connect an agent to that organization: your role there can't see its apps, or you aren't a member.")
		return false
	case errors.Is(err, agentconnections.ErrAgentsNotAllowed):
		h.renderCliAuthRefused(w, r, "Agents not allowed", "That organization's agent policy doesn't allow agents. Ask an owner or admin, or connect to another organization.")
		return false
	case err != nil:
		h.log.Error("cli_auth.org_policy", "err", err)
		h.renderCliAuthRefused(w, r, "Try again", "Gregale couldn't check the organization's agent policy.")
		return false
	}
	if err := store.SetCliAuthCodeOrg(r.Context(), hash, orgID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			h.renderCliAuthError(w, r, "Code expired", "Start the connection again from your agent.")
			return false
		}
		h.log.Error("cli_auth.bind_org", "err", err)
		h.renderCliAuthRefused(w, r, "Try again", "Gregale couldn't record the organization for this connection.")
		return false
	}
	return true
}

// renderCliAuthRefused renders the approval page refusing the connection,
// with why.
func (h *cliAuthHandlers) renderCliAuthRefused(w http.ResponseWriter, r *http.Request, title, detail string) {
	page := dashboard.Page{Title: title, Body: "cli_auth", Data: map[string]any{"Refused": true, "Detail": detail}}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		h.log.Error("cli_auth.render_refused", "err", err)
		renderProblem(w, h.log, err)
	}
}
