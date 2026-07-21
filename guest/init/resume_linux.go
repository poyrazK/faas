//go:build linux

package main

import (
	"fmt"
	"io"
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
	reseedBytes = 256            // enough to fully re-key the pool
	// resumeDiagLog is where the resume hook appends one JSON line per op,
	// visible to the operator and the §14 V6 metal test. /etc/faas/ is
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
// Layout MUST match the kernel — we use unsafe.Sizeof checks in unit tests if any
// are added later. The pool treats entropy_count as a credit against the SHA1
// pool's entropy estimate; setting it equal to len(buf)*8 fully credits every
// byte, which is what we want for fresh CSPRNG output.
type randPoolInfo struct {
	entropyCount int32
	bufSize      int32
	buf          [4096]byte // over-large; we only read first bufSize bytes
}

// RunResumeHook performs the post-restore hook: inject host-supplied CSPRNG
// bytes into /dev/urandom via ioctl(RNDADDENTROPY) FIRST so each restore
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
	resumeDiag(fmt.Sprintf("resume: start hostEntropy=%dB nano=%d", len(hostEntropy), hostTimeUnixNano))
	if err := addHostEntropy(hostEntropy); err != nil {
		resumeDiag(fmt.Sprintf("resume: addHostEntropy err=%v", err))
		return err
	}
	if err := reseedFromHWRNG(); err != nil {
		resumeDiag(fmt.Sprintf("resume: reseedFromHWRNG err=%v", err))
		return err
	}
	if err := stepClockTo(hostTimeUnixNano); err != nil {
		resumeDiag(fmt.Sprintf("resume: stepClockTo err=%v", err))
		return err
	}
	if err := writeUUIDMarker(hostTimeUnixNano); err != nil {
		resumeDiag(fmt.Sprintf("resume: writeUUIDMarker err=%v", err))
		return err
	}
	resumeDiag("resume: ok")
	return nil
}

// addHostEntropy injects the host-supplied bytes into /dev/urandom via
// ioctl(RNDADDENTROPY). Empty input is a no-op (still returns nil) — the
// cold-boot path never calls us, but a future caller might pass nil.
//
// NOTE: a plain write(2) to /dev/urandom does NOT credit the pool — the
// kernel silently treats it as a draw, not a deposit. The RNDADDENTROPY
// ioctl is the only public API that adds entropy. Without this ioctl, every
// restored guest's UUID would still be unique-by-virtio-rng — which is
// snapshot-shared, hence identical across restores (V6 fails). ADR-022.
func addHostEntropy(entropy []byte) error {
	if len(entropy) == 0 {
		resumeDiag("addHostEntropy: skipped (empty)")
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
	resumeDiag(fmt.Sprintf("addHostEntropy: about to ioctl bytes=%d head8=%x ec=%d bs=%d", len(entropy), entropy[:8], pool.entropyCount, pool.bufSize))
	// SAFETY: pool lives on this goroutine's stack and the syscall is
	// synchronous — escape analysis won't move it before Syscall returns,
	// so unsafe.Pointer(&pool) is valid for the duration of the ioctl. Do
	// NOT extract this block into a helper that closes over pool, and do
	// NOT pass pool across a goroutine boundary.
	r1, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(rndaddentropyIoctl),
		uintptr(unsafe.Pointer(&pool)), //nolint:gosec // pool is on the calling goroutine's stack; syscall is synchronous, no escape. See SAFETY comment above.
	)
	resumeDiag(fmt.Sprintf("addHostEntropy: ioctl r1=%d errno=%d", r1, errno))
	if errno != 0 {
		return fmt.Errorf("ioctl(RNDADDENTROPY): %w", errno)
	}
	return nil
}

// reseedFromHWRNG copies fresh virtio-rng bytes into /dev/urandom. Belt-and-
// suspenders on top of addHostEntropy: virtio-rng is snapshotted, but mixing
// a fresh (same-on-every-restore) input on top of a unique prefix keeps the
// pool advancing.
func reseedFromHWRNG() error {
	//nolint:forbidigo // hwrngPath is the virtio-rng chardev (/dev/hwrng by default) exposed to the microVM. Kernel-internal device, the customer has no write surface to a chardev on the guest side; symlink-attack impossible.
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
