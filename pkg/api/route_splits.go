package api

import (
	"context"
	"net/url"
	"time"
)

// Route splits (ADR-20261121170000 amendment 2): applying a suggested split
// creates a second app from the app's live build, routes the route to it and,
// for a memory split, sizes the app down. The work runs in the background;
// the split's phase reports where it is.

// Route split phases.
const (
	RouteSplitPhaseCreating   = "creating"
	RouteSplitPhaseDeploying  = "deploying"
	RouteSplitPhaseRuleLive   = "rule_live"
	RouteSplitPhaseDone       = "done"
	RouteSplitPhaseRolledBack = "rolled_back"
	RouteSplitPhaseUndone     = "undone"
)

const (
	// RouteSplitDeployTimeout is how long a split waits for the new app's
	// deployment to go live before it rolls back.
	RouteSplitDeployTimeout = 15 * time.Minute
	// RouteSplitCreateTimeout is how long a split may stay in phase
	// creating (apid died between recording it and creating the app)
	// before the worker rolls it back.
	RouteSplitCreateTimeout = 5 * time.Minute
	// RouteSplitsListMax bounds the splits GET /v1/apps/{slug}/route-splits
	// returns.
	RouteSplitsListMax = 20
)

// CreateRouteSplitRequest is POST /v1/apps/{slug}/route-splits. The fields
// repeat the suggestion the caller saw; apid recomputes it over the same
// window and refuses with route_split_changed when it no longer matches.
type CreateRouteSplitRequest struct {
	Kind       string `json:"kind"`
	Route      string `json:"route"`
	Method     string `json:"method,omitempty"`
	RouteRAMMB int    `json:"route_ram_mb"`
	MainRAMMB  int    `json:"main_ram_mb"`
	// Since is the analysed window, as for the route-resources read.
	Since string `json:"since,omitempty"`
}

// RouteSplitResponse is one applied split.
type RouteSplitResponse struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	TargetSlug string `json:"target_slug"`
	Kind       string `json:"kind"`
	Route      string `json:"route"`
	Method     string `json:"method,omitempty"`
	PathGlob   string `json:"path_glob"`
	RouteRAMMB int    `json:"route_ram_mb"`
	MainRAMMB  int    `json:"main_ram_mb"`
	// PreviousRAMMB is the app's size before the split; undo restores it.
	PreviousRAMMB int      `json:"previous_ram_mb"`
	MainResized   bool     `json:"main_resized"`
	EdgeRuleIDs   []string `json:"edge_rule_ids"`
	Phase         string   `json:"phase"`
	Warning       string   `json:"warning,omitempty"`
	Error         string   `json:"error,omitempty"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

// RouteSplitsResponse is GET /v1/apps/{slug}/route-splits, newest first.
type RouteSplitsResponse struct {
	Splits []RouteSplitResponse `json:"splits"`
}

// CreateAppRouteSplit applies a suggested split.
func (c *Client) CreateAppRouteSplit(ctx context.Context, slug string, req CreateRouteSplitRequest) (RouteSplitResponse, error) {
	var out RouteSplitResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/route-splits", req, &out)
}

// ListAppRouteSplits lists an app's splits.
func (c *Client) ListAppRouteSplits(ctx context.Context, slug string) (RouteSplitsResponse, error) {
	var out RouteSplitsResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-splits", nil, &out)
}

// GetAppRouteSplit reads one split.
func (c *Client) GetAppRouteSplit(ctx context.Context, slug, id string) (RouteSplitResponse, error) {
	var out RouteSplitResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-splits/"+url.PathEscape(id), nil, &out)
}

// UndoAppRouteSplit reverses a finished split: the rule goes, the app gets
// its previous size back and the route's app is deleted.
func (c *Client) UndoAppRouteSplit(ctx context.Context, slug, id string) (RouteSplitResponse, error) {
	var out RouteSplitResponse
	return out, c.do(ctx, "DELETE", "/v1/apps/"+url.PathEscape(slug)+"/route-splits/"+url.PathEscape(id), nil, &out)
}
