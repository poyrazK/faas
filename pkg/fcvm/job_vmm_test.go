// adr: 099

package fcvm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 385 — failed receipts may carry a structured outcome but cannot publish artifacts.
func TestValidateJobExitPayloadAllowsOutcomeOnFailureButNoArtifacts(t *testing.T) {
	base := JobExitPayload{
		ExitCode: 75, ErrorClass: "failed", FinishedAtUnixNano: time.Now().UnixNano(), LeaseToken: "lease-1",
		OutputManifest: json.RawMessage(`{"version":1,"artifacts":[],"outcome_code":"transient_upstream"}`),
	}
	if err := validateJobExitPayload(base); err != nil {
		t.Fatalf("valid classified failure receipt: %v", err)
	}
	base.OutputManifest = json.RawMessage(`{"version":1,"artifacts":[{"name":"x","uri":"s3://b/x","size_bytes":0,"sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"outcome_code":"transient_upstream"}`)
	if err := validateJobExitPayload(base); err == nil {
		t.Fatal("accepted artifact declaration for failed task")
	}
}

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

func TestEffectiveDestroyWaitCoversEveryAcceptedJobTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{name: "hobby", seconds: 300, want: 390 * time.Second},
		{name: "pro", seconds: 1800, want: 1890 * time.Second},
		{name: "scale", seconds: 3600, want: 3690 * time.Second},
		{name: "host ceiling", seconds: JobMaxTaskTimeoutSec, want: JobDestroyWaitDefault},
		{name: "invalid oversized timeout", seconds: JobMaxTaskTimeoutSec + 1, want: JobDestroyWaitDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveDestroyWait(tc.seconds); got != tc.want {
				t.Fatalf("EffectiveDestroyWait(%d) = %s, want %s", tc.seconds, got, tc.want)
			}
		})
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
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %+v, want %+v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitJobExit did not return")
	}
}

// sendJobExitFrame writes one exit frame as guest-init does and returns the
// ack byte.
func sendJobExitFrame(t *testing.T, socket string, payload JobExitPayload) byte {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	body, err := json.Marshal(payload)
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
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		t.Fatal(err)
	}
	return ack[0]
}

// vmmd accepts the exit itself: the frame is recorded through the sink
// before the ack, with no WaitJobExit call open, and WaitJobExit can be
// called again after a call is cut short, as across a vmmd restart.
func TestJobExitReceiverRecordsBeforeAckAndIsReCallable(t *testing.T) {
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
	recorded := make(chan JobExitPayload, 1)
	v.WithJobExitSink(func(instance string, p JobExitPayload) {
		if instance == lease.Instance {
			recorded <- p
		}
	})
	if err := v.prepareJobExitListener(lease); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.closeGuestVsockListeners(lease.Instance) })

	// A call cut short leaves the receiver running.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.WaitJobExit(ctx, lease, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v", err)
	}
	if _, err := v.WaitJobExit(context.Background(), lease, 10*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired wait = %v", err)
	}

	want := JobExitPayload{ExitCode: 3, ErrorClass: "failed", FinishedAtUnixNano: time.Now().UnixNano(), LeaseToken: "lease-1"}
	if ack := sendJobExitFrame(t, v.jobExitUDSSock(lease.Instance), want); ack != 0 {
		t.Fatalf("ack = %d, want 0", ack)
	}
	select {
	case got := <-recorded:
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("recorded %+v, want %+v", got, want)
		}
	default:
		t.Fatal("the exit was acknowledged before it was recorded")
	}
	for i := 0; i < 2; i++ {
		got, err := v.WaitJobExit(context.Background(), lease, time.Second)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("wait %d = %+v, %v", i, got, err)
		}
	}
	// Teardown forgets the receiver.
	v.closeGuestVsockListeners(lease.Instance)
	if _, err := v.WaitJobExit(context.Background(), lease, time.Second); err == nil || !strings.Contains(err.Error(), "not prepared") {
		t.Fatalf("wait after teardown = %v", err)
	}
}

// fakeListenerStore is systemd's fd store for one process lifetime.
type fakeListenerStore struct {
	mu      sync.Mutex
	stored  map[string]*net.UnixListener
	removed []string
}

func (s *fakeListenerStore) Store(name string, ln *net.UnixListener) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stored == nil {
		s.stored = map[string]*net.UnixListener{}
	}
	s.stored[name] = ln
	return nil
}

func (s *fakeListenerStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.stored, name)
	s.removed = append(s.removed, name)
	return nil
}

func (s *fakeListenerStore) Take(name string) *net.UnixListener {
	s.mu.Lock()
	defer s.mu.Unlock()
	ln := s.stored[name]
	delete(s.stored, name)
	return ln
}

// A job's exit sent while no vmmd runs waits in the stored listener's
// backlog, and the successor's receiver reads it. A fresh listener is kept
// in the store until its receiver is done.
func TestAdoptJobExitListenerReadsTheStoredBacklog(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "fj-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	lease := Lease{Instance: "job-1", UID: os.Getuid(), GID: os.Getgid()}
	name := jobExitStoreName(lease.Instance)

	// The predecessor bound and stored the listener, then stopped.
	predecessor := NewJailerVMM(base, time.Second)
	if err := os.MkdirAll(predecessor.chrootRoot(lease.Instance), 0o755); err != nil {
		t.Fatal(err)
	}
	path := predecessor.jobExitUDSSock(lease.Instance)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false) // a dead process unlinks nothing
	store := &fakeListenerStore{stored: map[string]*net.UnixListener{name: ln}}

	// The guest connects and writes its frame with nobody accepting.
	want := JobExitPayload{ExitCode: 0, ErrorClass: "succeeded", FinishedAtUnixNano: 9, LeaseToken: "lease-1"}
	acked := make(chan byte, 1)
	go func() { acked <- sendJobExitFrame(t, path, want) }()

	successor := NewJailerVMM(base, time.Second).WithListenerStore(store)
	if err := successor.adoptJobExitListener(lease); err != nil {
		t.Fatal(err)
	}
	got, err := successor.WaitJobExit(context.Background(), lease, 2*time.Second)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("WaitJobExit = %+v, %v", got, err)
	}
	if ack := <-acked; ack != 0 {
		t.Fatalf("ack = %d", ack)
	}
	// The exit is recorded: the listener left the store, and a retry of the
	// frame, whose first ack the guest never saw, is acknowledged at once.
	// The receiver publishes the exit before it unstores the listener, so
	// the removal can trail WaitJobExit.
	var removed []string
	for deadline := time.Now().Add(5 * time.Second); ; {
		store.mu.Lock()
		removed = append([]string(nil), store.removed...)
		store.mu.Unlock()
		if len(removed) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(removed) != 1 || removed[0] != name {
		t.Fatalf("after the exit the store still holds the listener: removed %v", removed)
	}
	if ack := sendJobExitFrame(t, path, want); ack != 0 {
		t.Fatalf("retry ack = %d, want 0", ack)
	}
	// Teardown closes it and unlinks the path.
	successor.closeGuestVsockListeners(lease.Instance)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("listener path after teardown: %v", err)
	}

	// Without a stored listener the successor re-binds and stores the new one.
	lease2 := Lease{Instance: "job-2", UID: os.Getuid(), GID: os.Getgid()}
	if err := os.MkdirAll(successor.chrootRoot(lease2.Instance), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := successor.adoptJobExitListener(lease2); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { successor.closeGuestVsockListeners(lease2.Instance) })
	store.mu.Lock()
	_, kept := store.stored[jobExitStoreName(lease2.Instance)]
	store.mu.Unlock()
	if !kept {
		t.Fatal("the re-bound listener was not stored")
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

func TestJobManifestStorageRootUsesOptimizedUpper(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "upper")
	if err := os.Mkdir(upper, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := jobManifestStorageRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != upper {
		t.Fatalf("job manifest root = %q, want %q", got, upper)
	}
}

func TestJobManifestStorageRootUsesFullRoot(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		t.Fatal(err)
	}
	got, err := jobManifestStorageRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("job manifest root = %q, want %q", got, root)
	}
}

func TestJobManifestStorageRootRejectsMalformedLayouts(t *testing.T) {
	t.Run("missing optimized upper", func(t *testing.T) {
		if _, err := jobManifestStorageRoot(t.TempDir()); err == nil {
			t.Fatal("missing optimized upper accepted")
		}
	})
	t.Run("symlinked optimized upper", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "upper")); err != nil {
			t.Fatal(err)
		}
		if _, err := jobManifestStorageRoot(root); err == nil {
			t.Fatal("symlinked optimized upper accepted")
		}
	})
	t.Run("invalid full-rootfs marker", func(t *testing.T) {
		root := t.TempDir()
		marker := filepath.Join(root, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte("invalid\n"), 0o444); err != nil {
			t.Fatal(err)
		}
		if _, err := jobManifestStorageRoot(root); err == nil {
			t.Fatal("invalid full-rootfs marker accepted")
		}
	})
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

func TestReleaseJobStartUsesHostInitiatedConnect(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "fj-start-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	v := NewJailerVMM(base, time.Second)
	lease := Lease{Instance: "job-held-1"}
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
		if got := binary.BigEndian.Uint32(frame[:4]); got != VsockJobStartMsgType || binary.BigEndian.Uint32(frame[4:]) != 0 {
			serverErr <- fmt.Errorf("start frame = %x", frame)
			return
		}
		_, err = conn.Write([]byte{vsockJobControlAckOK})
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := v.ReleaseJobStart(ctx, lease); err != nil {
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
