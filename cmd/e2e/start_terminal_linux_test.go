//go:build linux

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Only stdin is a terminal. Output stays plain and captured, as when the user
// redirects a transcript. No test-only CLI flag or prompt implementation is used.
type startTerminal struct {
	t      *testing.T
	cmd    *exec.Cmd
	input  *os.File
	output syncBuffer
	done   chan struct{}
	err    error
	cursor int
	token  string
}

func newStartTerminal(t *testing.T, bin, dir, token string, env []string) *startTerminal {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open start terminal: %v", err)
	}
	master := os.NewFile(uintptr(fd), "start-terminal")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock start terminal: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("locate start terminal: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open start terminal input: %v", err)
	}
	defer func() { _ = slave.Close() }()
	// Disable echo: submitted paths and future secret input must not appear in
	// the terminal's unread master buffer or in failure diagnostics.
	settings, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	settings.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, settings); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	s := &startTerminal{t: t, input: master, done: make(chan struct{}), token: token}
	s.cmd = exec.CommandContext(ctx, bin, "start")
	s.cmd.Dir, s.cmd.Env, s.cmd.Stdin = dir, env, slave
	s.cmd.Stdout, s.cmd.Stderr = &s.output, &s.output
	if err := s.cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start gregale: %v", err)
	}
	go func() { s.err = s.cmd.Wait(); close(s.done) }()
	t.Cleanup(func() {
		cancel()
		<-s.done
		if t.Failed() {
			t.Logf("gregale start transcript:\n%s", s.transcript())
		}
	})
	return s
}

func (s *startTerminal) transcript() string {
	return strings.ReplaceAll(s.output.String(), s.token, "[REDACTED]")
}

func (s *startTerminal) expect(want string) {
	s.t.Helper()
	timer := time.NewTimer(40 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		// Observe exit before reading the output: Wait returns only after the
		// process's output is copied, so a process that printed want and then
		// exited is still matched below instead of being reported as an early
		// exit.
		exited := false
		select {
		case <-s.done:
			exited = true
		default:
		}
		out := s.output.String()
		if at := strings.Index(out[s.cursor:], want); at >= 0 {
			s.cursor += at + len(want)
			return
		}
		if want == "Keep building in this project." && strings.Contains(out[s.cursor:], "Your deployment is saved. Let's check its response.") {
			s.t.Fatalf("app response check failed\n%s", s.transcript())
		}
		if exited {
			s.t.Fatalf("gregale exited before %q: %v\n%s", want, s.err, s.transcript())
		}
		select {
		case <-s.done:
		case <-timer.C:
			s.t.Fatalf("timed out waiting for %q\n%s", want, s.transcript())
		case <-ticker.C:
		}
	}
}

func (s *startTerminal) answer(prompt, value string) {
	s.t.Helper()
	s.expect(prompt)
	if _, err := io.WriteString(s.input, value+"\n"); err != nil {
		s.t.Fatalf("answer start prompt: %v", err)
	}
}

func (s *startTerminal) wait(want int) {
	s.t.Helper()
	select {
	case <-s.done:
	case <-time.After(40 * time.Second):
		s.t.Fatalf("gregale did not exit\n%s", s.transcript())
	}
	code := 0
	var exit *exec.ExitError
	if errors.As(s.err, &exit) {
		code = exit.ExitCode()
	} else if s.err != nil {
		s.t.Fatalf("wait for gregale: %v", s.err)
	}
	if code != want {
		s.t.Fatalf("gregale exit=%d, want %d\n%s", code, want, s.transcript())
	}
}
