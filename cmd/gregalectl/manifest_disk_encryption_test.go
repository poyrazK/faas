package main

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// ADR-20261109120000: a host unlocks from every other host's Tang server,
// never its own, and its Tang server listens on its manifest (private)
// address rather than the default route's, which on bare metal is public.
func TestRenderDiskEncryptionVars(t *testing.T) {
	const recipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
	hosts := []manifest.Host{
		{Name: "cp-1", Address: "10.20.0.1:50051"},
		{Name: "compute-1", Address: "10.20.0.2:50051"},
		{Name: "compute-2", Address: "10.20.0.3:50051"},
		// A database host unlocks from the others but serves no Tang.
		{Name: "db-1", Role: manifest.RoleDatabase, Address: "10.20.0.4:50051"},
	}
	m := &manifest.Manifest{
		Fleet:   manifest.Fleet{Hosts: hosts},
		Overlay: manifest.Overlay{Provider: "static", CIDR: "10.20.0.0/24", Interface: "eno2"},
	}

	off, err := renderDiskEncryptionVars(m, hosts[0])
	if err != nil || off != "faas_disk_encryption: false\n" {
		t.Fatalf("without the section the volume stays plain: %q, %v", off, err)
	}

	m.DiskEncryption = &manifest.DiskEncryption{RecoveryRecipients: []string{recipient}}
	out, err := renderDiskEncryptionVars(m, hosts[1])
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Enabled    bool     `yaml:"faas_disk_encryption"`
		Port       int      `yaml:"faas_tang_port"`
		Listen     string   `yaml:"faas_tang_listen_address"`
		Peers      []string `yaml:"faas_tang_peer_urls"`
		Allowed    []string `yaml:"faas_tang_allowed_cidrs"`
		Recipients []string `yaml:"faas_disk_recovery_recipients"`
	}
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("vars are not YAML: %v\n%s", err, out)
	}
	want := struct {
		Enabled    bool     `yaml:"faas_disk_encryption"`
		Port       int      `yaml:"faas_tang_port"`
		Listen     string   `yaml:"faas_tang_listen_address"`
		Peers      []string `yaml:"faas_tang_peer_urls"`
		Allowed    []string `yaml:"faas_tang_allowed_cidrs"`
		Recipients []string `yaml:"faas_disk_recovery_recipients"`
	}{
		Enabled: true, Port: 7500, Listen: "10.20.0.2",
		Peers:      []string{"http://10.20.0.1:7500", "http://10.20.0.3:7500"},
		Allowed:    []string{"10.20.0.0/24"},
		Recipients: []string{recipient},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v\n%s", got, want, out)
	}
}

// The warm standby and database hosts are Tang clients of every
// control-plane and compute host.
func TestRenderDiskEncryptionVarsForTangClients(t *testing.T) {
	m := &manifest.Manifest{
		Fleet: manifest.Fleet{Hosts: []manifest.Host{
			{Name: "cp-1", Role: roleControlPlane, Address: "10.20.0.1:50051"},
			{Name: "compute-1", Role: roleComputeOnly, Address: "10.20.0.2:50051"},
			{Name: "db-1", Role: manifest.RoleDatabase, Address: "10.20.0.4:50051"},
		}},
		Overlay:        manifest.Overlay{Provider: "static", CIDR: "10.20.0.0/24"},
		DiskEncryption: &manifest.DiskEncryption{RecoveryRecipients: []string{"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"}},
	}
	peersOf := func(out string) []string {
		var got struct {
			Peers []string `yaml:"faas_tang_peer_urls"`
		}
		if err := yaml.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("vars are not YAML: %v\n%s", err, out)
		}
		return got.Peers
	}
	want := []string{"http://10.20.0.1:7500", "http://10.20.0.2:7500"}
	db, err := renderDiskEncryptionVars(m, m.Fleet.Hosts[2])
	if err != nil {
		t.Fatal(err)
	}
	if got := peersOf(db); !reflect.DeepEqual(got, want) {
		t.Errorf("database host peers = %v, want %v", got, want)
	}
	standby, err := renderStandbyDiskEncryptionVars(m, &manifest.PostgresStandby{Name: "pg-standby", Address: "10.20.0.9"})
	if err != nil {
		t.Fatal(err)
	}
	if got := peersOf(standby); !reflect.DeepEqual(got, want) {
		t.Errorf("standby peers = %v, want %v", got, want)
	}
}
