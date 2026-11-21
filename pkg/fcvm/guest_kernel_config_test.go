// adr: 20261027090013 §10 — sandbox sessions give the tenant root in the
// guest, so every path that builds the guest kernel turns off loadable
// modules and kexec: root cannot load kernel code.
package fcvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuestKernelBuildsLoadNoKernelCode(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	release := read("scripts/build-firecracker-kernel.sh")
	for _, want := range []string{
		`-e 's/^CONFIG_MODULES=.*/# CONFIG_MODULES is not set/'`,
		`-e 's/^CONFIG_KEXEC_FILE=.*/# CONFIG_KEXEC_FILE is not set/'`,
		`for option in MODULES KEXEC KEXEC_FILE; do`,
		`grep -q "^CONFIG_${option}=y$"`,
		`grep -q '=m$'`,
	} {
		if !strings.Contains(release, want) {
			t.Errorf("the release kernel build lacks %q", want)
		}
	}
	// The checks after olddefconfig must follow it.
	if strings.Index(release, "olddefconfig") > strings.Index(release, `for option in MODULES KEXEC KEXEC_FILE; do`) {
		t.Error("the release build checks the config before olddefconfig")
	}
	packer := read("deploy/packer/scripts/prebuild-kernel.sh")
	ansible := read("deploy/ansible/roles/firecracker/tasks/main.yml")
	for _, opt := range []string{"MODULES", "KEXEC_FILE"} {
		if !strings.Contains(packer, "s/^CONFIG_"+opt+"=.*/# CONFIG_"+opt+" is not set/") {
			t.Errorf("the packer kernel build keeps CONFIG_%s", opt)
		}
		if !strings.Contains(ansible, "    - "+opt+"\n") {
			t.Errorf("the Ansible kernel build keeps CONFIG_%s", opt)
		}
	}
	if !strings.Contains(ansible, `line: "# CONFIG_{{ item }} is not set"`) || !strings.Contains(ansible, "no_kernel_code_config.changed") {
		t.Error("the Ansible kernel build does not disable them, or does not rebuild when it does")
	}
}
