// adr: 1027
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestDatabaseVMConfigDefaultsAndValidation(t *testing.T) {
	leases := fcvm.NewAllocator()
	rcfg, err := DatabaseVMConfig{KernelPath: "/k", Bases: map[string]string{"17": "/b17"}}.runtimeConfig(leases)
	if err != nil {
		t.Fatal(err)
	}
	if rcfg.JailBase != defaultDatabaseJailBase || rcfg.VolumeRoot != defaultDatabaseVolumeRoot || rcfg.ParentCgroup != defaultDatabaseParentCgroup || rcfg.Bases[17] != "/b17" {
		t.Fatalf("runtime config = %+v", rcfg)
	}
	for name, cfg := range map[string]DatabaseVMConfig{
		"no kernel":   {Bases: map[string]string{"17": "/b"}},
		"no bases":    {KernelPath: "/k"},
		"bad major":   {KernelPath: "/k", Bases: map[string]string{"12": "/b"}},
		"non-numeric": {KernelPath: "/k", Bases: map[string]string{"latest": "/b"}},
		"empty path":  {KernelPath: "/k", Bases: map[string]string{"17": ""}},
	} {
		if _, err := cfg.runtimeConfig(leases); err == nil {
			t.Errorf("%s: accepted", name)
		}
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
