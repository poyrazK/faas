//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// startJobSessionTest serves one interactive job session for argv with the
// production supervisor and returns the host side and the exit receipt.
func startJobSessionTest(t *testing.T, taskTimeoutSec int, signals chan os.Signal, argv ...string) (net.Conn, <-chan JobExitPayload) {
	t.Helper()
	host, guest := net.Pipe()
	m := JobManifest{Kind: "job", Command: argv, TaskTimeoutSec: taskTimeoutSec, LeaseToken: "lease",
		Interactive: &JobInteractive{AttachTimeoutSec: 5}}
	if signals == nil {
		signals = make(chan os.Signal, 1)
	}
	receipts := make(chan JobExitPayload, 1)
	go func() {
		receipts <- serveInteractiveJobSession(guest, m, guestJobProcess(m, []string{"PATH=/usr/bin:/bin"}), signals, slog.Default())
	}()
	t.Cleanup(func() { _ = host.Close() })
	return host, receipts
}

func jobSessionRequest(tty bool, timeoutSec int) apptaskproto.Request {
	req := apptaskproto.Request{Version: apptaskproto.InteractiveVersion, TaskID: "job-instance",
		Command: []string{"job"}, TimeoutSeconds: timeoutSec, Interactive: true, TTY: tty}
	if tty {
		req.Rows, req.Cols = 31, 97
	}
	return req
}

func awaitReceipt(t *testing.T, receipts <-chan JobExitPayload) JobExitPayload {
	t.Helper()
	select {
	case p := <-receipts:
		return p
	case <-time.After(60 * time.Second):
		t.Fatal("no job exit receipt")
		return JobExitPayload{}
	}
}

func TestInteractiveJobPipesRelayAndExitCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, receipts := startJobSessionTest(t, 30, nil, "/bin/sh", "-c", `[ -t 0 ] || echo tty:no; read line; echo got:$line; echo err >&2; exit 3`)
	client, _ := apptaskproto.NewClient(host)
	input := make(chan apptaskproto.InputEvent, 2)
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdin, Data: []byte("hello\n")}
	out := &interactiveTestOutput{}
	result, err := client.Interact(ctx, jobSessionRequest(false, 30), input, out.receive)
	if err != nil {
		t.Fatal(err)
	}
	_ = host.Close()
	if result.Status != apptaskproto.StatusFailed || result.ExitCode == nil || *result.ExitCode != 3 {
		t.Fatalf("result = %+v", result)
	}
	for _, want := range []string{"tty:no", "got:hello", "err"} {
		out.waitFor(t, want)
	}
	if p := awaitReceipt(t, receipts); p.ExitCode != 3 || p.ErrorClass != "failed" || p.LeaseToken != "lease" {
		t.Fatalf("receipt = %+v", p)
	}
}

func TestInteractiveJobTTYRoundTrip(t *testing.T) {
	requirePTY(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, receipts := startJobSessionTest(t, 30, nil, "/bin/sh", "-c", `[ -t 0 ] && echo tty:yes; stty size; read line; echo got:$line`)
	client, _ := apptaskproto.NewClient(host)
	input := make(chan apptaskproto.InputEvent, 2)
	out := &interactiveTestOutput{}
	done := make(chan struct{})
	var result apptaskproto.Result
	var interactErr error
	go func() {
		defer close(done)
		result, interactErr = client.Interact(ctx, jobSessionRequest(true, 30), input, out.receive)
	}()
	out.waitFor(t, "31 97")
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdin, Data: []byte("hi\n")}
	<-done
	_ = host.Close()
	if interactErr != nil {
		t.Fatal(interactErr)
	}
	if result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("result = %+v", result)
	}
	out.waitFor(t, "tty:yes")
	out.waitFor(t, "got:hi")
	if p := awaitReceipt(t, receipts); p.ExitCode != 0 || p.ErrorClass != "succeeded" {
		t.Fatalf("receipt = %+v", p)
	}
}

func TestInteractiveJobClientDisconnectCancelsTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, receipts := startJobSessionTest(t, 30, nil, "/bin/sh", "-c", `echo ready; sleep 300`)
	client, _ := apptaskproto.NewClient(host)
	out := &interactiveTestOutput{}
	clientCtx, detach := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := client.Interact(clientCtx, jobSessionRequest(false, 30), nil, out.receive)
		done <- err
	}()
	out.waitFor(t, "ready")
	detach()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Interact error = %v", err)
	}
	if p := awaitReceipt(t, receipts); p.ErrorClass != "cancelled" || p.ExitCode != 128+int32(syscall.SIGHUP) {
		t.Fatalf("receipt = %+v, want cancelled by SIGHUP", p)
	}
}

func TestInteractiveJobTimeoutIsTaskTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The session limit equals the task timeout, as vmmd sends it; the
	// receipt must say timeout, not a client disconnect.
	host, receipts := startJobSessionTest(t, 1, nil, "/bin/sh", "-c", `echo ready; sleep 300`)
	client, _ := apptaskproto.NewClient(host)
	out := &interactiveTestOutput{}
	result, _ := client.Interact(ctx, jobSessionRequest(false, 1), nil, out.receive)
	_ = host.Close()
	if p := awaitReceipt(t, receipts); p.ErrorClass != "timeout" || p.ExitCode != 124 {
		t.Fatalf("receipt = %+v, want timeout 124 (client result %+v)", p, result)
	}
}

func TestInteractiveJobHostCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	signals := make(chan os.Signal, 1)
	host, receipts := startJobSessionTest(t, 30, signals, "/bin/sh", "-c", `echo ready; sleep 300`)
	client, _ := apptaskproto.NewClient(host)
	out := &interactiveTestOutput{}
	done := make(chan apptaskproto.Result, 1)
	go func() {
		result, _ := client.Interact(ctx, jobSessionRequest(false, 30), nil, out.receive)
		done <- result
	}()
	out.waitFor(t, "ready")
	signals <- syscall.SIGTERM
	result := <-done
	_ = host.Close()
	if result.FailureCode != "cancelled" {
		t.Fatalf("client result = %+v, want cancelled", result)
	}
	if p := awaitReceipt(t, receipts); p.ErrorClass != "cancelled" || p.ExitCode != 128+int32(syscall.SIGTERM) {
		t.Fatalf("receipt = %+v", p)
	}
}

func TestInteractiveJobRejectsBatchRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, receipts := startJobSessionTest(t, 30, nil, "/bin/sh", "-c", `echo must-not-run`)
	client, _ := apptaskproto.NewClient(host)
	result, err := client.Execute(ctx, apptaskproto.Request{Version: apptaskproto.Version, TaskID: "job-instance",
		Command: []string{"job"}, TimeoutSeconds: 30, MaxOutputBytes: apptaskproto.MinOutputBytes})
	_ = host.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.FailureCode != "interactive_required" {
		t.Fatalf("result = %+v", result)
	}
	if p := awaitReceipt(t, receipts); p.ErrorClass != "infra" || p.ExitCode != 75 {
		t.Fatalf("receipt = %+v, want infra 75", p)
	}
}

func TestAcceptJobSession(t *testing.T) {
	listen := func(t *testing.T) net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return ln
	}
	t.Run("connection", func(t *testing.T) {
		ln := listen(t)
		go func() {
			if conn, err := net.Dial("tcp", ln.Addr().String()); err == nil {
				defer conn.Close()
				time.Sleep(time.Second)
			}
		}()
		conn, sig := acceptJobSession(ln, 5*time.Second, make(chan os.Signal))
		if conn == nil || sig != nil {
			t.Fatalf("conn = %v, signal = %v", conn, sig)
		}
		_ = conn.Close()
	})
	t.Run("attach window closes", func(t *testing.T) {
		conn, sig := acceptJobSession(listen(t), 50*time.Millisecond, make(chan os.Signal))
		if conn != nil || sig != nil {
			t.Fatalf("conn = %v, signal = %v", conn, sig)
		}
	})
	t.Run("cancelled while waiting", func(t *testing.T) {
		signals := make(chan os.Signal, 1)
		signals <- syscall.SIGTERM
		conn, sig := acceptJobSession(listen(t), 5*time.Second, signals)
		if conn != nil || sig != syscall.SIGTERM {
			t.Fatalf("conn = %v, signal = %v", conn, sig)
		}
	})
}
