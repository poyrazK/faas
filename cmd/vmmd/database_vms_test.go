// adr: 1027
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestDatabaseVMConfigDefaultsAndValidation(t *testing.T) {
	leases := fcvm.NewAllocator()
	rcfg, images, err := DatabaseVMConfig{KernelPath: "/k", Bases: map[string]string{"17": "/b17"}}.runtimeConfig(leases)
	if err != nil {
		t.Fatal(err)
	}
	if rcfg.JailBase != defaultDatabaseJailBase || rcfg.VolumeRoot != defaultDatabaseVolumeRoot || rcfg.ParentCgroup != defaultDatabaseParentCgroup || rcfg.Bases[17] != "/b17" || len(images) != 0 {
		t.Fatalf("runtime config = %+v", rcfg)
	}
	for name, cfg := range map[string]DatabaseVMConfig{
		"no kernel":   {Bases: map[string]string{"17": "/b"}},
		"no bases":    {KernelPath: "/k"},
		"bad major":   {KernelPath: "/k", Bases: map[string]string{"12": "/b"}},
		"non-numeric": {KernelPath: "/k", Bases: map[string]string{"latest": "/b"}},
		"empty path":  {KernelPath: "/k", Bases: map[string]string{"17": ""}},
		"tag image":   {KernelPath: "/k", Images: map[string]string{"17": "docker.io/library/postgres:17"}},
		"base+image":  {KernelPath: "/k", Bases: map[string]string{"17": "/b"}, Images: map[string]string{"17": testPinnedImage}},
	} {
		if _, _, err := cfg.runtimeConfig(leases); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

const testPinnedImage = "docker.io/library/postgres@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df"

func TestDatabaseVMConfigImagesAreBuiltPerMajor(t *testing.T) {
	_, images, err := DatabaseVMConfig{KernelPath: "/k", Images: map[string]string{"17": testPinnedImage, "16": testPinnedImage}}.runtimeConfig(fcvm.NewAllocator())
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].major != 16 || images[1].major != 17 {
		t.Fatalf("images = %+v, want 16 then 17", images)
	}
}

func TestLoadConfigParsesDatabaseVMs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	body := "[database_vms]\nkernel_path = \"/srv/fc/kernel/1.7.0\"\nvolume_root = \"/srv/db/v\"\n[database_vms.bases]\n\"17\" = \"/srv/fc/base/postgres-17.ext4\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseVMs == nil || cfg.DatabaseVMs.KernelPath != "/srv/fc/kernel/1.7.0" || cfg.DatabaseVMs.VolumeRoot != "/srv/db/v" || cfg.DatabaseVMs.Bases["17"] != "/srv/fc/base/postgres-17.ext4" {
		t.Fatalf("database_vms = %+v", cfg.DatabaseVMs)
	}
	empty := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadConfig(empty); err != nil || cfg.DatabaseVMs != nil {
		t.Fatalf("absent table = %+v, %v; want nil", cfg.DatabaseVMs, err)
	}
}

// The database_vmmd role's template must load as a database node with every
// key decoded (a misplaced key would silently fall back to a default).
func TestDatabaseVmmdTemplateLoads(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "database_vmmd", "templates", "vmmd.toml.j2"))
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.NewReplacer(
		"{{ database_vmmd_overlay_ip }}", "100.64.7.1",
		"{{ database_vmmd_port }}", "50051",
		"{{ fc_release | regex_replace('^v', '') }}", "1.7.0",
		"{{ database_vmmd_volume_root }}", "/srv/db/volumes",
		"{% if dbbackupd_enabled | default(false) | bool %}\n", "",
		"{% endif %}\n", "",
		"{{ dbbackupd_socket_path | default('/run/faas-dbbackupd/dbbackupd.sock') }}", "/run/faas-dbbackupd/dbbackupd.sock",
		"{% for major, image in database_vmmd_postgres_images.items() %}\n", "",
		"{% for spare in database_vmmd_spares %}\n", "",
		"{% endfor %}\n", "",
		"{{ major }}", "17",
		"{{ image }}", testPinnedImage,
		"{{ spare.postgres_major }}", "17",
		"{{ spare.vcpu_count }}", "1",
		"{{ spare.mem_mib }}", "512",
		"{{ spare.count }}", "2",
	).Replace(string(body))
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "{%") {
		t.Fatalf("unrendered template expressions:\n%s", rendered)
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != "database" || cfg.ListenAddr != "tcp://100.64.7.1:50051" || cfg.TLSCertPath != "/etc/faas/tls/vmmd/server.crt" || cfg.DatabaseVMs == nil ||
		cfg.DatabaseVMs.KernelPath != "/srv/fc/kernel/1.7.0" || cfg.DatabaseVMs.Images["17"] != testPinnedImage ||
		cfg.DatabaseVMs.ArchiveSocket != "/run/faas-dbbackupd/dbbackupd.sock" || len(cfg.DatabaseVMs.Spares) != 1 || cfg.DatabaseVMs.Spares[0].Count != 2 {
		t.Fatalf("loaded %+v / %+v", cfg, cfg.DatabaseVMs)
	}
	if _, _, err := cfg.DatabaseVMs.runtimeConfig(fcvm.NewAllocator()); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseVMConfigSpares(t *testing.T) {
	base := DatabaseVMConfig{KernelPath: "/k", Bases: map[string]string{"17": "/b"}}
	cfg := base
	cfg.Spares = []DatabaseSpareConfig{{PostgresMajor: 17, VcpuCount: 1, MemMiB: 512, Count: 2}, {PostgresMajor: 16, VcpuCount: 2, MemMiB: 1024, Count: 1}}
	rcfg, _, err := cfg.runtimeConfig(fcvm.NewAllocator())
	if err != nil {
		t.Fatal(err)
	}
	if rcfg.Spares[dbvm.SpareShape{Major: 17, VcpuCount: 1, MemSizeMiB: 512}] != 2 || len(rcfg.Spares) != 2 {
		t.Fatalf("spares = %v", rcfg.Spares)
	}
	for name, spares := range map[string][]DatabaseSpareConfig{
		"bad major": {{PostgresMajor: 13, VcpuCount: 1, MemMiB: 512, Count: 1}},
		"no vcpus":  {{PostgresMajor: 17, MemMiB: 512, Count: 1}},
		"tiny":      {{PostgresMajor: 17, VcpuCount: 1, MemMiB: 64, Count: 1}},
		"no count":  {{PostgresMajor: 17, VcpuCount: 1, MemMiB: 512}},
		"twice":     {{PostgresMajor: 17, VcpuCount: 1, MemMiB: 512, Count: 1}, {PostgresMajor: 17, VcpuCount: 1, MemMiB: 512, Count: 3}},
	} {
		cfg := base
		cfg.Spares = spares
		if _, _, err := cfg.runtimeConfig(fcvm.NewAllocator()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
