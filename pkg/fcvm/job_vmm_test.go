package fcvm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBuildJobColdBootConfigUsesPrivateWritableDrive(t *testing.T) {
	cfg := BuildJobColdBootConfig(JobColdBootSpec{
		KernelKey: "kernel", BaseKey: "base", ImageRef: "job-image",
		VcpuCount: 1, MemSizeMiB: 256, Tap: "tap0",
	}, 3)
	if cfg.EphemeralWritable {
		t.Fatal("job drive selected aliasing builder scratch path; want private clone/copy")
	}
	if len(cfg.Drives) != 2 || cfg.Drives[1].IsReadOnly {
		t.Fatalf("job drives = %+v", cfg.Drives)
	}
}

func TestWaitJobExitAcceptsGuestInitiatedStream(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "fj-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	v := NewJailerVMM(base, time.Second)
	lease := Lease{Instance: "job-1", UID: os.Getuid(), GID: os.Getgid()}
	if err := os.MkdirAll(v.chrootRoot(lease.Instance), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := v.prepareJobExitListener(lease); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.closeJobExitListener(lease.Instance) })

	want := JobExitPayload{
		ExitCode: 0, ErrorClass: "succeeded", FinishedAtUnixNano: time.Now().UnixNano(), LeaseToken: "lease-1",
	}
	resultCh := make(chan JobExitPayload, 1)
	errCh := make(chan error, 1)
	go func() {
		got, err := v.WaitJobExit(context.Background(), lease, 2*time.Second)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- got
	}()

	conn, err := net.DialTimeout("unix", v.jobExitUDSSock(lease.Instance), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], VsockJobExitMsgType)
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(body)))
	if _, err := conn.Write(append(hdr[:], body...)); err != nil {
		t.Fatal(err)
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if ack[0] != 0 {
		t.Fatalf("ack = %d, want 0", ack[0])
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	case got := <-resultCh:
		if got != want {
			t.Fatalf("payload = %+v, want %+v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitJobExit did not return")
	}
}

func TestReadJobExitEnvelopeRejectsUnknownFields(t *testing.T) {
	body := []byte(`{"exit_code":0,"error_class":"succeeded","signal":0,"finished_at_unix_nano":1,"lease_token":"lease","extra":true}`)
	var frame [8]byte
	binary.BigEndian.PutUint32(frame[:4], VsockJobExitMsgType)
	binary.BigEndian.PutUint32(frame[4:], uint32(len(body)))
	if _, err := readJobExitEnvelope(io.MultiReader(bytes.NewReader(frame[:]), bytes.NewReader(body))); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestEnsureJobManifestDirectoryRejectsImageSymlink(t *testing.T) {
	root := t.TempDir()
	escape := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(root, "etc")); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureJobManifestDirectory(root); err == nil {
		t.Fatal("image-provided /etc symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(escape, "faas")); !os.IsNotExist(err) {
		t.Fatalf("staging escaped through image symlink: %v", err)
	}
}

func TestSignalJobGuestUsesHostInitiatedConnect(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "fj-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	v := NewJailerVMM(base, time.Second)
	lease := Lease{Instance: "job-2"}
	sock := v.vsockUDSSock(lease.Instance)
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			serverErr <- err
			return
		}
		if line != fmt.Sprintf("CONNECT %d\n", VsockJobControlPort) {
			serverErr <- fmt.Errorf("CONNECT frame = %q", line)
			return
		}
		if _, err := io.WriteString(conn, "OK 42\n"); err != nil {
			serverErr <- err
			return
		}
		var frame [8]byte
		if _, err := io.ReadFull(reader, frame[:]); err != nil {
			serverErr <- err
			return
		}
		if got := binary.BigEndian.Uint32(frame[:4]); got != VsockJobCancelMsgType {
			serverErr <- fmt.Errorf("message type = %d", got)
			return
		}
		if got := syscall.Signal(binary.BigEndian.Uint32(frame[4:])); got != syscall.SIGTERM {
			serverErr <- fmt.Errorf("signal = %d", got)
			return
		}
		_, err = conn.Write([]byte{vsockJobControlAckOK})
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := v.signalJobGuest(ctx, lease, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestProcessExitedDoesNotRelayExpectedJobShutdown(t *testing.T) {
	called := false
	m := &Manager{
		live: map[string]*Instance{"job-1": {IsJob: true}},
		livenessRelay: func(context.Context, string, string) {
			called = true
		},
	}
	m.ProcessExited("job-1", 0)
	if called {
		t.Fatal("expected job shutdown was relayed through app liveness recovery")
	}
}
