package api

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EnvironmentCacheRule is an unconditional cache rule for one immutable
// environment host. Host and rule identity are assigned by the platform.
type EnvironmentCacheRule struct {
	Path     string              `json:"path"`
	Priority int                 `json:"priority"`
	Cache    EdgeRuleCacheAction `json:"cache"`
}

type EnvironmentCachePolicy struct {
	AppID         string                 `json:"app_id"`
	ProjectID     string                 `json:"project_id"`
	EnvironmentID string                 `json:"environment_id"`
	Scope         string                 `json:"scope"`
	Revision      int64                  `json:"revision"`
	Inherited     bool                   `json:"inherited"`
	Rules         []EnvironmentCacheRule `json:"rules"`
	UpdatedAt     *time.Time             `json:"updated_at,omitempty"`
}

// EnvironmentCachePolicySource pins an explicit override on another named
// environment of the same app and project. Its rules are checked atomically.
type EnvironmentCachePolicySource struct {
	Scope         string `json:"scope"`
	EnvironmentID string `json:"environment_id"`
	Revision      int64  `json:"revision"`
}

type SetEnvironmentCachePolicyRequest struct {
	RestoreRevision *int64                        `json:"restore_revision,omitempty"`
	CopyFrom        *EnvironmentCachePolicySource `json:"copy_from,omitempty"`
	// Inherited restores app policy; Rules must be an explicit empty list.
	Inherited        bool                   `json:"inherited,omitempty"`
	AppID            string                 `json:"app_id"`
	ProjectID        string                 `json:"project_id"`
	EnvironmentID    string                 `json:"environment_id"`
	RequestID        string                 `json:"request_id"`
	ExpectedRevision *int64                 `json:"expected_revision"`
	Rules            []EnvironmentCacheRule `json:"rules"`
}

// NormalizeEnvironmentCacheRules validates and copies the complete replacement.
// A non-nil empty list deliberately disables cache policy in this environment.
func NormalizeEnvironmentCacheRules(rules []EnvironmentCacheRule) ([]EnvironmentCacheRule, error) {
	if rules == nil || len(rules) > EnvironmentCacheRuleLimit {
		return nil, errors.New("rules must be an explicit bounded list; use [] to disable environment caching")
	}
	out := make([]EnvironmentCacheRule, len(rules))
	priorities := map[int]bool{}
	for i, r := range rules {
		if !strings.HasPrefix(r.Path, "/") || len(r.Path) > EnvironmentCachePathMaxBytes || strings.ContainsAny(r.Path, "{}\\\x00\r\n") || r.Priority < 0 || r.Priority > EnvironmentCachePriorityMax || priorities[r.Priority] {
			return nil, errors.New("cache rules require valid paths and distinct bounded priorities")
		}
		if _, err := MatchEdgeRulePath(r.Path, "/"); err != nil {
			return nil, errors.New("invalid cache path glob")
		}
		priorities[r.Priority] = true
		r.Cache.Methods = append([]string(nil), r.Cache.Methods...)
		r.Cache.VaryOn = append([]string(nil), r.Cache.VaryOn...)
		if r.Cache.Validate() != nil {
			return nil, errors.New("invalid environment cache settings")
		}
		out[i] = r
	}
	return out, nil
}

func ValidateEnvironmentCachePolicyRequest(req SetEnvironmentCachePolicyRequest) error {
	for _, id := range []string{req.AppID, req.ProjectID, req.EnvironmentID, req.RequestID} {
		if _, err := uuid.Parse(id); err != nil {
			return errors.New("immutable UUID identities and request_id are required")
		}
	}
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 0 || *req.ExpectedRevision >= RouteRequirementsMaxRevision {
		return errors.New("a bounded expected_revision is required")
	}
	if req.Inherited && len(req.Rules) != 0 {
		return errors.New("inherited policy requires an explicit empty rules list")
	}
	if rev := req.RestoreRevision; rev != nil && (*rev < 0 || *rev > RouteRequirementsMaxRevision || req.CopyFrom != nil) {
		return errors.New("restore_revision must be bounded and cannot be combined with copy_from")
	}
	if source := req.CopyFrom; source != nil {
		if _, err := uuid.Parse(source.EnvironmentID); err != nil {
			return errors.New("copy source requires an immutable environment UUID")
		}
		if req.Inherited || source.EnvironmentID == req.EnvironmentID || !ValidProjectEnvironmentSlug(source.Scope) || source.Revision < 1 || source.Revision > RouteRequirementsMaxRevision {
			return errors.New("copy source must pin a different named environment's explicit positive revision")
		}
	}
	_, err := NormalizeEnvironmentCacheRules(req.Rules)
	return err
}

func environmentCachePolicyPath(slug, environment string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/cache-policy?environment=" + url.QueryEscape(environment)
}
func (c *Client) GetEnvironmentCachePolicy(ctx context.Context, slug, environment string) (EnvironmentCachePolicy, error) {
	var out EnvironmentCachePolicy
	err := c.do(ctx, "GET", environmentCachePolicyPath(slug, environment), nil, &out)
	return out, err
}
func (c *Client) SetEnvironmentCachePolicy(ctx context.Context, slug, environment string, req SetEnvironmentCachePolicyRequest) (EnvironmentCachePolicy, error) {
	var out EnvironmentCachePolicy
	err := c.doWithIdempotencyKey(ctx, "PUT", environmentCachePolicyPath(slug, environment), req, &out, req.RequestID)
	return out, err
}
