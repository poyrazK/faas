package gateway

// adr: 190

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// RouteLookupStaleServedForTest exposes the ADR-190 counter to the
// external test package.
func RouteLookupStaleServedForTest(m *Metrics) float64 {
	return testutil.ToFloat64(m.routeLookupStaleServed)
}

func TestStaleRoutesExpireByTTL(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000, 0)
	s := newStaleRoutes(10, time.Minute)
	s.now = func() time.Time { return now }
	s.Put("a", App{ID: "app-a"})

	app, ok, shouldLog := s.Get("a")
	if !ok || app.ID != "app-a" || !shouldLog {
		t.Fatalf("first get = (%+v,%v,%v)", app, ok, shouldLog)
	}
	if _, _, shouldLog := s.Get("a"); shouldLog {
		t.Fatal("second get within a minute must not ask to log again")
	}
	now = now.Add(staleLogEvery)
	if _, _, shouldLog := s.Get("a"); !shouldLog {
		t.Fatal("log gate must reopen after staleLogEvery")
	}

	now = now.Add(time.Minute) // > ttl since resolution
	if _, ok, _ := s.Get("a"); ok {
		t.Fatal("entry past ttl must be a miss")
	}
	if s.Len() != 0 {
		t.Fatal("expired entry must be dropped")
	}
}

func TestStaleRoutesFIFOEvictionAndDelete(t *testing.T) {
	t.Parallel()
	s := newStaleRoutes(2, time.Hour)
	s.Put("a", App{ID: "1"})
	s.Put("b", App{ID: "2"})
	s.Put("c", App{ID: "3"}) // evicts a
	if _, ok, _ := s.Get("a"); ok {
		t.Fatal("oldest entry must be evicted at cap")
	}
	if _, ok, _ := s.Get("c"); !ok {
		t.Fatal("newest entry missing")
	}
	s.Put("b", App{ID: "2b"}) // update in place, no eviction
	if app, ok, _ := s.Get("b"); !ok || app.ID != "2b" {
		t.Fatalf("update in place failed: %+v %v", app, ok)
	}
	s.Delete("b")
	s.Delete("b") // idempotent
	if _, ok, _ := s.Get("b"); ok || s.Len() != 1 {
		t.Fatal("delete must remove the entry")
	}
}

func TestStaleRoutesDisabledAndNil(t *testing.T) {
	t.Parallel()
	off := newStaleRoutes(10, 0)
	off.Put("a", App{ID: "x"})
	if _, ok, _ := off.Get("a"); ok || off.Len() != 0 {
		t.Fatal("ttl=0 must store nothing")
	}
	var nilS *staleRoutes
	nilS.Put("a", App{})
	nilS.Delete("a")
	if _, ok, _ := nilS.Get("a"); ok || nilS.Len() != 0 {
		t.Fatal("nil tier must be inert")
	}
}

func TestRouteStaleTTLEnv(t *testing.T) {
	cases := map[string]time.Duration{
		"":    defaultRouteStaleTTL,
		"0":   0,
		"30s": 30 * time.Second,
		"bad": defaultRouteStaleTTL,
		"-5m": defaultRouteStaleTTL,
	}
	for raw, want := range cases {
		t.Setenv(RouteStaleTTLEnv, raw)
		if got := routeStaleTTL(); got != want {
			t.Fatalf("%q: got %s want %s", raw, got, want)
		}
	}
}
