package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/dbmaintenance"
)

type ManagedPostgresMaintenanceWindow struct {
	Weekday         int `json:"weekday"`
	StartMinute     int `json:"start_minute"`
	DurationMinutes int `json:"duration_minutes"`
}

func (w ManagedPostgresMaintenanceWindow) Valid() bool {
	return dbmaintenance.Window(w).Valid()
}

// SetManagedPostgresMaintenanceRequest replaces the weekly UTC window. A
// null window disables the schedule.
type SetManagedPostgresMaintenanceRequest struct {
	Window *ManagedPostgresMaintenanceWindow `json:"window"`
}

type ManagedPostgresMaintenanceStatus struct {
	Window      *ManagedPostgresMaintenanceWindow `json:"window"`
	WindowStart *time.Time                        `json:"window_start,omitempty"`
	WindowEnd   *time.Time                        `json:"window_end,omitempty"`
	WindowOpen  bool                              `json:"window_open"`
	StaleSince  *time.Time                        `json:"stale_since,omitempty"`
	LastRefresh *ManagedPostgresBaseRefresh       `json:"last_refresh,omitempty"`
}

type ManagedPostgresBaseRefresh struct {
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Outcome    string     `json:"outcome"`
	Phase      string     `json:"phase,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

func (c *Client) SetManagedPostgresMaintenance(ctx context.Context, id string, req SetManagedPostgresMaintenanceRequest) (ManagedPostgresMaintenanceStatus, error) {
	var result ManagedPostgresMaintenanceStatus
	err := c.do(ctx, http.MethodPut, "/v1/postgres/databases/"+url.PathEscape(id)+"/maintenance-window", req, &result)
	return result, err
}
