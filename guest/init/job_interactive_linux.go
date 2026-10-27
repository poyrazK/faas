//go:build linux

package main

// Interactive image runs (ADR-1330): the job command waits for one session
// on the app-task session port, then runs with the session's terminal or
// pipes as its stdio. The job supervisor still owns the task timeout, host
// cancellation and the exit receipt; the session's terminal result carries
// the same outcome to the client.

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	osSignal "os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func runInteractiveJob(m JobManifest, proc jobProcess, log *slog.Logger) error {
	signals := make(chan os.Signal, 4)
	osSignal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP)
	defer osSignal.Stop(signals)
	closeControl := listenJobCancellation(signals, log)
	defer closeControl()

	ln, err := listenAppTaskHook()
	if err != nil {
		log.Error("runJob: interactive session listener unavailable", "err", err)
		return shipAndPoweroff(JobExitPayload{ExitCode: 75, ErrorClass: "infra", LeaseToken: m.LeaseToken}, log)
	}
	conn, cancelSignal := acceptJobSession(ln, time.Duration(m.Interactive.AttachTimeoutSec)*time.Second, signals)
	if conn == nil {
		payload := JobExitPayload{ExitCode: jobAttachTimeoutExitCode, ErrorClass: "failed",
			LeaseToken: m.LeaseToken, FinishedAtUnixNano: time.Now().UnixNano()}
		if sig, ok := cancelSignal.(syscall.Signal); ok {
			payload.ExitCode, payload.ErrorClass, payload.Signal = 128+int32(sig), "cancelled", int32(sig)
		} else {
			log.Warn("runJob: no client attached before the attach window closed")
		}
		return shipAndPoweroff(payload, log)
	}
	return shipAndPoweroff(serveInteractiveJobSession(conn, m, proc, signals, log), log)
}

// acceptJobSession waits for the host's session connection. It returns no
// connection when the run is cancelled first (with the signal) or when the
// attach window closes.
func acceptJobSession(ln net.Listener, window time.Duration, signals <-chan os.Signal) (net.Conn, os.Signal) {
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()
	timer := time.NewTimer(window)
	defer timer.Stop()
	var conn net.Conn
	var sig os.Signal
	select {
	case c, ok := <-accepted:
		if ok {
			conn = c
		}
	case <-timer.C:
	case sig = <-signals:
	}
	_ = ln.Close()
	if conn == nil {
		// A connection accepted while the listener closed is dropped: the
		// run is already over.
		if late, ok := <-accepted; ok {
			_ = late.Close()
		}
	}
	return conn, sig
}

// serveInteractiveJobSession runs the job command for one session and
// returns the exit receipt. A request that never starts the command (a
// batch request, or one that fails validation) is an infrastructure failure.
func serveInteractiveJobSession(conn net.Conn, m JobManifest, proc jobProcess, signals <-chan os.Signal, log *slog.Logger) JobExitPayload {
	defer func() { _ = conn.Close() }()
	var payload *JobExitPayload
	batch := func(context.Context, apptaskproto.Request, *apptaskproto.OutputWriter, *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
		return appTaskInfraFailure("interactive_required", "this task accepts only an interactive session", 2), nil
	}
	interactive := func(ctx context.Context, session *apptaskproto.InteractiveSession) (apptaskproto.Result, error) {
		result := superviseInteractiveJob(ctx, m, proc, session, signals, log)
		payload = &result
		return jobSessionResult(result), nil
	}
	if err := apptaskproto.ServeSession(context.Background(), conn, batch, interactive); err != nil {
		log.Warn("runJob: interactive session exchange failed", "err", err)
	} else if err := awaitAppTaskHostClose(context.Background(), conn); err != nil {
		log.Debug("runJob: interactive session host close", "err", err)
	}
	if payload == nil {
		return JobExitPayload{ExitCode: 75, ErrorClass: "infra", LeaseToken: m.LeaseToken, FinishedAtUnixNano: time.Now().UnixNano()}
	}
	return *payload
}

func superviseInteractiveJob(ctx context.Context, m JobManifest, proc jobProcess, session *apptaskproto.InteractiveSession, signals <-chan os.Signal, log *slog.Logger) JobExitPayload {
	stop := make(chan os.Signal, 4)
	done := make(chan struct{})
	defer close(done)
	go forwardJobSessionSignals(ctx, signals, stop, done)
	var stdio jobStdio = &jobPipeStdio{session: session}
	if session.Request.TTY {
		stdio = &jobTTYStdio{session: session, credential: proc.Credential}
	}
	return superviseJobCommandWithStdio(m, proc, jobTerminationGrace, log, stdio, stop)
}

// jobTTYStdio runs the command on a fresh pseudo-terminal whose master is
// relayed to the session.
type jobTTYStdio struct {
	session       *apptaskproto.InteractiveSession
	credential    *syscall.Credential
	master, slave *os.File
	outputDone    chan struct{}
}

func (s *jobTTYStdio) attach(cmd *exec.Cmd) error {
	// The slave belongs to the process identity so programs that reopen
	// their terminal by name keep working after the credential drop.
	master, slave, err := openAppTaskPTY(s.credential)
	if err != nil {
		return err
	}
	if err := setAppTaskPTYSize(master, s.session.Request.Rows, s.session.Request.Cols); err != nil {
		_ = master.Close()
		_ = slave.Close()
		return err
	}
	s.master, s.slave = master, slave
	cmd.Env = withDefaultTerm(cmd.Env)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// The slave is the child's fd 0; Setsid + Setctty make it the controlling
	// terminal of a new session whose process group is the child's pid, so
	// the supervisor's process-group signals still reach every descendant.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	return nil
}

func (s *jobTTYStdio) started() {
	_ = s.slave.Close()
	s.slave = nil
	s.outputDone = make(chan struct{})
	go func() {
		defer close(s.outputDone)
		// Reading the master returns EIO once every slave descriptor is
		// closed, which ends the copy.
		_, _ = io.Copy(s.session.Stdout, s.master)
	}()
	go applyAppTaskInput(s.session.Input, func(event apptaskproto.InputEvent) error {
		switch event.Kind {
		case apptaskproto.InputStdin:
			_, err := s.master.Write(event.Data)
			return err
		case apptaskproto.InputResize:
			return setAppTaskPTYSize(s.master, event.Rows, event.Cols)
		case apptaskproto.InputStdinClose:
			_, err := s.master.Write([]byte{appTaskTTYEndOfTransmit})
			return err
		}
		return nil
	})
}

func (s *jobTTYStdio) finish() {
	if s.slave != nil {
		_ = s.slave.Close()
	}
	if s.master == nil {
		return
	}
	if s.outputDone != nil {
		drainJobOutput(s.outputDone)
	}
	_ = s.master.Close()
	if s.outputDone != nil {
		<-s.outputDone
	}
}

// jobPipeStdio runs the command with plain pipes. They are explicit os.Pipe
// pairs rather than exec's copying goroutines because guest-init as PID 1
// reaps the command itself and never calls cmd.Wait.
type jobPipeStdio struct {
	session    *apptaskproto.InteractiveSession
	stdin      *os.File
	childEnds  []*os.File
	readers    []*os.File
	outputDone chan struct{}
}

func (s *jobPipeStdio) attach(cmd *exec.Cmd) error {
	var files []*os.File
	pipe := func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err == nil {
			files = append(files, r, w)
		}
		return r, w, err
	}
	inR, inW, err := pipe()
	var outR, outW, errR, errW *os.File
	if err == nil {
		outR, outW, err = pipe()
	}
	if err == nil {
		errR, errW, err = pipe()
	}
	if err != nil {
		closeJobFiles(files)
		return err
	}
	s.stdin, s.childEnds, s.readers = inW, []*os.File{inR, outW, errW}, []*os.File{outR, errR}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func (s *jobPipeStdio) started() {
	closeJobFiles(s.childEnds)
	s.childEnds = nil
	var copies sync.WaitGroup
	for i, writer := range []io.Writer{s.session.Stdout, s.session.Stderr} {
		copies.Go(func() { _, _ = io.Copy(writer, s.readers[i]) })
	}
	s.outputDone = make(chan struct{})
	go func() {
		copies.Wait()
		close(s.outputDone)
	}()
	go applyAppTaskInput(s.session.Input, func(event apptaskproto.InputEvent) error {
		switch event.Kind {
		case apptaskproto.InputStdin:
			_, err := s.stdin.Write(event.Data)
			return err
		case apptaskproto.InputStdinClose:
			return s.stdin.Close()
		}
		return nil
	})
}

func (s *jobPipeStdio) finish() {
	closeJobFiles(s.childEnds)
	if s.outputDone != nil {
		drainJobOutput(s.outputDone)
	}
	closeJobFiles(s.readers)
	if s.outputDone != nil {
		<-s.outputDone
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
}

// drainJobOutput waits briefly for output to reach the session. A background
// process that kept a descriptor open must not hold the session open.
func drainJobOutput(done <-chan struct{}) {
	timer := time.NewTimer(appTaskOutputDrainWindow)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func closeJobFiles(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
