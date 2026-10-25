//go:build linux

package main

import (
	"encoding/binary"
	"log/slog"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// Production-us rc.294: scaling h14-wk in killed its VM in ~140 ms and the
// worker never logged its SIGTERM handler, because vmmd signalled the
// Firecracker host process. A worker's stop signal now arrives over the
// control port; a job keeps its narrower cancel set (Hunt 14 H14-59).
func TestControlPortDeliversWorkerStopSignals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allow   func(syscall.Signal) bool
		signal  syscall.Signal
		wantAck byte
	}{
		{"worker SIGTERM", isWorkloadStopSignal, syscall.SIGTERM, VsockJobControlAckOK},
		{"worker SIGUSR1", isWorkloadStopSignal, syscall.SIGUSR1, VsockJobControlAckOK},
		{"job refuses SIGUSR1", isJobCancelSignal, syscall.SIGUSR1, VsockJobControlAckError},
		{"worker refuses SIGKILL", isWorkloadStopSignal, syscall.SIGKILL, VsockJobControlAckError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(fds[0]) }()
			signals := make(chan os.Signal, 1)
			go handleControlSignal(fds[1], signals, tc.allow)
			var frame [8]byte
			binary.BigEndian.PutUint32(frame[:4], VsockJobCancelMsgType)
			binary.BigEndian.PutUint32(frame[4:], uint32(tc.signal))
			if _, err := unix.Write(fds[0], frame[:]); err != nil {
				t.Fatal(err)
			}
			var ack [1]byte
			if _, err := unix.Read(fds[0], ack[:]); err != nil || ack[0] != tc.wantAck {
				t.Fatalf("ack = %d (err %v), want %d", ack[0], err, tc.wantAck)
			}
			delivered := len(signals) == 1
			if delivered != (tc.wantAck == VsockJobControlAckOK) {
				t.Fatalf("delivered = %v, want %v", delivered, tc.wantAck == VsockJobControlAckOK)
			}
			if delivered {
				if got := <-signals; got != tc.signal {
					t.Fatalf("delivered %v, want %v", got, tc.signal)
				}
			}
		})
	}
}

// TestLivenessPortDeliversWorkerStopSignals is the metal finding behind H14-59:
// a long-running guest's liveness listener owns vsock port 1028, so the host's
// stop frame reached it and drew a liveness error reply whose first byte the
// host read as the job-control OK. The liveness listener now dispatches the
// stop frame to the registered workload signal loop and answers with the
// one-byte ack.
func TestLivenessPortDeliversWorkerStopSignals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		register bool
		signal   syscall.Signal
		wantAck  byte
	}{
		{"registered SIGTERM", true, syscall.SIGTERM, VsockJobControlAckOK},
		{"registered SIGUSR2", true, syscall.SIGUSR2, VsockJobControlAckOK},
		{"no signal loop yet", false, syscall.SIGTERM, VsockJobControlAckError},
		{"SIGKILL refused", true, syscall.SIGKILL, VsockJobControlAckError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signals := make(chan os.Signal, 1)
			if tc.register {
				defer registerWorkloadStopSignals(signals)()
			}
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(fds[0]) }()
			done := make(chan struct{})
			go func() {
				defer close(done)
				handleLivenessConn(os.NewFile(uintptr(fds[1]), "liveness"), slog.New(slog.DiscardHandler))
			}()
			var frame [8]byte
			binary.BigEndian.PutUint32(frame[:4], VsockJobCancelMsgType)
			binary.BigEndian.PutUint32(frame[4:], uint32(tc.signal))
			if _, err := unix.Write(fds[0], frame[:]); err != nil {
				t.Fatal(err)
			}
			<-done
			reply := make([]byte, 16)
			n, err := unix.Read(fds[0], reply)
			if err != nil || n != 1 || reply[0] != tc.wantAck {
				t.Fatalf("reply = %v (err %v), want exactly the ack byte %d", reply[:max(n, 0)], err, tc.wantAck)
			}
			delivered := len(signals) == 1
			if delivered != (tc.wantAck == VsockJobControlAckOK) {
				t.Fatalf("delivered = %v, want %v", delivered, tc.wantAck == VsockJobControlAckOK)
			}
		})
	}
}
