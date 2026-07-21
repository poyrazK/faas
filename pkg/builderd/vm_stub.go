//go:build !metal

package builderd

import (
	"context"
	"crypto/tls"
)

// ErrNotMetal is the sentinel returned by the non-metal VM stub. The metal
// implementation lives in vm_metal.go (//go:build metal); the split keeps
// non-metal builds (CI on a Mac, unit tests) free of /dev/kvm dependencies.

// VMMDriver is the non-metal stub. The metal implementation is in vm_metal.go.
type VMMDriver struct{}

// NewVMMDriver returns the non-metal VMMDriver. The orchestrator ignores the
// driver unless the VM spawn is invoked, where it returns ErrNotMetal.
func NewVMMDriver(_, _, _, _ string) (*VMMDriver, error) { return &VMMDriver{}, nil }

// NewVMMDriverContext mirrors the metal signature so both build tags
// compile. It never dials; the spawn path always returns ErrNotMetal.
func NewVMMDriverContext(_ context.Context, _ string, _ *tls.Config, _, _, _ string) (*VMMDriver, error) {
	return &VMMDriver{}, nil
}

// Close is a no-op on the stub.
func (s *VMMDriver) Close() error { return nil }

// Spawn is the non-metal implementation of the VM interface. It always
// returns ErrNotMetal — the spawn path is metal-only.
func (s *VMMDriver) Spawn(_ context.Context, _ VMRequest) (BuildHandle, error) {
	return BuildHandle{}, ErrNotMetal
}

// WaitForCompletion is the non-metal pairing; it also errors out.
func (s *VMMDriver) WaitForCompletion(_ context.Context, _ BuildHandle) (BuildOutcome, error) {
	return BuildOutcome{}, ErrNotMetal
}
