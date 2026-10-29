//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// runAgentGuest serves exactly one agent run and powers off on every path,
// like an execution guest: a disposable run never falls back into an app or
// idle state. vmmd owns the VM's lifetime, so there is no accept deadline.
func runAgentGuest(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	defer func() { _ = poweroffExecution() }()
	// A run VM has no network interface, so nothing has brought loopback up
	// for the relay listener yet (ADR-20261027090010 §7). Without it the harness's
	// model calls fail fast; the run itself still runs.
	if err := bringUpLoopback(); err != nil {
		log.Warn("agent run: loopback unavailable", "err", err)
	} else if err := startOutboundRelay(log); err != nil {
		log.Warn("agent run: outbound relay unavailable", "err", err)
	}
	ln, err := listenGuestVsock(agentrunproto.VsockPort)
	if err != nil {
		log.Error("agent run: listen", "err", err)
		return err
	}
	// A preset snapshot is captured here, waiting for the run (ADR-20261027090010
	// §6): the resume hook reseeds entropy and steps the clock on every
	// restore before the run's request can arrive (spec §11 V6).
	agentRunGuest.Store(true)
	if err := listenResumeHook(log); err != nil {
		log.Warn("agent run: resume listener unavailable", "err", err)
	}
	guestStage(agentReadyStage)
	conn, err := ln.Accept()
	_ = ln.Close()
	if err != nil {
		log.Error("agent run: accept", "err", err)
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := agentrunproto.Serve(context.Background(), conn, runAgentRequest(log)); err != nil {
		log.Error("agent run: serve", "err", err)
		return err
	}
	return nil
}

// runAgentRequest stages the inputs, runs the harness under the job
// supervisor (timeout, SIGTERM then SIGKILL, the whole process group), and
// collects the outputs of a successful run.
func runAgentRequest(log *slog.Logger) agentrunproto.Handler {
	return func(_ context.Context, req agentrunproto.Request, stdout, stderr io.Writer, cancelled <-chan struct{}) (agentrunproto.Result, error) {
		home, err := prepareAgentRun(req)
		if err != nil {
			log.Error("agent run: prepare", "err", err)
			return agentrunproto.Result{Status: agentrunproto.StatusFailed, ExitCode: 126, FailureCode: agentrunproto.FailureInfra}, nil
		}
		if req.Checkout != nil {
			if code, err := stageAgentCheckout(*req.Checkout); err != nil {
				log.Error("agent run: checkout", "err", err)
				return agentrunproto.Result{Status: agentrunproto.StatusFailed, ExitCode: 126, FailureCode: code}, nil
			}
		}
		env := agentRunEnv(home, req.Env)
		argv := append([]string(nil), req.Command...)
		argv[0] = resolveWorkloadCommandPath("/", argv[0], env)
		proc := jobProcess{Argv: argv, Env: env, Dir: agentWorkDir,
			Credential: &syscall.Credential{Uid: agentUID, Gid: agentUID, Groups: []uint32{}}}
		signals := make(chan os.Signal, 1)
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-cancelled:
				signals <- syscall.SIGTERM
			case <-done:
			}
		}()
		oomBefore := readOOMKills()
		started := time.Now()
		stdio, err := newAgentRunStdio(stdout, stderr, agentUID)
		if err != nil {
			log.Error("agent run: output pipes", "err", err)
			return agentrunproto.Result{Status: agentrunproto.StatusFailed, ExitCode: 126, FailureCode: agentrunproto.FailureInfra}, nil
		}
		payload := superviseJobCommandWithStdio(JobManifest{Kind: "job", RunID: req.RunID, Command: req.Command,
			TaskTimeoutSec: req.TimeoutSec}, proc, api.AgentRunStopGraceSec*time.Second, log, stdio, signals)
		status, failure := agentRunStatus(payload, readOOMKills() > oomBefore)
		result := agentrunproto.Result{Status: status, ExitCode: payload.ExitCode, Signal: payload.Signal, FailureCode: failure,
			StartedAtUnixNano: started.UnixNano(), FinishedAtUnixNano: payload.FinishedAtUnixNano}
		if status != agentrunproto.StatusSucceeded {
			return result, nil
		}
		outputs, err := collectAgentOutputs(agentOutputDir, req)
		if err != nil {
			log.Error("agent run: collect outputs", "err", err)
			result.Status, result.FailureCode = agentrunproto.StatusFailed, agentrunproto.FailureInfra
			return result, nil
		}
		if outputs.failureCode != "" {
			result.Status, result.FailureCode = agentrunproto.StatusFailed, outputs.failureCode
			return result, nil
		}
		result.Result, result.Files = outputs.result, outputs.files
		return result, nil
	}
}

// prepareAgentRun writes the inputs and gives the harness its working,
// output and home directories. It returns the home directory.
func prepareAgentRun(req agentrunproto.Request) (string, error) {
	if err := stageAgentInputs(agentInputDir, req.Files); err != nil {
		return "", err
	}
	home := imagePasswdHome(agentUID)
	if home == "/" || home == "" {
		home = "/home/app"
	}
	for _, dir := range []string{agentWorkDir, agentOutputDir, home} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chown(dir, agentUID, agentUID); err != nil {
			return "", fmt.Errorf("chown %s: %w", dir, err)
		}
	}
	return home, nil
}

// agentRunStdio gives the harness output pipes its own user owns, and drains
// them before the run's result is sent. As PID 1 guest-init reaps with
// wait4(-1), not cmd.Wait, so exec's copy goroutines for a plain writer
// could still be running when supervision returns and drop a fast command's
// output (seen on a restored run whose command took 31 ms).
type agentRunStdio struct{ out, err *workloadOutputPipe }

func newAgentRunStdio(stdout, stderr io.Writer, uid int) (*agentRunStdio, error) {
	out, err := newWorkloadOutputPipe(stdout, uid, uid)
	if err != nil {
		return nil, err
	}
	errPipe, err := newWorkloadOutputPipe(stderr, uid, uid)
	if err != nil {
		out.finish()
		return nil, err
	}
	return &agentRunStdio{out: out, err: errPipe}, nil
}

func (s *agentRunStdio) attach(cmd *exec.Cmd) error {
	cmd.Stdout, cmd.Stderr = s.out.w, s.err.w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func (s *agentRunStdio) started() {
	s.out.closeWriter()
	s.err.closeWriter()
}

func (s *agentRunStdio) finish() {
	s.out.finish()
	s.err.finish()
}

// agentInputDrive is the input drive's place after the base and the agent
// layer (ADR-20261027090010 §3).
const agentInputDrive = 2

// agentReadyStage is the console stage vmmd waits for before it captures a
// preset snapshot.
const agentReadyStage = "agent-ready"

// stageAgentCheckout verifies the input drive against the request and
// unpacks it at /input/repo, owned by the harness's user so it can edit the
// tree. It returns the failure code to report on error.
func stageAgentCheckout(c agentrunproto.Checkout) (string, error) {
	//nolint:forbidigo // guest-init reads its own input drive.
	drive, err := os.Open(blkDriveDevice(agentInputDrive, bootRootOnPmem()))
	if err != nil {
		return agentrunproto.FailureCheckout, fmt.Errorf("open input drive: %w", err)
	}
	defer func() { _ = drive.Close() }()
	// A restored guest's page cache still holds the drive's sectors from
	// the capture boot's partition scan; drop them so the read reaches this
	// run's image.
	if err := unix.IoctlSetInt(int(drive.Fd()), unix.BLKFLSBUF, 0); err != nil {
		return agentrunproto.FailureCheckout, fmt.Errorf("flush input drive cache: %w", err)
	}
	archive, err := agentrunproto.OpenInputArchive(drive, c)
	if err != nil {
		return agentrunproto.FailureCheckout, err
	}
	dir := filepath.Join(agentInputDir, agentrunproto.CheckoutDir)
	if err := agentrunproto.ExtractCheckout(archive, dir, c); err != nil {
		if errors.Is(err, agentrunproto.ErrCheckoutTooLarge) {
			return agentrunproto.FailureCheckoutTooLarge, err
		}
		return agentrunproto.FailureCheckout, err
	}
	err = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return os.Lchown(p, agentUID, agentUID)
	})
	if err != nil {
		return agentrunproto.FailureCheckout, fmt.Errorf("chown checkout: %w", err)
	}
	return "", nil
}

// readOOMKills is the guest kernel's oom_kill counter (Linux 4.13+); zero
// when it cannot be read, which reports OOM deaths as plain failures.
func readOOMKills() int64 {
	//nolint:forbidigo // guest-init reads its own kernel's counters.
	f, err := os.Open("/proc/vmstat")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	return oomKillCount(f)
}
