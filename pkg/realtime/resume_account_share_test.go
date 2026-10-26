package realtime

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Resume, inbox and SSE subscriptions share one node pool. One account holds
// at most RealtimeAccountNodeSharePercent of it, as with connections, so a
// tenant opening many connections cannot take every slot on the node.
func TestResumeSubscriptionsKeepAnAccountToItsNodeShare(t *testing.T) {
	m := &Manager{}
	noisy, quiet := &accountConnections{}, &accountConnections{}
	share := maxResumeSubscriptions * api.RealtimeAccountNodeSharePercent / 100
	for i := range share {
		if !m.reserveResume(noisy) {
			t.Fatalf("reservation %d within the account share refused", i)
		}
	}
	if m.reserveResume(noisy) {
		t.Fatal("reservation past the account share admitted")
	}
	if got := noisy.resume.Load(); got != int64(share) {
		t.Fatalf("noisy account holds %d, want %d", got, share)
	}
	for i := range maxResumeSubscriptions - share {
		if !m.reserveResume(quiet) {
			t.Fatalf("another account's reservation %d refused", i)
		}
	}
	// The node cap still holds, and a refused reservation leaves no count.
	if m.reserveResume(&accountConnections{}) || m.reserveResume(nil) {
		t.Fatal("reservation past the node cap admitted")
	}
	m.releaseResume(noisy)
	if noisy.resume.Load() != int64(share-1) || m.resumeCount.Load() != maxResumeSubscriptions-1 {
		t.Fatalf("after release: account %d node %d", noisy.resume.Load(), m.resumeCount.Load())
	}
	if !m.reserveResume(noisy) {
		t.Fatal("released slot not reusable")
	}
}
