// adr: 532 — environment intent and runtime ownership contracts.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

type recoveryHandle struct {
	signalErr, waitErr, closeErr error
	signals                      []syscall.Signal
	waited, closed               bool
	onWait                       func(context.Context) error
}

func (h *recoveryHandle) Signal(signal syscall.Signal) error {
	h.signals = append(h.signals, signal)
	return h.signalErr
}

func (h *recoveryHandle) Wait(ctx context.Context) error {
	h.waited = true
	if h.onWait != nil {
		return h.onWait(ctx)
	}
	return h.waitErr
}

func (h *recoveryHandle) Close() error {
	h.closed = true
	return h.closeErr
}

func TestNativeRecoveryRetirementPinsEveryExactMatchAndWaitsForExit(t *testing.T) {
	root := t.TempDir()
	id := "qualification-recovery"
	writeNativeRecoveryProcess(t, root, 41, id, JailUIDBase+3, 101)
	writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+4, 102)
	writeNativeRecoveryProcess(t, root, 43, id+"-other", JailUIDBase+5, 103)
	handles := map[int]*recoveryHandle{41: {}, 42: {}}
	var pinned []int
	r := nativeProcessRetirer{probe: nativeProcessProbe{root: root}, open: func(pid int) (nativeProcessHandle, error) {
		pinned = append(pinned, pid)
		return handles[pid], nil
	}}
	targets, err := r.stopObserved(t.Context(), id)
	if err != nil || len(targets) != 2 || !reflect.DeepEqual(pinned, []int{41, 42}) {
		t.Fatalf("targets=%+v pinned=%v err=%v", targets, pinned, err)
	}
	for pid, h := range handles {
		if !h.waited || !h.closed || !reflect.DeepEqual(h.signals, []syscall.Signal{syscall.SIGKILL}) {
			t.Fatalf("process %d lifecycle: %+v", pid, h)
		}
	}
}

func TestNativeRecoveryRetirementRejectsPIDReuseBeforeSignalling(t *testing.T) {
	for _, change := range []string{"start_time", "instance", "uid", "cgroup"} {
		t.Run(change, func(t *testing.T) {
			root, id := t.TempDir(), "qualification-recovery"
			dir := writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+3, 101)
			h := &recoveryHandle{}
			r := nativeProcessRetirer{probe: nativeProcessProbe{root: root}, open: func(int) (nativeProcessHandle, error) {
				switch change {
				case "start_time":
					writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+3, 102)
				case "instance":
					writeNativeRecoveryProcess(t, root, 42, id+"-replacement", JailUIDBase+3, 101)
				case "uid":
					writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+4, 101)
				case "cgroup":
					if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/user.slice/"+id), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return h, nil
			}}
			targets, err := r.stopObserved(t.Context(), id)
			if err == nil || len(targets) != 1 || len(h.signals) != 0 || h.waited || !h.closed {
				t.Fatalf("reused identity targets=%+v handle=%+v err=%v", targets, h, err)
			}
		})
	}
}

func TestNativeRecoveryRetirementPreservesUncertainOutcomes(t *testing.T) {
	for _, failure := range []string{"open", "signal", "wait", "close", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			root, id := t.TempDir(), "qualification-recovery"
			writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+3, 101)
			cause, h := errors.New("native exit uncertain"), &recoveryHandle{}
			ctx := t.Context()
			switch failure {
			case "signal":
				h.signalErr = cause
			case "wait":
				h.waitErr = cause
			case "close":
				h.closeErr = cause
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
				cause = context.DeadlineExceeded
				h.onWait = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
			}
			r := nativeProcessRetirer{probe: nativeProcessProbe{root: root}, open: func(int) (nativeProcessHandle, error) {
				if failure == "open" {
					return nil, cause
				}
				return h, nil
			}}
			targets, err := r.stopObserved(ctx, id)
			if !errors.Is(err, cause) || len(targets) != 1 || failure != "open" && !h.closed {
				t.Fatalf("targets=%+v handle=%+v err=%v, want %v", targets, h, err, cause)
			}
		})
	}
}

func TestNativeRecoveryRetirementWaitsEvenWhenSignalReportsGone(t *testing.T) {
	root, id := t.TempDir(), "qualification-recovery"
	writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+3, 101)
	h := &recoveryHandle{signalErr: syscall.ESRCH, waitErr: errors.New("exit not acknowledged")}
	r := nativeProcessRetirer{probe: nativeProcessProbe{root: root}, open: func(int) (nativeProcessHandle, error) { return h, nil }}
	if _, err := r.stopObserved(t.Context(), id); !errors.Is(err, h.waitErr) || !h.waited || !h.closed {
		t.Fatalf("signal disappearance bypassed exit wait: %+v err=%v", h, err)
	}
}

func TestNativeRecoveryRetirementJoinsDisappearanceAndDoesNotSignalReusedPID(t *testing.T) {
	root, id := t.TempDir(), "qualification-recovery"
	dir := writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+3, 101)
	h := &recoveryHandle{}
	r := nativeProcessRetirer{probe: nativeProcessProbe{root: root}, open: func(int) (nativeProcessHandle, error) {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		return h, nil
	}}
	if targets, err := r.stopObserved(t.Context(), id); err != nil || len(targets) != 1 || len(h.signals) != 0 || !h.waited || !h.closed {
		t.Fatalf("exiting process targets=%+v handle=%+v err=%v", targets, h, err)
	}
}
