package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestRenderManifestAnsibleFiles_ScaleMatrix(t *testing.T) {
	for _, computeCount := range []int{1, 10, 100, manifest.MaxComputeNodes} {
		t.Run(fmt.Sprintf("%d-compute-nodes", computeCount), func(t *testing.T) {
			m := scaleTestManifest(t, computeCount)
			if errs := m.Validate(); errs != nil {
				t.Fatalf("scale manifest validation: %v", errs)
			}

			generatedDir := t.TempDir()
			files, err := renderManifestAnsibleFiles(m, generatedDir)
			if err != nil {
				t.Fatalf("renderManifestAnsibleFiles: %v", err)
			}
			inventory, hostVars, groupVars := splitGeneratedManifestFiles(t, files)
			assertScaleInventory(t, inventory, hostVars, groupVars, computeCount)

			// Keep the connection-free Ansible gate on the largest supported
			// topology. The other matrix points exercise the same renderer
			// contract without paying the process startup cost four times.
			if computeCount == manifest.MaxComputeNodes {
				runManifestScaleAnsibleCheck(t, files)
			}
		})
	}
}

func scaleTestManifest(t *testing.T, computeCount int) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(validManifestYAML))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}

	hosts := make([]manifest.Host, 0, computeCount+1)
	hosts = append(hosts, manifest.Host{
		Name:    "control-plane",
		Role:    "control-plane",
		Address: "control-plane.gregale.dev:7100",
	})
	for i := 1; i <= computeCount; i++ {
		name := fmt.Sprintf("compute-%04d", i)
		hosts = append(hosts, manifest.Host{
			Name:    name,
			Role:    "compute-only",
			Address: name + ".gregale.dev:50051",
		})
	}
	m.Fleet.Hosts = hosts
	return m
}

func splitGeneratedManifestFiles(t *testing.T, files []manifestAnsibleFile) (string, map[string]string, string) {
	t.Helper()
	var inventory string
	var groupVars string
	hostVars := make(map[string]string)
	for _, file := range files {
		switch {
		case strings.HasSuffix(filepath.ToSlash(file.Path), "/inventory/hosts.ini"):
			inventory = string(file.Body)
		case strings.HasSuffix(filepath.ToSlash(file.Path), "/inventory/group_vars/all.yml"):
			groupVars = string(file.Body)
		case strings.HasSuffix(filepath.ToSlash(file.Path), ".yml"):
			name := strings.TrimSuffix(filepath.Base(file.Path), ".yml")
			hostVars[name] = string(file.Body)
		}
	}
	if inventory == "" {
		t.Fatal("generated files did not include inventory/hosts.ini")
	}
	return inventory, hostVars, groupVars
}

func assertScaleInventory(t *testing.T, inventory string, hostVars map[string]string, groupVars string, computeCount int) {
	t.Helper()
	controlHosts := inventoryGroupHosts(inventory, "control_plane")
	computeHosts := inventoryGroupHosts(inventory, "compute_nodes")
	if len(controlHosts) != 1 || controlHosts[0] != "control-plane" {
		t.Fatalf("control_plane hosts = %v, want [control-plane]", controlHosts)
	}
	if len(computeHosts) != computeCount {
		t.Fatalf("compute_nodes hosts = %d, want %d", len(computeHosts), computeCount)
	}
	if strings.Contains(inventory, "[box]") || strings.Contains(inventory, "box:children") {
		t.Fatalf("generated inventory contains retired one-box group")
	}
	if len(hostVars) != computeCount+1 {
		t.Fatalf("host_vars files = %d, want %d", len(hostVars), computeCount+1)
	}

	allHosts := append([]string{"control-plane"}, computeHosts...)
	sharedPrivateHosts := computeCount+1 > manifestPrivateHostsGroupVarsThreshold
	if sharedPrivateHosts {
		if groupVars == "" {
			t.Fatal("large generated inventory did not include inventory/group_vars/all.yml")
		}
		if got := strings.Count(groupVars, "\n  - "); got != len(allHosts) {
			t.Fatalf("shared private peer records = %d, want %d", got, len(allHosts))
		}
	} else if groupVars != "" {
		t.Fatalf("small generated inventory unexpectedly included shared group_vars")
	}
	seenEndpoints := make(map[string]string, computeCount)
	for _, hostName := range allHosts {
		body, ok := hostVars[hostName]
		if !ok {
			t.Fatalf("missing host_vars for %s", hostName)
		}
		if got := strings.Count(body, "\n  - "); sharedPrivateHosts && got != 0 || !sharedPrivateHosts && got != len(allHosts) {
			want := 0
			if !sharedPrivateHosts {
				want = len(allHosts)
			}
			t.Fatalf("%s private peer records = %d, want %d", hostName, got, want)
		}
		if !strings.Contains(body, `faas_private_dns_mode: "managed_hosts"`) ||
			!strings.Contains(body, `faas_private_dns_zone: "gregale.dev"`) {
			t.Fatalf("%s is missing the provider-neutral private DNS contract", hostName)
		}
		if strings.Contains(body, `ansible_host: "10.`) {
			t.Fatalf("%s leaked a provider IP into ansible_host", hostName)
		}
	}

	controlVars := hostVars["control-plane"]
	if !strings.Contains(controlVars, "faas_box_role: control-plane") ||
		!strings.Contains(controlVars, "faas_compute_gateway_discovery: database") ||
		!strings.Contains(controlVars, `faas_apid_app_errors_listen: "tcp://0.0.0.0:9093"`) {
		t.Fatalf("control-plane host_vars do not describe the production control role")
	}
	if !strings.Contains(controlVars, `faas_postgres_listen_addresses: "control-plane.gregale.dev"`) {
		t.Fatalf("control-plane host_vars lost the stable PostgreSQL endpoint")
	}

	for i, hostName := range computeHosts {
		body := hostVars[hostName]
		address := hostName + ".gregale.dev"
		target := `faas_vmmd_target_url: "tcp://` + address + `:50051"`
		gatewayTarget := `faas_gateway_target_url: "tcp://` + address + `:8080"`
		if !strings.Contains(body, "faas_box_role: compute-only") ||
			!strings.Contains(body, "faas_node_name: "+hostName+".faas\n") ||
			!strings.Contains(body, `ansible_host: "`+address+`"`) ||
			!strings.Contains(body, target) ||
			!strings.Contains(body, gatewayTarget) ||
			!strings.Contains(body, `faas_gatewayd_app_errors_target: "tcp://apid.faas:9093"`) ||
			!strings.Contains(body, `faas_vmmd_schedd_target: "tcp://`+address+`:7100"`) ||
			!strings.Contains(body, `faas_gatewayd_schedd_target: "tcp://`+address+`:7100"`) {
			t.Fatalf("%s host_vars are missing stable node-specific routing", hostName)
		}
		if strings.Contains(body, `faas_vmmd_target_url: "tcp://10.`) ||
			strings.Contains(body, `faas_gateway_target_url: "tcp://10.`) {
			t.Fatalf("%s leaked a provider IP into a compute target", hostName)
		}
		if previous, exists := seenEndpoints[target]; exists {
			t.Fatalf("compute target %q is shared by %s and %s", target, previous, hostName)
		}
		seenEndpoints[target] = hostName
		if i == len(computeHosts)-1 && !strings.Contains(body, address) {
			t.Fatalf("last compute host %s is absent from its generated peer map", hostName)
		}
	}
}

func inventoryGroupHosts(inventory, group string) []string {
	marker := "[" + group + "]"
	start := strings.Index(inventory, marker)
	if start < 0 {
		return nil
	}
	section := inventory[start+len(marker):]
	if next := strings.Index(section, "\n["); next >= 0 {
		section = section[:next]
	}
	var hosts []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		hosts = append(hosts, line)
	}
	return hosts
}

func runManifestScaleAnsibleCheck(t *testing.T, files []manifestAnsibleFile) {
	t.Helper()
	if os.Getenv("GREGALE_ANSIBLE_SCALE_CHECK") != "1" {
		t.Skip("set GREGALE_ANSIBLE_SCALE_CHECK=1 to run the connection-free Ansible check-mode gate")
	}
	ansiblePlaybook, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skipf("ansible-playbook is not installed; generated inventory checks still passed: %v", err)
	}
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the scale test")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", ".."))
	var inventoryPath string
	for _, file := range files {
		if err := writeGeneratedAnsibleFile(file.Path, file.Body, false); err != nil {
			t.Fatalf("write generated file %s: %v", file.Path, err)
		}
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/inventory/hosts.ini") {
			inventoryPath = file.Path
		}
	}
	if inventoryPath == "" {
		t.Fatal("generated files did not include inventory/hosts.ini")
	}
	playbookPath := filepath.Join(repoRoot, "deploy", "ansible", "scale_check.yml")
	cmd := exec.Command(ansiblePlaybook, "--check", "--inventory", inventoryPath, playbookPath)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_DEPRECATION_WARNINGS=false")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("ansible scale check failed: %v\n%s", err, output.String())
	}
}
