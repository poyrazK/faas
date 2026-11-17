// adr: 20261109120000 — data volumes encrypted at rest, unlocked through the other hosts' Tang servers.
package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostFirewallAdmitsTangFromTheFleetOnly(t *testing.T) {
	off := renderHostFirewall(t, map[string]any{"faas_private_iface": "eno2"})
	if strings.Contains(off, "Tang") {
		t.Errorf("Tang admitted without disk encryption:\n%s", off)
	}
	on := renderHostFirewall(t, map[string]any{
		"faas_private_iface": "eno2", "faas_disk_encryption": true,
		"faas_tang_port": 7500, "faas_tang_allowed_cidrs": []string{"10.20.0.0/24"},
	})
	want := `iifname "eno2" ip saddr 10.20.0.0/24 tcp dport 7500 accept comment "faas Tang key server from the fleet (ADR-20261109120000)"`
	if !strings.Contains(on, want) {
		t.Errorf("Tang rule missing or not bound to the private card; want %q in:\n%s", want, on)
	}
	for _, line := range strings.Split(on, "\n") {
		if strings.Contains(line, "7500") && !strings.Contains(line, want) {
			t.Errorf("Tang port admitted outside the fleet rule: %q", line)
		}
	}
}

func readRepoText(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The storage role encrypts only a volume it is creating, keeps the volume
// key off disk and out of logs, seals the recovery key to the operator, and
// makes every faas unit require the mount so a locked volume fails closed.
func TestStorageEncryptsNewVolumesAndFailsClosed(t *testing.T) {
	main := readRepoText(t, "deploy/ansible/roles/storage/tasks/main.yml")
	include := strings.Index(main, "include_tasks: encrypted.yml")
	mkfs := strings.Index(main, "- mkfs.xfs")
	if include < 0 || mkfs < 0 || include > mkfs {
		t.Fatal("storage must encrypt the volume before mkfs.xfs puts a filesystem on it")
	}
	gate := main[include:mkfs]
	for _, want := range []string{"faas_disk_encryption | default(false) | bool", "not (faas_storage_existing_valid | bool)"} {
		if !strings.Contains(gate, want) {
			t.Errorf("encrypted.yml include must be gated on %q", want)
		}
	}
	if !strings.Contains(main, "refuse while the encrypted volume is locked") {
		t.Error("a locked encrypted volume must stop the role before the device checks")
	}

	enc := readRepoText(t, "deploy/ansible/roles/storage/tasks/encrypted.yml")
	for _, want := range []string{
		"cryptsetup luksFormat --type luks2",
		`clevis luks bind -y -d "$device" -k "$work/key" sss`,
		`"t": 1`,
		"/etc/faas/disk-recovery/$label.key.age",
		"mktemp -d /run/faas-luks.",
		"shred -u",
		"no_log: true",
		"none luks,_netdev",
		"clevis-luks-askpass.path",
		"RequiresMountsFor={{ faas_storage_mount }}",
		"/etc/systemd/system/faas-.service.d/10-faas-encrypted-data.conf",
		"_netdev,nofail",
	} {
		if !strings.Contains(enc, want) {
			t.Errorf("encrypted.yml lost %q", want)
		}
	}
	// The host's own Tang server never unlocks its volume; only the
	// manifest's peer list (other hosts) is bound.
	if strings.Contains(enc, "faas_tang_listen_address") || strings.Contains(enc, "127.0.0.1") || strings.Contains(enc, "localhost") {
		t.Error("encrypted.yml must bind only the other hosts' Tang servers")
	}
}

func TestTangServerListensOnThePrivateAddressOnly(t *testing.T) {
	tasks := readRepoText(t, "deploy/ansible/roles/tang_server/tasks/main.yml")
	for _, want := range []string{
		"ListenStream=\n",
		"ListenStream={{ faas_tang_listen_hostport }}:{{ faas_tang_port }}",
		"is_private",
	} {
		if !strings.Contains(tasks, want) {
			t.Errorf("tang_server lost %q", want)
		}
	}
	boot := readRepoText(t, "deploy/ansible/bootstrap.yml")
	tang := strings.Index(boot, "role: tang_server")
	storage := strings.Index(boot, "role: storage")
	if tang < 0 || storage < 0 || tang > storage {
		t.Error("bootstrap.yml must bring up Tang on every host before any storage role runs")
	}
}
