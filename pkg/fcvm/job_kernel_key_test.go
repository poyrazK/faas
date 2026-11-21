package fcvm

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storagekey"
)

// ADR-20261112030000 amendment 1: schedd names the version-only kernel key
// for jobs; a node that boots a content-addressed kernel must boot the same
// kernel for jobs as for its app VMs.
//
// adr: 20261112030000
func TestJobKernelKeyFollowsTheNodeKernel(t *testing.T) {
	contentKey := storagekey.KernelForDigest("1.17.0", strings.Repeat("ab", 32))
	m := &Manager{paths: Paths{Kernel: contentKey}, fcVersion: "1.17.0"}
	cases := []struct{ name, requested, want string }{
		{"version-only alias maps to the node key", "kernel/1.17.0", contentKey},
		{"the node key stays", contentKey, contentKey},
		{"another version stays", "kernel/1.16.0", "kernel/1.16.0"},
		{"an operator pin stays", "kernel/pinned-test", "kernel/pinned-test"},
		{"empty stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.jobKernelKey(tc.requested); got != tc.want {
				t.Fatalf("jobKernelKey(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
	legacy := &Manager{paths: Paths{Kernel: "kernel/1.17.0"}, fcVersion: "1.17.0"}
	if got := legacy.jobKernelKey("kernel/1.17.0"); got != "kernel/1.17.0" {
		t.Fatalf("a node on the version-only key changed the job kernel to %q", got)
	}
}
