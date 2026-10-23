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
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/jobresult"
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
	secrets, err := loadSecrets(log)
	if err != nil {
		// A one-shot attempt must not silently run with a partial environment.
		log.Error("runJob: load runtime secrets", "err", err)
		return shipAndPoweroff(JobExitPayload{ExitCode: 75, ErrorClass: "infra", LeaseToken: manifest.LeaseToken}, log)
	}
	defer clear(secrets)
	apiEnv := make(map[string]string, len(manifest.Env))
	for key, value := range manifest.Env {
		apiEnv[key] = value
	}
	receiptToken, receiptMACKey, receiptErr := takeQualificationConfigReceiptControl(apiEnv)
	if receiptErr != nil || receiptToken != "" && !manifest.StartHeld {
		clear(receiptMACKey)
		log.Error("runJob: invalid qualification configuration controls", "err", receiptErr)
		return shipAndPoweroff(JobExitPayload{ExitCode: 75, ErrorClass: "infra", LeaseToken: manifest.LeaseToken}, log)
	}
	manifest.Env = apiEnv
	defer clear(receiptMACKey)
	if len(manifest.Command) == 0 {
		return shipAndPoweroff(JobExitPayload{
			ExitCode:   126, // POSIX "command found but not executable" sentinel
			ErrorClass: "infra",
			LeaseToken: manifest.LeaseToken,
		}, log)
	}
	proc, failure := prepareJobProcess(*manifest, secrets, log)
	if failure != nil {
		return shipAndPoweroff(*failure, log)
	}
	if manifest.Interactive != nil {
		if manifest.StartHeld || manifest.Interactive.AttachTimeoutSec <= 0 {
			log.Error("runJob: invalid interactive manifest")
			return shipAndPoweroff(JobExitPayload{ExitCode: 75, ErrorClass: "infra", LeaseToken: manifest.LeaseToken}, log)
		}
		return runInteractiveJob(*manifest, proc, log)
	}
	if manifest.StartHeld {
		control, err := listenJobStartGate(log)
		if err != nil {
			log.Error("runJob: start gate unavailable", "err", err)
			return shipAndPoweroff(JobExitPayload{ExitCode: 75, ErrorClass: "infra", LeaseToken: manifest.LeaseToken}, log)
		}
		defer control.Close()
		if receiptToken != "" {
			emitQualificationConfigReceipt(log, "main", receiptToken, receiptMACKey, apiEnv, secrets, nil)
		}
		released, cancelSignal := waitForJobStart(control.started, control.signals, JobStartGateTimeout)
		if !released {
			payload := JobExitPayload{ExitCode: 75, ErrorClass: "infra", LeaseToken: manifest.LeaseToken}
			if cancelSignal != nil {
				signal := syscall.SIGTERM
				if got, ok := cancelSignal.(syscall.Signal); ok {
					signal = got
				}
				payload.ExitCode = 128 + int32(signal)
				payload.ErrorClass = "cancelled"
				payload.Signal = int32(signal)
			}
			return shipAndPoweroff(payload, log)
		}
		payload := superviseJobCommandWithSignals(*manifest, proc, jobTerminationGrace, log, os.Stdout, os.Stderr, control.signals)
		return shipAndPoweroff(payload, log)
	}
	return runViaOSExec(*manifest, proc, log)
}

// runViaOSExec keeps guest-init alive as PID 1, supervises the workload, ships
// its terminal result, and powers the VM off.
func runViaOSExec(m JobManifest, proc jobProcess, log *slog.Logger) error {
	payload := superviseJobCommandWithSignals(m, proc, jobTerminationGrace, log, os.Stdout, os.Stderr, nil)
	return shipAndPoweroff(payload, log)
}

func superviseJobCommand(m JobManifest, env []string, grace time.Duration, log *slog.Logger) JobExitPayload {
	return superviseJobCommandWithOutput(m, env, grace, log, os.Stdout, os.Stderr)
}

// superviseJobCommandWithOutput is the testable implementation of the job
// supervisor. Production passes guest-init's inherited console descriptors;
// tests can provide bounded in-memory writers without redirecting process-wide
// stdout/stderr.
func superviseJobCommandWithOutput(m JobManifest, env []string, grace time.Duration, log *slog.Logger, stdout, stderr io.Writer) JobExitPayload {
	return superviseJobCommandWithSignals(m, guestJobProcess(m, env), grace, log, stdout, stderr, nil)
}

func superviseJobCommandWithSignals(m JobManifest, proc jobProcess, grace time.Duration, log *slog.Logger, stdout, stderr io.Writer, controlSignals chan os.Signal) JobExitPayload {
	// The guest's stdout and stderr are inherited by guest-init from the
	// serial console. vmmd already routes that console into the per-instance
	// customer log ring, so inheriting both streams makes job output available
	// through the normal logs API. Keep the streams separate here only until
	// they cross the guest console boundary; the host deliberately merges them
	// in arrival order, matching the app-runtime log contract.
	return superviseJobCommandWithStdio(m, proc, grace, log, consoleJobStdio{stdout: stdout, stderr: stderr}, controlSignals)
}

// jobStdio wires a job command's standard streams: attach before Start,
// started after a successful Start, and finish once supervision ends (also
// after a failed Start) to release descriptors and drain output.
type jobStdio interface {
	attach(cmd *exec.Cmd) error
	started()
	finish()
}

// consoleJobStdio is the batch task's stdio: no stdin, output to the console.
type consoleJobStdio struct {
	stdout, stderr io.Writer
}

func (c consoleJobStdio) attach(cmd *exec.Cmd) error {
	cmd.Stdout, cmd.Stderr = c.stdout, c.stderr
	if cmd.Stdout == nil {
		cmd.Stdout = io.Discard
	}
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func (consoleJobStdio) started() {}
func (consoleJobStdio) finish()  {}

func superviseJobCommandWithStdio(m JobManifest, proc jobProcess, grace time.Duration, log *slog.Logger, stdio jobStdio, controlSignals chan os.Signal) JobExitPayload {
	if log == nil {
		log = slog.Default()
	}
	stopSignals := controlSignals
	if stopSignals == nil {
		stopSignals = make(chan os.Signal, 2)
		osSignal.Notify(stopSignals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP)
		defer osSignal.Stop(stopSignals)
		closeControl := listenJobCancellation(stopSignals, log)
		defer closeControl()
	}

	cmd := exec.Command(proc.Argv[0], proc.Argv[1:]...)
	cmd.Env = proc.Env
	cmd.Dir = proc.Dir
	defer stdio.finish()
	err := stdio.attach(cmd)
	if err == nil && proc.Credential != nil {
		// stdio chooses the process group or session; the identity is the
		// image's USER or the -u override (ADR-1790).
		cmd.SysProcAttr.Credential = proc.Credential
	}
	if err != nil {
		log.Error("runJob: attach command stdio", "err", err)
		return JobExitPayload{
			ExitCode:           126,
			ErrorClass:         "infra",
			LeaseToken:         m.LeaseToken,
			FinishedAtUnixNano: time.Now().UnixNano(),
		}
	}
	if err := cmd.Start(); err != nil {
		log.Error("runJob: os/exec start", "err", err, "command", proc.Argv[0])
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
	stdio.started()

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
			payload := jobExitPayloadFromWait(waitResult, reason, stopSignal, m.LeaseToken)
			if m.Env["GREGALE_OUTPUT_MANIFEST_PATH"] != "" {
				output, err := readGuestJobOutputManifest(m.Env["GREGALE_OUTPUT_MANIFEST_PATH"])
				if err != nil {
					log.Error("runJob: invalid output manifest", "err", err)
					if payload.ErrorClass == "succeeded" {
						payload.ExitCode = 65
						payload.ErrorClass = "failed"
					}
				} else {
					if len(output) > 0 {
						manifest, _ := jobresult.Validate(output) // validated by readGuestJobOutputManifest
						if payload.ErrorClass != "succeeded" && len(manifest.Artifacts) > 0 {
							// Artifact bytes are committed only with a successful task. Keep
							// the structured outcome code from failed tasks while dropping
							// artifact claims that the host will not publish.
							manifest.Artifacts = []jobresult.Artifact{}
							output, _ = json.Marshal(manifest)
						}
						payload.OutputManifest = output
					}
				}
			}
			return payload
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

type jobStartControl struct {
	fd        int
	started   chan struct{}
	signals   chan os.Signal
	startOnce sync.Once
	closeOnce sync.Once
}

func listenJobStartGate(log *slog.Logger) (*jobStartControl, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("create job start-gate vsock: %w", err)
	}
	addr := &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: VsockJobControlPort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("bind job start-gate vsock: %w", err)
	}
	if err := unix.Listen(fd, 8); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("listen job start-gate vsock: %w", err)
	}
	control := &jobStartControl{fd: fd, started: make(chan struct{}), signals: make(chan os.Signal, 2)}
	go control.accept(log)
	return control, nil
}

func (c *jobStartControl) Close() {
	if c != nil {
		c.closeOnce.Do(func() { _ = unix.Close(c.fd) })
	}
}

func (c *jobStartControl) accept(log *slog.Logger) {
	for {
		raw, peer, err := unix.Accept4(c.fd, unix.SOCK_CLOEXEC)
		if err != nil {
			return
		}
		vmPeer, ok := peer.(*unix.SockaddrVM)
		if !ok || vmPeer.CID != unix.VMADDR_CID_HOST {
			_ = unix.Close(raw)
			continue
		}
		go handleJobStartControl(raw, c.started, c.signals, &c.startOnce)
	}
}

func handleJobStartControl(fd int, started chan struct{}, signals chan<- os.Signal, startOnce *sync.Once) {
	f := os.NewFile(uintptr(fd), "job-control")
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
	msgType, value := binary.BigEndian.Uint32(frame[:4]), binary.BigEndian.Uint32(frame[4:])
	switch msgType {
	case VsockJobStartMsgType:
		if value != 0 {
			_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
			return
		}
		startOnce.Do(func() { close(started) })
		_, _ = unix.Write(fd, []byte{VsockJobControlAckOK})
	case VsockJobCancelMsgType:
		sig := syscall.Signal(value)
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
	default:
		_, _ = unix.Write(fd, []byte{VsockJobControlAckError})
	}
}

func waitForJobStart(started <-chan struct{}, signals <-chan os.Signal, timeout time.Duration) (bool, os.Signal) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-started:
		select {
		case signal := <-signals:
			return false, signal
		default:
			return true, nil
		}
	case signal := <-signals:
		return false, signal
	case <-timer.C:
		return false, nil
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

func readGuestJobOutputManifest(path string) (json.RawMessage, error) {
	f, err := os.Open(path) //nolint:forbidigo // This platform-owned guest path is read only for the job result manifest.
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, jobresult.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if _, err := jobresult.Validate(raw); err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
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

	// Retry until acknowledged, for jobExitShipBudget: a vmmd restart can
	// leave no listener for a few seconds before its successor re-binds.
	attempts, err := retryJobExit(jobExitShipBudget, time.Now, time.Sleep, func(i int) error {
		err := shipExitOnce(frame)
		if err != nil {
			log.Warn("job-exit vsock attempt", "err", err, "attempt", i)
		}
		return err
	})
	if err != nil {
		log.Error("job-exit all attempts failed", "attempts", attempts)
		return fmt.Errorf("job-exit all attempts failed: %w", err)
	}
	log.Info("job-exit shipped",
		"exit_code", payload.ExitCode,
		"error_class", payload.ErrorClass,
		"signal", payload.Signal,
		"attempt", attempts)
	return nil
}

// shipExitOnce makes one connect, write and ack wait. Connect, write and the
// ack wait are each bounded, so a missing host listener costs one bounded
// attempt.
func shipExitOnce(frame []byte) error {
	sock, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer func() { _ = unix.Close(sock) }()
	if err := setSockTimeout(sock, unix.SO_SNDTIMEO, 1500*time.Millisecond); err != nil {
		return fmt.Errorf("send timeout: %w", err)
	}
	if err := setSockTimeout(sock, unix.SO_RCVTIMEO, 1500*time.Millisecond); err != nil {
		return fmt.Errorf("receive timeout: %w", err)
	}
	if err := unix.Connect(sock, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockJobExitPort}); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := writeJobExitFrame(sock, frame); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	var ack [1]byte
	n, err := unix.Read(sock, ack[:])
	switch {
	case err != nil:
		return fmt.Errorf("read ack: %w", err)
	case n != 1 || ack[0] != 0:
		return fmt.Errorf("invalid ack n=%d value=%d", n, ack[0])
	}
	return nil
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
