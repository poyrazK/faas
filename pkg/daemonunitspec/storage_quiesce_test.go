// adr: 20261109120000 — moving daemon state onto the encrypted volume waits only for running daemons.
package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// host_hardening runs just before storage and leaves faas-kernel-lockdown
// (a oneshot with RemainAfterExit) "active (exited)". Counting active units
// refused every first converge of an encrypted host; only running services
// can write to the state being moved.
func TestStorageQuiesceCountsOnlyRunningServices(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/ansible/roles/storage/tasks/encrypted_paths.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "systemctl list-units --type=service --state=running") {
		t.Fatal("the quiesce check must list running faas services, not active ones")
	}
	if strings.Contains(text, "--state=active") {
		t.Fatal("--state=active counts finished oneshots such as faas-kernel-lockdown.service")
	}
	lockdown, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/ansible/roles/host_hardening/templates/faas-kernel-lockdown.service.j2"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lockdown), "Type=oneshot") {
		t.Fatal("faas-kernel-lockdown changed type; recheck the storage quiesce check")
	}
}
