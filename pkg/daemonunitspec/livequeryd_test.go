package daemonunitspec

import (
	"slices"
	"testing"
)

// adr: 20261016151947 — livequeryd is an operator-configured add-on: the
// generated unit names no database and carries no secret. The
// livequeryd_service role's drop-in supplies both, as flags and as systemd
// credentials, and the hub writes only its checkpoint directory.
func TestLivequerydUnit(t *testing.T) {
	u := UnitLivequeryd()
	if u.User != "faas" || u.Group != "faas" || u.Slice != FaasCPSlice {
		t.Errorf("livequeryd runs as %s:%s in %s, want faas:faas in %s", u.User, u.Group, u.Slice, FaasCPSlice)
	}
	if u.ExecStart != "/opt/faas/current/bin/livequeryd" {
		t.Errorf("ExecStart = %q: per-database flags belong in the role's drop-in", u.ExecStart)
	}
	if len(u.Environment) != 0 || u.EnvironmentFile != "" || len(u.LoadCredential) != 0 {
		t.Error("the generated unit must not name a database or a secret; the role's drop-in does")
	}
	if !slices.Equal(u.ReadWritePaths, []string{"/var/lib/faas/livequeryd"}) {
		t.Errorf("ReadWritePaths = %v, want only the checkpoint directory", u.ReadWritePaths)
	}
	for _, entry := range Registry {
		if entry.Name == "livequeryd" {
			t.Fatal("livequeryd must stay out of the always-on registry")
		}
	}
}
