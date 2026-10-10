//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

type interactiveTestOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *interactiveTestOutput) receive(_ context.Context, _ string, chunk []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf.Write(chunk)
	return nil
}

func (o *interactiveTestOutput) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		got := o.buf.String()
		o.mu.Unlock()
		if strings.Contains(got, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	t.Fatalf("output never contained %q; got %q", want, o.buf.String())
}

func requirePTY(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(appTaskPtsDir + "/ptmx"); err != nil {
		t.Skip("host has no devpts instance")
	}
}

// startInteractiveTest serves one interactive request whose process is argv,
// run by the production TTY or pipe runner, and returns the host client side.
func startInteractiveTest(t *testing.T, ctx context.Context, tty bool, argv ...string) (net.Conn, <-chan error) {
	t.Helper()
	host, guest := net.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- apptaskproto.ServeSession(ctx, guest, func(context.Context, apptaskproto.Request, *apptaskproto.OutputWriter, *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
			return apptaskproto.Result{}, errors.New("batch handler must not run")
		}, func(sessionCtx context.Context, session *apptaskproto.InteractiveSession) (apptaskproto.Result, error) {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			if tty {
				return runAppTaskTTY(sessionCtx, session, cmd, nil, slog.Default())
			}
			return runAppTaskPipes(sessionCtx, session, cmd, nil)
		})
	}()
	return host, served
}

func TestInteractiveTTYRoundTrip(t *testing.T) {
	requirePTY(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, served := startInteractiveTest(t, ctx, true, "/bin/sh", "-c",
		`[ -t 0 ] && echo tty:yes; echo term:$TERM; stty size; read line; echo got:$line; read again; stty size; exit 7`)
	defer host.Close()
	client, _ := apptaskproto.NewClient(host)
	input := make(chan apptaskproto.InputEvent, 4)
	out := &interactiveTestOutput{}
	done := make(chan struct{})
	var result apptaskproto.Result
	var interactErr error
	go func() {
		defer close(done)
		result, interactErr = client.Interact(ctx, apptaskproto.Request{
			Version: apptaskproto.InteractiveVersion, TaskID: "task-tty", Command: []string{"/bin/sh"},
			TimeoutSeconds: 10, Interactive: true, TTY: true, Rows: 31, Cols: 97,
		}, input, out.receive)
	}()
	out.waitFor(t, "31 97")
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdin, Data: []byte("hello\n")}
	out.waitFor(t, "got:hello")
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputResize, Rows: 40, Cols: 132}
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdin, Data: []byte("\n")}
	<-done
	if interactErr != nil {
		t.Fatal(interactErr)
	}
	if result.Status != apptaskproto.StatusFailed || result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatalf("result = %+v", result)
	}
	for _, want := range []string{"tty:yes", "term:" + appTaskDefaultTerm, "40 132"} {
		out.waitFor(t, want)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveTTYStdinCloseEndsShell(t *testing.T) {
	requirePTY(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, served := startInteractiveTest(t, ctx, true, "/bin/cat")
	defer host.Close()
	client, _ := apptaskproto.NewClient(host)
	input := make(chan apptaskproto.InputEvent, 2)
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdinClose}
	result, err := client.Interact(ctx, apptaskproto.Request{
		Version: apptaskproto.InteractiveVersion, TaskID: "task-eof", Command: []string{"/bin/cat"},
		TimeoutSeconds: 10, Interactive: true, TTY: true, Rows: 24, Cols: 80,
	}, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveHangupKillsProcessGroup(t *testing.T) {
	requirePTY(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	marker := t.TempDir() + "/child.pid"
	host, served := startInteractiveTest(t, ctx, true, "/bin/sh", "-c", `sleep 300 & echo $! > `+marker+`; echo ready; wait`)
	client, _ := apptaskproto.NewClient(host)
	out := &interactiveTestOutput{}
	clientCtx, detach := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := client.Interact(clientCtx, apptaskproto.Request{
			Version: apptaskproto.InteractiveVersion, TaskID: "task-hup", Command: []string{"/bin/sh"},
			TimeoutSeconds: 10, Interactive: true, TTY: true, Rows: 24, Cols: 80,
		}, nil, out.receive)
		done <- err
	}()
	out.waitFor(t, "ready")
	detach()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Interact error = %v", err)
	}
	select {
	case <-served:
	case <-ctx.Done():
		t.Fatal("guest session did not end after hang-up")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(string(raw))
	if _, err := os.Stat("/proc/" + pid); err == nil {
		if status, _ := os.ReadFile("/proc/" + pid + "/stat"); !strings.Contains(string(status), ") Z ") {
			t.Fatalf("background child %s survived the hang-up", pid)
		}
	}
}

func TestInteractivePipesRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, served := startInteractiveTest(t, ctx, false, "/bin/sh", "-c", `[ -t 0 ] || echo tty:no; cat; echo done >&2`)
	defer host.Close()
	client, _ := apptaskproto.NewClient(host)
	input := make(chan apptaskproto.InputEvent, 3)
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdin, Data: []byte("piped\n")}
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdinClose}
	out := &interactiveTestOutput{}
	result, err := client.Interact(ctx, apptaskproto.Request{
		Version: apptaskproto.InteractiveVersion, TaskID: "task-pipe", Command: []string{"/bin/sh"},
		TimeoutSeconds: 10, Interactive: true,
	}, input, out.receive)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("result = %+v", result)
	}
	for _, want := range []string{"tty:no", "piped", "done"} {
		out.waitFor(t, want)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestWithDefaultTermKeepsExplicitValue(t *testing.T) {
	if got := withDefaultTerm([]string{"TERM=dumb"}); len(got) != 1 || got[0] != "TERM=dumb" {
		t.Fatalf("explicit TERM replaced: %v", got)
	}
	if got := withDefaultTerm(nil); len(got) != 1 || got[0] != "TERM="+appTaskDefaultTerm {
		t.Fatalf("default TERM missing: %v", got)
	}
}
