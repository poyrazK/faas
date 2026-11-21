// adr: 20261109120000 — an encrypted host boots to multi-user with its firewall loaded.
package daemonunitspec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The encrypted /srv/fc unlocks through Tang only after early boot. A fstab
// mount beneath it without nofail is required by local-fs.target, waits for
// the unlock, times out after 90 s and drops the host into emergency mode
// (bhs-3, 2026-11-19: the compute cache and Grype binds).
func TestMountsBeneathTheDataVolumeAreNofailWhenEncrypted(t *testing.T) {
	seen := 0
	for role, files := range roleTaskFiles(t) {
		for _, f := range files {
			for _, task := range flattenRoleTasks(f, loadYAMLList(t, f)) {
				mount, ok := task.body["ansible.posix.mount"].(map[string]any)
				if !ok {
					continue
				}
				path, _ := mount["path"].(string)
				src, _ := mount["src"].(string)
				if path == "{{ faas_storage_mount }}" || !(beneathDataVolume(path) || beneathDataVolume(src)) {
					continue
				}
				seen++
				opts, _ := mount["opts"].(string)
				// state=mounted checks os.path.ismount, which is false for a bind
				// whose parent is a bind of the same filesystem (/var/lib/faas on
				// an encrypted host): the module stacked a bind per converge.
				if strings.Contains(opts, "bind") && mount["state"] == "mounted" {
					t.Errorf("%s %s: %q is a bind beneath /srv/fc with state: mounted; write it as present and mount it only when findmnt finds nothing",
						role, filepath.Base(f), task.body["name"])
				}
				if !strings.Contains(opts, "nofail") {
					t.Errorf("%s %s: %q mounts beneath /srv/fc without nofail (opts %q); on an encrypted host boot waits for Tang and drops to emergency mode",
						role, filepath.Base(f), task.body["name"], opts)
				}
			}
		}
	}
	if seen < 4 {
		t.Fatalf("found %d mounts beneath /srv/fc, want at least 4 (cache, Grype, jail tmpfs, encrypted paths); did the roles move?", seen)
	}
}

func beneathDataVolume(s string) bool {
	return strings.HasPrefix(s, "/srv/fc/") || strings.HasPrefix(s, "{{ faas_storage_mount }}/") ||
		s == "{{ faas_storage_cache_mount }}" || s == "{{ faas_storage_grype_mount }}"
}

// A nofail mount is no longer ordered before local-fs.target, so the faas
// units must require it themselves.
func TestFaasUnitsRequireTheNofailMounts(t *testing.T) {
	for _, tc := range []struct{ file, dropin, mounts string }{
		{"deploy/ansible/roles/storage/tasks/main.yml", "12-faas-storage-binds.conf", "RequiresMountsFor={{ faas_storage_cache_mount }} {{ faas_storage_grype_mount }}"},
		{"deploy/ansible/roles/xfs/tasks/main.yml", "13-faas-jail-tmpfs.conf", "RequiresMountsFor=/srv/fc/jail"},
	} {
		body := readRepoFile(t, tc.file)
		if !strings.Contains(body, "/etc/systemd/system/faas-.service.d/"+tc.dropin) || !strings.Contains(body, tc.mounts) {
			t.Errorf("%s must install faas-.service.d/%s with %s", tc.file, tc.dropin, tc.mounts)
		}
	}
}

// crypttab marks the volume _netdev, so systemd-cryptsetup-generator hangs
// the unlock on remote-cryptsetup.target alone. OVH's Ubuntu 24.04 image
// leaves that target disabled: nothing asked for the unlock at boot.
func TestEncryptedVolumeUnlockIsQueuedAtBoot(t *testing.T) {
	var enabled bool
	f := filepath.Join(repoRoot(t), "deploy/ansible/roles/storage/tasks/main.yml")
	for _, task := range flattenRoleTasks(f, loadYAMLList(t, f)) {
		svc, ok := task.body["ansible.builtin.systemd_service"].(map[string]any)
		if ok && svc["name"] == "remote-cryptsetup.target" && svc["enabled"] == true {
			enabled = true
		}
	}
	if !enabled {
		t.Fatal("the storage role must enable remote-cryptsetup.target on an encrypted host")
	}
	if !strings.Contains(readRepoFile(t, "deploy/ansible/roles/storage/tasks/encrypted.yml"), "none luks,_netdev") {
		t.Fatal("crypttab is no longer _netdev; recheck which target queues the unlock")
	}
}

// br-tenants-up is ordered before nftables, which Debian orders before
// network-pre.target. With default dependencies it is also after
// basic.target, which on a cloud-init host waits on the network: systemd
// breaks that cycle by dropping the nftables start job (bhs-3, 2026-11-19).
func TestTenantBridgeUnitCannotCycleWithTheFirewall(t *testing.T) {
	unit := readRepoFile(t, "deploy/ansible/roles/nftables/files/br-tenants-up.service")
	var defaultDeps, before, after string
	for _, line := range strings.Split(unit, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "DefaultDependencies":
			defaultDeps = v
		case "Before":
			before += " " + v
		case "After":
			after += " " + v
		}
	}
	if defaultDeps != "no" {
		t.Error("br-tenants-up.service must set DefaultDependencies=no; the implicit After=basic.target cycles with nftables")
	}
	if !strings.Contains(before, "nftables.service") {
		t.Error("br-tenants-up.service must stay ordered before nftables.service")
	}
	for _, bad := range []string{"basic.target", "sysinit.target", "network", "faas-cp.slice"} {
		if strings.Contains(after, bad) {
			t.Errorf("br-tenants-up.service must not be ordered after %s", bad)
		}
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// On an encrypted host faas_storage_device is the LUKS container: blkid
// reads it as crypto_LUKS and its UUID is the LUKS header's. The xfs role
// must leave it to the storage role (which unlocks it and writes crypttab
// and fstab for /dev/mapper/<label>-crypt) and only check the mounted
// filesystem; otherwise every bootstrap and join of an encrypted host stops
// at "already formatted with an unsupported filesystem" (2026-11-20).
func TestXFSRoleLeavesAnEncryptedDeviceToTheStorageRole(t *testing.T) {
	f := filepath.Join(repoRoot(t), "deploy/ansible/roles/xfs/tasks/main.yml")
	tasks := flattenRoleTasks(f, loadYAMLList(t, f))
	probe, decide, gated := -1, -1, 0
	for i, task := range tasks {
		name, _ := task.body["name"].(string)
		if fact, ok := task.body["ansible.builtin.set_fact"].(map[string]any); ok {
			if _, ok := fact["xfs_fc_encrypted"]; ok && probe < 0 {
				probe = i
			}
			if v, ok := fact["xfs_device_managed"].(string); ok {
				decide = i
				if !strings.Contains(v, "xfs_fc_encrypted") {
					t.Errorf("xfs_device_managed must depend on xfs_fc_encrypted, got %q", v)
				}
			}
			continue
		}
		if !mentionsStorageDevice(task.body) {
			continue
		}
		gated++
		if decide < 0 || i < decide {
			t.Errorf("%q uses faas_storage_device before the role decides whether it owns the device", name)
		}
		if !strings.Contains(whenText(task.body["when"]), "xfs_device_managed") {
			t.Errorf("%q uses faas_storage_device without `when: xfs_device_managed`; on an encrypted host that is the LUKS container", name)
		}
	}
	if probe < 0 || decide < 0 || probe > decide {
		t.Fatalf("the encryption probe (task %d) must set xfs_fc_encrypted before xfs_device_managed (task %d)", probe, decide)
	}
	if gated < 8 {
		t.Fatalf("found %d tasks using faas_storage_device, want at least 8; did the role move?", gated)
	}
}

func mentionsStorageDevice(body map[string]any) bool {
	for k, v := range body {
		if k == "name" || k == "when" {
			continue
		}
		if strings.Contains(fmt.Sprint(v), "faas_storage_device") {
			return true
		}
	}
	return false
}

func whenText(v any) string {
	if list, ok := v.([]any); ok {
		parts := make([]string, 0, len(list))
		for _, p := range list {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, " && ")
	}
	return fmt.Sprint(v)
}
