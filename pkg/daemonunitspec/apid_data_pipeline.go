package daemonunitspec

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// Pipeline workers are opt-in, private instances of the apid binary.
func pipelineWorkerUnit() daemonunit.Unit {
	return daemonunit.Unit{
		Documentation:         "https://gregale.dev/docs/ops/apid",
		After:                 []string{"network.target", "postgresql.service", "faas-cp.slice"},
		Wants:                 []string{"faas-cp.slice", "postgresql.service"},
		StartLimitIntervalSec: "60s", StartLimitBurst: "5",
		Type: "simple", User: "faas-apid", Group: "faas",
		Restart: "on-failure", RestartSec: "2s",
		Slice: "faas-cp.slice", MemoryHigh: "768M",
		MemoryMax: fmt.Sprint(api.PostgresCopyWorkerMemoryMaxBytes), CPUQuota: fmt.Sprintf("%d%%", api.PostgresCopyWorkerCPUMillicoresMax/10), TasksMax: fmt.Sprint(api.PostgresCopyWorkerTasksMax),
		CapabilityBoundingSet: []string{}, AmbientCapabilities: []string{""},
		EnvironmentFile: "-/etc/faas/storage.env -/etc/faas/otel.env",
		NoNewPrivileges: true, ProtectSystem: "strict", ProtectHome: true, PrivateTmp: daemonunit.BoolPtr(true), PrivateDevices: true,
		ProtectKernelTunables: true, ProtectKernelModules: true, ProtectControlGroups: true,
		SystemCallArchitectures: "native", LockPersonality: true, RestrictNamespaces: true, RestrictRealtime: true, RestrictSUIDSGID: true,
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET", "AF_INET6"}, ProtectHostname: true, ProtectClock: true, ProtectProc: "invisible",
		ReadOnlyPaths: []string{"/etc/faas"}, WantedBy: "multi-user.target",
	}
}

func UnitApidDataPipelineWorker() daemonunit.Unit {
	u := pipelineWorkerUnit()
	u.Description = "Gregale private data pipeline worker (ADR-1591)"
	u.After = append(u.After, "faas-apid.service")
	u.ExecStart = "/opt/faas/current/bin/apid --data-pipeline-worker --config /etc/faas/apid.toml"
	u.Environment = []daemonunit.KV{
		{Key: "FAAS_APID_ROLE", Value: "control-plane"},
		{Key: "FAAS_FLEET_AGE_IDENTITY_PATH", Value: "%d/faas_fleet_age_identity"},
		{Key: "FAAS_FLEET_AGE_RECIPIENT_PATH", Value: "%d/faas_fleet_age_recipient"},
		{Key: "FAAS_HOST_HMAC_KEY_PATH", Value: "%d/faas_host_hmac_key"},
		{Key: "GOMAXPROCS", Value: "1"}, {Key: "GOMEMLIMIT", Value: "768MiB"},
	}
	u.LoadCredential = []daemonunit.LoadCred{
		{Name: "faas_fleet_age_identity", Path: "/etc/faas/secrets/fleet.age"},
		{Name: "faas_fleet_age_recipient", Path: "/etc/faas/secrets/fleet.age.pub"},
		{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
		{Name: "faas_host_hmac_key", Path: "/etc/faas/secrets/host.hmac.key"},
	}
	u.ReadWritePaths = []string{"/var/spool/faas/builds", "/var/lib/faas/cache"}
	return u
}

func UnitApidDataPipelineAlertWorker() daemonunit.Unit {
	u := pipelineWorkerUnit()
	u.Description = "Gregale private pipeline health notification worker (ADR-1591)"
	u.ExecStart = "/opt/faas/current/bin/apid --data-pipeline-alert-worker --config /etc/faas/apid.toml"
	u.Environment = []daemonunit.KV{{Key: "FAAS_APID_ROLE", Value: "control-plane"}, {Key: "GOMAXPROCS", Value: "1"}, {Key: "GOMEMLIMIT", Value: "768MiB"}}
	return u
}
