package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostHardeningLocksTheKernelDown pins ADR-20261107220000: every host
// raises kernel lockdown to integrity from a unit that runs before
// sysinit.target, the unit only ever raises the level (writing the current
// level back is an error the kernel returns), kexec and io_uring are off,
// and the module lock stays opt-in.
func TestHostHardeningLocksTheKernelDown(t *testing.T) {
	role := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "host_hardening")
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(role, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	defaults := readRoleDefaults(t, role)
	if got := yamlScalar(defaults["faas_hardening_kernel_lockdown"]); got != "integrity" {
		t.Errorf("faas_hardening_kernel_lockdown = %q, want integrity (confidentiality blocks the kernel reads tracing needs)", got)
	}
	if got, ok := defaults["faas_hardening_modules_lock"].(bool); !ok || got {
		t.Errorf("faas_hardening_modules_lock = %v; the module lock must stay opt-in (false) until real module sets are collected", defaults["faas_hardening_modules_lock"])
	}
	sysctls, _ := defaults["faas_hardening_sysctl"].(map[string]any)
	for key, want := range map[string]string{
		"kernel.kexec_load_disabled": "1",
		"kernel.io_uring_disabled":   "2",
		"dev.tty.ldisc_autoload":     "0",
	} {
		if got := yamlScalar(sysctls[key]); got != want {
			t.Errorf("faas_hardening_sysctl[%s] = %q, want %s", key, got, want)
		}
	}
	preload, _ := defaults["faas_hardening_modules_preload"].([]any)
	seen := map[string]bool{}
	for _, m := range preload {
		seen[yamlScalar(m)] = true
	}
	for _, m := range []string{"kvm", "vhost_vsock", "tun", "bridge", "veth", "nf_tables", "nft_nat", "nft_ct", "sch_tbf", "wireguard", "overlay"} {
		if !seen[m] {
			t.Errorf("faas_hardening_modules_preload lacks %s, which the platform loads after boot", m)
		}
	}

	unit := read("templates/faas-kernel-lockdown.service.j2")
	for _, want := range []string{
		"DefaultDependencies=no",
		"After=systemd-modules-load.service",
		"Before=sysinit.target",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"WantedBy=sysinit.target",
		"ConditionPathExists=/sys/kernel/security/lockdown",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("faas-kernel-lockdown.service missing %q", want)
		}
	}

	script := read("templates/faas-kernel-lockdown.sh.j2")
	for _, want := range []string{
		`if [ "$(rank "$current")" -lt "$(rank "$want")" ]; then`,
		`echo "$want" > "$lockdown"`,
		`{% if faas_hardening_modules_lock | bool %}`,
		`echo 1 > /proc/sys/kernel/modules_disabled`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("faas-kernel-lockdown script missing %q", want)
		}
	}
	if strings.Index(script, "modprobe") > strings.Index(script, "echo 1 > /proc/sys/kernel/modules_disabled") {
		t.Error("the script sets modules_disabled before loading the preload list")
	}

	tasks := read("tasks/main.yml")
	for _, want := range []string{"faas-kernel-lockdown.service", "prove the kernel is locked down"} {
		if !strings.Contains(tasks, want) {
			t.Errorf("host_hardening tasks missing %q", want)
		}
	}
}
