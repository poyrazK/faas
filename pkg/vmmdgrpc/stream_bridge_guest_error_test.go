package vmmdgrpc

import "testing"

// A bridge whose guest connection keeps failing is replaced after
// streamBridgeGuestErrorFence consecutive failures; any guest answer in
// between resets the streak, so an occasional failure never fences.
func TestStreamBridgeGuestErrorStreakFencesOnce(t *testing.T) {
	m := newStreamBridgeManager(nil)
	lease := &streamBridgeLease{manager: m, entry: &streamBridgeEntry{instance: "i-1"}}

	for i := 1; i < streamBridgeGuestErrorFence; i++ {
		if streak, fence := m.noteGuestTransport(lease, true); streak != i || fence {
			t.Fatalf("failure %d: streak=%d fence=%v, want %d,false", i, streak, fence, i)
		}
	}
	if streak, fence := m.noteGuestTransport(lease, false); streak != 0 || fence {
		t.Fatalf("success: streak=%d fence=%v, want reset", streak, fence)
	}
	var fences int
	for i := 1; i <= streamBridgeGuestErrorFence+2; i++ {
		if _, fence := m.noteGuestTransport(lease, true); fence {
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

func TestStreamBridgeGuestErrorNilSafe(t *testing.T) {
	var m *streamBridgeManager
	if streak, fence := m.noteGuestTransport(nil, true); streak != 0 || fence {
		t.Fatal("nil manager must not fence")
	}
}
