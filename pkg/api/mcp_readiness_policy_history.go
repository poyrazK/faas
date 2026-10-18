package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type MCPReadinessPolicyHistory struct {
	Policies           []MCPReadinessPolicy `json:"policies"`
	NextBeforeRevision string               `json:"next_before_revision,omitempty"`
}
type RestoreMCPReadinessPolicyRequest struct {
	Scope            string `json:"scope,omitempty"`
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func ValidateRestoreMCPReadinessPolicy(r RestoreMCPReadinessPolicyRequest, revision string) error {
	if ValidateScope(MCPConnectionScope(r.Scope)) != nil || uuid.Validate(revision) != nil || uuid.Validate(r.ExpectedRevision) != nil || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 512 {
		return errors.New("require a valid scope, historical revision, expected current revision and 1–512 byte audit reason")
	}
	return nil
}

type MCPReadinessPolicyChange struct {
	Path   string          `json:"path"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}
type MCPReadinessPolicyDiff struct {
	Scope        string                     `json:"scope"`
	FromRevision string                     `json:"from_revision"`
	ToRevision   string                     `json:"to_revision"`
	Changes      []MCPReadinessPolicyChange `json:"changes"`
}

// DiffMCPReadinessPolicies compares configuration only. Audit metadata and role
// ordering do not change policy behavior. Role paths use JSON Pointer escaping.
func DiffMCPReadinessPolicies(from, to MCPReadinessPolicy) MCPReadinessPolicyDiff {
	out := MCPReadinessPolicyDiff{Scope: MCPConnectionScope(to.Scope), FromRevision: from.Revision, ToRevision: to.Revision, Changes: []MCPReadinessPolicyChange{}}
	add := func(path string, a, b any) {
		before, _ := json.Marshal(a)
		after, _ := json.Marshal(b)
		if !bytes.Equal(before, after) {
			out.Changes = append(out.Changes, MCPReadinessPolicyChange{Path: path, Before: before, After: after})
		}
	}
	add("/enabled", from.Enabled, to.Enabled)
	add("/endpoint", from.Endpoint, to.Endpoint)
	add("/max_age_seconds", from.MaxAgeSeconds, to.MaxAgeSeconds)
	left, right := map[string]*MCPReadinessRole{}, map[string]*MCPReadinessRole{}
	names := map[string]bool{}
	for _, role := range from.Roles {
		left[role.Name] = &role
		names[role.Name] = true
	}
	for _, role := range to.Roles {
		right[role.Name] = &role
		names[role.Name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		add("/roles/"+strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1"), left[name], right[name])
	}
	return out
}
func (c *Client) ListMCPReadinessPolicyHistory(ctx context.Context, slug, scope, before string, limit int) (MCPReadinessPolicyHistory, error) {
	var out MCPReadinessPolicyHistory
	q := url.Values{"scope": {MCPConnectionScope(scope)}}
	if before != "" {
		q.Set("before_revision", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-readiness-policy/history?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) GetMCPReadinessPolicyRevision(ctx context.Context, slug, scope, revision string) (MCPReadinessPolicy, error) {
	var out MCPReadinessPolicy
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-readiness-policy/history/"+url.PathEscape(revision)+"?scope="+url.QueryEscape(MCPConnectionScope(scope)), nil, &out)
	return out, err
}
func (c *Client) DiffMCPReadinessPolicy(ctx context.Context, slug, scope, from, to string) (MCPReadinessPolicyDiff, error) {
	var out MCPReadinessPolicyDiff
	q := url.Values{"scope": {MCPConnectionScope(scope)}, "from_revision": {from}}
	if to != "" {
		q.Set("to_revision", to)
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-readiness-policy/diff?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) RestoreMCPReadinessPolicy(ctx context.Context, slug, revision string, r RestoreMCPReadinessPolicyRequest) (MCPReadinessPolicy, error) {
	var out MCPReadinessPolicy
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/mcp-readiness-policy/history/"+url.PathEscape(revision)+"/restore", r, &out)
	return out, err
}
