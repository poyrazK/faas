package main

// A template's start and ready commands (ADR-20261027090013 step 10): the
// start command runs in the background for the session's life, and the
// session serves only once the ready command exits 0, so the first call
// finds the template's server up.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

const (
	// sandboxReadyPollInterval spaces ready-command attempts.
	sandboxReadyPollInterval = time.Second
	// sandboxReadyAttemptMax bounds one ready-command attempt.
	sandboxReadyAttemptMax = 10 * time.Second
	// sandboxStartLog is where the start command's output goes.
	sandboxStartLog = "/tmp/gregale-start.log"
)

// errSandboxNotReady is a ready command that never passed in time.
var errSandboxNotReady = errors.New("sandbox: the template's ready command did not pass")

// runSandboxStartup starts m's start command as the session user, with its
// output in out, then runs the ready command until it exits 0 or the ready
// timeout passes. Without commands it returns at once.
func runSandboxStartup(ctx context.Context, runner sandboxRunner, m sandboxManifest, workspace, home string, out io.Writer, log *slog.Logger) error {
	env := sandboxEnv(home, "app", false, nil)
	if m.StartCmd != "" {
		p, err := runner.start(sandboxCommandSpec{argv: []string{"/bin/sh", "-c", m.StartCmd}, env: env, dir: workspace})
		if err != nil {
			return fmt.Errorf("sandbox: start command: %w", err)
		}
		_ = p.stdin().Close()
		go func() { _, _ = io.Copy(out, p.stdout()) }()
		go func() { _, _ = io.Copy(out, p.stderr()) }()
		go func() {
			exit := p.wait()
			p.closeOutput()
			log.Info("sandbox: start command ended", "code", exit.code)
		}()
	}
	if m.ReadyCmd == "" {
		return nil
	}
	deadline := time.Now().Add(time.Duration(m.ReadyTimeoutMs) * time.Millisecond)
	for attempt := 1; ; attempt++ {
		if code, err := runReadyAttempt(ctx, runner, m.ReadyCmd, env, workspace); err == nil && code == 0 {
			log.Info("sandbox: ready", "attempts", attempt)
			return nil
		}
		if time.Now().Add(sandboxReadyPollInterval).After(deadline) {
			return fmt.Errorf("%w in %d ms", errSandboxNotReady, m.ReadyTimeoutMs)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sandboxReadyPollInterval):
		}
	}
}

// runReadyAttempt runs the ready command once, for at most
// sandboxReadyAttemptMax, and returns its exit code.
func runReadyAttempt(ctx context.Context, runner sandboxRunner, cmd string, env []string, dir string) (int, error) {
	p, err := runner.start(sandboxCommandSpec{argv: []string{"/bin/sh", "-c", cmd}, env: env, dir: dir})
	if err != nil {
		return -1, err
	}
	_ = p.stdin().Close()
	go func() { _, _ = io.Copy(io.Discard, p.stdout()) }()
	go func() { _, _ = io.Copy(io.Discard, p.stderr()) }()
	exited := make(chan sandboxExit, 1)
	go func() { exited <- p.wait() }()
	timer := time.NewTimer(sandboxReadyAttemptMax)
	defer timer.Stop()
	select {
	case exit := <-exited:
		p.closeOutput()
		return exit.code, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	_ = p.signal(9)
	<-exited
	p.closeOutput()
	return -1, errors.New("sandbox: ready attempt timed out")
}
