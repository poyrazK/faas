package daemonunitspec

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// UnitApidCloneWorker runs the same customer-intent owner in a dedicated
// cgroup. OptionalRegistry keeps this private, unqualified data path out of
// automatic activation. It opens no API, advisory, bridge or metrics socket.
func UnitApidCloneWorker() daemonunit.Unit {
	u := UnitApid()
	u.Description = "Gregale private project-environment clone worker (ADR-375)"
	u.After = []string{"network.target", "postgresql.service", "faas-cp.slice", "faas-apid.service"}
	u.Requires = []string{"postgresql.service"}
	u.ExecStart = `/opt/faas/current/bin/apid --clone-worker --config /etc/faas/apid.toml`
	u.MemoryHigh = "768M"
	u.MemoryMax = fmt.Sprintf("%d", api.PostgresCopyWorkerMemoryMaxBytes)
	u.CPUQuota = fmt.Sprintf("%d%%", api.PostgresCopyWorkerCPUMillicoresMax/10)
	u.TasksMax = fmt.Sprint(api.PostgresCopyWorkerTasksMax)
	u.Environment = []daemonunit.KV{
		{Key: "FAAS_APID_ROLE", Value: "control-plane"},
		{Key: "FAAS_FLEET_AGE_IDENTITY_PATH", Value: "%d/faas_fleet_age_identity"},
		{Key: "FAAS_FLEET_AGE_RECIPIENT_PATH", Value: "%d/faas_fleet_age_recipient"},
		{Key: "FAAS_HOST_HMAC_KEY_PATH", Value: "%d/faas_host_hmac_key"},
		{Key: "FAAS_CLONE_WORKER_SPOOL_DIR", Value: "/var/spool/faas/apid-clone-worker"},
		{Key: "GOMAXPROCS", Value: "1"},
		{Key: "GOMEMLIMIT", Value: "768MiB"},
	}
	u.LoadCredential = []daemonunit.LoadCred{
		{Name: "faas_fleet_age_identity", Path: "/etc/faas/secrets/fleet.age"},
		{Name: "faas_fleet_age_recipient", Path: "/etc/faas/secrets/fleet.age.pub"},
		{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
		{Name: "faas_host_age_identity_previous", Path: "/etc/faas/secrets/host.age.previous", Optional: true},
		{Name: "faas_host_hmac_key", Path: "/etc/faas/secrets/host.hmac.key"},
	}
	u.ReadWritePaths = []string{"/var/spool/faas/apid-clone-worker"}
	return u
}
