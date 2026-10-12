//go:build metal

package fcvm

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Metal helpers for the external fcvm_test package, whose tests import
// packages (pkg/sched) that themselves import fcvm.

func NewMetalVMMForTest(t *testing.T, timeout time.Duration) *JailerVMM {
	return newMetalVMM(t, timeout)
}

func MetalImagesForTest(t *testing.T) (kernel, base, layer string) { return metalImages(t) }

func WithCgroupRootAtForTest(t *testing.T, path string) { withCgroupRootAt(t, path) }

func FirstResponseForTest(url string, within time.Duration) (*http.Response, error) {
	return firstResponse(url, within)
}

func MemAvailableMBForTest(t *testing.T) int64 { return memAvailableMB(t) }

// VMCgroupIntForTest reads one integer file of an instance's cgroup.
func VMCgroupIntForTest(plan api.Plan, instance, name string) (int64, error) {
	return readCgroupInt(cgroupRoot+"/"+ParentCgroupFor(plan)+"/"+PerInstanceScope(instance), name)
}
