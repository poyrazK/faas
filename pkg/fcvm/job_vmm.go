// Package fcvm — job-task VMM plumbing (issue #1184 Workstream A / ADR-099).
//
// Job VMs are a sibling workload class to app VMs. The shape is
// intentionally similar to ColdBootSpec + BootColdBoot (we re-use
// the per-instance chroot, jailer cgroup, vsock device, and the
// two-drive base/per-instance layout), with three deltas:
//
//  1. drive1 is the materialized job IMAGE, not an app layer. The
//     StorageBackend key lives in JobColdBootSpec.ImageRef and
//     resolves through the same restoreSourceFromStorage path the
//     app path uses (single-backend semantic). imaged publishes the
//     OCI-derived ext4 under a job-owned immutable storage key before dispatch.
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
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
	"github.com/onebox-faas/faas/pkg/ociidentity"
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
//	  "image_ref":       "jobs/<job-id>__<attempt-id>.ext4", // key echoed from spec
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
	StartHeld           bool              `json:"start_held,omitempty"`
	VsockJobExitPort    int               `json:"vsock_job_exit_port,omitempty"`
	VsockJobExitMsgType int               `json:"vsock_job_exit_msg_type,omitempty"`
	// Interactive is set only for an interactive image run (ADR-1330). A
	// guest-init that predates it rejects the unknown field and fails the
	// task rather than running the command without stdin.
	Interactive *JobInteractive `json:"interactive,omitempty"`
	// ImageProcess runs the command with the image's ENV, USER and WORKDIR
	// from /etc/faas/app.json (ADR-1790); User and Workdir override them.
	ImageProcess bool   `json:"image_process,omitempty"`
	User         string `json:"user,omitempty"`
	Workdir      string `json:"workdir,omitempty"`
}

// JobInteractive is ADR-1330's guest contract: the supervisor accepts one
// session on the app-task session port within the attach window, then runs
// the job command with the session's terminal or pipes as its stdio.
type JobInteractive struct {
	TTY              bool `json:"tty,omitempty"`
	AttachTimeoutSec int  `json:"attach_timeout_s"`
}

const (
	// JobInteractiveDefaultAttachTimeout is the attach window when the caller
	// does not choose one; JobInteractiveMaxAttachTimeout caps it.
	JobInteractiveDefaultAttachTimeout = 60 * time.Second
	JobInteractiveMaxAttachTimeout     = 5 * time.Minute
	// JobInteractiveMaxTaskTimeoutSec is the session limit, matching the
	// guest session protocol's hard range.
	JobInteractiveMaxTaskTimeoutSec = 3600
)

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
	ExitCode           int32           `json:"exit_code"`
	ErrorClass         string          `json:"error_class"`
	Signal             int32           `json:"signal"`
	FinishedAtUnixNano int64           `json:"finished_at_unix_nano"`
	LeaseToken         string          `json:"lease_token"`
	OutputManifest     json.RawMessage `json:"output_manifest,omitempty"`
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
	case len(s.SecretsEnvJSON) > JobManifestMaxBytes:
		return fmt.Errorf("fcvm: job cold boot: sealed env projection exceeds %d bytes", JobManifestMaxBytes)
	case s.VcpuCount < 1:
		return fmt.Errorf("fcvm: job cold boot: vcpu_count %d < 1", s.VcpuCount)
	case s.MemSizeMiB < 1:
		return fmt.Errorf("fcvm: job cold boot: mem_size_mib %d < 1", s.MemSizeMiB)
	case s.Tap == "":
		return fmt.Errorf("fcvm: job cold boot: empty tap device")
	}
	if err := s.validateImageProcess(); err != nil {
		return err
	}
	if in := s.Interactive; in != nil {
		switch {
		case s.StartHeld:
			return fmt.Errorf("fcvm: job cold boot: an interactive job cannot be start-held")
		case s.TaskTimeoutSec > JobInteractiveMaxTaskTimeoutSec:
			return fmt.Errorf("fcvm: job cold boot: interactive task_timeout_s %d exceeds %d", s.TaskTimeoutSec, JobInteractiveMaxTaskTimeoutSec)
		case in.AttachTimeoutSec < 1 || time.Duration(in.AttachTimeoutSec)*time.Second > JobInteractiveMaxAttachTimeout:
			return fmt.Errorf("fcvm: job cold boot: attach_timeout_s %d is outside 1..%d", in.AttachTimeoutSec, int(JobInteractiveMaxAttachTimeout/time.Second))
		}
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

// validateImageProcess checks ADR-1790's overrides, which apply only to an
// image process.
func (s JobColdBootSpec) validateImageProcess() error {
	if !s.ImageProcess {
		if s.ProcessUser != "" || s.ProcessWorkdir != "" {
			return fmt.Errorf("fcvm: job cold boot: a process user or workdir requires an image process")
		}
		return nil
	}
	if len(s.ProcessUser) > api.ImageRunUserMaxBytes || ociidentity.ValidateSpec(s.ProcessUser) != nil {
		return fmt.Errorf("fcvm: job cold boot: invalid process user")
	}
	if err := api.ValidateImageRunWorkdir(s.ProcessWorkdir); err != nil {
		return fmt.Errorf("fcvm: job cold boot: %w", err)
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
	VsockJobControlPort          = 1028
	VsockJobCancelMsgType        = 5
	VsockJobStartMsgType         = 6
	vsockJobControlAckOK    byte = 0
	vsockJobControlAckError byte = 1
)

// JobDestroyWaitDefault is the default firecracker destroy timeout
// for job VMs. The legacy app-VM default is 11 minutes; for jobs
// the cap is min(task_timeout_s + 90s, JobDestroyWaitDefault). The
// +90s covers the SIGTERM→30s grace→SIGKILL cleanup budget. For
// the typical Hobby 300s task, that's 390s < 11min — so most jobs
// use the smaller cap and destroy faster on timeout. Bound the wait by
// the host's accepted task timeout plus cleanup grace, not 30 minutes:
// the old ceiling ended a valid Scale 3600s task 30 minutes early.
const JobDestroyWaitDefault = time.Duration(JobMaxTaskTimeoutSec+90) * time.Second

// JobStartGateTimeout is the bounded pre-execution window for held Jobs.
// guest/init uses the same 90-second bound and exits with an infrastructure
// receipt if the scheduler never releases the start gate.
const JobStartGateTimeout = 90 * time.Second

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
// Cap at JobDestroyWaitDefault so an invalid oversized timeout cannot
// pin a jail slot beyond the host's accepted maximum. Production Scale
// cap = 3600s → 3690s, below the 5490s host ceiling.
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
	if err := v.prepareJournalLaunch(l); err != nil {
		return err
	}
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
		StartHeld:           spec.StartHeld,
		VsockJobExitPort:    VsockJobExitPort,
		VsockJobExitMsgType: VsockJobExitMsgType,
		Interactive:         spec.Interactive,
		ImageProcess:        spec.ImageProcess,
		User:                spec.ProcessUser,
		Workdir:             spec.ProcessWorkdir,
	}

	// bootNoWait provisions a private drive1 first, then stages the manifest and
	// binds the guest-initiated vsock listener before Firecracker receives its
	// config. A short job therefore cannot beat the host listener, and the
	// customer image/cache is never modified to carry per-run state.
	return v.bootNoWait(ctx, l, BuildJobColdBootConfig(spec, l.Slot), nil, spec.SecretsEnvJSON, nil, "", false, &manifest, nil)
}

// stageJobManifest writes the JSON-encoded JobManifest to the private drive1
// at /etc/faas/job.json, mode 0600, in one sandboxed drive session
// (ADR-20261025090100). A second write to the same path replaces the file
// (rare; the chroot is per-instance so the path is unique).
//
// CR-C / code-review #2 round-3: the previous shape stats
// "drive1.img" which is NOT the canonical in-chroot drive1 image
// — vmm.go::mkChroot / stageEphemeralWritableAs provisions
// `layerImageName` (constant defined in vmm.go:1524) inside the
// chroot. Stat'ing drive1.img always returns ENOENT, every job
// boot fails, the guest never sees /etc/faas/job.json, vsock
// never gets a job_exit frame. Fix: stat the canonical name.
func (v *JailerVMM) stageJobManifest(instance string, m JobManifest) error {
	ctx, cancel := v.driveStagingContext()
	defer cancel()
	owner, err := v.nativeDriveStagingOwner(ctx, instance)
	if err != nil {
		return err
	}
	return v.stageJobManifestForOwner(ctx, owner, instance, m)
}

func (v *JailerVMM) stageJobManifestForOwner(ctx context.Context, owner nativeLaunchRecord, instance string, m JobManifest) error {
	if v.chrootBase == "" {
		return fmt.Errorf("vmm: stageJobManifest: chrootBase not configured")
	}
	drive1Img := filepath.Join(v.chrootRoot(instance), layerImageName)
	if _, err := os.Stat(drive1Img); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: %s missing at %s: %w", layerImageName, drive1Img, err)
	}
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("vmm: stageJobManifest: marshal: %w", err)
	}
	if len(blob) > JobManifestMaxBytes {
		return fmt.Errorf("vmm: stageJobManifest: manifest is %d bytes (max %d)", len(blob), JobManifestMaxBytes)
	}
	// job.json lands where the guest's root is: below /upper on an optimized
	// artifact (the overlay upperdir), at the ext4 root of a full-rootfs one.
	// Writing every manifest at the ext4 root once made optimized job VMs
	// miss job.json and start as ordinary HTTP apps.
	return v.stageDriveFile(ctx, owner, instance, drive1Img, driveFile{what: "vmm: stageJobManifest", path: jobManifestPath, want: blob, mode: 0o600})
}

// jobManifestPath is job.json in the optimized layout.
const jobManifestPath = "upper/etc/faas/job.json"

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
			BootArgs: withGuestTimer(coldBootArgs),
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
	return v.guestVsockUDSSock(instance, VsockJobExitPort)
}

// ListenerStore keeps job-exit listeners across a vmmd restart: systemd's
// file descriptor store on a node (daemonunit.FDStore; vmmd restart adoption
// ADR, guest channels). A guest that connects while no vmmd runs then waits
// in the listener's backlog, and the successor reads its frame.
type ListenerStore interface {
	Store(name string, ln *net.UnixListener) error
	Remove(name string) error
	// Take returns, once, a listener the predecessor stored.
	Take(name string) *net.UnixListener
}

// WithListenerStore keeps every job-exit listener in store from its bind to
// its release.
func (v *JailerVMM) WithListenerStore(store ListenerStore) *JailerVMM {
	v.fdStoreMu.Lock()
	v.listenerStore = store
	v.fdStoreMu.Unlock()
	return v
}

// jobExitStoreName is the listener's FDNAME. systemd rejects ':' in it.
func jobExitStoreName(instance string) string {
	return "gvsock-" + instance + "-" + strconv.Itoa(VsockJobExitPort)
}

// storeJobExitListener puts a freshly bound listener in the store. A failure
// only costs the restart window: the successor re-binds instead.
func (v *JailerVMM) storeJobExitListener(instance string, ln *net.UnixListener) {
	v.fdStoreMu.Lock()
	defer v.fdStoreMu.Unlock()
	if v.listenerStore == nil {
		return
	}
	if err := v.listenerStore.Store(jobExitStoreName(instance), ln); err != nil {
		slog.Warn("vmm: job-exit listener not stored for restart", "instance", instance, "err", err)
		return
	}
	if v.storedJobExits == nil {
		v.storedJobExits = make(map[string]*net.UnixListener)
	}
	v.storedJobExits[instance] = ln
}

// unstoreJobExitListener removes ln from the store once its receiver is done.
// A newer listener stored for the same instance is left alone.
func (v *JailerVMM) unstoreJobExitListener(instance string, ln *net.UnixListener) {
	v.fdStoreMu.Lock()
	defer v.fdStoreMu.Unlock()
	if v.listenerStore == nil || v.storedJobExits[instance] != ln {
		return
	}
	delete(v.storedJobExits, instance)
	_ = v.listenerStore.Remove(jobExitStoreName(instance))
}

// adoptJobExitListener gives an adopted job its exit receiver back: on the
// listener the predecessor stored, whose backlog holds any frame sent while
// no vmmd ran, or else on a re-bound one, which a guest retrying its frame
// reaches.
func (v *JailerVMM) adoptJobExitListener(l Lease) error {
	ln := v.takeStoredJobExitListener(l.Instance)
	if ln == nil {
		return v.prepareJobExitListener(l)
	}
	v.mu.Lock()
	if v.guestVsockListeners == nil {
		v.guestVsockListeners = make(map[guestVsockListenerKey]*net.UnixListener)
	}
	v.guestVsockListeners[guestVsockListenerKey{instance: l.Instance, port: VsockJobExitPort}] = ln
	v.mu.Unlock()
	v.startJobExitReceiver(l.Instance, ln)
	return nil
}

// takeStoredJobExitListener returns the stored listener when it is still
// bound at the instance's path. One whose path is gone can never be reached
// again, so it is closed and removed from the store.
func (v *JailerVMM) takeStoredJobExitListener(instance string) *net.UnixListener {
	v.fdStoreMu.Lock()
	defer v.fdStoreMu.Unlock()
	if v.listenerStore == nil {
		return nil
	}
	name := jobExitStoreName(instance)
	ln := v.listenerStore.Take(name)
	if ln == nil {
		return nil
	}
	path := v.jobExitUDSSock(instance)
	addr, ok := ln.Addr().(*net.UnixAddr)
	info, err := os.Lstat(path)
	if !ok || addr.Name != path || err != nil || info.Mode()&os.ModeSocket == 0 {
		_ = ln.Close()
		_ = v.listenerStore.Remove(name)
		return nil
	}
	ln.SetUnlinkOnClose(true)
	if v.storedJobExits == nil {
		v.storedJobExits = make(map[string]*net.UnixListener)
	}
	v.storedJobExits[instance] = ln
	return ln
}

// prepareJobExitListener binds the host endpoint before Firecracker starts
// and starts its receiver. Short-lived jobs can otherwise finish all retries
// before schedd issues its WaitJobExit RPC. The socket is owned by the jailer
// identity because the unprivileged Firecracker process is the connecting
// peer.
func (v *JailerVMM) prepareJobExitListener(l Lease) error {
	if err := v.prepareGuestVsockListener(l, VsockJobExitPort); err != nil {
		return err
	}
	ln := v.guestVsockListener(l.Instance, VsockJobExitPort)
	if ln == nil {
		return fmt.Errorf("vmm: job-exit listener for %s is missing", l.Instance)
	}
	v.storeJobExitListener(l.Instance, ln)
	v.startJobExitReceiver(l.Instance, ln)
	return nil
}

func (v *JailerVMM) closeJobExitListener(instance string) {
	v.closeGuestVsockListener(instance, VsockJobExitPort)
}

// jobExitFrameTimeout bounds one connection's frame read. guest-init writes
// the frame in one go right after connecting.
const jobExitFrameTimeout = 5 * time.Second

// jobExitReceiver is vmmd's own accept loop on one job's exit listener (vmmd
// restart adoption ADR, workload kinds). It accepts as soon as the listener
// exists, independent of any WaitJobExit call, so a restart that ends the
// RPC loses nothing: the sink records the payload before the guest is
// acknowledged, and a later WaitJobExit, on this vmmd or on its successor,
// returns it.
type jobExitReceiver struct {
	done    chan struct{} // closed once payload or err is set
	payload JobExitPayload
	err     error
}

// WithJobExitSink installs the callback that records a job's exit before the
// guest is acknowledged. It is read when a frame arrives, so receivers
// started before it is installed use it too.
func (v *JailerVMM) WithJobExitSink(cb func(instance string, payload JobExitPayload)) *JailerVMM {
	v.mu.Lock()
	v.jobExitSink = cb
	v.mu.Unlock()
	return v
}

func (v *JailerVMM) startJobExitReceiver(instance string, ln *net.UnixListener) {
	r := &jobExitReceiver{done: make(chan struct{})}
	v.mu.Lock()
	if v.jobExits == nil {
		v.jobExits = make(map[string]*jobExitReceiver)
	}
	v.jobExits[instance] = r
	v.mu.Unlock()
	go v.receiveJobExit(instance, ln, r)
}

// receiveJobExit accepts until one valid frame arrives and records it. It
// then keeps acknowledging retries of that frame until teardown closes the
// listener: a frame that waited in the backlog while no vmmd ran is read
// after the guest gave up on its ack, and without an answer to its next try
// the guest would retry for jobExitShipBudget before powering off. Invalid
// frames are rejected with a bounded budget so untrusted guest bytes cannot
// allocate unbounded memory or spin a host goroutine forever.
func (v *JailerVMM) receiveJobExit(instance string, ln *net.UnixListener, r *jobExitReceiver) {
	defer v.releaseGuestVsockListener(instance, VsockJobExitPort, ln)
	settled := false
	settle := func(payload JobExitPayload, err error) {
		r.payload, r.err, settled = payload, err, true
		close(r.done)
		// The exit is in the adoption block now; a successor needs no
		// listener for it.
		v.unstoreJobExitListener(instance, ln)
	}
	const maxInvalidFrames = 8
	var lastErr error
	for invalid := 0; ; {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if !settled {
				settle(JobExitPayload{}, fmt.Errorf("vmm: job exit %s: accept: %w", instance, err))
			}
			return
		}
		_ = conn.SetDeadline(time.Now().Add(jobExitFrameTimeout))
		payload, readErr := readJobExitEnvelope(conn)
		if readErr != nil {
			_, _ = conn.Write([]byte{1})
			_ = conn.Close()
			if lastErr, invalid = readErr, invalid+1; invalid >= maxInvalidFrames {
				if !settled {
					settle(JobExitPayload{}, fmt.Errorf("vmm: job exit %s: too many invalid frames: %w", instance, lastErr))
				}
				return
			}
			continue
		}
		if !settled {
			v.mu.Lock()
			sink := v.jobExitSink
			v.mu.Unlock()
			if sink != nil {
				sink(instance, payload)
			}
		}
		// Receipt is durable at the scheduler boundary, not here. The ack only
		// tells guest-init that vmmd parsed and recorded a complete, valid
		// frame; a failed ack must not discard a result already recorded.
		_, _ = conn.Write([]byte{0})
		_ = conn.Close()
		if !settled {
			settle(payload, nil)
		}
	}
}

// WaitJobExit returns the job's exit once its receiver has it. Returning on
// ctx or the deadline leaves the receiver running, so the call can be made
// again, which the scheduler does across a vmmd restart.
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
	r := v.jobExits[l.Instance]
	v.mu.Unlock()
	if r == nil {
		return zero, fmt.Errorf("vmm: WaitJobExit: listener for %s was not prepared", l.Instance)
	}
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case <-r.done:
		if r.err != nil {
			return zero, r.err
		}
		return r.payload, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-timer.C:
		return zero, context.DeadlineExceeded
	}
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
	if len(payload.OutputManifest) > 0 {
		manifest, err := jobresult.Validate(payload.OutputManifest)
		if err != nil {
			return err
		}
		if payload.ErrorClass != "succeeded" && len(manifest.Artifacts) > 0 {
			return fmt.Errorf("failed job result cannot declare artifacts")
		}
	}
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

// SignalJob delivers a graceful stop to guest-init, for jobs and for workers
// (stopSignalsGuest). StopInstance cannot signal the customer by signalling the
// Firecracker host process; it must use a host-initiated vsock connection to
// the supervisor inside the VM.
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
	// Firecracker keeps running a guest that stopped on its own: a worker's
	// guest-init exits as PID 1 once its workload drains, and a job guest
	// powers off after its exit report is acknowledged. Reap it as soon as the
	// console shows that, as watchBuilderGuestHalt does for builders, rather
	// than waiting out the grace and reporting a SIGKILL (Hunt 14 H14-59).
	poll := time.NewTicker(guestStopPollInterval)
	defer poll.Stop()
	for {
		select {
		case <-rec.done:
			v.mu.Lock()
			code := int32(rec.exitCode)
			v.mu.Unlock()
			return false, code, nil
		case <-poll.C:
			stopped, guestCode := consoleShowsGuestStopped(rec.consolePath)
			if !stopped {
				continue
			}
			_, _, err := v.SignalAndKill(context.WithoutCancel(ctx), l, 0, 0)
			return false, guestCode, err
		case <-ctx.Done():
		case <-timer.C:
		}
		break
	}
	killed, code, err := v.SignalAndKill(context.WithoutCancel(ctx), l, 0, 0)
	return killed, code, err
}

// errGuestStopUnsupported is a stop frame answered by something other than a
// stop handler: a guest-init without the H14-59 dispatch serves port 1028 with
// its liveness listener, whose error reply also starts with a zero byte.
var errGuestStopUnsupported = errors.New("guest answered the stop frame with a liveness reply")

// confirmJobControlAckEnd checks that the one-byte ack is the whole reply: a
// stop handler closes the connection after it. Anything more is a liveness
// reply, so the stop never reached the workload (Hunt 14 H14-59).
func confirmJobControlAckEnd(conn net.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(jobControlAckEndWait))
	var extra [1]byte
	if n, _ := conn.Read(extra[:]); n > 0 {
		return errGuestStopUnsupported
	}
	return nil
}

// jobControlAckEndWait bounds the wait for the guest to close after its ack.
const jobControlAckEndWait = 500 * time.Millisecond

// guestStopPollInterval is how often SignalJob reads the console for a guest
// that stopped on its own.
const guestStopPollInterval = 100 * time.Millisecond

// guestInitExitMarker is the kernel's panic line when guest-init (PID 1)
// exits; the value after it is the wait status, so the exit code is bits 8-15.
const guestInitExitMarker = "Attempted to kill init! exitcode=0x"

// consoleShowsGuestStopped reports whether the console shows the guest
// stopped on its own: guest-init exited, or the guest halted after a power-off.
// code is guest-init's exit code when the kernel printed it, else 0.
func consoleShowsGuestStopped(path string) (stopped bool, code int32) {
	tail := consoleTail(path)
	if i := bytes.LastIndex(tail, []byte(guestInitExitMarker)); i >= 0 {
		digits := tail[i+len(guestInitExitMarker):]
		if n := bytes.IndexFunc(digits, func(r rune) bool { return !strings.ContainsRune("0123456789abcdefABCDEF", r) }); n >= 0 {
			digits = digits[:n]
		}
		if status, err := strconv.ParseUint(string(digits), 16, 32); err == nil {
			return true, int32((status >> 8) & 0xff)
		}
		return true, 0
	}
	return bytes.Contains(tail, []byte("Attempted to kill init!")) || bytes.Contains(tail, []byte("System halted")), 0
}

func (v *JailerVMM) signalJobGuest(ctx context.Context, l Lease, signal syscall.Signal) error {
	if v == nil || l.Instance == "" || v.chrootBase == "" {
		return fmt.Errorf("vmm: signal job: invalid VMM or instance")
	}
	switch signal {
	// A worker manifest may also declare SIGUSR1 or SIGUSR2; a job guest still
	// refuses them, and SignalJob then falls back to the hard stop.
	case syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGUSR2:
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
			if err == nil {
				err = confirmJobControlAckEnd(conn)
			}
		}
		_ = conn.Close()
		if err == nil {
			return nil
		}
		if errors.Is(err, errGuestStopUnsupported) {
			return fmt.Errorf("vmm: signal job %s: %w", l.Instance, err)
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

// ReleaseJobStart tells a held guest Job to execute its command. Repeated
// calls are safe: the guest acknowledges both the first release and retries
// after an ambiguous host-side transport failure.
func (v *JailerVMM) ReleaseJobStart(ctx context.Context, l Lease) error {
	if v == nil || l.Instance == "" || v.chrootBase == "" {
		return fmt.Errorf("vmm: release job start: invalid VMM or instance")
	}
	end := time.Now().Add(resumeHookDialDeadline)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(end) {
		end = deadline
	}
	lastErr := context.DeadlineExceeded
	for time.Now().Before(end) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("vmm: release job start %s: %w", l.Instance, err)
		}
		conn, err := net.DialTimeout("unix", v.vsockUDSSock(l.Instance), 200*time.Millisecond)
		if err != nil {
			lastErr = err
		} else {
			_ = conn.SetDeadline(end)
			if err = writeJobControlFrame(conn, []byte(fmt.Sprintf("CONNECT %d\n", VsockJobControlPort))); err == nil {
				var ack string
				ack, err = readConnectAck(conn)
				if err == nil && ack != "OK" {
					err = fmt.Errorf("CONNECT rejected: %q", ack)
				}
			}
			if err == nil {
				var frame [8]byte
				binary.BigEndian.PutUint32(frame[:4], VsockJobStartMsgType)
				if err = writeJobControlFrame(conn, frame[:]); err == nil {
					var reply [1]byte
					_, err = io.ReadFull(conn, reply[:])
					if err == nil && reply[0] != vsockJobControlAckOK {
						err = fmt.Errorf("guest rejected held-job release (ack=%d)", reply[0])
					}
				}
			}
			_ = conn.Close()
			if err == nil {
				return nil
			}
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("vmm: release job start %s: %w", l.Instance, ctx.Err())
		case <-time.After(resumeHookDialStep):
		}
	}
	return fmt.Errorf("vmm: release job start %s: %w", l.Instance, lastErr)
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
