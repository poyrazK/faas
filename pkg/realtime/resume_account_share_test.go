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
	noisyState, quietState := &endpointState{account: noisy}, &endpointState{account: quiet}
	share := maxResumeSubscriptions * api.RealtimeAccountNodeSharePercent / 100
	for i := range share {
		if !m.reserveResume(noisyState) {
			t.Fatalf("reservation %d within the account share refused", i)
		}
	}
	if m.reserveResume(noisyState) {
		t.Fatal("reservation past the account share admitted")
	}
	if got := noisy.resume.Load(); got != int64(share) {
		t.Fatalf("noisy account holds %d, want %d", got, share)
	}
	for i := range maxResumeSubscriptions - share {
		if !m.reserveResume(quietState) {
			t.Fatalf("another account's reservation %d refused", i)
		}
	}
	// The node cap still holds, and a refused reservation leaves no count.
	if m.reserveResume(&endpointState{account: &accountConnections{}}) || m.reserveResume(nil) {
		t.Fatal("reservation past the node cap admitted")
	}
	m.releaseResume(noisy)
	if noisy.resume.Load() != int64(share-1) || m.resumeCount.Load() != maxResumeSubscriptions-1 {
		t.Fatalf("after release: account %d node %d", noisy.resume.Load(), m.resumeCount.Load())
	}
	if !m.reserveResume(noisyState) {
		t.Fatal("released slot not reusable")
	}
}

// adr: 1323 — with fair use, an account holds at most its plan's resume
// subscriptions on a node; realtimed reads the plan from the account
// connection allowance apid registers the endpoint with.
func TestResumeSubscriptionsKeepAnAccountToItsPlan(t *testing.T) {
	hobby, ok := api.RealtimeLimitsFor(api.PlanHobby)
	if !ok {
		t.Fatal("no Hobby realtime limits")
	}
	for _, fairUse := range []bool{true, false} {
		m := &Manager{cfg: Config{FairUse: fairUse}}
		state := &endpointState{account: &accountConnections{}}
		state.config.Store(&Endpoint{AccountMaxConnections: hobby.ConnectionsPerAccount})
		want := hobby.ResumeSubscriptionsPerNode
		if !fairUse {
			want = maxResumeSubscriptions * api.RealtimeAccountNodeSharePercent / 100
		}
		for i := range want {
			if !m.reserveResume(state) {
				t.Fatalf("fair use %v: reservation %d within the allowance refused", fairUse, i)
			}
		}
		if m.reserveResume(state) {
			t.Fatalf("fair use %v: reservation past %d admitted", fairUse, want)
		}
	}
}
