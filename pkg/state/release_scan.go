package state

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ReleaseScanCursor resumes a bounded release worker after a timestamp/ID
// pair. Its timestamp is ReleaseAt for schedules and RolloutCompletedAt for
// the post-release watch. The zero cursor starts a new pass.
type ReleaseScanCursor struct {
	At time.Time
	ID string
}

func (c ReleaseScanCursor) parameters() (pgtype.Timestamptz, pgtype.UUID, error) {
	var id pgtype.UUID
	if c.At.IsZero() && c.ID == "" {
		return pgtype.Timestamptz{}, id, nil
	}
	if c.At.IsZero() || c.ID == "" {
		return pgtype.Timestamptz{}, id, ErrInvalidArgument
	}
	if err := id.Scan(c.ID); err != nil || !id.Valid {
		return pgtype.Timestamptz{}, id, ErrInvalidArgument
	}
	return pgTimestamptzUTC(c.At), id, nil
}

func (c ReleaseScanCursor) includes(at time.Time, id string) bool {
	return c.At.IsZero() || at.After(c.At) || at.Equal(c.At) && id > c.ID
}
