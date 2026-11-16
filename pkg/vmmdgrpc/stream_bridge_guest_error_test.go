package vmmdgrpc

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// A bridge whose guest connection keeps failing is replaced after
// streamBridgeGuestErrorFence consecutive failures; any guest answer in
// between resets the streak, so an occasional failure never fences.
func TestStreamBridgeGuestErrorStreakFencesOnce(t *testing.T) {
	m := newStreamBridgeManager(nil)
	lease := &streamBridgeLease{manager: m, entry: &streamBridgeEntry{instance: "i-1"}}

	for i := 1; i < streamBridgeGuestErrorFence; i++ {
		if v := m.noteGuestTransport(lease, true); v.streak != i || v.fenceBridge {
			t.Fatalf("failure %d: %+v, want streak %d without fence", i, v, i)
		}
	}
	if v := m.noteGuestTransport(lease, false); v != (guestTransportVerdict{}) {
		t.Fatalf("success: %+v, want reset", v)
	}
	var fences int
	for i := 1; i <= streamBridgeGuestErrorFence+2; i++ {
		if v := m.noteGuestTransport(lease, true); v.fenceBridge {
			fences++
			if i != streamBridgeGuestErrorFence {
				t.Fatalf("fenced at failure %d, want %d", i, streamBridgeGuestErrorFence)
			}
		}
	}
	if fences != 1 {
		t.Fatalf("fences = %d, want exactly one per streak", fences)
	}
}

// The instance streak survives a bridge replacement: when the fresh bridge
// fails the same way, the instance is reported once; forget clears it.
func TestStreamBridgeInstanceReportedAfterFreshBridgeFails(t *testing.T) {
	m := newStreamBridgeManager(nil)
	first := &streamBridgeLease{manager: m, entry: &streamBridgeEntry{instance: "i-1"}}
	second := &streamBridgeLease{manager: m, entry: &streamBridgeEntry{instance: "i-1"}}
	var reports int
	for range streamBridgeGuestErrorFence {
		if m.noteGuestTransport(first, true).reportInstance {
			reports++
		}
	}
	for range streamBridgeGuestErrorFence + 3 {
		if m.noteGuestTransport(second, true).reportInstance {
			reports++
		}
	}
	if reports != 1 {
		t.Fatalf("instance reports = %d, want 1 across both bridges", reports)
	}
	m.forget(context.Background(), "i-1")
	if _, ok := m.instanceGuestErrors["i-1"]; ok {
		t.Fatal("forget kept the instance streak")
	}
}

func TestStreamBridgeGuestErrorNilSafe(t *testing.T) {
	var m *streamBridgeManager
	if v := m.noteGuestTransport(nil, true); v != (guestTransportVerdict{}) {
		t.Fatal("nil manager must not fence or report")
	}
}

type reportingVMM struct {
	VmmdAPI
	got chan [2]string
}

func (r reportingVMM) ReportLivenessFailed(_ context.Context, instance, reason string) {
	r.got <- [2]string{instance, reason}
}

// The report reaches schedd's liveness path with a reason that replaces the
// VM without counting toward parking the app.
func TestReportTrafficUnreachableUsesUnbudgetedReason(t *testing.T) {
	vmm := reportingVMM{got: make(chan [2]string, 1)}
	s := &Server{vmm: vmm, log: slog.Default()}
	s.reportTrafficUnreachable(context.Background(), "i-1")
	select {
	case got := <-vmm.got:
		if got != [2]string{"i-1", fcvm.LivenessReasonTrafficUnreachable} {
			t.Fatalf("reported %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no liveness report")
	}
	if fcvm.LivenessReasonBudgeted(fcvm.LivenessReasonTrafficUnreachable) {
		t.Fatal("traffic-unreachable restarts must not count toward parking the app")
	}
}
