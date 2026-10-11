//go:build metal

package leakcheck

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The spec §11 fence pins are systemd child slices beside the VM scopes; they
// hold cgroup.procs like a scope but are permanent host infrastructure.
func TestListVMScopesSkipsFencePinSlices(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "faas.slice", "faas-cp.slice", "faas-cp-build.slice")
	scope := filepath.Join(build, "0b9c2b2e-6f37-4f5e-9d1c-1f2a3b4c5d6e")
	for _, dir := range []string{scope, filepath.Join(build, "faas-cp-build-fence.slice")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := listVMScopes(root); !reflect.DeepEqual(got, []string{scope}) {
		t.Errorf("listVMScopes = %v, want only the VM scope %s", got, scope)
	}
}
