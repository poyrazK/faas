// adr: 590
package pgtest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// LockCluster protects fixtures that use cluster-wide role or database names.
// All packages sharing a resource must use the same name and keep conn open
// until cleanup. Register resource cleanup after this call so it runs before
// the lock is released. A lost connection also releases the PostgreSQL lock.
func LockCluster(t *testing.T, conn *pgx.Conn, resource string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", resource); err != nil {
		t.Fatalf("pgtest: lock cluster fixture: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var unlocked bool
		err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", resource).Scan(&unlocked)
		if err != nil || !unlocked {
			t.Errorf("pgtest: release cluster fixture lock: unlocked=%t err=%v", unlocked, err)
		}
	})
}
