package apidserver

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A hosted MCP sign-in can be limited to projects of the account or
// organization it acts in, chosen on the approval page (ADR-20261104085712).

// mcpProjectChoice is a project a connection may be limited to.
type mcpProjectChoice struct {
	ID, Slug, Owner string
}

// mcpProjectChoices lists the account's projects in its personal account and
// in the organizations it may connect an agent to, for the approval page.
func (h *cliAuthHandlers) mcpProjectChoices(r *http.Request, accountID, raw string, orgs []mcpOrgChoice) []mcpProjectChoice {
	if _, ok := h.srv.store.(state.KeyProjectLimitStore); !ok || !isMCPAuthCode(raw) {
		return nil
	}
	projects, err := h.srv.store.ListProjectsForAccount(r.Context(), accountID)
	if err != nil {
		h.log.Warn("cli_auth.list_projects", "err", err)
		return nil
	}
	owners := map[string]string{}
	for _, org := range orgs {
		owners[org.ID] = org.Name
	}
	personal, _ := h.srv.store.OrgByPersonalAccount(r.Context(), accountID)
	var out []mcpProjectChoice
	for _, project := range projects {
		owner, shared := owners[project.OrgID]
		if !shared {
			if project.OrgID != "" && project.OrgID != personal.ID {
				continue
			}
			owner = "personal account"
		}
		out = append(out, mcpProjectChoice{ID: project.ID, Slug: project.Slug, Owner: owner})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return strings.ToLower(out[i].Owner) < strings.ToLower(out[j].Owner)
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// bindMCPProjects records the projects chosen on the approval page for a
// hosted MCP code, after checking each belongs to the account or organization
// the connection acts in. It renders the refusal and returns false when one
// does not.
func (h *cliAuthHandlers) bindMCPProjects(w http.ResponseWriter, r *http.Request, accountID, rawCode string, hash []byte) bool {
	ids := slices.Compact(slices.Sorted(slices.Values(r.PostForm["project"])))
	if len(ids) == 0 || !isMCPAuthCode(rawCode) {
		return true
	}
	store, ok := h.srv.store.(state.KeyProjectLimitStore)
	if !ok || len(ids) > state.MaxKeyProjects {
		h.renderCliAuthRefused(w, r, "Projects unavailable", "Gregale can't limit this connection to those projects. Connect with every project, or try again later.")
		return false
	}
	orgID := strings.TrimSpace(r.PostForm.Get("org"))
	if orgID == "" {
		personal, err := h.srv.store.OrgByPersonalAccount(r.Context(), accountID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			h.renderCliAuthRefused(w, r, "Try again", "Gregale couldn't check the projects for this connection.")
			return false
		}
		orgID = personal.ID
	}
	for _, id := range ids {
		project, err := h.srv.store.ProjectByID(r.Context(), id)
		if err != nil || project.AccountID != accountID || project.OrgID != "" && project.OrgID != orgID {
			h.renderCliAuthRefused(w, r, "Project not in this organization", "Choose only projects of the account or organization the agent acts in.")
			return false
		}
	}
	if err := store.SetCliAuthCodeProjects(r.Context(), hash, ids); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			h.renderCliAuthError(w, r, "Code expired", "Start the connection again from your agent.")
			return false
		}
		h.log.Error("cli_auth.bind_projects", "err", err)
		h.renderCliAuthRefused(w, r, "Try again", "Gregale couldn't record the projects for this connection.")
		return false
	}
	return true
}

// mintBrowserAuthKey is createBrowserAuthKey for a sign-in limited to
// projects: the key holds no database capabilities (databases belong to the
// organization) and is limited to the projects. A key that cannot be limited
// is revoked rather than left with every project.
func (h *cliAuthHandlers) mintBrowserAuthKey(r *http.Request, req api.CliAuthExchangeRequest, accountID string, keyHash []byte, label string, scopes []string, expiresAt time.Time) (state.APIKey, []string, *api.Problem) {
	projects, limited, err := h.browserAuthProjects(r, req)
	if err != nil {
		h.log.Error("cli_auth.code_projects", "err", err)
		return state.APIKey{}, nil, api.ErrCapacity("could not read the sign-in's projects")
	}
	if !limited {
		return h.createBrowserAuthKey(r, req, accountID, keyHash, label, scopes, expiresAt)
	}
	scopes = slices.DeleteFunc(slices.Clone(scopes), func(scope string) bool {
		return scope == api.ScopeManagedPostgresManage || scope == api.ScopeManagedPostgresRead
	})
	k, scopes, p := h.createBrowserAuthKey(r, req, accountID, keyHash, label, scopes, expiresAt)
	if p != nil {
		return k, scopes, p
	}
	store, _ := h.srv.store.(state.KeyProjectLimitStore)
	if store != nil && store.SetAPIKeyProjectLimit(r.Context(), k.ID, projects) == nil {
		return k, scopes, nil
	}
	if k.OrgID != "" {
		_, _ = h.srv.store.RevokeOrgAPIKey(r.Context(), k.OrgID, k.ID)
	} else {
		_ = h.srv.store.DeleteAPIKey(r.Context(), accountID, k.ID)
	}
	return state.APIKey{}, nil, api.ErrCapacity("could not limit the connection to its projects; connect the agent again")
}

// browserAuthProjects is the projects a hosted MCP sign-in's code was limited
// to at approval, and whether it was.
func (h *cliAuthHandlers) browserAuthProjects(r *http.Request, req api.CliAuthExchangeRequest) ([]string, bool, error) {
	store, ok := h.srv.store.(state.KeyProjectLimitStore)
	hash, valid := normalizeCliAuthCode(req.Code)
	if !ok || !valid || !isMCPAuthCode(req.Code) {
		return nil, false, nil
	}
	ids, limited, err := store.CliAuthCodeProjects(r.Context(), hash)
	if errors.Is(err, state.ErrNotFound) {
		return nil, false, nil
	}
	return ids, limited, err
}
