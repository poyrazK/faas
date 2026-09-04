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
//     the command, captures exit, writes the vsock DGRAM, and powers
//     off. It never binds :8080 — SkipReady is forced.
//
//  3. The terminal exit envelope is a DGRAM, not a STREAM, at the
//     same vsock port (1026) as characterize. The discriminator is
//     the wire msg_type byte: characterize = 3, job_exit = 4. The
//     port number is shared because Linux limits vsock ports per
//     guest-cid and we want to keep the per-VM device count down
//     (every guest still has one vsock device with N ports, not N
//     vsock devices with 1 port each).
//
// This file implements BootColdBootForJob + WaitJobExit on
// *JailerVMM, plus the Manager.BootJob / WaitJobExit wrappers that
// schedd calls. Cmd/vmmdgrpc/server.go exposes them as gRPC.

package fcvm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
// DGRAM (port 1026, msg_type 4) when the customer's command exits.
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
	case len(s.Command) == 0:
		return fmt.Errorf("fcvm: job cold boot: empty command")
	case len(s.Command) > 64:
		return fmt.Errorf("fcvm: job cold boot: command has %d args (>64)", len(s.Command))
	case s.TaskTimeoutSec <= 0:
		return fmt.Errorf("fcvm: job cold boot: task_timeout_s %d must be > 0", s.TaskTimeoutSec)
	case s.TaskTimeoutSec > JobMaxTaskTimeoutSec:
		return fmt.Errorf("fcvm: job cold boot: task_timeout_s %d exceeds cap %d", s.TaskTimeoutSec, JobMaxTaskTimeoutSec)
	case s.LeaseToken == "":
		return fmt.Errorf("fcvm: job cold boot: empty lease token")
	case s.VcpuCount < 1:
		return fmt.Errorf("fcvm: job cold boot: vcpu_count %d < 1", s.VcpuCount)
	case s.MemSizeMiB < 1:
		return fmt.Errorf("fcvm: job cold boot: mem_size_mib %d < 1", s.MemSizeMiB)
	case s.Tap == "":
		return fmt.Errorf("fcvm: job cold boot: empty tap device")
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

	// Stage the manifest onto drive1 BEFORE Boot — guest-init reads
	// /etc/faas/job.json during its first-boot phase (decideMode),
	// same gating as /etc/faas/app.json for app VMs. We use the
	// loopback mount path through chrootRoot so the file lands
	// inside the chroot the guest sees.
	if err := v.stageJobManifest(l.Instance, JobManifest{
		Kind:                "job",
		AccountID:           spec.AccountID,
		RunID:               spec.RunID,
		TaskIndex:           spec.TaskIndex,
		LeaseToken:          spec.LeaseToken,
		ImageRef:            spec.ImageRef, // already a tmp path; guest ignores
		Command:             spec.Command,
		Env:                 spec.Env,
		TaskTimeoutSec:      spec.TaskTimeoutSec,
		VsockJobExitPort:    VsockJobExitPort,
		VsockJobExitMsgType: VsockJobExitMsgType,
	}); err != nil {
		return fmt.Errorf("vmm: stage job manifest: %w", err)
	}

	// bootNoWait = skipReady=true. HealthcheckPath is "" — the
	// supervisor doesn't bind :8080; the exit envelope over vsock
	// is the readiness signal.
	return v.bootNoWait(ctx, l, BuildJobColdBootConfig(spec, l.Slot), nil, nil, nil)
}

// stageJobManifest writes the JSON-encoded JobManifest to drive1
// at /etc/faas/job.json inside the chroot. Uses the same loopback
// mount pattern as StageSecretsEnv / StageAPIEnv so the file is
// visible to guest-init on first boot and invisible to the host
// after the mount is torn down (no /var/lib/faas/jobs/<id>.json
// shadow files leaking across instances).
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
func (v *JailerVMM) stageJobManifest(instance string, m JobManifest) error {
	if v.chrootBase == "" {
		return fmt.Errorf("vmm: stageJobManifest: chrootBase not configured")
	}
	root := v.chrootRoot(instance)
	drive1Img := filepath.Join(root, layerImageName)
	if _, err := os.Stat(drive1Img); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: %s missing at %s: %w", layerImageName, drive1Img, err)
	}
	mnt := filepath.Join(root, "mnt-job")
	if err := os.MkdirAll(mnt, 0o755); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: mkdir mnt: %w", err)
	}
	if out, err := exec.Command("mount", "-o", "loop,rw", drive1Img, mnt).CombinedOutput(); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: mount: %w: %s", err, string(out))
	}
	defer func() {
		// Best-effort umount. Chroot lives on tmpfs and is
		// cleared on Kill anyway, so a stuck mount is benign
		// until then.
		_ = exec.Command("umount", mnt).Run()
	}()

	etc := filepath.Join(mnt, "etc", "faas")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: mkdir etc/faas: %w", err)
	}
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("vmm: stageJobManifest: marshal: %w", err)
	}
	dst := filepath.Join(etc, "job.json")
	if err := os.WriteFile(dst, blob, 0o644); err != nil {
		return fmt.Errorf("vmm: stageJobManifest: write %s: %w", dst, err)
	}
	return nil
}

// BuildJobColdBootConfig builds the Firecracker config for a job
// VM. Mirrors BuildColdBootConfig (line 274) but emits only one
// per-instance drive (drive1 = the customer image) and forces
// SkipReady semantics by setting EphemeralWritable=true (the same
// signal bootNoWait uses for builder VMs).
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
		Entropy:           &Entropy{},
		VsockDevice:       NewVsockDevice(slot),
		EphemeralWritable: true, // forces SkipReady at bootNoWait
	}
}

// WaitJobExit accepts the FIRST guest-initiated job-exit envelope
// on the per-instance vsock UDS at port VsockJobExitPort=1026 and
// returns the parsed JobExitPayload. Parallel to
// WaitCharacterizationReport at vmm.go:2786 — same CONNECT-port
// handshake, same framing (4-byte msg_type + 4-byte body_len + JSON),
// discriminated ONLY by msg_type = VsockJobExitMsgType (=4) vs
// characterize's =3.
//
// Wire direction: GUEST INITIATES — the supervisor dials host CID
// 2 at port 1026 and writes one DGRAM-equivalent frame (the Linux
// vsock UDS is a STREAM at the wire level; the application layer
// treats each direction as a single message). On timeout the
// caller (Engine.HandleJobExit) treats the task as crashed and
// the reaper takes over after JobReaperTTL.
//
// We accept AT MOST ONE envelope per call; a second guest write
// (theoretically possible if the supervisor retries) is dropped
// after the deadline elapses. A second envelope's contents would
// be an unrelated post-poweroff write to a closed UDS — FC
// returns EPIPE on the guest side, the supervisor exits.
//
// Defense-in-depth mirrors TriggerResumeHook / WaitCharacterizationReport:
// nil receiver, empty instance, unconfigured chroot root are all
// explicit errors so a refactor passing an uninitialised VMM
// surfaces a useful message instead of ENOENT.
//
// Deadline is measured from function entry; pass
// EffectiveDestroyWait(task_timeout_s) so the per-task cap fits
// the listener window.
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
	sock := v.vsockUDSSock(l.Instance)

	// Step 1: dial the vsock UDS. The UDS is created by FC at boot;
	// waitReady ensures it's live before we get here, but a freshly-
	// created UDS can race the firecracker listener thread for a few
	// hundred ms. Retry with a short step until deadline.
	dialDeadline := time.Now().Add(deadline)
	var conn net.Conn
	var lastErr error
	for time.Now().Before(dialDeadline) {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		var err error
		conn, err = net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err == nil {
			break
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(resumeHookDialStep):
		}
	}
	if conn == nil {
		return zero, fmt.Errorf("vmm: dial vsock uds %s: %w", sock, lastErr)
	}
	defer func() { _ = conn.Close() }()

	// Step 2: send CONNECT <port> to FC, which proxies to the
	// guest's vsock listener on port 1026. Same pattern as the
	// characterize path.
	connectCmd := fmt.Sprintf("CONNECT %d\n", VsockJobExitPort)
	if _, err := conn.Write([]byte(connectCmd)); err != nil {
		return zero, fmt.Errorf("vmm: write CONNECT %d: %w", VsockJobExitPort, err)
	}
	connectAck, err := readConnectAck(conn)
	if err != nil {
		return zero, fmt.Errorf("vmm: read CONNECT ack: %w", err)
	}
	if connectAck != "OK" {
		return zero, fmt.Errorf("vmm: CONNECT rejected: %q", connectAck)
	}

	_ = conn.SetDeadline(time.Now().Add(deadline))

	// Step 3: read the framed envelope. Format is
	// [4B BE msg_type][4B BE body_len][N B JSON], matching the
	// characterize-report wire format at vmm.go:2841. We validate
	// msg_type = VsockJobExitMsgType (=4); a wrong type means a
	// misrouted frame (the supervisor dialed the right port but
	// the wrong listener, or the characterize report arrived at
	// a job call site by mistake).
	var hdr [8]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return zero, fmt.Errorf("vmm: read job-exit frame header: %w", err)
	}
	msgType := binary.BigEndian.Uint32(hdr[:4])
	bodyLen := binary.BigEndian.Uint32(hdr[4:8])
	if msgType != uint32(VsockJobExitMsgType) {
		return zero, fmt.Errorf("vmm: job-exit msg_type=%d, want %d", msgType, VsockJobExitMsgType)
	}
	if bodyLen == 0 || bodyLen > VsockJobExitMaxBody {
		return zero, fmt.Errorf("vmm: job-exit body_len=%d out of range (0, %d]", bodyLen, VsockJobExitMaxBody)
	}

	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return zero, fmt.Errorf("vmm: read job-exit body (%d bytes): %w", bodyLen, err)
	}

	var payload JobExitPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return zero, fmt.Errorf("vmm: parse job-exit JSON: %w", err)
	}

	// Step 4: write a 1-byte ack (0x00) so the guest's
	// supervisor can close its end cleanly. The supervisor
	// treats a missing ack as a transient write failure and
	// retries the DGRAM (same as characterize).
	if _, err := conn.Write([]byte{0x00}); err != nil {
		return zero, fmt.Errorf("vmm: write job-exit ack: %w", err)
	}

	return payload, nil
}

// VsockJobExitMaxBody caps the JSON body at 8 KiB. Job exit
// envelopes are tiny (exit_code + error_class + signal +
// finished_at + lease_token ≈ 200 bytes); 8 KiB is generous
// headroom for a future field addition without protocol
// renegotiation. The guest hard-truncates BEFORE json.Marshal
// (M8 supervisor) so the receiver never sees a malformed body.
const VsockJobExitMaxBody = 8 * 1024
