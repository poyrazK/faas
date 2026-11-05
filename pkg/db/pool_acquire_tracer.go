// pool_acquire_tracer.go — counts pool acquisitions a caller abandoned while
// waiting, which is what <daemon>_db_pool_canceled_acquires_total and the
// FaasDBPoolStarved page are specified to measure (spec §12).
//
// pgxpool's own Stat().CanceledAcquireCount() is broader: puddle counts an
// acquisition whose context had already ended before Acquire was called,
// with idle connections available and nothing waited for. A client that
// disconnects just before its handler queries is enough. Production-us
// paged FaasDBPoolStarved for schedd on fsn-3 with 9-10 idle connections,
// no empty acquires and no acquire wait time (hunt #10, H10-108).
package db

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/pgpoolbounds"
)

// Keep daemon pool metrics on the same lightweight tracer the guest uses.
type acquireAbandonTracer = pgpoolbounds.Tracer

// abandonedAcquires reports the tracer's count for pool, or false when the
// pool was built without it.
func abandonedAcquires(pool *pgxpool.Pool) (int64, bool) {
	t, ok := pool.Config().ConnConfig.Tracer.(*acquireAbandonTracer)
	if !ok {
		return 0, false
	}
	return t.AbandonedAcquires(), true
}
