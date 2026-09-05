//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux resume operations (spec §4.8). These are the concrete side effects the
// resume hook performs; the ordering that makes them correct is in resume.go and
// is unit-tested. The vsock trigger that invokes RunResumeHook after a restore is
// wired during the M3 metal bring-up (it needs AF_VSOCK on the guest kernel).
const (
	hwrngPath   = "/dev/hwrng"   // virtio-rng (always attached, spec §11)
	urandomPath = "/dev/urandom" // kernel entropy pool
	reseedBytes = 256            // maximum optional hardware bytes to mix
	// resumeDiagLog records resume failures for operators and the §14 V6
	// metal test. Successful resumes do not write diagnostic files. /etc/faas/ is
	// already writable on the upper overlay (the layer build script
	// pre-creates it). We don't go through slog here because PID 1's
	// stderr doesn't reliably reach FC's serial console; the file
	// approach is direct and the path is observable by busybox httpd.
	resumeDiagLog = "/etc/faas/resume.log"
	// RNDADDENTROPY is the ioctl that injects entropy bytes into the kernel
	// pool and credits them at the supplied entropy_count (in bits). Per
	// arch in golang.org/x/sys/unix (zerrors_linux_*.go). NOT exposed as a
	// const in the unix package; we hardcode the value here to avoid a
	// per-arch switch. The bit pattern is the same on all Linux arches
	// we target; verified against Linux 6.1 sources.
	rndaddentropyIoctl = 0x40085203
)

// resumeDiag appends one line to resumeDiagLog. Best-effort: a write
// failure is swallowed because the diagnostic is purely informational;
// the resume hook's actual error path is the ioctl return value, not
// this log. We use the file path directly (not fs.FS) because the
// overlay upper is reachable through the standard filesystem
// post-pivot.
func resumeDiag(msg string) {
	f, err := os.OpenFile(resumeDiagLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, msg)
}

// randPoolInfo mirrors the kernel's struct rand_pool_info (include/linux/random.h)
// for the RNDADDENTROPY ioctl: an int entropy_count (bits credited) + int buf_size
// (bytes that follow) + a flexible-array byte buffer holding the actual bytes.
//
// Layout MUST match the kernel. Linux 6.1 mixes the bytes into its BLAKE2s
// input pool and credits entropy_count toward initial CRNG readiness. An
// already initialized CRNG still needs the explicit reseed below.
type randPoolInfo struct {
	entropyCount int32
	bufSize      int32
	buf          [4096]byte // over-large; we only read first bufSize bytes
}

// resumeTraceparent is the W3C trace context the host shipped over vsock
// (issue #555 PR-4). Captured by RunResumeHook from the resume JSON body
// and read by runAppWithEnv when the supervisor starts the runner. The
// package var is the cleanest seam here: the resume hook runs in a
// vsock-accept goroutine; the supervisor runs on the main goroutine
// after boot() returns. They never overlap. Empty string = no OTel
// configured (legacy single-box without OTel), the runner's
// TRACEPARENT env is simply unset.
var resumeTraceparent string

// SetResumeTraceparent is the test seam: tests that drive RunResumeHook
// directly can stamp a controlled traceparent without going through the
// vsock listener. Production code reads the body through handleResumeConn.
func SetResumeTraceparent(tp string) { resumeTraceparent = tp }

// GetResumeTraceparent returns the most-recently-captured traceparent
// (empty if none). Used by runAppWithEnv.
func GetResumeTraceparent() string { return resumeTraceparent }

// RunResumeHook performs the post-restore hook: inject host-supplied CSPRNG
// bytes into /dev/urandom and explicitly reseed the kernel CRNG FIRST so each restore
// mixes a unique prefix into the pool (virtio-rng state is snapshotted, so
// without this every restore draws the same UUID). Then re-seed from
// virtio-rng (mixes a snapshotted-but-fresh-Linux-pool-aware quantity into the
// already-unique pool), step the wall clock to the host time captured at
// resume (unix nanos), then record /proc/sys/kernel/random/uuid at
// UUIDMarkerPath so the §14 V6 metal test (and any operator tool) can observe
// the freshly-rekeyed UUID.
//
// hostEntropy may be nil/empty (e.g. on cold boot the hook isn't invoked, but
// if a future caller does invoke with no entropy, AddEntropy is a no-op).
func RunResumeHook(hostTimeUnixNano int64, hostEntropy []byte) error {
	if err := addHostEntropy(hostEntropy); err != nil {
		resumeDiag(fmt.Sprintf("resume: addHostEntropy err=%v", err))
		return fmt.Errorf("resume: add entropy: %w", err)
	}
	if err := reseedFromHWRNG(); err != nil {
		resumeDiag(fmt.Sprintf("resume: reseedFromHWRNG err=%v", err))
		return fmt.Errorf("resume: reseed entropy: %w", err)
	}
	if err := stepClockTo(hostTimeUnixNano); err != nil {
		resumeDiag(fmt.Sprintf("resume: stepClockTo err=%v", err))
		return fmt.Errorf("resume: step clock: %w", err)
	}
	if err := writeUUIDMarker(hostTimeUnixNano); err != nil {
		resumeDiag(fmt.Sprintf("resume: writeUUIDMarker err=%v", err))
		return fmt.Errorf("resume: write uuid marker: %w", err)
	}
	return nil
}

// addHostEntropy injects the host-supplied bytes into /dev/urandom via
// ioctl(RNDADDENTROPY). Empty input is a no-op (still returns nil) — the
// cold-boot path never calls us, but a future caller might pass nil.
//
// A plain write mixes bytes without crediting the pool; RNDADDENTROPY also
// credits initialization entropy. Neither immediately rekeys an initialized
// Linux 6.1 CRNG. Both paths must finish with RNDRESEEDCRNG before any caller
// can observe readiness or draw the UUID marker (ADR-022).
func addHostEntropy(entropy []byte) error {
	if len(entropy) == 0 {
		return nil
	}
	if len(entropy) > 4096 {
		return fmt.Errorf("resume: host entropy %d bytes exceeds ioctl buffer cap", len(entropy))
	}
	fd, err := unix.Open(urandomPath, unix.O_RDWR, 0)
	if err != nil {
		resumeDiag(fmt.Sprintf("addHostEntropy: open err=%v", err))
		return fmt.Errorf("open %s: %w", urandomPath, err)
	}
	defer func() { _ = unix.Close(fd) }()

	pool := randPoolInfo{entropyCount: int32(len(entropy)) * 8, bufSize: int32(len(entropy))}
	copy(pool.buf[:], entropy)
	// SAFETY: pool lives on this goroutine's stack and the syscall is
	// synchronous — escape analysis won't move it before Syscall returns,
	// so unsafe.Pointer(&pool) is valid for the duration of the ioctl. Do
	// NOT extract this block into a helper that closes over pool, and do
	// NOT pass pool across a goroutine boundary.
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(rndaddentropyIoctl),
		uintptr(unsafe.Pointer(&pool)), //nolint:gosec // pool is on the calling goroutine's stack; syscall is synchronous, no escape. See SAFETY comment above.
	)
	if errno != 0 {
		// Some Firecracker guest kernels expose /dev/urandom but reject
		// RNDADDENTROPY for the guest's capability set. A host CSPRNG
		// write still mixes unique bytes into the pool (it does not claim
		// entropy credit), which is sufficient to prevent identical UUID
		// streams across restores. Keep failing closed for actual device
		// write failures rather than allowing a restore with no new input.
		if errno == unix.EPERM || errno == unix.EINVAL {
			fallback, openErr := os.OpenFile(urandomPath, os.O_WRONLY, 0)
			if openErr != nil {
				return fmt.Errorf("ioctl(RNDADDENTROPY): %w; fallback open %s: %w", errno, urandomPath, openErr)
			}
			_, writeErr := fallback.Write(entropy)
			_ = fallback.Close()
			if writeErr != nil {
				return fmt.Errorf("ioctl(RNDADDENTROPY): %w; fallback write: %w", errno, writeErr)
			}
			return reseedCRNG(fd)
		}
		return fmt.Errorf("ioctl(RNDADDENTROPY): %w", errno)
	}
	return reseedCRNG(fd)
}

// Linux 6.1 credit_init_bits is a no-op once the CRNG is initialized.
// RNDRESEEDCRNG extracts the newly mixed host input and advances the CRNG
// generation, invalidating snapshotted per-CPU random batches. Fail closed
// if the kernel or guest capabilities cannot perform this required step.
func reseedCRNG(fd int) error {
	if err := unix.IoctlSetInt(fd, unix.RNDRESEEDCRNG, 0); err != nil {
		return fmt.Errorf("ioctl(RNDRESEEDCRNG): %w", err)
	}
	return nil
}

// reseedFromHWRNG mixes up to reseedBytes currently available from virtio-rng.
// Host entropy and the explicit CRNG reseed have already made this restore
// unique. The optional hardware mix must not wait for a device refill before
// allowing the clock, UUID marker, and resume ACK to advance (ADR-148).
func reseedFromHWRNG() error {
	return mixHWRNG(hwrngPath, urandomPath)
}

// mixHWRNG takes explicit device paths so unavailable hardware can be exercised
// with a FIFO in unprivileged Linux tests.
func mixHWRNG(source, destination string) error {
	fd, err := unix.Open(source, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		// The host-supplied entropy is the authoritative uniqueness input.
		// Some otherwise valid Firecracker kernels omit CONFIG_HW_RANDOM_VIRTIO,
		// leaving /dev/hwrng absent even though the VM boots with virtio-rng
		// configured. Do not turn that optional belt-and-suspenders mix into a
		// restore outage; addHostEntropy has already re-keyed the pool before
		// this function runs. Other errors still fail closed so a real device
		// or filesystem problem is visible to the resume caller.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var entropy [reseedBytes]byte
	// Use the raw nonblocking syscall: os.File.Read can use Go's poller to
	// wait for readability after EAGAIN, defeating the device's O_NONBLOCK.
	n, err := unix.Read(fd, entropy[:])
	if err != nil && !errors.Is(err, unix.EAGAIN) {
		return fmt.Errorf("read %s: %w", source, err)
	}
	if n <= 0 {
		return nil
	}
	dst, err := os.OpenFile(destination, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", destination, err)
	}
	defer func() { _ = dst.Close() }()
	if _, err := dst.Write(entropy[:n]); err != nil {
		return fmt.Errorf("reseed write: %w", err)
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
