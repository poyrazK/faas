//go:build !metal

package builderd

import "context"

// ErrNotMetal is the sentinel returned by the non-metal VM stub. The metal
// implementation lives in vm_metal.go (//go:build metal); the split keeps
// non-metal builds (CI on a Mac, unit tests) free of /dev/kvm dependencies.

// VMMDriver is the non-metal stub. The metal implementation is in vm_metal.go.
type VMMDriver struct{}

// NewVMMDriver returns the non-metal VMMDriver. The orchestrator ignores the
// driver unless the VM spawn is invoked, where it returns ErrNotMetal.
func NewVMMDriver(_ string) (*VMMDriver, error) { return &VMMDriver{}, nil }

// Spawn is the non-metal implementation of the VM interface. It always
// returns ErrNotMetal — the spawn path is metal-only.
func (s *VMMDriver) Spawn(ctx context.Context, req VMRequest) (VMResult, error) {
	return VMResult{}, ErrNotMetal
}
