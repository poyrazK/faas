// adr: 375
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestTargetLifetimeGuestListenerCapturesOriginalVM(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "lifetime")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	v := NewJailerVMM(base, time.Second)
	lease := Lease{Instance: "instance", UID: os.Getuid(), GID: os.Getgid()}
	if _, err := v.mkChroot(lease.Instance); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	receipt := make(chan GuestVsockStreamOrigin, 2)
	var startOnce sync.Once
	if err := v.RegisterGuestVsockStreamHandler(VsockGuestEventHostPort, func(_ string, conn net.Conn) (string, error) {
		startOnce.Do(func() { close(started) })
		<-release
		origin, ok := GuestVsockOrigin(conn)
		if !ok {
			return "identity", os.ErrInvalid
		}
		receipt <- origin
		_, err := io.ReadAll(conn)
		return "", err
	}); err != nil {
		t.Fatal(err)
	}
	fields := wire.CorrelationFields{AppID: "app", WakeID: "old", NodeID: "source"}
	ctx := wire.WithContext(t.Context(), fields)
	if err := v.prepareRegisteredGuestVsockListenersForWake(ctx, lease); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.closeGuestVsockListeners(lease.Instance) })
	conn, err := net.Dial("unix", v.guestVsockUDSSock(lease.Instance, VsockGuestEventHostPort))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	<-started
	fields.WakeID, fields.NodeID = "fresh", "destination"
	v.closeGuestVsockListeners(lease.Instance)
	if err := v.prepareRegisteredGuestVsockListenersForWake(wire.WithContext(ctx, fields), lease); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case origin := <-receipt:
		if origin.AppID != "app" || origin.WakeID != "old" || origin.NodeID != "source" {
			t.Fatalf("retired listener borrowed new identity: %+v", origin)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not return identity")
	}
	fresh, err := net.Dial("unix", v.guestVsockUDSSock(lease.Instance, VsockGuestEventHostPort))
	if err != nil {
		t.Fatal(err)
	}
	_ = fresh.Close()
	select {
	case origin := <-receipt:
		if origin.AppID != "app" || origin.WakeID != "fresh" || origin.NodeID != "destination" {
			t.Fatalf("replacement listener identity=%+v", origin)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement listener did not return identity")
	}

}

func TestTargetLifetimeReadinessLoopReplacementKeepsCapturedIdentity(t *testing.T) {
	mgr := NewManager(nil, nil, Paths{}, "", nil, nil)
	mgr.live["instance"] = &Instance{Lease: Lease{Slot: 1}, AppID: "app", WakeID: "old", NodeID: "source"}
	type receipt struct {
		ctx context.Context
		cfg ReadinessProbeConfig
	}
	started := make(chan receipt, 2)
	mgr.WithReadinessProbeStarter(func(ctx context.Context, _ string, _ int, _ string, cfg ReadinessProbeConfig) {
		started <- receipt{ctx, cfg}
	})
	mgr.startReadinessLoop(t.Context(), "instance", 1, json.RawMessage(`{"path":"/readyz"}`))
	old := <-started
	mgr.mu.Lock()
	mgr.live["instance"] = &Instance{Lease: Lease{Slot: 2}, AppID: "app", WakeID: "fresh", NodeID: "destination"}
	mgr.mu.Unlock()
	mgr.startReadinessLoop(t.Context(), "instance", 2, json.RawMessage(`{"path":"/readyz"}`))
	fresh := <-started
	defer mgr.cancelReadinessLoop("instance")
	if old.cfg.WakeID != "old" || old.cfg.NodeID != "source" || fresh.cfg.WakeID != "fresh" || fresh.cfg.NodeID != "destination" {
		t.Fatalf("loop identities old=%+v fresh=%+v", old.cfg, fresh.cfg)
	}
	select {
	case <-old.ctx.Done():
	default:
		t.Fatal("replacement did not cancel retired loop")
	}
}

func TestTargetLifetimePausedPoolResumeStartsReadiness(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "resumed", true: "resume failed"}[fails], func(t *testing.T) {
			vmm := &fakeVMM{}
			if fails {
				vmm.resumeErr = errors.New("resume failed")
			}
			mgr := NewManager(nil, vmm, Paths{}, "", nil, nil)
			inst := &Instance{Lease: Lease{Instance: "instance", Slot: 1}, AppID: "app", WakeID: "pool-wake", NodeID: "node", Paused: true, ReadinessProbe: json.RawMessage(`{"path":"/readyz"}`)}
			mgr.live["instance"] = inst
			started := make(chan ReadinessProbeConfig, 1)
			mgr.WithReadinessProbeStarter(func(_ context.Context, _ string, _ int, _ string, cfg ReadinessProbeConfig) { started <- cfg })
			err := mgr.ResumeVM(wire.WithContext(t.Context(), wire.CorrelationFields{WakeID: "unrelated-resume-caller"}), "instance")
			defer mgr.cancelReadinessLoop("instance")
			if (err != nil) != fails || inst.Paused != fails || len(started) != map[bool]int{false: 1, true: 0}[fails] {
				t.Fatalf("resume err=%v paused=%t readiness loops=%d", err, inst.Paused, len(started))
			}
			if !fails {
				if cfg := <-started; cfg.WakeID != "pool-wake" || cfg.NodeID != "node" {
					t.Fatalf("pool probe identity=%+v", cfg)
				}
			}
		})
	}
}
