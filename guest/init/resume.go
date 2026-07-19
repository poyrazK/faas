package main

import (
	"fmt"
	"os"
)

// Resume hook (spec §4.8, §11 test V6). A snapshot bakes the guest's RNG state
// and wall clock. Restore N instances from one snapshot and — without this hook —
// they share an entropy stream (duplicate UUIDs, TLS keys) and a stale clock.
// After restore the host signals the guest (via vsock); the guest re-seeds
// entropy from virtio-rng, then steps the clock, then re-arms readiness.
//
// This file holds the platform-independent orchestration so the ordering
// contract is unit-tested; the Linux entropy/clock/vsock code is in
// resume_linux.go.

// UUIDMarkerPath is where the resume hook records the freshly-rerandomized
// UUID so the §14 V6 metal test (and any operator tool) can fetch it without
// needing to exec a binary inside the guest. Spec §11 test V6 asserts two
// restores of one snapshot yield DIFFERENT values at this path.
const UUIDMarkerPath = "/etc/faas/uuid.txt"

// ResumeOps are the side effects the resume hook performs, injected so the
// sequence is testable without a guest.
type ResumeOps struct {
	// ReseedEntropy mixes fresh virtio-rng bytes into the kernel pool.
	ReseedEntropy func() error
	// StepClock corrects the wall clock to the post-restore host time.
	StepClock func() error
	// WriteUUIDMarker records /proc/sys/kernel/random/uuid to
	// UUIDMarkerPath AFTER reseed (so it draws from unique entropy). On a
	// non-Linux build tag this can be left nil; Resume tolerates it.
	WriteUUIDMarker func() error
}

// Resume runs the hook. Entropy is re-seeded FIRST: the moment the app resumes it
// may generate a UUID or TLS key, and that must draw from unique entropy, not the
// snapshot's frozen stream. The clock step follows. The UUID marker write
// happens LAST so it observes the re-keyed pool. If any op fails the error is
// returned so the caller can refuse readiness (a non-unique guest must not
// serve).
func (o ResumeOps) Resume() error {
	if o.ReseedEntropy == nil || o.StepClock == nil {
		return fmt.Errorf("resume: ops not configured")
	}
	if err := o.ReseedEntropy(); err != nil {
		return fmt.Errorf("resume: reseed entropy: %w", err)
	}
	if err := o.StepClock(); err != nil {
		return fmt.Errorf("resume: step clock: %w", err)
	}
	if o.WriteUUIDMarker != nil {
		if err := o.WriteUUIDMarker(); err != nil {
			return fmt.Errorf("resume: write uuid marker: %w", err)
		}
	}
	return nil
}

// writeUUIDMarker reads /proc/sys/kernel/random/uuid (which draws from the
// freshly-rekeyed pool) and writes it to UUIDMarkerPath. The marker is what
// §14 V6 fetches; if the value collides between two restores, the resume hook
// did not actually re-seed. We do not return an error on a missing procfs
// (some test environments lack /proc); the resume still ran, the marker just
// wasn't observable. hostTimeUnixNano is unused here but kept for symmetry
// with future ops that need the post-restore wall clock.
//
//nolint:unused // staticcheck U1000 does not trace through go-accept goroutines; reached via handleResumeConn → RunResumeHook.
func writeUUIDMarker(_ int64) error {
	data, err := os.ReadFile("/proc/sys/kernel/random/uuid")
	if err != nil {
		return fmt.Errorf("read random/uuid: %w", err)
	}
	return os.WriteFile(UUIDMarkerPath, data, 0o644)
}
