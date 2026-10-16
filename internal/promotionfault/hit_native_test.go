//go:build linux && amd64 && metal && promotionacceptance

package promotionfault

import "testing"

func TestNativeBarrierRejectsProductionCatalog(t *testing.T) {
	t.Setenv(SocketEnv, "/nonexistent/publication.sock")
	t.Setenv("FAAS_PROMOTION_ROLLOUT_QUALIFICATION", "1")
	t.Setenv("DATABASE_URL", "postgres:///production?host=/run/postgresql")
	if err := Hit(t.Context(), "project", "promotion", CutoverBeforeCommit, "release"); err == nil {
		t.Fatal("native barrier accepted a production catalog")
	}
}
