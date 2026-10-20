package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Release freeze kinds (ADR-20261019071733).
const (
	ReleaseFreezeKindWindow        = "window"
	ReleaseFreezeKindWeekly        = "weekly"
	ReleaseFreezeKindAfterRollback = "after_rollback"
)

// ReleaseFreezeWeekly is a weekly window: it opens on each listed day at
// Start (HH:MM in TimeZone) and lasts DurationMinutes, so it may run past
// midnight or into the next week.
type ReleaseFreezeWeekly struct {
	// Days are lowercase three-letter weekdays: sun, mon, tue, wed, thu, fri, sat.
	Days            []string `json:"days"`
	Start           string   `json:"start"`
	DurationMinutes int      `json:"duration_minutes"`
	TimeZone        string   `json:"time_zone"`
}

// ReleaseFreeze withholds production releases of one app or, with App empty,
// of all the account's apps.
type ReleaseFreeze struct {
	ID string `json:"id"`
	// App is the app's slug; empty covers every app of the account.
	App  string `json:"app,omitempty"`
	Kind string `json:"kind"`
	// StartsAt and EndsAt bound a window; EndsAt empty means until lifted.
	StartsAt string               `json:"starts_at,omitempty"`
	EndsAt   string               `json:"ends_at,omitempty"`
	Weekly   *ReleaseFreezeWeekly `json:"weekly,omitempty"`
	// AfterRollbackMinutes is how long an after_rollback policy freezes an
	// app Gregale rolled back automatically.
	AfterRollbackMinutes int    `json:"after_rollback_minutes,omitempty"`
	Reason               string `json:"reason"`
	// Source is manual, or automatic_rollback for a freeze an
	// after_rollback policy created.
	Source             string `json:"source"`
	SourceDeploymentID string `json:"source_deployment_id,omitempty"`
	CreatedBy          string `json:"created_by"`
	CreatedAt          string `json:"created_at"`
	// Active reports whether the freeze withholds releases now.
	Active bool `json:"active"`
	// NextStartsAt is the next time an inactive window or weekly freeze
	// starts, within a week.
	NextStartsAt string `json:"next_starts_at,omitempty"`
}

// CreateReleaseFreezeRequest creates a freeze. Kind window takes StartsAt
// (default now) and optional EndsAt; weekly takes Weekly; after_rollback takes
// AfterRollbackMinutes.
type CreateReleaseFreezeRequest struct {
	App                  string               `json:"app,omitempty"`
	Kind                 string               `json:"kind"`
	StartsAt             string               `json:"starts_at,omitempty"`
	EndsAt               string               `json:"ends_at,omitempty"`
	Weekly               *ReleaseFreezeWeekly `json:"weekly,omitempty"`
	AfterRollbackMinutes int                  `json:"after_rollback_minutes,omitempty"`
	Reason               string               `json:"reason"`
}

// ListReleaseFreezesResponse lists unlifted freezes that have not ended.
type ListReleaseFreezesResponse struct {
	Freezes []ReleaseFreeze `json:"freezes"`
}

// ReleaseFreezeStatus answers whether a production release of App would be
// refused now.
type ReleaseFreezeStatus struct {
	App    string          `json:"app"`
	Frozen bool            `json:"frozen"`
	Active []ReleaseFreeze `json:"active"`
	// Next is the freeze that starts next within a week, if any.
	Next *ReleaseFreeze `json:"next,omitempty"`
}

// ErrReleaseFrozen refuses a production release while freezes are active.
func ErrReleaseFrozen(app string, active []ReleaseFreeze) *Problem {
	reasons := make([]string, 0, len(active))
	for _, f := range active {
		until := "until lifted"
		if f.EndsAt != "" {
			until = "until " + f.EndsAt
		} else if f.Kind == ReleaseFreezeKindWeekly {
			until = "during its weekly window"
		}
		reasons = append(reasons, fmt.Sprintf("%q by %s, %s", f.Reason, f.CreatedBy, until))
	}
	p := NewProblem(http.StatusConflict, CodeReleaseFrozen, "Production releases are frozen",
		fmt.Sprintf("Production releases of %s are frozen: %s.", app, strings.Join(reasons, "; "))).
		WithHint("Wait for the freeze to end, lift it with `gregale freeze lift`, or release anyway with override_freeze and a reason (CLI: --override-freeze REASON); the override is audited.").
		WithDocs(docsBase + "/deploys#deploy-freezes")
	p.Freezes = active
	return p
}

// ErrReleaseFreezeNotFound reports a missing or already-lifted freeze.
func ErrReleaseFreezeNotFound(id string) *Problem {
	return NewProblem(http.StatusNotFound, CodeReleaseFreezeNotFound, "Freeze not found",
		fmt.Sprintf("No unlifted freeze %q exists in this account.", id)).
		WithHint("List freezes with `gregale freeze list`.")
}

// ErrReleaseFreezeLimit reports the per-account freeze limit.
func ErrReleaseFreezeLimit(observed int) *Problem {
	return NewProblem(http.StatusConflict, CodeReleaseFreezeLimit, "Too many freezes",
		fmt.Sprintf("An account can hold %d freezes that have not ended; this one holds %d.", ReleaseFreezesMax, observed)).
		WithHint("Lift freezes you no longer need with `gregale freeze lift`.").
		WithDocs(docsBase + "/deploys#deploy-freezes")
}

// ListReleaseFreezes lists the account's freezes; app filters to the freezes
// covering one app.
func (c *Client) ListReleaseFreezes(ctx context.Context, app string) (ListReleaseFreezesResponse, error) {
	var out ListReleaseFreezesResponse
	path := "/v1/freezes"
	if app != "" {
		path += "?app=" + url.QueryEscape(app)
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// CreateReleaseFreeze creates a freeze.
func (c *Client) CreateReleaseFreeze(ctx context.Context, req CreateReleaseFreezeRequest) (ReleaseFreeze, error) {
	var out ReleaseFreeze
	return out, c.do(ctx, http.MethodPost, "/v1/freezes", req, &out)
}

// LiftReleaseFreeze lifts a freeze.
func (c *Client) LiftReleaseFreeze(ctx context.Context, id string) (ReleaseFreeze, error) {
	var out ReleaseFreeze
	return out, c.do(ctx, http.MethodDelete, "/v1/freezes/"+url.PathEscape(id), nil, &out)
}

// GetReleaseFreezeStatus reports whether a production release of slug would
// be refused now.
func (c *Client) GetReleaseFreezeStatus(ctx context.Context, slug string) (ReleaseFreezeStatus, error) {
	var out ReleaseFreezeStatus
	return out, c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/freeze-status", nil, &out)
}
