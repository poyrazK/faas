//go:build linux

// adr: 568 — real Unix peer/pidfd tests are not Firecracker capture acceptance.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func nativeSnapshotControlPeerFixture(t *testing.T, handler http.Handler) (string, nativeLaunchRecord) {
	t.Helper()
	// t.TempDir includes the long test name, which can exceed sun_path even
	// when the configured scratch root is valid for ordinary file fixtures.
	directory, err := os.MkdirTemp("", "nctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(directory, "api.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})
	start, err := nativeHostHelperStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// Only the real kernel IPC/process boundary is exercised here. This
	// process has neither a managed VM lease nor a qualification receipt.
	return socket, nativeLaunchRecord{Authorized: true, PID: os.Getpid(), StartTime: start,
		Lease: Lease{UID: os.Geteuid(), GID: os.Getegid()}}
}

func TestNativeSnapshotControlSendsOneOriginalProcessEffect(t *testing.T) {
	var calls atomic.Int32
	socket, owner := nativeSnapshotControlPeerFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPatch || r.URL.Path != "/vm" || body["state"] != "Paused" || r.Header.Get("Content-Type") != "application/json" || !r.Close {
			t.Error("native control changed its original one-shot request", r.Method, r.URL, body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := nativeSnapshotProcessRequest(ctx, socket, owner, http.MethodPatch, "/vm", map[string]any{"state": "Paused"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("native control did not make exactly one original effect", calls.Load())
	}
}

func TestNativeSnapshotControlLostResponseAndRedirectNeverReplay(t *testing.T) {
	for _, outcome := range []string{"lost_response", "redirect", "conflict"} {
		t.Run(outcome, func(t *testing.T) {
			var calls atomic.Int32
			socket, owner := nativeSnapshotControlPeerFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if outcome == "lost_response" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
				} else if outcome == "redirect" {
					w.Header().Set("Location", "http://unrelated.invalid/snapshot/create")
					w.WriteHeader(http.StatusTemporaryRedirect)
				} else {
					w.WriteHeader(http.StatusConflict)
				}
			}))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := nativeSnapshotProcessRequest(ctx, socket, owner, http.MethodPut, "/snapshot/create", map[string]any{"snapshot_type": "Full"}); err == nil {
				t.Fatal("uncertain/rejected control effect supplied success")
			}
			if calls.Load() != 1 {
				t.Fatal("uncertain effect was retried or redirected", calls.Load())
			}
		})
	}
}

func TestNativeSnapshotControlRejectsChangedKernelAndPeerIdentityBeforeSend(t *testing.T) {
	for _, change := range []string{"start_time", "uid", "gid", "pid", "unauthorized", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int32
			socket, owner := nativeSnapshotControlPeerFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			switch change {
			case "start_time":
				owner.StartTime++
			case "uid":
				owner.Lease.UID++
			case "gid":
				owner.Lease.GID++
			case "unauthorized":
				owner.Authorized = false
			case "cancelled":
				cancel()
			case "pid":
				child := exec.CommandContext(ctx, "sleep", "5")
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
				owner.PID = child.Process.Pid
				var err error
				owner.StartTime, err = nativeHostHelperStartTime(owner.PID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := nativeSnapshotProcessRequest(ctx, socket, owner, http.MethodPatch, "/vm", map[string]any{"state": "Paused"}); err == nil {
				t.Fatal("changed process or peer authorized an API effect")
			}
			if calls.Load() != 0 {
				t.Fatal("identity refusal occurred after request bytes were sent")
			}
		})
	}
}

func TestNativeSnapshotControlCancellationJoinsBlockedRequest(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	socket, owner := nativeSnapshotControlPeerFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(started)
		<-finish
	}))
	defer close(finish)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- nativeSnapshotProcessRequest(ctx, socket, owner, http.MethodPatch, "/vm", map[string]any{"state": "Paused"})
	}()
	select {
	case <-started:
	case err := <-finished:
		t.Fatal("control failed before reaching the original API peer", err)
	case <-time.After(time.Second):
		t.Fatal("control did not reach the original API peer")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled control request lost cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not join the original API request")
	}
}

func TestNativeSnapshotControlOnlyDerivesOriginalCaptureOutputNames(t *testing.T) {
	capture := uuid.NewString()
	method, path, body, err := nativeSnapshotControlRequest(capture, nativeSnapshotCreate)
	expected := map[string]any{"snapshot_type": "Full", "snapshot_path": "capture-" + capture + "-vmstate", "mem_file_path": "capture-" + capture + "-mem"}
	if err != nil || method != http.MethodPut || path != "/snapshot/create" || !reflect.DeepEqual(body, expected) {
		t.Fatal("snapshot control supplied host paths or another capture", method, path, body, err)
	}
	if _, _, _, err := nativeSnapshotControlRequest(capture, nativeSnapshotControlAction(0)); err == nil {
		t.Fatal("unknown effect reached the API")
	}
	if _, _, _, err := nativeSnapshotControlRequest("../capture", nativeSnapshotCreate); err == nil {
		t.Fatal("caller path reached output name derivation")
	}
}

func TestNativeSnapshotControlRefusesIncompleteCaptureBeforeKernelEffects(t *testing.T) {
	f := nativeCaptureOutputsFixture(t)
	for _, action := range []nativeSnapshotControlAction{nativeSnapshotPause, nativeSnapshotCreate, nativeSnapshotResume} {
		if err := f.v.controlNativeQualificationSnapshot(f.ctx, f.owner.Lease, action); err == nil {
			t.Fatal("absent original private drive reached native control")
		}
	}
	if f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("control primitive enabled incomplete publication")
	}
}

func TestNativeSnapshotControlRequiresEveryOriginalBinding(t *testing.T) {
	for _, change := range []string{"complete", "missing_outputs", "drive_unready", "memory_unready", "state_unready", "memory_retired", "state_readonly", "other_capture"} {
		t.Run(change, func(t *testing.T) {
			f := nativeCaptureOutputsFixture(t)
			prepared := f.owner
			prepared.Authorized, prepared.PID, prepared.StartTime = false, 0, 0
			if err := f.q.owner.write(prepared); err != nil {
				t.Fatal(err)
			}
			if _, err := f.j.stageWritable(f.ctx, prepared, f.root, "/fixture-original-drive", layerImageName); err != nil {
				t.Fatal(err)
			}
			if err := f.q.owner.write(f.owner); err != nil {
				t.Fatal(err)
			}
			if change != "missing_outputs" {
				if _, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory); err != nil {
					t.Fatal(err)
				}
			}
			records, err := f.j.records()
			if err != nil {
				t.Fatal(err)
			}
			memory, _ := nativeSnapshotOutputName(f.capture.CaptureID, "mem")
			vmstate, _ := nativeSnapshotOutputName(f.capture.CaptureID, "vmstate")
			for _, record := range records {
				ref := &record.References[0]
				switch {
				case change == "drive_unready" && ref.Name == layerImageName,
					change == "memory_unready" && ref.Name == memory,
					change == "state_unready" && ref.Name == vmstate:
					ref.Ready = false
				case change == "memory_retired" && ref.Name == memory:
					ref.TargetRemoved = true
				case change == "state_readonly" && ref.Name == vmstate:
					ref.ReadOnly, ref.AddPerms = true, 0o044
				}
				record.Desired, err = desiredNativeImageMetadata(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.j.write(record); err != nil {
					t.Fatal(err)
				}
			}
			capture := f.capture.CaptureID
			if change == "other_capture" {
				capture = uuid.NewString()
			}
			err = f.j.requireSnapshotControlBindings(f.owner, f.root, capture, nativeSnapshotCreate)
			if (err == nil) != (change == "complete") {
				t.Fatal("create did not require the complete original drive and both writable output bindings", err)
			}
		})
	}
}
