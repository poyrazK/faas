package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
)

// Runtime report availability (DataAPIStatus.RuntimeReports). Usage reports,
// monthly caps, keyless live reads and change hooks reach the platform only
// under workload identity (ADR-1141); an installation without it accepts none.
const (
	DataAPIRuntimeReportsAvailable   = "available"
	DataAPIRuntimeReportsUnavailable = "unavailable"
)

// Change hook publishing states a runtime reports: ok when its last
// publication reached the platform or nothing waits, retrying when recorded
// changes wait for a publication that keeps failing.
const (
	DataAPIChangeHooksPublishingOK       = "ok"
	DataAPIChangeHooksPublishingRetrying = "retrying"
)

// DataAPIStatus is what the platform knows about an app's Data API without
// waking it: the runtime it serves, whether runtime reports reach it, and the
// change hook delivery its runtime last reported.
type DataAPIStatus struct {
	RuntimeVersion string                   `json:"runtime_version"`
	RuntimeReports string                   `json:"runtime_reports"`
	ChangeHooks    *DataAPIChangeHookStatus `json:"change_hooks,omitempty"`
}

// DataAPIChangeHookStatus is the change hook delivery a runtime last
// reported (ADR-20261022121642): recorded changes still waiting, changes the
// platform rejected, and when the oldest waiting change was recorded.
type DataAPIChangeHookStatus struct {
	ReportedAt      time.Time  `json:"reported_at"`
	Pending         int64      `json:"pending"`
	Failed          int64      `json:"failed"`
	OldestPendingAt *time.Time `json:"oldest_pending_at,omitempty"`
	LastPublishedAt *time.Time `json:"last_published_at,omitempty"`
	LastFailureCode string     `json:"last_failure_code,omitempty"`
	Publishing      string     `json:"publishing"`
	// LastDroppedAt is when capture last left a change out at the backlog
	// bound (ADR-20261022121642 amendment 3): changes went unrecorded.
	LastDroppedAt *time.Time `json:"last_dropped_at,omitempty"`
}

// DataAPIChangeHookReport is a runtime's private change hook delivery report,
// read from its change event table.
type DataAPIChangeHookReport struct {
	Version         int        `json:"version"`
	Pending         int64      `json:"pending"`
	Failed          int64      `json:"failed"`
	OldestPendingAt *time.Time `json:"oldest_pending_at,omitempty"`
	LastPublishedAt *time.Time `json:"last_published_at,omitempty"`
	LastFailureCode string     `json:"last_failure_code,omitempty"`
	Publishing      string     `json:"publishing"`
	LastDroppedAt   *time.Time `json:"last_dropped_at,omitempty"`
}

var dataAPIChangeFailureCode = lazyregexp.New(`^[a-z0-9_]{1,64}$`)

// Validate checks a report's version, bounds and times against now.
func (r DataAPIChangeHookReport) Validate(now time.Time) error {
	if r.Version != 1 {
		return errors.New("version must be 1")
	}
	if r.Pending < 0 || r.Pending > DataAPIChangeHookReportCountMax || r.Failed < 0 || r.Failed > DataAPIChangeHookReportCountMax {
		return errors.New("pending and failed must be counts within bounds")
	}
	if r.Publishing != DataAPIChangeHooksPublishingOK && r.Publishing != DataAPIChangeHooksPublishingRetrying {
		return errors.New("publishing must be ok or retrying")
	}
	if r.LastFailureCode != "" && !dataAPIChangeFailureCode.MatchString(r.LastFailureCode) {
		return errors.New("last_failure_code must be a lowercase code of at most 64 bytes")
	}
	if (r.Pending == 0) != (r.OldestPendingAt == nil) {
		return errors.New("oldest_pending_at is set exactly when changes are pending")
	}
	for _, at := range []*time.Time{r.OldestPendingAt, r.LastPublishedAt, r.LastDroppedAt} {
		if at != nil && (at.After(now.Add(DataAPIChangeHookReportClockSkew)) || at.Year() < 2000) {
			return errors.New("report times must not be in the future")
		}
	}
	return nil
}

// GetDataAPIStatus reads an app's Data API status without waking it.
func (c *Client) GetDataAPIStatus(ctx context.Context, slug string) (DataAPIStatus, error) {
	var out DataAPIStatus
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/data-api/status", nil, &out)
	return out, err
}
