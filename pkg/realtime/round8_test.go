package realtime

import (
	"sync/atomic"
	"testing"
	"time"
)

// adr: 1323 (amendment of 2026-11-03) — a restarted process restores the
// endpoints its predecessor saved, credentials and policy included, so
// reconnects after a deploy are not refused until apid's next reconcile.
func TestEndpointSnapshotRestoresRegistrations(t *testing.T) {
	var changes atomic.Int32
	old := NewManager(Config{EndpointsChanged: func() { changes.Add(1) }}, nil)
	defer old.Close()
	endpoint := Endpoint{
		ID: "ep-1", AppID: "app-1", AccountID: "acct-1",
		CallbackURL: "https://app.example", MessagePath: "/messages",
		AuthToken: "secret-token", AllowedOrigins: []string{"https://app.example"},
		MaxConnections: 25,
	}
	if err := old.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	if err := old.RegisterEndpoint(Endpoint{ID: "ep-2", AppID: "app-2", AccountID: "acct-1"}); err != nil {
		t.Fatal(err)
	}
	old.RemoveEndpoint("ep-2")
	if changes.Load() != 3 {
		t.Fatalf("changes = %d, want 3 (two registrations and a removal)", changes.Load())
	}
	data, err := old.EndpointSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	successor := NewManager(Config{}, nil)
	defer successor.Close()
	restored, err := successor.RestoreEndpoints(data)
	if err != nil || restored != 1 {
		t.Fatalf("restore = (%d, %v), want 1 endpoint", restored, err)
	}
	value, ok := successor.endpoints.Load("ep-1")
	if !ok {
		t.Fatal("the restored endpoint is not served")
	}
	got := *value.(*endpointState).config.Load()
	if got.AuthToken != "secret-token" || got.MaxConnections != 25 || len(got.AllowedOrigins) != 1 || got.ClientAuth.Mode != AuthModeStaticBearer {
		t.Fatalf("restored endpoint = %+v", got)
	}
	if _, err := successor.RestoreEndpoints([]byte(`{"version":99}`)); err == nil {
		t.Fatal("a snapshot of an unknown version was restored")
	}
}

// adr: 1323 (amendment of 2026-11-03) — a registration's fingerprint follows
// its content and changes when the previous token's grace ends.
func TestEndpointFingerprint(t *testing.T) {
	now := time.Unix(10_000, 0)
	base := Endpoint{ID: "ep", AuthToken: "a", AuthTokenPrevious: "b", AuthTokenPreviousExpiresAt: now.Add(time.Minute)}
	same := base
	if EndpointFingerprint(base, now) != EndpointFingerprint(same, now) {
		t.Fatal("equal registrations differ")
	}
	changed := base
	changed.AuthToken = "c"
	if EndpointFingerprint(base, now) == EndpointFingerprint(changed, now) {
		t.Fatal("a new token kept the fingerprint")
	}
	if EndpointFingerprint(base, now) == EndpointFingerprint(base, now.Add(2*time.Minute)) {
		t.Fatal("the end of the previous token's grace kept the fingerprint")
	}
}

// adr: 1323 (amendment of 2026-11-03) — route reports of different channels
// run in parallel; one channel's stay in order.
func TestRouteReportsLockPerChannel(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	a, b := channelKey{endpointID: "ep", channel: "a"}, channelKey{endpointID: "ep", channel: "b"}
	unlockA := m.lockRouteReport(a)
	done := make(chan struct{})
	go func() { m.lockRouteReport(b)(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("another channel's report waited for this one")
	}
	again := make(chan struct{})
	go func() { m.lockRouteReport(a)(); close(again) }()
	select {
	case <-again:
		t.Fatal("the same channel's reports ran together")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	<-again
	m.routeReportMu.Lock()
	left := len(m.routeReportLocks)
	m.routeReportMu.Unlock()
	if left != 0 {
		t.Fatalf("%d route report locks left after use", left)
	}
}
