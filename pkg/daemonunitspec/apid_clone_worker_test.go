// adr:567
package daemonunitspec

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunit"
)

func TestPrivateCloneWorkerResourceAndCredentialBoundary(t *testing.T) {
	u := UnitApidCloneWorker()
	if u.User != UnitApid().User || u.Slice != FaasCPSlice || u.MemoryMax != fmt.Sprint(api.PostgresCopyWorkerMemoryMaxBytes) ||
		u.CPUQuota != "100%" || u.TasksMax != fmt.Sprint(api.PostgresCopyWorkerTasksMax) || u.Delegate {
		t.Fatal("clone worker lost bounded APID ownership")
	}
	if u.ExecStart != "/opt/faas/current/bin/apid --clone-worker --config /etc/faas/apid.toml" {
		t.Fatal("worker does not run APID's headless entry point")
	}
	if len(u.ReadWritePaths) != 1 || u.ReadWritePaths[0] != "/var/spool/faas/apid-clone-worker" {
		t.Fatal("worker widened spool writes")
	}
	for _, name := range []string{"FAAS_SESSION_KEY", "FAAS_APID_ADVISORY_SOCK", "FAAS_APID_METRICS_ADDR", "FAAS_LOG_ARCHIVE_CREDS_PATH"} {
		for _, e := range u.Environment {
			if e.Key == name {
				t.Fatalf("worker receives unused API credential/listener %s", name)
			}
		}
	}
	for _, c := range u.LoadCredential {
		if c.Name == "faas_session_key" || c.Name == "faas_archive_creds" {
			t.Fatal("worker receives unrelated credential")
		}
	}
	if !hasOptionalLoadCredential(u, "faas_host_age_identity_previous", "/etc/faas/secrets/host.age.previous") {
		t.Fatal("lost host rotation overlap")
	}
	decoded, err := daemonunit.Decode(u.Render())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.CPUQuota != u.CPUQuota || decoded.TasksMax != u.TasksMax {
		t.Fatal("resource caps did not round trip")
	}
	decoded.CPUQuota, decoded.TasksMax = "", ""
	if diff := daemonunit.Diff(u, decoded); len(diff) != 2 {
		t.Fatalf("resource cap drift not detected: %v", diff)
	}
}
