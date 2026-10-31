package managedpostgres

import (
	"context"
	"fmt"
)

// usageSweepLockKey names the session advisory lock that keeps the usage
// collection and cap sweep to one apid at a time.
const usageSweepLockKey = "managed-postgres-usage-sweep"

// TryAcquireUsageSweep takes the sweep's session advisory lock on a dedicated
// connection, or reports that another process holds it. Release unlocks and
// returns the connection; a crashed holder's lock ends with its session.
func (s *PostgresStore) TryAcquireUsageSweep(ctx context.Context) (func(), bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("managed postgres: acquire usage sweep connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, usageSweepLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("managed postgres: take usage sweep lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, usageSweepLockKey); err != nil {
			// The lock outlives a failed unlock only as long as the session:
			// destroy the connection rather than return it to the pool locked.
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
		}
		conn.Release()
	}, true, nil
}
