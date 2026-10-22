//go:build linux

package fcvm

// adr: 595 Real procfs/mmap/Unix I/O, simulated Firecracker and guest peers.

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"golang.org/x/sys/unix"
)

func TestSnapshotResumeMappingChild(t *testing.T) {
	if os.Getenv("GREGALE_SNAPSHOT_RESUME_CHILD") != "1" {
		return
	}
	fd, err := strconv.Atoi(os.Getenv("GREGALE_SNAPSHOT_MEMORY_FD"))
	if err != nil || fd < 5 {
		t.Fatal("missing inherited memory descriptor", err)
	}
	memory, err := unix.Mmap(fd, 0, 1<<20, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if memory != nil {
			_ = unix.Munmap(memory)
		}
	}()
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
	memory[0], memory[len(memory)-1] = 7, 9
	if _, err := io.WriteString(os.Stdout, "READY\n"); err != nil {
		t.Fatal(err)
	}
	var command [1]byte
	for {
		if _, err := io.ReadFull(os.Stdin, command[:]); err != nil {
			return
		}
		if command[0] == 'u' {
			if err := unix.Munmap(memory); err != nil {
				t.Fatal(err)
			}
			memory = nil
			if _, err := io.WriteString(os.Stdout, "UNMAPPED\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func startSnapshotResumeMappingProcess(t *testing.T, f protectedRestoreFixture) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	handoff := f.spec.verifiedSnapshot.inputs.owner
	files := make([]*os.File, 0, len(handoff.drives)+1)
	for _, drive := range handoff.drives {
		file := drive.file
		if !drive.observation.ReadOnly {
			file, err = os.OpenFile(filepath.Join(f.root, drive.path), os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = file.Close() })
		}
		files = append(files, file)
	}
	memoryFD := 3 + len(files)
	files = append(files, handoff.restoreBlobs[0].file)
	cmd := exec.Command(self, "-test.run=^TestSnapshotResumeMappingChild$")
	cmd.ExtraFiles = files
	cmd.Env = append(os.Environ(), "GREGALE_SNAPSHOT_RESUME_CHILD=1", "GREGALE_SNAPSHOT_MEMORY_FD="+strconv.Itoa(memoryFD))
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if pipe, ok := output.(*os.File); ok {
		if err := pipe.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	rec := &instanceRecord{cmd: cmd, done: make(chan struct{})}
	f.vmm.mu.Lock()
	f.vmm.proc[f.lease.Instance], f.vmm.recs[f.lease.Instance] = cmd, rec
	f.vmm.mu.Unlock()
	go func() {
		err := cmd.Wait()
		if cmd.ProcessState != nil {
			err = nil // Signal exit is a confirmed exit, matching native retirement.
		}
		f.vmm.mu.Lock()
		rec.waitErr, rec.exited = err, err == nil
		close(rec.done)
		f.vmm.mu.Unlock()
	}()
	t.Cleanup(func() {
		_ = input.Close()
		_ = cmd.Process.Kill()
		select {
		case <-rec.done:
		case <-time.After(3 * time.Second):
			t.Error("mapping child did not retire")
		}
	})
	reader := bufio.NewReader(output)
	if line, err := reader.ReadString('\n'); err != nil || line != "READY\n" {
		t.Fatal("mapping child did not establish its private mapping", err)
	}
	return cmd, input, reader
}

func TestSnapshotResumeCouplesActualProcessAndAcknowledgments(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("this procfs unit fixture uses a positive unprivileged process UID; root/KVM acceptance remains separate")
	}
	for _, fault := range []string{"accepted", "hook nack", "unmapped after resume", "different process", "different start", "expired fresh grant", "expired historical load"} {
		t.Run(fault, func(t *testing.T) {
			f, _ := newSnapshotResumeFixture(t)
			cmd, input, reader := startSnapshotResumeMappingProcess(t, f)
			clock := time.Now()
			plan := f.spec.verifiedSnapshot
			if fault == "expired historical load" {
				// Simulate elapsed grant history, without simulating any process,
				// mapping, descriptor, backing bytes or resume acknowledgment.
				clock = expireSnapshotResumeLoadFixture(t, f, clock)
				currentDrives, currentSnapshot, err := f.vmm.ObservedRuntimeSnapshotConsumption(t.Context(), f.lease)
				if !errors.Is(err, runtimeadmission.ErrExpired) || !currentDrives.IsZero() || !currentSnapshot.IsZero() || plan.resumeAttempted {
					t.Fatal("ordinary observation renewed historical load authority", err)
				}
			}
			drives, snapshot, err := f.vmm.observedRuntimeSnapshotConsumptionAt(t.Context(), f.lease, clock)
			if err != nil || drives.ProcessPID != uint32(cmd.Process.Pid) || snapshot.MappedMemoryBytes != 1<<20 {
				t.Fatal("actual coupled consumption refused", err)
			}
			p := snapshotResumePromotion(t, f, drives, snapshot)
			p.Parent.CompletedAtUnixNano = clock.UnixNano()
			switch fault {
			case "different process":
				p.Parent.ArtifactConsumption.ProcessPID++
			case "different start":
				p.Parent.ArtifactConsumption.ProcessStart += "1"
			case "expired fresh grant":
				p.Binding.IssuedAtUnixNano -= int64(2 * time.Hour)
				p.Binding.ExpiresAtUnixNano -= int64(2 * time.Hour)
			}
			bindSnapshotResumePromotion(t, &p)
			var calls atomic.Int32
			f.vmm.closeClient(f.lease.Instance)
			bindTestSocket(t, f.vmm.socketPath(f.lease.Instance), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != http.MethodPatch || r.URL.Path != "/vm" || string(body) != `{"state":"Resumed"}` {
					t.Error("incorrect native resume command", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if fault == "unmapped after resume" {
					if _, err := input.Write([]byte{'u'}); err != nil {
						t.Error(err)
					}
					if line, err := reader.ReadString('\n'); err != nil || line != "UNMAPPED\n" {
						t.Error("child did not unmap", err)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			beforeMutation := fault == "different process" || fault == "different start" || fault == "expired fresh grant"
			var hookResults chan resumeHookPeerResult
			if !beforeMutation {
				hookResults = bindSnapshotResumeHookPeer(t, f, fault == "hook nack")
			}
			result, err := f.vmm.PromoteSnapshotVerified(t.Context(), f.lease, p)
			var hook resumeHookPeerResult
			if !beforeMutation {
				select {
				case hook = <-hookResults:
				case <-time.After(3 * time.Second):
					t.Fatal("resume hook peer did not complete")
				}
				if hook.err != nil {
					t.Fatal(hook.err)
				}
			}
			if fault != "accepted" && fault != "expired historical load" {
				if err == nil || !reflect.DeepEqual(result, RuntimeSnapshotResumeObservation{}) || plan.resumeAttempted == beforeMutation || calls.Load() != map[bool]int32{true: 0, false: 1}[beforeMutation] {
					t.Fatalf("failed resume returned proof or wrong side effects: err=%v result=%+v", err, result)
				}
				if !beforeMutation {
					if _, err := f.vmm.currentRuntimeDriveProcess(f.lease); err == nil {
						t.Fatal("failed attempted resume retained a serving process")
					}
				}
				return
			}
			if err != nil || result.Check(p, time.Now()) != nil || !result.Request.Equal(p) || !result.ArtifactConsumption.Equal(drives) || result.SnapshotConsumption != snapshot || result.ArtifactConsumption.ConfigHash != runtimeadmission.SnapshotLoadCommandHash(true) || result.ResumeEvidence.ResumeCommandHash != runtimeResumePayloadHash("gregale.runtime-resume.command.v1\x00", []byte(`{"state":"Resumed"}`)) || result.ResumeEvidence.ResumeHookPayloadHash != runtimeResumePayloadHash("gregale.runtime-resume.hook.v1\x00", hook.frame) || !plan.keepPaused || !plan.resumeAttempted || calls.Load() != 1 {
				t.Fatalf("native coupled facts lost original paused load: result=%+v error=%v", result, err)
			}
			serving, err := result.receipt(p, time.Now())
			if err != nil || f.vmm.checkSnapshotParent(t.Context(), f.lease, serving) != nil || plan.resumeEvidence != serving.SnapshotResumeEvidence {
				t.Fatal("actual resumed owner lost capture-parent lineage", err)
			}
			changed := serving.Clone()
			changed.SnapshotResumeEvidence.ResumeHookPayloadHash = strings.Repeat("0", 64)
			if f.vmm.checkSnapshotParent(t.Context(), f.lease, changed) == nil {
				t.Fatal("capture accepted resume facts not retained by the native owner")
			}
			if result.ResumeEvidence.CommandCompletedAtUnixNano > result.ResumeEvidence.HostTimeUnixNano || result.ResumeEvidence.HostTimeUnixNano > result.ResumeEvidence.HookCompletedAtUnixNano || result.ResumeEvidence.HookCompletedAtUnixNano > result.ResumeEvidence.CompletedAtUnixNano || result.ResumeEvidence.HookCompletedAtUnixNano < hook.acked {
				t.Fatal("resume/hook observation clocks are not ordered")
			}
			if _, err := f.vmm.PromoteSnapshotVerified(t.Context(), f.lease, p); err == nil || calls.Load() != 1 {
				t.Fatal("resume replay reached the API", err)
			}
			result.ArtifactConsumption.Drives[0].DriveID = "reader-edit"
			if p.Parent.ArtifactConsumption.Drives[0].DriveID == "reader-edit" {
				t.Fatal("returned native facts aliased request history")
			}
		})
	}
}

func bindSnapshotResumeHookPeer(t *testing.T, f protectedRestoreFixture, nack bool) chan resumeHookPeerResult {
	t.Helper()
	socket := f.vmm.vsockUDSSock(f.lease.Instance)
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.(*net.UnixListener).SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	results := make(chan resumeHookPeerResult, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			results <- resumeHookPeerResult{err: err}
			return
		}
		var ack byte
		if nack {
			ack = 1
		}
		results <- captureResumeHookPeer(conn, ack, false, nil)
	}()
	return results
}
