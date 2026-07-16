package main

import "fmt"

// Resume hook (spec §4.8, §11 test V6). A snapshot bakes the guest's RNG state
// and wall clock. Restore N instances from one snapshot and — without this hook —
// they share an entropy stream (duplicate UUIDs, TLS keys) and a stale clock.
// After restore the host signals the guest (via vsock); the guest re-seeds
// entropy from virtio-rng, then steps the clock, then re-arms readiness.
//
// This file holds the platform-independent orchestration so the ordering
// contract is unit-tested; the Linux entropy/clock/vsock code is in
// resume_linux.go.

// ResumeOps are the side effects the resume hook performs, injected so the
// sequence is testable without a guest.
type ResumeOps struct {
	// ReseedEntropy mixes fresh virtio-rng bytes into the kernel pool.
	ReseedEntropy func() error
	// StepClock corrects the wall clock to the post-restore host time.
	StepClock func() error
}

// Resume runs the hook. Entropy is re-seeded FIRST: the moment the app resumes it
// may generate a UUID or TLS key, and that must draw from unique entropy, not the
// snapshot's frozen stream. The clock step follows. If either op fails the error
// is returned so the caller can refuse readiness (a non-unique guest must not
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
	return nil
}
