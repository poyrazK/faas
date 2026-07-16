//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// Linux resume operations (spec §4.8). These are the concrete side effects the
// resume hook performs; the ordering that makes them correct is in resume.go and
// is unit-tested. The vsock trigger that invokes RunResumeHook after a restore is
// wired during the M3 metal bring-up (it needs AF_VSOCK on the guest kernel).
const (
	hwrngPath   = "/dev/hwrng"   // virtio-rng (always attached, spec §11)
	urandomPath = "/dev/urandom" // kernel entropy pool
	reseedBytes = 256            // enough to fully re-key the pool
)

// RunResumeHook performs the post-restore hook: re-seed entropy from virtio-rng,
// then step the wall clock to the host time captured at resume (unix nanos).
func RunResumeHook(hostTimeUnixNano int64) error {
	return ResumeOps{
		ReseedEntropy: reseedFromHWRNG,
		StepClock:     func() error { return stepClockTo(hostTimeUnixNano) },
	}.Resume()
}

// reseedFromHWRNG copies fresh virtio-rng bytes into /dev/urandom, mixing them
// into the pool so a restored guest immediately diverges from its snapshot's RNG
// stream (test V6: two restores of one snapshot must yield distinct
// /proc/sys/kernel/random/uuid).
func reseedFromHWRNG() error {
	src, err := os.Open(hwrngPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", hwrngPath, err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(urandomPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", urandomPath, err)
	}
	defer func() { _ = dst.Close() }()
	if _, err := io.CopyN(dst, src, reseedBytes); err != nil {
		return fmt.Errorf("reseed copy: %w", err)
	}
	return nil
}

// stepClockTo sets the wall clock to the post-restore host time (restored guests
// wake with a stale clock, which breaks TLS validity and time-based UUIDs).
func stepClockTo(unixNano int64) error {
	tv := syscall.NsecToTimeval(unixNano)
	if err := syscall.Settimeofday(&tv); err != nil {
		return fmt.Errorf("settimeofday: %w", err)
	}
	return nil
}
