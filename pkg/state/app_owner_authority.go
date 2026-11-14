// adr: 20261110210000
package state

import (
	"context"
	"fmt"
	"time"
)

// AppOwnerAuthorityStatus is what the control plane reports about
// apps.node_id writes (ADR-20261110210000 §4): the trigger's mode and how
// many owner changes in the window came from a login outside
// faas_owner_authority. Under enforce those are refused and leave no row, so
// the count is observe mode's evidence; enforce refusals surface as SQLSTATE
// 42501 to the caller and a faas_app_owner_refused Postgres log line.
type AppOwnerAuthorityStatus struct {
	Mode         string
	Unauthorized int64
}

// AppOwnerAuthorityReader is implemented by stores that carry the
// owner-authority tables (Postgres).
type AppOwnerAuthorityReader interface {
	AppOwnerAuthorityStatus(ctx context.Context, window time.Duration) (AppOwnerAuthorityStatus, error)
}

// AppOwnerAuthorityStatus reads the mode and the unauthorized owner writes
// recorded within window.
func (s *PgStore) AppOwnerAuthorityStatus(ctx context.Context, window time.Duration) (AppOwnerAuthorityStatus, error) {
	var out AppOwnerAuthorityStatus
	err := s.pool.QueryRow(ctx, `
		select coalesce((select mode from app_owner_authority where singleton), 'any'),
		       (select count(*) from app_owner_writes
		         where not authorized and written_at > now() - make_interval(secs => $1))`,
		window.Seconds()).Scan(&out.Mode, &out.Unauthorized)
	if err != nil {
		return AppOwnerAuthorityStatus{}, fmt.Errorf("state: app owner authority status: %w", err)
	}
	return out, nil
}
