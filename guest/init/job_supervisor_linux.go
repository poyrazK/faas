// Package main — job-task supervisor linux implementation
// (issue #1184 Workstream A / ADR-099).
//
// This file implements the runJob entry point + signal handling +
// guest-initiated vsock STREAM shipping for job-task VMs.
//
// Flow (single iteration, no restart loop — jobs run once to
// terminal):
//
//  1. Load /etc/faas/job.json (JobManifest).
//  2. Fork the command into its own process group while guest-init remains PID 1.
//  3. Forward stop signals and enforce task_timeout_s with TERM→30s→KILL.
//  4. Capture the direct child's wait status and reap remaining descendants.
//  5. Write JobExitPayload to the host listener (port 1026, msg_type 4).
//  6. Power off through the reboot syscall.

package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	osSignal "os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const jobTerminationGrace = 30 * time.Second

type jobTerminationReason uint8

const (
	jobExitedNaturally jobTerminationReason = iota
	jobTimedOut
	jobCancelled
)

type jobWaitResult struct {
	status    syscall.WaitStatus
	hasStatus bool
	err       error
}

// RunJob is the entry point the boot dispatcher calls when
// decideMode picks modeJob. It is total — on any internal error
// it still attempts to ship a terminal envelope (so the host's
// HandleJobExit sees a terminal transition, not a hang), then
// powers off. The VM is single-shot; there's no restart loop.
func RunJob(log *slog.Logger) error {
	manifest, err := loadJobManifest()
	if err != nil {
		log.Error("runJob: load manifest", "err", err)
		return shipAndPoweroff(JobExitPayload{
			ExitCode:   127, // POSIX "command not found" sentinel
			ErrorClass: "infra",
			LeaseToken: "",
		}, log)
	}
	if len(manifest.Command) == 0 {
		return shipAndPoweroff(JobExitPayload{
			ExitCode:   126, // POSIX "command found but not executable" sentinel
			ErrorClass: "infra",
			LeaseToken: manifest.LeaseToken,
		}, log)
	}

	// Build merged env (systemEnv ⊕ job.Env ⊕ jobEnvBaseline).
	env := buildEnvForJob(*manifest)
	return runViaOSExec(*manifest, env, log)
}

// runViaOSExec keeps guest-init alive as PID 1, supervises the workload, ships
// its terminal result, and powers the VM off.
func runViaOSExec(m JobManifest, env []string, log *slog.Logger) error {
	payload := superviseJobCommand(m, env, jobTerminationGrace, log)
	return shipAndPoweroff(payload, log)
}

func superviseJobCommand(m JobManifest, env []string, grace time.Duration, log *slog.Logger) JobExitPayload {
	if log == nil {
		log = slog.Default()
	}
	stopSignals := make(chan os.Signal, 2)
	osSignal.Notify(stopSignals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP)
	defer osSignal.Stop(stopSignals)
	closeControl := listenJobCancellation(stopSignals, log)
	defer closeControl()

	cmd := exec.Command(m.Command[0], m.Command[1:]...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Stdout / Stderr → null so the customer's command's output
	// doesn't get mixed into guest-init's log ring. Logs are
	// captured via the vsock log channel (future M-extra work — M8
	// ships with stdout discarded; logs land in pkg/fcvm/logbuf
	// once we add a log forwarder).
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		log.Error("runJob: os/exec start", "err", err, "command", m.Command[0])
		exitCode := int32(126)
		if errors.Is(err, os.ErrNotExist) {
			exitCode = 127
		}
		return JobExitPayload{
			ExitCode:           exitCode,
			ErrorClass:         "infra",
			LeaseToken:         m.LeaseToken,
			FinishedAtUnixNano: time.Now().UnixNano(),
		}
	}

	waitCh := make(chan jobWaitResult, 1)
	go func() { waitCh <- waitJobCommand(cmd) }()

	timeout := time.NewTimer(time.Duration(m.TaskTimeoutSec) * time.Second)
	defer timeout.Stop()
	timeoutC := timeout.C
	var graceTimer *time.Timer
	var graceC <-chan time.Time
	var forceExitTimer *time.Timer
	var forceExitC <-chan time.Time
	reason := jobExitedNaturally
	stopSignal := syscall.SIGTERM

	for {
		select {
		case waitResult := <-waitCh:
			if graceTimer != nil {
				graceTimer.Stop()
			}
			if forceExitTimer != nil {
				forceExitTimer.Stop()
			}
			// A shell may leave background descendants behind. The workload owns
			// the complete process group, so no child survives terminal reporting.
			_ = signalJobProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
			reapJobChildren(250 * time.Millisecond)
			return jobExitPayloadFromWait(waitResult, reason, stopSignal, m.LeaseToken)
		case <-timeoutC:
			reason = jobTimedOut
			stopSignal = syscall.SIGTERM
			timeoutC = nil
			_ = signalJobProcessGroup(cmd.Process.Pid, stopSignal)
			graceTimer = time.NewTimer(grace)
			graceC = graceTimer.C
		case raw := <-stopSignals:
			if reason != jobExitedNaturally {
				continue
			}
			reason = jobCancelled
			if sig, ok := raw.(syscall.Signal); ok {
				stopSignal = sig
			}
			timeoutC = nil
			if !timeout.Stop() {
				select {
				case <-timeout.C:
				default:
				}
			}
			_ = signalJobProcessGroup(cmd.Process.Pid, stopSignal)
			graceTimer = time.NewTimer(grace)
			graceC = graceTimer.C
		case <-graceC:
			graceC = nil
			_ = signalJobProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
			forceExitTimer = time.NewTimer(grace)
			forceExitC = forceExitTimer.C
		case <-forceExitC:
			// SIGKILL normally makes Wait return immediately. Bound the pathological
			// uninterruptible-task case so one guest cannot pin its lease forever.
			return jobExitPayloadFromWait(jobWaitResult{err: errors.New("job did not exit after SIGKILL")}, reason, stopSignal, m.LeaseToken)
		}
	}
}

func listenJobCancellation(signals chan<- os.Signal, log *slog.Logger) func() {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		log.Debug("job cancel vsock unavailable", "err", err)
		return func() {}
	}
	addr := &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: VsockJobControlPort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		log.Debug("job cancel vsock bind unavailable", "err", err)
		return func() {}
	}
	if err := unix.Listen(fd, 4); err != nil {
		_ = unix.Close(fd)
		log.Debug("job cancel vsock listen unavailable", "err", err)
		return func() {}
	}
	go func() {
		for {
			raw, peer, err := unix.Accept4(fd, unix.SOCK_CLOEXEC)
			if err != nil {
				return
			}
			vmPeer, ok := peer.(*unix.SockaddrVM)
			if !ok || vmPeer.CID != unix.VMADDR_CID_HOST {
				_ = unix.Close(raw)
				continue
			}
			go handleJobCancellation(raw, signals)
		}
	}()
	return func() { _ = unix.Close(fd) }
}

func handleJobCancellation(fd int, signals chan<- os.Signal) {
	f := os.NewFile(uintptr(fd), "job-cancel")
	if f == nil {
		_ = unix.Close(fd)
		return
	}
	defer func() { _ = f.Close() }()
	if err := setSockTimeout(fd, unix.SO_RCVTIMEO, 1500*time.Millisecond); err != nil {
		_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
		return
	}
	if err := setSockTimeout(fd, unix.SO_SNDTIMEO, 1500*time.Millisecond); err != nil {
		_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
		return
	}
	var frame [8]byte
	if _, err := io.ReadFull(f, frame[:]); err != nil {
		_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
		return
	}
	if binary.BigEndian.Uint32(frame[:4]) != VsockJobCancelMsgType {
		_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
		return
	}
	sig := syscall.Signal(binary.BigEndian.Uint32(frame[4:]))
	switch sig {
	case syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP:
	default:
		_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
		return
	}
	select {
	case signals <- sig:
		_, _ = unix.Write(fd, []byte{VsockJobControlAckOK})
	default:
		_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
	}
}

func waitJobCommand(cmd *exec.Cmd) jobWaitResult {
	if os.Getpid() != 1 {
		err := cmd.Wait()
		if cmd.ProcessState == nil {
			return jobWaitResult{err: err}
		}
		status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		return jobWaitResult{status: status, hasStatus: ok, err: err}
	}

	// As PID 1, own wait4(-1) so daemonized grandchildren adopted by init are
	// reaped throughout the job instead of accumulating as zombies. Only the
	// direct command's status terminates supervision; other children are drained.
	for {
		var status unix.WaitStatus
		pid, err := unix.Wait4(-1, &status, 0, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return jobWaitResult{err: err}
		}
		if pid == cmd.Process.Pid {
			return jobWaitResult{status: syscall.WaitStatus(status), hasStatus: true}
		}
	}
}

func signalJobProcessGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return nil
	}
	err := unix.Kill(-pgid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func reapJobChildren(budget time.Duration) {
	if os.Getpid() != 1 {
		return
	}
	deadline := time.Now().Add(budget)
	for {
		var status unix.WaitStatus
		pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
		if errors.Is(err, syscall.ECHILD) {
			return
		}
		if err != nil {
			return
		}
		if pid > 0 {
			continue
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func jobExitPayloadFromWait(waitResult jobWaitResult, reason jobTerminationReason, stopSignal syscall.Signal, leaseToken string) JobExitPayload {
	exitCode := int32(1)
	var signalNumber int32
	if waitResult.hasStatus {
		if waitResult.status.Signaled() {
			signalNumber = int32(waitResult.status.Signal())
			exitCode = 128 + signalNumber
		} else {
			exitCode = int32(waitResult.status.ExitStatus())
		}
	}
	errorClass := mapExitToErrorClass(exitCode, signalNumber)
	if waitResult.err != nil && !waitResult.hasStatus {
		errorClass = "infra"
	}
	switch reason {
	case jobTimedOut:
		exitCode = 124
		errorClass = "timeout"
	case jobCancelled:
		exitCode = 128 + int32(stopSignal)
		errorClass = "cancelled"
	}
	return JobExitPayload{
		ExitCode:           exitCode,
		ErrorClass:         errorClass,
		Signal:             signalNumber,
		FinishedAtUnixNano: time.Now().UnixNano(),
		LeaseToken:         leaseToken,
	}
}

// loadJobManifest reads + decodes /etc/faas/job.json from the
// post-pivot root fs.
//
// Returns (nil, nil) when the file is missing — decideMode
// already proved the file existed before calling RunJob, so
// missing here means a race (vmmd didn't stage the file in time)
// and the supervisor should treat it as infra error.
// A present-but-malformed file returns the decode error so a
// corrupted staging write surfaces as a panic rather than a
// silent app-mode fallback.
func loadJobManifest() (*JobManifest, error) {
	// open from cwd-relative path — guest-init has pivot_root'd
	// into /, so a relative path resolves against the merged
	// overlay root.
	//nolint:forbidigo // guest-init reads its own staged manifest from the
	// post-pivot rootfs; openCustomerFile is for host daemons reading customer
	// bytes, not for in-guest init reading OS-owned staging files.
	f, err := os.Open(filepath.Join("/", jobManifestPath))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", jobManifestPath, err)
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil {
		return nil, fmt.Errorf("stat %s: %w", jobManifestPath, err)
	} else if info.Size() > JobManifestMaxBytes {
		return nil, fmt.Errorf("%s is %d bytes (max %d)", jobManifestPath, info.Size(), JobManifestMaxBytes)
	}
	var m JobManifest
	decoder := json.NewDecoder(io.LimitReader(f, JobManifestMaxBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", jobManifestPath, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode %s: trailing content", jobManifestPath)
	}
	if err := validateJobManifest(m); err != nil {
		return nil, fmt.Errorf("validate %s: %w", jobManifestPath, err)
	}
	return &m, nil
}

// shipAndPoweroff writes the JobExitPayload envelope to the host, then powers
// off the VM using the reboot syscall so a customer layer cannot shadow the
// shutdown binary.
func shipAndPoweroff(payload JobExitPayload, log *slog.Logger) error {
	if payload.FinishedAtUnixNano == 0 {
		payload.FinishedAtUnixNano = time.Now().UnixNano()
	}
	shipErr := shipExitEnvelope(payload, log)
	unix.Sync()
	_ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
	return shipErr
}

// shipExitEnvelope is the inner STREAM writer. Firecracker maps a guest
// connection to CID 2 / port 1026 onto the host's pre-bound
// vsock.sock_1026 Unix listener.
//
// Format: [4B BE msg_type][4B BE body_len][N B JSON].
// Matches pkg/fcvm.WaitJobExit's parse (vmm.go).
func shipExitEnvelope(payload JobExitPayload, log *slog.Logger) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("job-exit marshal: %w", err)
	}
	if len(body) > VsockJobExitMaxBody {
		return fmt.Errorf("job-exit body is %d bytes (max %d)", len(body), VsockJobExitMaxBody)
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], VsockJobExitMsgType)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	frame := append(hdr[:], body...)

	addr := &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_HOST,
		Port: VsockJobExitPort,
	}
	const attempts = 3
	backoffs := []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}
	var lastErr error
	for i := 0; i < attempts; i++ {
		sock, sockErr := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if sockErr != nil {
			lastErr = sockErr
			log.Warn("job-exit vsock socket", "err", sockErr, "attempt", i)
			if i < attempts-1 {
				time.Sleep(backoffs[i])
			}
			continue
		}
		// Bound connect, write, and ack wait. A missing host listener must not
		// keep PID 1 alive beyond the scheduler's reaper window.
		if timeoutErr := setSockTimeout(sock, unix.SO_SNDTIMEO, 1500*time.Millisecond); timeoutErr != nil {
			lastErr = timeoutErr
			log.Warn("job-exit vsock send timeout", "err", timeoutErr, "attempt", i)
			_ = unix.Close(sock)
			if i < attempts-1 {
				time.Sleep(backoffs[i])
			}
			continue
		}
		if timeoutErr := setSockTimeout(sock, unix.SO_RCVTIMEO, 1500*time.Millisecond); timeoutErr != nil {
			lastErr = timeoutErr
			log.Warn("job-exit vsock receive timeout", "err", timeoutErr, "attempt", i)
			_ = unix.Close(sock)
			if i < attempts-1 {
				time.Sleep(backoffs[i])
			}
			continue
		}
		if connectErr := unix.Connect(sock, addr); connectErr != nil {
			lastErr = connectErr
			log.Warn("job-exit vsock connect", "err", connectErr, "attempt", i)
			_ = unix.Close(sock)
			if i < attempts-1 {
				time.Sleep(backoffs[i])
			}
			continue
		}
		if writeErr := writeJobExitFrame(sock, frame); writeErr != nil {
			lastErr = writeErr
			log.Warn("job-exit vsock write", "err", writeErr, "attempt", i)
			_ = unix.Close(sock)
			if i < attempts-1 {
				time.Sleep(backoffs[i])
			}
			continue
		}
		var ack [1]byte
		n, readErr := unix.Read(sock, ack[:])
		_ = unix.Close(sock)
		if readErr != nil || n != 1 || ack[0] != 0 {
			if readErr != nil {
				lastErr = fmt.Errorf("read ack: %w", readErr)
			} else {
				lastErr = fmt.Errorf("invalid ack n=%d value=%d", n, ack[0])
			}
			log.Warn("job-exit vsock ack", "err", lastErr, "attempt", i)
			if i < attempts-1 {
				time.Sleep(backoffs[i])
			}
			continue
		}
		log.Info("job-exit shipped",
			"exit_code", payload.ExitCode,
			"error_class", payload.ErrorClass,
			"signal", payload.Signal,
			"attempt", i+1)
		return nil
	}
	log.Error("job-exit all attempts failed", "attempts", attempts)
	return fmt.Errorf("job-exit all attempts failed: %w", lastErr)
}

func writeJobExitFrame(fd int, frame []byte) error {
	for len(frame) > 0 {
		n, err := unix.Write(fd, frame)
		if n > 0 {
			frame = frame[n:]
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

// jobManifestFixturePath is a test-only fs.FS fixture path used
// by job_supervisor_linux_test.go's loadJobManifest fixture
// helper. Drift between this and jobManifestPath is a test
// failure (the test asserts the paths match).
const jobManifestFixturePath = "etc/faas/job.json"

// hasJobManifest is the boot-time check decideMode calls before
// dispatching to RunJob. Mirrors the build.json / app.json
// presence check at decideMode.
//
// Reads /etc/faas/job.json from a fs.FS so unit tests can drive
// it with testing/fstest.MapFS without touching the real root
// fs. The real boot path passes os.DirFS("/") so the path
// resolves against the merged overlay root.
//
// Returns true ONLY if the file exists AND its "kind" field is
// "job". A present file with a missing/wrong "kind" is treated
// as "not a job VM" — the same fail-soft posture
// characterize_linux.go uses for missing fields (so a future
// schema drift doesn't panic every VM).
func hasJobManifest(fsys fs.FS) bool {
	data, err := fs.ReadFile(fsys, jobManifestFixturePath)
	if err != nil {
		return false
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if jErr := json.Unmarshal(data, &probe); jErr != nil {
		return false
	}
	return probe.Kind == "job"
}
