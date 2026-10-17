//go:build !linux || !amd64 || !metal || !promotionacceptance

package promotionfault

import "testing"

func TestProductionBuildCannotActivateBarrier(t *testing.T) {
	t.Setenv(SocketEnv, "/nonexistent/publication.sock")
	if err := Hit(t.Context(), "project", "promotion", CutoverBeforeCommit, "release"); err != nil {
		t.Fatal("production build activated native fault injection", err)
	}
}
