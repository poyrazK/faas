package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestRenderManifestAnsibleFiles_DerivesRouting(t *testing.T) {
	yaml := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n", 1)
	m, err := manifest.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("manifest.Validate: %v", errs)
	}

	files, err := renderManifestAnsibleFiles(m, t.TempDir())
	if err != nil {
		t.Fatalf("renderManifestAnsibleFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("generated files = %d, want inventory + 2 host_vars", len(files))
	}
	var inventory, computeVars string
	for _, file := range files {
		switch {
		case strings.HasSuffix(file.Path, filepath.Join("inventory", "hosts.ini")):
			inventory = string(file.Body)
		case strings.HasSuffix(file.Path, "fsn-2.yml"):
			computeVars = string(file.Body)
		}
	}
	if !strings.Contains(inventory, "[compute_nodes]\nfsn-2\n") {
		t.Errorf("inventory missing compute node:\n%s", inventory)
	}
	if strings.Contains(inventory, "[box]") || strings.Contains(inventory, "box:children") {
		t.Errorf("generated production inventory contains retired combined box group:\n%s", inventory)
	}
	if !strings.Contains(computeVars, `ansible_host: "10.42.0.2"`) {
		t.Errorf("compute host vars missing host address:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `faas_vmmd_target_url: "tcp://vmmd.faas:50051"`) {
		t.Errorf("compute host vars missing derived target:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `faas_gateway_listen: "0.0.0.0:8080"`) {
		t.Errorf("compute host vars missing split gateway listener:\n%s", computeVars)
	}
	// Multi-host safety cluster PR-9 (audit F8-B): the manifest
	// renderer must emit faas_public_listen_addr + faas_public_control_addr
	// so a correctly bootstrapped fleet never reaches the PR-8
	// boot-time check (gatewayd-public refuses to start on a
	// loopback default when FAAS_NODE_NAME is set).
	if !strings.Contains(computeVars, `faas_public_listen_addr: "10.42.0.2:443"`) {
		t.Errorf("compute host vars missing faas_public_listen_addr emission:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `faas_public_control_addr: "10.42.0.2:9092"`) {
		t.Errorf("compute host vars missing faas_public_control_addr emission:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `faas_gatewayd_egress_listen: "tcp://0.0.0.0:9092"`) {
		t.Errorf("compute host vars missing split egress listener:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `faas_vmmd_schedd_target: "tcp://schedd.faas:7100"`) {
		t.Errorf("compute host vars missing scheduler target:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `faas_gatewayd_apid_loopback: "http://10.42.0.1:8081"`) {
		t.Errorf("compute host vars missing control-plane apid target:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `overlay_cidrs: ["10.42.0.0/24"]`) {
		t.Errorf("compute host vars missing manifest overlay CIDR:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `faas_overlay_provider: "wireguard"`) || !strings.Contains(computeVars, "faas_overlay_iface: wg0") {
		t.Errorf("compute host vars missing manifest overlay provider/interface:\n%s", computeVars)
	}
	var controlVars string
	for _, file := range files {
		if strings.HasSuffix(file.Path, "fsn-1.yml") {
			controlVars = string(file.Body)
		}
	}
	if !strings.Contains(controlVars, `faas_postgres_allowed_cidrs: ["10.42.0.2/32"]`) {
		t.Errorf("control host vars missing compute PostgreSQL allowlist:\n%s", controlVars)
	}
	if !strings.Contains(controlVars, `faas_compute_allowed_cidrs: ["10.42.0.2/32"]`) {
		t.Errorf("control host vars missing compute scheduler allowlist:\n%s", controlVars)
	}
	if !strings.Contains(controlVars, `faas_gatewayd_internal_target: "tcp://10.42.0.2:8080"`) {
		t.Errorf("control host vars missing split gateway target:\n%s", controlVars)
	}
	if !strings.Contains(controlVars, `faas_schedd_gateway_synth_target: "tcp://10.42.0.2:8080"`) {
		t.Errorf("control host vars missing schedd synth target:\n%s", controlVars)
	}
	if !strings.Contains(controlVars, `faas_schedd_gateway_metrics_url: ""`) {
		t.Errorf("control host vars should disable unreachable remote gateway metrics:\n%s", controlVars)
	}
	if !strings.Contains(controlVars, "faas_meterd_config_managed: true") ||
		!strings.Contains(controlVars, `faas_meterd_egress_socket: "tcp://egress.faas:9092"`) {
		t.Errorf("control host vars missing managed meterd egress route:\n%s", controlVars)
	}
	if !strings.Contains(computeVars, `faas_control_plane_allowed_cidrs: ["10.42.0.1/32"]`) {
		t.Errorf("compute host vars missing control-plane service allowlist:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `10.42.0.1"`) || !strings.Contains(computeVars, `schedd.faas`) {
		t.Errorf("compute host vars missing control-plane private alias:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `10.42.0.2"`) || !strings.Contains(computeVars, `vmmd.faas`) {
		t.Errorf("compute host vars missing compute private alias:\n%s", computeVars)
	}
	if !strings.Contains(computeVars, `egress.faas`) {
		t.Errorf("compute host vars missing egress private alias:\n%s", computeVars)
	}
}

func TestRenderManifestAnsibleFiles_HostnameEndpointsUseOverlayBoundary(t *testing.T) {
	yaml := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:7100\n    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n", 1)
	m, err := manifest.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("manifest.Validate: %v", errs)
	}

	files, err := renderManifestAnsibleFiles(m, t.TempDir())
	if err != nil {
		t.Fatalf("renderManifestAnsibleFiles: %v", err)
	}
	var controlVars, computeVars string
	for _, file := range files {
		switch {
		case strings.HasSuffix(file.Path, "fsn-1.yml"):
			controlVars = string(file.Body)
		case strings.HasSuffix(file.Path, "fsn-2.yml"):
			computeVars = string(file.Body)
		}
	}
	for _, want := range []string{
		`ansible_host: "fsn-1.gregale.dev"`,
		`faas_gatewayd_internal_target: "tcp://fsn-2.gregale.dev:8080"`,
		`faas_postgres_listen_addresses: "fsn-1.gregale.dev"`,
		`faas_postgres_allowed_cidrs: ["10.42.0.0/24"]`,
		`faas_compute_allowed_cidrs: ["10.42.0.0/24"]`,
	} {
		if !strings.Contains(controlVars, want) {
			t.Errorf("control host vars missing %q:\n%s", want, controlVars)
		}
	}
	for _, want := range []string{
		`ansible_host: "fsn-2.gregale.dev"`,
		`faas_gatewayd_apid_loopback: "http://fsn-1.gregale.dev:8081"`,
		`faas_control_plane_allowed_cidrs: ["10.42.0.0/24"]`,
	} {
		if !strings.Contains(computeVars, want) {
			t.Errorf("compute host vars missing %q:\n%s", want, computeVars)
		}
	}
	for _, want := range []string{
		`faas_private_dns_mode: "managed_hosts"`,
		`faas_private_dns_zone: "gregale.dev"`,
		`faas_private_hosts:`,
		`- inventory_host: "fsn-1"`,
		`names: ["fsn-1.gregale.dev", "schedd.faas"]`,
		`- inventory_host: "fsn-2"`,
		`names: ["fsn-2.gregale.dev", "vmmd.faas", "egress.faas"]`,
	} {
		if !strings.Contains(computeVars, want) {
			t.Fatalf("hostname endpoints missing generated private resolver record %q:\n%s", want, computeVars)
		}
	}
	if strings.Contains(computeVars, "10.42.0.2") || strings.Contains(computeVars, "faas_internal_hosts:") {
		t.Fatalf("hostname endpoints leaked a provider IP or legacy private-host variable:\n%s", computeVars)
	}
}

func TestRenderManifestAnsibleFiles_RejectsSingleBox(t *testing.T) {
	yaml := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane",
		"    - name: fsn-1\n      role: single-box\n      address: 127.0.0.1:7100", 1)
	m, err := manifest.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("manifest.Validate: %v", errs)
	}
	if _, err := renderManifestAnsibleFiles(m, t.TempDir()); err == nil || !strings.Contains(err.Error(), "unsupported production role") {
		t.Fatalf("single-box manifest error = %v, want unsupported production role", err)
	}
}

func TestWriteGeneratedAnsibleFile_RefusesDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory", "hosts.ini")
	if err := writeGeneratedAnsibleFile(path, []byte("first\n"), false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeGeneratedAnsibleFile(path, []byte("second\n"), false); err == nil {
		t.Fatal("drifted generated file was overwritten without --force")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "first\n" {
		t.Fatalf("file after refused overwrite = %q, err=%v", got, err)
	}
}
