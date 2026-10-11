package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitProfiled keeps profile parsing and backend I/O outside root vmmd.
func UnitProfiled() daemonunit.Unit {
	u := UnitRealtimed()
	u.Description = "onebox-faas profiled — continuous CPU profile collector"
	u.Documentation = "https://gregale.dev/docs/profiling"
	u.ExecStart = "/opt/faas/current/bin/profiled"
	u.EnvironmentFile = "-/etc/faas/profiling.env -/etc/faas/otel.env"
	u.Environment = []daemonunit.KV{{Key: "FAAS_PROFILE_SOCKET", Value: "/run/faas/profiled.sock"}}
	u.ReadWritePaths = []string{"/run/faas"}
	return u
}
