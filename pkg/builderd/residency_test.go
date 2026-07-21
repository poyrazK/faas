package builderd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseResidentPct_PlainValue(t *testing.T) {
	const body = `# HELP fcvm_resident_ram_pct tenant residency vs ceiling
# TYPE fcvm_resident_ram_pct gauge
fcvm_resident_ram_pct 12.5
`
	pct, err := parseResidentPct(stringReader(body))
	if err != nil {
		t.Fatalf("parseResidentPct: %v", err)
	}
	if pct != 12.5 {
		t.Errorf("pct = %v, want 12.5", pct)
	}
}

func TestParseResidentPct_WithLabels(t *testing.T) {
	const body = `
# HELP fcvm_resident_ram_pct tenant residency
fcvm_resident_ram_pct{tenant="acme"} 47.3
fcvm_snapshot_fleet_avg_bytes 130000000
`
	pct, err := parseResidentPct(stringReader(body))
	if err != nil {
		t.Fatalf("parseResidentPct: %v", err)
	}
	if pct != 47.3 {
		t.Errorf("pct = %v, want 47.3", pct)
	}
}

func TestParseResidentPct_Zero(t *testing.T) {
	const body = `fcvm_resident_ram_pct 0`
	pct, err := parseResidentPct(stringReader(body))
	if err != nil {
		t.Fatalf("parseResidentPct: %v", err)
	}
	if pct != 0 {
		t.Errorf("pct = %v, want 0", pct)
	}
}

func TestParseResidentPct_MissingReturnsError(t *testing.T) {
	const body = `fcvm_snapshot_fleet_avg_bytes 1`
	_, err := parseResidentPct(stringReader(body))
	if err == nil {
		t.Fatal("expected error when gauge missing")
	}
}

func TestParseResidentPct_GarbageValue(t *testing.T) {
	const body = `fcvm_resident_ram_pct banana`
	_, err := parseResidentPct(stringReader(body))
	if err == nil {
		t.Fatal("expected parse error on non-numeric value")
	}
}

// TestMetricsResident_HappyPath serves a canned /metrics body, asserts the
// probe converts percentage → MB correctly, and confirms a background
// poll updates the cached value when schedd's view changes.
func TestMetricsResident_HappyPath(t *testing.T) {
	var hits atomic.Int32
	var currentPct atomic.Int64
	currentPct.Store(50) // 50 % × 47,600 = 23,800 MB resident
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "fcvm_resident_ram_pct %d\n", currentPct.Load())
	}))
	defer srv.Close()

	// Tighten the poll for the test — the production 2 s cadence would
	// make the second-poll assertion slow.
	old := residentPollInterval
	residentPollInterval = 100 * time.Millisecond
	t.Cleanup(func() { residentPollInterval = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := NewMetricsResident(ctx, srv.URL+"/metrics")

	// First call is a synchronous prime, so we should see 23,800 MB
	// already without sleeping.
	if got := p.ResidentMB(); got != 23_800 {
		t.Errorf("initial probe = %d MB, want 23_800", got)
	}
	if hits.Load() < 1 {
		t.Error("expected at least 1 scrape on construction")
	}

	// Bump schedd's view to 30 % — one tick later, the probe picks it up.
	currentPct.Store(30) // 30 % × 47,600 = 14,280
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.ResidentMB() == 14_280 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := p.ResidentMB(); got != 14_280 {
		t.Errorf("after poll update = %d MB, want 14_280 (hits=%d)", got, hits.Load())
	}
}

// TestMetricsResident_EmptyURLReturnsFixedZero pins the "no schedd wired"
// behaviour — empty URL ⇒ probe reports 0 (same as nil-probe path before
// this PR). DecideSlot then grants opportunistic by default (the safe
// ramp-up posture when startup is fresh).
func TestMetricsResident_EmptyURLReturnsFixedZero(t *testing.T) {
	p := NewMetricsResident(context.Background(), "")
	if got := p.ResidentMB(); got != 0 {
		t.Errorf("empty-URL probe = %d MB, want 0", got)
	}
}

// TestMetricsResident_HTTPDownSwallowsError exercises the error path. A
// refused connection must NOT panic and ResidentMB() must keep returning
// the most recently cached value (zero, here, since the prime failed).
func TestMetricsResident_HTTPDownSwallowsError(t *testing.T) {
	p := NewMetricsResident(context.Background(), "http://127.0.0.1:1/metrics")
	if got := p.ResidentMB(); got != 0 {
		t.Errorf("refused-conn probe = %d MB, want 0", got)
	}
}

// TestFixedResident mirrors the slot_test.go contract for the trivial
// probe — confirms ResidentMB round-trips SetMB's last value (i.e., the
// assignment isn't racy under concurrent SetMB+ResidentMB).
func TestFixedResident(t *testing.T) {
	p := FixedResident(1_000)
	if got := p.ResidentMB(); got != 1_000 {
		t.Errorf("FixedResident(1000).ResidentMB = %d, want 1000", got)
	}
}

// stringReader is a tiny io.Reader adapter for parseResidentPct's tests.
type stringReaderImpl struct {
	s string
	p int
}

func (r *stringReaderImpl) Read(p []byte) (int, error) {
	if r.p >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.p:])
	r.p += n
	return n, nil
}

func stringReader(s string) *stringReaderImpl { return &stringReaderImpl{s: s} }
