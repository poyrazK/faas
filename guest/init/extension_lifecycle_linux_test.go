//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/extension"
	"golang.org/x/sys/unix"
)

func TestHandleResumeConnPreSnapshotDispatchesCallback(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	guest := os.NewFile(uintptr(fds[0]), "guest-hook")
	host := os.NewFile(uintptr(fds[1]), "host-hook")
	t.Cleanup(func() {
		_ = guest.Close()
		_ = host.Close()
	})

	var callbacks atomic.Int32
	done := make(chan struct{})
	go func() {
		handleResumeConnWithCallbacks(guest, slog.Default(), resumeHookCallbacks{
			onPreSnapshot: func() { callbacks.Add(1) },
		})
		close(done)
	}()

	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], VsockPreSnapshotMsgType)
	// A zero-length body is the host's pre-snapshot envelope.
	if _, err := host.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	var ack [1]byte
	if _, err := io.ReadFull(host, ack[:]); err != nil {
		t.Fatal(err)
	}
	if ack[0] != VsockResumeAckOK {
		t.Fatalf("ack = %d, want %d", ack[0], VsockResumeAckOK)
	}
	<-done
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("pre-snapshot callbacks = %d, want 1", got)
	}
}

func TestWithInvokeLifecycleEmitsStartAndEnd(t *testing.T) {
	events := make(chan extension.Event, 2)
	dispatcher := extension.NewDispatcher("in-memory")
	dispatcher.Dial = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			line, err := bufio.NewReader(server).ReadBytes('\n')
			if err != nil {
				return
			}
			var event extension.Event
			if json.Unmarshal(line[:len(line)-1], &event) != nil {
				return
			}
			events <- event
			ack, _ := json.Marshal(extension.Ack{Version: extension.ProtocolVersion, Sequence: event.Sequence, Status: extension.AckOK})
			_, _ = server.Write(append(ack, '\n'))
		}()
		return client, nil
	}
	hooks := &extensionLifecycle{dispatcher: dispatcher, log: slog.Default()}
	handler := withInvokeLifecycle(func(context.Context, executionproto.Request, *executionproto.OutputWriter, *executionproto.OutputWriter) (executionproto.Result, error) {
		return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`null`)}, nil
	}, hooks)
	_, err := handler(context.Background(), executionproto.Request{
		ExecutionID: "invoke-test",
		Runtime:     api.ExecutionRuntimeNode22,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	start, end := <-events, <-events
	if start.Phase != extension.PhaseInvoke || start.Metadata["state"] != "start" || start.Metadata["execution_id"] != "invoke-test" {
		t.Fatalf("start event = %+v", start)
	}
	if end.Phase != extension.PhaseInvoke || end.Metadata["state"] != "end" || end.Metadata["status"] != string(api.ExecutionStatusSucceeded) {
		t.Fatalf("end event = %+v", end)
	}
}
