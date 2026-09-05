//go:build metal

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
	for _, name := range []string{"faas.slice/faas-tenant.slice/tenant-pro/app-a", "faas.slice/faas-cp.slice/faas-cp-build.slice/build-b", "faas-tenant.slice/legacy-c"} {
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
	if got := listVMScopes(root); len(got) != 3 {
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
