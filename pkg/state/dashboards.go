package state

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// Dashboard is one account's saved dashboard (ADR-20261022173833). Widgets
// is the validated JSON array of widget references; apid owns its shape.
type Dashboard struct {
	ID        string
	AccountID string
	Name      string
	Widgets   json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DashboardStore keeps an account's dashboards. apid is the only writer.
type DashboardStore interface {
	// CreateDashboard returns *DashboardQuotaError past limit and
	// ErrConflict when the account already has the name.
	CreateDashboard(ctx context.Context, d Dashboard, limit int) (Dashboard, error)
	// ReplaceDashboard replaces name and widgets; ErrNotFound when the
	// account has no such dashboard, ErrConflict when the name is taken.
	ReplaceDashboard(ctx context.Context, d Dashboard) (Dashboard, error)
	// ListDashboards returns the account's dashboards by name.
	ListDashboards(ctx context.Context, accountID string) ([]Dashboard, error)
	// DashboardByID returns ErrNotFound for another account's dashboard.
	DashboardByID(ctx context.Context, accountID, id string) (Dashboard, error)
	DeleteDashboard(ctx context.Context, accountID, id string) error
}

// DashboardQuotaError reports the per-account dashboard cap.
type DashboardQuotaError struct {
	Limit    int
	Observed int
}

func (e *DashboardQuotaError) Error() string {
	return fmt.Sprintf("state: account has %d of %d dashboards", e.Observed, e.Limit)
}

// dashboardName is dashboards_name_shape.
var dashboardName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidDashboardName reports whether name fits the stored name shape.
func ValidDashboardName(name string) bool { return dashboardName.MatchString(name) }

const dashboardWidgetsMaxBytes = 32 * 1024

func validateDashboard(d Dashboard) error {
	if d.AccountID == "" {
		return fmt.Errorf("state: dashboard requires an account")
	}
	if !ValidDashboardName(d.Name) {
		return fmt.Errorf("state: dashboard name %q must match %s", d.Name, dashboardName)
	}
	var widgets []json.RawMessage
	if err := json.Unmarshal(d.Widgets, &widgets); err != nil {
		return fmt.Errorf("state: dashboard widgets must be a JSON array: %w", err)
	}
	if len(d.Widgets) > dashboardWidgetsMaxBytes {
		return fmt.Errorf("state: dashboard widgets exceed %d bytes", dashboardWidgetsMaxBytes)
	}
	return nil
}
