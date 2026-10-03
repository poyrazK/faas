//go:build metal

// adr: 459 — helper cgroup holdings must be visible to leak acceptance.

package leakcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionedJailsAndBuilderScopes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"firecracker", "firecracker-v1.7.0"} {
		parent := filepath.Join(root, name)
		if err := os.MkdirAll(parent, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if got := jailChrootsAt(root); len(got) != 0 {
		t.Fatalf("empty version parents flagged: %v", got)
	}
	for _, name := range []string{"firecracker/app-a/root", "firecracker-v1.7.0/build-b/root"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if got := jailChrootsAt(root); len(got) != 2 {
		t.Fatalf("missed versioned jail: %v", got)
	}
	for _, name := range []string{"faas.slice/faas-tenant.slice/tenant-pro/app-a", "faas.slice/faas-cp.slice/faas-cp-build.slice/build-b", "faas-tenant.slice/legacy-c", "faas.slice/faas-cp.slice/faas-vmmd.service/gregale-host-helpers/helper-a", "gregale-host-helpers/helper-b"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "cgroup.procs"), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	parent := filepath.Join(root, "faas.slice/faas-tenant.slice/tenant-pro/cgroup.procs")
	if err := os.WriteFile(parent, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := listVMScopes(root); len(got) != 5 {
		t.Fatalf("wrong VM scopes: %v", got)
	}
}

func TestTenantNetdevNames(t *testing.T) {
	for _, name := range []string{"vh0", "vh12@if3", "tap-old", "ve-old"} {
		if !isTenantNetdev(name) {
			t.Errorf("missed tenant device %q", name)
		}
	}
	for _, name := range []string{"eth0", "docker0", "vhost0", "vh", "br-tenants"} {
		if isTenantNetdev(name) {
			t.Errorf("flagged infrastructure device %q", name)
		}
	}
}

func TestNativeLoopTokensIncludeIncompleteAndUnlinkedAttachments(t *testing.T) {
	for _, name := range []string{"gregale-loop:bd563861-501b-4ac2-9b4a-e2b782b3c0ad\x00\x00", "gregale-loop:", "gregale-loop:corrupt"} {
		if !nativeLoopTokenPresent([]byte(name)) {
			t.Errorf("missed managed loop attachment %q", name)
		}
	}
	for _, name := range []string{"/srv/fc/images/base.ext4", "/var/lib/snapd/snaps/system.snap\x00", "unrelated-loop"} {
		if nativeLoopTokenPresent([]byte(name)) {
			t.Errorf("flagged unrelated attachment %q", name)
		}
	}
}

func TestNativeImageAnchorMountsIncludeNonDefaultJailRoots(t *testing.T) {
	data := "123 1 8:1 /source /tmp/fixture/.native-processes/image-sources/points/epoch rw - ext4 /dev/sda1 rw\n"
	if got := nativeImageMountsFrom([]byte(data)); len(got) != 1 {
		t.Fatalf("missed private anchor: %v", got)
	}
	data = "123 1 8:1 /source /srv/fc/images rw - ext4 /dev/sda1 rw\n"
	if got := nativeImageMountsFrom([]byte(data)); len(got) != 0 {
		t.Fatalf("flagged unrelated mount: %v", got)
	}
	if got := nativeImageMountsFrom([]byte("incomplete")); len(got) == 0 {
		t.Fatal("unreadable mount proof passed leak inspection")
	}
}

func TestNativeTunMountsIncludeNonDefaultAndDeletedTargets(t *testing.T) {
	for _, point := range []string{"/tmp/fixture/firecracker/instance/root/faas-host-tun", "/tmp/fixture/firecracker/instance/root/faas-host-tun\\040(deleted)", "/tmp/fixture/firecracker/instance/root/faas-host-tun/nested"} {
		data := "125 1 0:7 /net/tun " + point + " rw,nosuid,noexec - devtmpfs devtmpfs rw\n"
		if got := nativeImageMountsFrom([]byte(data)); len(got) != 1 {
			t.Fatalf("missed managed device mount: %s %v", point, got)
		}
	}
}
