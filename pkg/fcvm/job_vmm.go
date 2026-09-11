// Package fcvm — job-task VMM plumbing (issue #1184 Workstream A / ADR-099).
//
// Job VMs are a sibling workload class to app VMs. The shape is
// intentionally similar to ColdBootSpec + BootColdBoot (we re-use
// the per-instance chroot, jailer cgroup, vsock device, and the
// two-drive base/per-instance layout), with three deltas:
//
//  1. drive1 is the customer-supplied job IMAGE, not an app layer.
//     The StorageBackend key lives in JobColdBootSpec.ImageRef and
//     resolves through the same restoreSourceFromStorage path the
//     app path uses (single-backend semantic). The image is the
//     customer-prepared rootfs (OCI / Dockerfile build) they want
//     the command to run on top of.
//
//  2. There is NO readiness probe. The guest's job supervisor
//     (guest/init/job_supervisor_linux.go, M8) reads job.json, runs
//     the command, captures exit, writes the vsock STREAM, and powers
//     off. It never binds :8080 — SkipReady is forced.
//
//  3. The terminal exit envelope is a guest-initiated STREAM at port
//     1026. vmmd binds Firecracker's documented <uds_path>_<port>
//     endpoint before the VM starts, then validates the framed message.
//
// This file implements BootColdBootForJob + WaitJobExit on
// *JailerVMM, plus the Manager.BootJob / WaitJobExit wrappers that
// schedd calls. Cmd/vmmdgrpc/server.go exposes them as gRPC.

package fcvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// JobManifest is the JSON shape vmmd writes to drive1 at
// /etc/faas/job.json so guest-init's job supervisor can read the
// per-task argv/env/timeout without a separate wire channel.
//
// The schema is intentionally narrow (no sidecar fields, no
// deployment_id, no AppID — jobs don't have those). It mirrors
// /etc/faas/app.json's shape so guest-init's loadManifest (M8) can
// re-use the same parser with a top-level "kind":"job" discriminator.
//
// Fields map 1:1 to JobColdBootSpec:
//
//	{
//	  "kind":            "job",                         // fixed
//	  "account_id":      "...",                         // stamped from spec
//	  "run_id":          "...",                         // stamped from spec
//	  "task_index":      7,                             // stamped from spec
//	  "lease_token":     "...",                         // stamped from spec
//	  "image_ref":       "oci://...",                   // echoed from spec
//	  "command":         ["/bin/sh", "-c", "..."],     // argv form
//	  "env":             {"KEY":"VAL", ...},            // merged per M5 plan
//	  "task_timeout_s":  300,                           // per-task cap
//	  "vsock_job_exit_port":      1026,                 // fixed
//	  "vsock_job_exit_msg_type":  4                     // fixed
//	}
type JobManifest struct {
	Kind                string            `json:"kind"`
	AccountID           string            `json:"account_id,omitempty"`
	RunID               string            `json:"run_id,omitempty"`
	TaskIndex           int               `json:"task_index,omitempty"`
	LeaseToken          string            `json:"lease_token,omitempty"`
	ImageRef            string            `json:"image_ref,omitempty"`
	Command             []string          `json:"command,omitempty"`
	Env                 map[string]string `json:"env,omitempty"`
	TaskTimeoutSec      int               `json:"task_timeout_s,omitempty"`
	VsockJobExitPort    int               `json:"vsock_job_exit_port,omitempty"`
	VsockJobExitMsgType int               `json:"vsock_job_exit_msg_type,omitempty"`
}

// JobExitPayload is the JSON the guest-init supervisor writes via
// STREAM (port 1026, msg_type 4) when the customer's command exits.
//
// The schema is the inverse of cmd/vmmdgrpc/proto.go::JobExit
// notification: schedd's HandleJobExit consumes the same shape
// over gRPC, so the field names line up exactly — the wire format
// is the wire format (no separate DTO per transport).
//
// ErrorClass is the canonical mapped string from the supervisor's
// signal/exit-code table:
//
//	"succeeded"        // exit_code == 0
//	"failed"           // exit_code != 0 && < 128  → user_error in some flows
//	"timeout"          // exit_code == 124 (coreutils `timeout` sentinel)
//	"oom"              // exit_code == 137 && OOM-killer confirmed
//	"cancelled"        // exit_code == 143 (SIGTERM, after 30s grace)
//	"infra"            // signal > 0 (SIGSEGV, etc.) OR reaper took over
//
// Signal is the raw signal number if the process was killed by a
// signal (Go's syscall.WaitStatus.Signaled() & Signaled()), else 0.
// FinishedAtUnixNano is the supervisor's monotonic → wall clock
// converted to UnixNano; schedd stamps it on job_tasks.exit_at.
type JobExitPayload struct {
	ExitCode           int32  `json:"exit_code"`
	ErrorClass         string `json:"error_class"`
	Signal             int32  `json:"signal"`
	FinishedAtUnixNano int64  `json:"finished_at_unix_nano"`
	LeaseToken         string `json:"lease_token"`
}

// Validate is the host-side gate; same shape as ColdBootSpec.Validate.
// Per the "load-bearing invariants" list at the top of vmm.go, the
// chroot is on tmpfs and we MUST reject early so a malformed wire
// call doesn't leave a half-built instance.
//
// Differences from ColdBootSpec.Validate:
//   - LayerKey is NOT required (job image is a separate field).
//   - ImageRef is required.
//   - TaskTimeoutSec must be > 0 (the supervisor enforces it; the
//     engine computes lease_expires_at from it).
//   - LeaseToken must be non-empty (HandleJobExit uses it to CAS
//     the row transition; an empty token would always reject).
//   - Tap is still required (every guest gets one — ADR-009).
func (s JobColdBootSpec) Validate() error {
	switch {
	case s.KernelKey == "":
		return fmt.Errorf("fcvm: job cold boot: empty kernel key")
	case s.BaseKey == "":
		return fmt.Errorf("fcvm: job cold boot: empty base rootfs key")
	case s.ImageRef == "":
		return fmt.Errorf("fcvm: job cold boot: empty image ref")
	case len(s.ImageRef) > 2048:
		return fmt.Errorf("fcvm: job cold boot: image ref exceeds 2048 bytes")
	case s.AccountID == "" || len(s.AccountID) > 128:
		return fmt.Errorf("fcvm: job cold boot: invalid account id")
	case s.RunID == "" || len(s.RunID) > 128:
		return fmt.Errorf("fcvm: job cold boot: invalid run id")
	case s.TaskIndex < 0:
		return fmt.Errorf("fcvm: job cold boot: negative task index")
	case len(s.Command) == 0:
		return fmt.Errorf("fcvm: job cold boot: empty command")
	case len(s.Command) > 64:
		return fmt.Errorf("fcvm: job cold boot: command has %d args (>64)", len(s.Command))
	case s.Command[0] == "":
		return fmt.Errorf("fcvm: job cold boot: empty executable")
	case s.TaskTimeoutSec <= 0:
		return fmt.Errorf("fcvm: job cold boot: task_timeout_s %d must be > 0", s.TaskTimeoutSec)
	case s.TaskTimeoutSec > JobMaxTaskTimeoutSec:
		return fmt.Errorf("fcvm: job cold boot: task_timeout_s %d exceeds cap %d", s.TaskTimeoutSec, JobMaxTaskTimeoutSec)
	case s.LeaseToken == "":
		return fmt.Errorf("fcvm: job cold boot: empty lease token")
	case len(s.LeaseToken) > 1024:
		return fmt.Errorf("fcvm: job cold boot: lease token exceeds 1024 bytes")
	case len(s.Env) > 256:
		return fmt.Errorf("fcvm: job cold boot: env has %d entries (>256)", len(s.Env))
	case s.VcpuCount < 1:
		return fmt.Errorf("fcvm: job cold boot: vcpu_count %d < 1", s.VcpuCount)
	case s.MemSizeMiB < 1:
		return fmt.Errorf("fcvm: job cold boot: mem_size_mib %d < 1", s.MemSizeMiB)
	case s.Tap == "":
		return fmt.Errorf("fcvm: job cold boot: empty tap device")
	}
	for i, arg := range s.Command {
		if strings.IndexByte(arg, 0) >= 0 {
			return fmt.Errorf("fcvm: job cold boot: command arg %d contains NUL", i)
		}
	}
	for key, value := range s.Env {
		if key == "" || len(key) > 128 || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("fcvm: job cold boot: invalid env key %q", key)
		}
		if len(value) > 32*1024 || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("fcvm: job cold boot: env value for %q is invalid or too large", key)
		}
	}
	return nil
}

// JobMaxTaskTimeoutSec is the host-side ceiling for a per-task
// wall-clock cap. The supervisor enforces it (SIGTERM at the
// deadline, SIGKILL after 30s grace); schedd also uses it to set
// job_tasks.lease_expires_at. The cap is larger than the largest
// plan-permitted value (Scale: 3600s) by a generous 50% so a
// caller bumping the plan cap doesn't immediately violate the
// host ceiling. Matches pkg/api/limits.go::JobTaskTimeoutSec[3]=3600
// and adds 1800s of headroom for the SIGTERM→SIGKILL grace window.
const JobMaxTaskTimeoutSec = 5400

// JobManifestMaxBytes bounds per-task configuration staged into the guest.
// The plan maximum is 256 values of 32 KiB; the extra headroom accommodates
// JSON escaping while keeping an internal gRPC caller from making vmmd stage
// an unbounded manifest.
const JobManifestMaxBytes = 16 * 1024 * 1024

const (
	VsockJobControlPort        = 1028
	VsockJobCancelMsgType      = 5
	vsockJobControlAckOK  byte = 0
)

// JobDestroyWaitDefault is the default firecracker destroy timeout
// for job VMs. The legacy app-VM default is 11 minutes; for jobs
// the cap is min(task_timeout_s + 90s, JobDestroyWaitDefault). The
// +90s covers the SIGTERM→30s grace→SIGKILL cleanup budget. For
// the typical Hobby 300s task, that's 390s < 11min — so most jobs
// use the smaller cap and destroy faster on timeout.
//
// Picked at 30 minutes (vs the app-VM 11m) so a Scale 3600s task
// fits: 3600 + 90 = 3690s ≈ 61.5min — well above 30m. The engine
// uses EffectiveDestroyWait(taskTimeoutSec) at job.VMM call time
// rather than this constant; this is the upper bound only.
const JobDestroyWaitDefault = 30 * time.Minute

// EffectiveDestroyWait returns the destroy timeout the engine
// should pass to vmmdgrpc at job wake time. Mirrors
// the per-task wall-clock cap + cleanup grace + host ceiling
// formula at engine.go::WakeJob.
//
// The +90s covers:
//   - 30s guest SIGTERM→SIGKILL grace (M8 supervisor)
//   - 30s firecracker /snapshot/create or clean Kill teardown
//   - 30s buffer for slow disks / cgroup writes
//
// Cap at JobDestroyWaitDefault (30m) so a misconfigured huge
// task_timeout_s doesn't pin a jail slot for hours. Production
// Scale cap = 3600s → 3690s; comfortably below the 30m ceiling.
func EffectiveDestroyWait(taskTimeoutSec int) time.Duration {
	d := time.Duration(taskTimeoutSec+90) * time.Second
	if d > JobDestroyWaitDefault {
		return JobDestroyWaitDefault
	}
	return d
}

// BootColdBootForJob is the VMM-interface entry point for job-task
// cold boot. It materializes the kernel/base/image StorageBackend
// keys into the chroot's tmp paths, writes /etc/faas/job.json on
// the per-job layer, builds the VMConfig, and delegates to
// bootNoWait (SkipReady=true — jobs don't expose :8080).
//
// Parallel to BootColdBoot (line 360). The differences are:
//   - Spec type is JobColdBootSpec (no LayerKey; ImageRef + Command + Env).
//   - The manifest written to drive1 is JobManifest, not WorkloadRoster.
//   - Ready is skipped (the supervisor exits; no :8080 to bind).
//
// Implemented on JailerVMM only. Tests that drive Boot directly
// with a fully-resolved VMConfig don't go through this entry.
func (v *JailerVMM) BootColdBootForJob(ctx context.Context, l Lease, spec JobColdBootSpec) (err error) {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("vmm: job cold boot: %w", err)
	}
	originalImageRef := spec.ImageRef
	kernelSrc, err := v.restoreSourceFromStorage(ctx, l.Instance, spec.KernelKey)
	if err != nil {
		return fmt.Errorf("vmm: stage kernel: %w", err)
	}
	baseSrc, err := v.restoreSourceFromStorage(ctx, l.Instance, spec.BaseKey)
	if err != nil {
		return fmt.Errorf("vmm: stage base: %w", err)
	}
	imageSrc, err := v.restoreSourceFromStorage(ctx, l.Instance, spec.ImageRef)
	if err != nil {
		return fmt.Errorf("vmm: stage image: %w", err)
	}
	spec.KernelKey = kernelSrc
	spec.BaseKey = baseSrc
	spec.ImageRef = imageSrc

	manifest := JobManifest{
		Kind:                "job",
		AccountID:           spec.AccountID,
		RunID:               spec.RunID,
		TaskIndex:           spec.TaskIndex,
		LeaseToken:          spec.LeaseToken,
		ImageRef:            originalImageRef,
		Command:             spec.Command,
		Env:                 spec.Env,
		TaskTimeoutSec:      spec.TaskTimeoutSec,
		VsockJobExitPort:    VsockJobExitPort,
		VsockJobExitMsgType: VsockJobExitMsgType,
	}

	// bootNoWait provisions a private drive1 first, then stages the manifest and
	// binds the guest-initiated vsock listener before Firecracker receives its
	// config. A short job therefore cannot beat the host listener, and the
	// customer image/cache is never modified to carry per-run state.
	return v.bootNoWait(ctx, l, BuildJobColdBootConfig(spec, l.Slot), nil, nil, nil, "", &manifest)
}

// stageJobManifest writes the JSON-encoded JobManifest to the private drive1
// at /etc/faas/job.json. The loop mount lives outside the chroot so a failed
// unmount can never make chroot cleanup traverse and delete a mounted image.
//
// Idempotent on overwrite: a second write to the same path
// truncates and replaces (rare; the chroot is per-instance so the
// path is unique). Best-effort umount on error so a partial write
// doesn't leak the mount.
//
// CR-C / code-review #2 round-3: the previous shape stats
// "drive1.img" which is NOT the canonical in-chroot drive1 image
// — vmm.go::mkChroot / stageEphemeralWritableAs provisions
// `layerImageName` (constant defined in vmm.go:1524) inside the
// chroot. Stat'ing drive1.img always returns ENOENT, every job
// boot fails, the guest never sees /etc/faas/job.json, vsock
// never gets a job_exit frame. Fix: stat the canonical name.
func (v *JailerVMM) stageJobManifest(instance string, m JobManifest) (retErr error) {
	if v.chrootBase == "" {
		return fmt.Errorf("vmm: stageJobManifest: chrootBase not configured")
	}
	root := v.chrootRoot(instance)
	drive1Img := filepath.Join(root, layerImageName)
	if _, err := os.Stat(drive1Img); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: %s missing at %s: %w", layerImageName, drive1Img, err)
	}
	mnt, err := os.MkdirTemp("", "faas-job-manifest-*")
	if err != nil {
		return fmt.Errorf("vmm: stageJobManifest: create mountpoint: %w", err)
	}
	mounted := false
	defer func() {
		if !mounted {
			_ = os.Remove(mnt)
		}
	}()
	if out, err := exec.Command("mount", "-o", "loop,rw,nodev,nosuid,noexec", drive1Img, mnt).CombinedOutput(); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: mount: %w: %s", err, string(out))
	}
	mounted = true
	defer func() {
		if out, err := exec.Command("umount", mnt).CombinedOutput(); err != nil {
			unmountErr := fmt.Errorf("vmm: stageJobManifest: umount: %w: %s", err, string(out))
			if lazyOut, lazyErr := exec.Command("umount", "-l", mnt).CombinedOutput(); lazyErr != nil {
				unmountErr = errors.Join(unmountErr, fmt.Errorf("lazy umount: %w: %s", lazyErr, string(lazyOut)))
			} else {
				mounted = false
			}
			retErr = errors.Join(retErr, unmountErr)
		} else {
			mounted = false
		}
		if !mounted {
			_ = os.Remove(mnt)
		}
	}()

	etc, err := ensureJobManifestDirectory(mnt)
	if err != nil {
		return fmt.Errorf("vmm: stageJobManifest: prepare etc/faas: %w", err)
	}
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("vmm: stageJobManifest: marshal: %w", err)
	}
	if len(blob) > JobManifestMaxBytes {
		return fmt.Errorf("vmm: stageJobManifest: manifest is %d bytes (max %d)", len(blob), JobManifestMaxBytes)
	}
	tmp, err := os.CreateTemp(etc, ".job.json-*")
	if err != nil {
		return fmt.Errorf("vmm: stageJobManifest: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vmm: stageJobManifest: chmod temp: %w", err)
	}
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vmm: stageJobManifest: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vmm: stageJobManifest: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: close temp: %w", err)
	}
	dst := filepath.Join(etc, "job.json")
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: publish %s: %w", dst, err)
	}
	dir, err := os.Open(etc)
	if err != nil {
		return fmt.Errorf("vmm: stageJobManifest: open parent: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("vmm: stageJobManifest: sync parent: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: close parent: %w", err)
	}
	return nil
}

// ensureJobManifestDirectory creates the fixed platform-owned directory one
// component at a time and refuses image-provided symlinks. The private image
// is not attached to a guest yet, so these Lstat/create checks cannot race a
// customer process; rejecting links prevents an absolute /etc or /etc/faas
// symlink from redirecting vmmd's root write into the host filesystem.
func ensureJobManifestDirectory(mountpoint string) (string, error) {
	current := mountpoint
	for _, component := range []string{"etc", "faas"} {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%s is not a real directory", current)
		}
	}
	return current, nil
}

// BuildJobColdBootConfig builds the Firecracker config for a job
// VM. Mirrors BuildColdBootConfig (line 274) but emits only one
// per-instance drive (drive1 = a private clone/copy of the customer image).
//
// Drive ordering matches the app path: drive0 = shared read-only
// base rootfs, drive1 = per-job image. guest-init's overlayfs
// assembly reads the drive ids (base + layer-main) and unions
// them in the same order it unions app layers today — no
// guest-init change is needed for the storage topology.
//
// The vsock device is the same per-slot device the app path uses;
// the supervisor piggybacks on the same vsock UDS at port 1026
// (msg_type 4). NetIface is the same eth0/tap0 (ADR-009).
//
// We do NOT attach characterize support — the job supervisor
// never writes a CharacterizationReport, and the WaitCharacterizationReport
// caller (Manager.Wake) is in the app path, not the job path.
func BuildJobColdBootConfig(s JobColdBootSpec, slot int) VMConfig {
	drives := []Drive{
		{DriveID: DriveBase, PathOnHost: s.BaseKey, IsRootDevice: true, IsReadOnly: true},
		{DriveID: DriveLayer, PathOnHost: s.ImageRef, IsRootDevice: false, IsReadOnly: false},
	}
	return VMConfig{
		BootSource: BootSource{
			KernelImagePath: s.KernelKey,
			// Same boot args as the app path. The guest's
			// decideMode (M8) reads /etc/faas/{app,job}.json to
			// pick runApp vs runJob; the kernel cmdline is
			// unchanged.
			BootArgs: coldBootArgs,
		},
		Drives:        drives,
		MachineConfig: Machine{VcpuCount: s.VcpuCount, MemSizeMib: s.MemSizeMiB, Smt: false},
		NetworkInterfaces: []NetIface{
			{IfaceID: "eth0", HostDevName: s.Tap},
		},
		Entropy:     &Entropy{},
		VsockDevice: NewVsockDevice(slot),
	}
}

// jobExitUDSSock is Firecracker's documented host endpoint for a
// guest-initiated connection: <configured uds_path>_<destination port>.
func (v *JailerVMM) jobExitUDSSock(instance string) string {
	return fmt.Sprintf("%s_%d", v.vsockUDSSock(instance), VsockJobExitPort)
}

// prepareJobExitListener binds the host endpoint before Firecracker starts.
// Short-lived jobs can otherwise finish all retries before schedd issues its
// WaitJobExit RPC. The socket is owned by the jailer identity because the
// unprivileged Firecracker process is the connecting peer.
func (v *JailerVMM) prepareJobExitListener(l Lease) error {
	if v == nil || l.Instance == "" {
		return fmt.Errorf("invalid VMM or empty instance")
	}
	v.closeJobExitListener(l.Instance)
	path := v.jobExitUDSSock(l.Instance)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen %s: %w", path, err)
	}
	ln.SetUnlinkOnClose(true)
	cleanup := func() {
		_ = ln.Close()
		_ = os.Remove(path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := chownJail(path, l.UID, l.GID); err != nil {
		cleanup()
		return err
	}
	v.mu.Lock()
	if v.jobExitListeners == nil {
		v.jobExitListeners = make(map[string]*net.UnixListener)
	}
	v.jobExitListeners[l.Instance] = ln
	v.mu.Unlock()
	return nil
}

func (v *JailerVMM) closeJobExitListener(instance string) {
	if v == nil || instance == "" {
		return
	}
	v.mu.Lock()
	ln := v.jobExitListeners[instance]
	delete(v.jobExitListeners, instance)
	v.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	_ = os.Remove(v.jobExitUDSSock(instance))
}

// WaitJobExit accepts the first valid guest-initiated STREAM on the listener
// prepared during cold boot. Invalid frames are rejected with a bounded retry
// budget so untrusted guest bytes cannot allocate unbounded memory or spin a
// host goroutine forever.
func (v *JailerVMM) WaitJobExit(ctx context.Context, l Lease, deadline time.Duration) (JobExitPayload, error) {
	var zero JobExitPayload
	if v == nil {
		return zero, fmt.Errorf("vmm: WaitJobExit: nil receiver")
	}
	if l.Instance == "" {
		return zero, fmt.Errorf("vmm: WaitJobExit: empty instance")
	}
	if v.chrootBase == "" {
		return zero, fmt.Errorf("vmm: WaitJobExit: chrootBase not configured")
	}
	if deadline <= 0 {
		return zero, fmt.Errorf("vmm: WaitJobExit: deadline must be positive")
	}
	v.mu.Lock()
	ln := v.jobExitListeners[l.Instance]
	v.mu.Unlock()
	if ln == nil {
		return zero, fmt.Errorf("vmm: WaitJobExit: listener for %s was not prepared", l.Instance)
	}
	defer v.closeJobExitListener(l.Instance)

	end := time.Now().Add(deadline)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(end) {
		end = ctxDeadline
	}
	const maxInvalidFrames = 8
	var lastErr error
	for invalid := 0; invalid < maxInvalidFrames; {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		now := time.Now()
		if !now.Before(end) {
			if lastErr != nil {
				return zero, fmt.Errorf("vmm: WaitJobExit: deadline after invalid frame: %w", lastErr)
			}
			return zero, context.DeadlineExceeded
		}
		pollDeadline := now.Add(200 * time.Millisecond)
		if end.Before(pollDeadline) {
			pollDeadline = end
		}
		if err := ln.SetDeadline(pollDeadline); err != nil {
			return zero, fmt.Errorf("vmm: WaitJobExit: set accept deadline: %w", err)
		}
		conn, err := ln.AcceptUnix()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			return zero, fmt.Errorf("vmm: WaitJobExit: accept: %w", err)
		}
		_ = conn.SetDeadline(end)
		payload, readErr := readJobExitEnvelope(conn)
		if readErr != nil {
			lastErr = readErr
			invalid++
			_, _ = conn.Write([]byte{1})
			_ = conn.Close()
			continue
		}
		// Receipt is durable at the scheduler boundary, not here. The ack only
		// tells guest-init that vmmd parsed a complete, valid frame; a failed ack
		// must not discard a terminal result that is already in memory.
		_, _ = conn.Write([]byte{0})
		_ = conn.Close()
		return payload, nil
	}
	return zero, fmt.Errorf("vmm: WaitJobExit: too many invalid frames: %w", lastErr)
}

func readJobExitEnvelope(conn io.Reader) (JobExitPayload, error) {
	var zero JobExitPayload
	var hdr [8]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return zero, fmt.Errorf("read frame header: %w", err)
	}
	msgType := binary.BigEndian.Uint32(hdr[:4])
	bodyLen := binary.BigEndian.Uint32(hdr[4:8])
	if msgType != uint32(VsockJobExitMsgType) {
		return zero, fmt.Errorf("msg_type=%d, want %d", msgType, VsockJobExitMsgType)
	}
	if bodyLen == 0 || bodyLen > VsockJobExitMaxBody {
		return zero, fmt.Errorf("body_len=%d out of range (0, %d]", bodyLen, VsockJobExitMaxBody)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return zero, fmt.Errorf("read body (%d bytes): %w", bodyLen, err)
	}
	var payload JobExitPayload
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return zero, fmt.Errorf("parse JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return zero, fmt.Errorf("parse JSON: trailing content")
	}
	if err := validateJobExitPayload(payload); err != nil {
		return zero, err
	}
	return payload, nil
}

func validateJobExitPayload(payload JobExitPayload) error {
	if payload.ExitCode < 0 || payload.ExitCode > 255 {
		return fmt.Errorf("exit_code=%d out of range", payload.ExitCode)
	}
	if payload.Signal < 0 || payload.Signal > 64 {
		return fmt.Errorf("signal=%d out of range", payload.Signal)
	}
	switch payload.ErrorClass {
	case "succeeded":
		if payload.ExitCode != 0 || payload.Signal != 0 {
			return fmt.Errorf("succeeded payload has exit_code=%d signal=%d", payload.ExitCode, payload.Signal)
		}
	case "timeout":
		if payload.ExitCode != 124 {
			return fmt.Errorf("timeout payload has exit_code=%d, want 124", payload.ExitCode)
		}
	case "oom":
		if payload.ExitCode != 137 {
			return fmt.Errorf("oom payload has exit_code=%d, want 137", payload.ExitCode)
		}
	case "cancelled", "failed", "infra":
		if payload.ExitCode == 0 {
			return fmt.Errorf("%s payload has successful exit_code", payload.ErrorClass)
		}
	default:
		return fmt.Errorf("unsupported error_class %q", payload.ErrorClass)
	}
	if payload.FinishedAtUnixNano <= 0 {
		return fmt.Errorf("finished_at_unix_nano must be positive")
	}
	if payload.LeaseToken == "" || len(payload.LeaseToken) > 1024 {
		return fmt.Errorf("lease_token length %d out of range", len(payload.LeaseToken))
	}
	return nil
}

// SignalJob delivers graceful cancellation to guest-init. StopInstance cannot
// signal the customer by signalling the Firecracker host process; it must use a
// host-initiated vsock connection to the supervisor inside the VM.
func (v *JailerVMM) SignalJob(ctx context.Context, l Lease, signal syscall.Signal, grace time.Duration) (bool, int32, error) {
	// Preserve StopInstance's immediate-stop contract. A zero grace or explicit
	// SIGKILL must not silently become a 30-second graceful stop for job VMs.
	if grace <= 0 || signal == syscall.SIGKILL {
		return v.SignalAndKill(ctx, l, 0, 0)
	}
	if signal == 0 {
		signal = syscall.SIGTERM
	}
	if err := v.signalJobGuest(ctx, l, signal); err != nil {
		// A guest without the control listener is still cancellable. Fall back to
		// killing Firecracker and surface the fallback through killSignalSent.
		killed, code, killErr := v.SignalAndKill(context.WithoutCancel(ctx), l, 0, 0)
		if killErr != nil {
			return killed, code, errors.Join(err, killErr)
		}
		return true, code, nil
	}

	v.mu.Lock()
	rec := v.recs[l.Instance]
	v.mu.Unlock()
	if rec == nil || rec.done == nil {
		return false, 0, fmt.Errorf("vmm: signal job %s: process record missing", l.Instance)
	}
	timer := time.NewTimer(grace + 5*time.Second)
	defer timer.Stop()
	select {
	case <-rec.done:
		v.mu.Lock()
		code := int32(rec.exitCode)
		v.mu.Unlock()
		return false, code, nil
	case <-ctx.Done():
	case <-timer.C:
	}
	killed, code, err := v.SignalAndKill(context.WithoutCancel(ctx), l, 0, 0)
	return killed, code, err
}

func (v *JailerVMM) signalJobGuest(ctx context.Context, l Lease, signal syscall.Signal) error {
	if v == nil || l.Instance == "" || v.chrootBase == "" {
		return fmt.Errorf("vmm: signal job: invalid VMM or instance")
	}
	switch signal {
	case syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP:
	default:
		return fmt.Errorf("vmm: signal job: unsupported signal %d", signal)
	}
	end := time.Now().Add(resumeHookDialDeadline)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(end) {
		end = deadline
	}
	lastErr := context.DeadlineExceeded
	for time.Now().Before(end) {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := net.DialTimeout("unix", v.vsockUDSSock(l.Instance), 200*time.Millisecond)
		if err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(resumeHookDialStep):
			}
			continue
		}
		remaining := time.Until(end)
		_ = conn.SetDeadline(time.Now().Add(remaining))
		if err = writeJobControlFrame(conn, []byte(fmt.Sprintf("CONNECT %d\n", VsockJobControlPort))); err == nil {
			var ack string
			ack, err = readConnectAck(conn)
			if err == nil && ack != "OK" {
				err = fmt.Errorf("CONNECT rejected: %q", ack)
			}
		}
		if err == nil {
			var frame [8]byte
			binary.BigEndian.PutUint32(frame[:4], VsockJobCancelMsgType)
			binary.BigEndian.PutUint32(frame[4:], uint32(signal))
			err = writeJobControlFrame(conn, frame[:])
		}
		if err == nil {
			var ack [1]byte
			_, err = io.ReadFull(conn, ack[:])
			if err == nil && ack[0] != vsockJobControlAckOK {
				err = fmt.Errorf("guest rejected cancellation")
			}
		}
		_ = conn.Close()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(resumeHookDialStep):
		}
	}
	return fmt.Errorf("vmm: signal job %s: %w", l.Instance, lastErr)
}

func writeJobControlFrame(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrUnexpectedEOF
		}
		payload = payload[n:]
	}
	return nil
}

// VsockJobExitMaxBody caps the JSON body at 8 KiB. Job exit
// envelopes are tiny (exit_code + error_class + signal +
// finished_at + lease_token ≈ 200 bytes); 8 KiB is generous
// headroom for a future field addition without protocol
// renegotiation. Oversize frames are rejected rather than truncated.
const VsockJobExitMaxBody = 8 * 1024
