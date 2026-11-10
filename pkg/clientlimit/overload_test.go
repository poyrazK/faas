// adr: 20261110123517
package clientlimit

import (
	"crypto/tls"
	"errors"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseOverloadMode(t *testing.T) {
	for in, want := range map[string]OverloadMode{"": OverloadOff, "off": OverloadOff, " observe ": OverloadObserve, "on": OverloadOn} {
		got, err := ParseOverloadMode(in)
		if err != nil || got != want {
			t.Errorf("ParseOverloadMode(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := ParseOverloadMode("1"); err == nil {
		t.Fatal(`ParseOverloadMode("1") accepted a value that is no mode`)
	}
}

func testThresholds() OverloadThresholds {
	return OverloadThresholds{
		SchedDelayP99: 10 * time.Millisecond, NodeCPUPressure: 0.5, GatewayCPUShare: 0.5,
		Step: 0.1, RecoveryStep: 0.02, CalmFraction: 0.7,
	}
}

func TestOverloadLevelFollowsTheSignals(t *testing.T) {
	hot := OverloadSignals{SchedDelayP99: 20 * time.Millisecond}
	warm := OverloadSignals{SchedDelayP99: 8 * time.Millisecond} // between 70% and 100% of the threshold
	calm := OverloadSignals{SchedDelayP99: time.Millisecond}
	tenantBusy := OverloadSignals{NodeCPUPressure: 0.9, GatewayCPUShare: 0.1}
	gatewaysBusy := OverloadSignals{NodeCPUPressure: 0.9, GatewayCPUShare: 0.8}

	for _, tc := range []struct {
		name  string
		ticks []OverloadSignals
		want  float64
	}{
		{"rises a step per overloaded tick", []OverloadSignals{hot, hot, hot}, 0.3},
		{"stops at 1", repeat(hot, 15), 1},
		{"holds between calm and overloaded", append(repeat(hot, 5), repeat(warm, 10)...), 0.5},
		{"falls slower than it rises", append(repeat(hot, 5), repeat(calm, 10)...), 0.3},
		{"stops at 0", append(repeat(hot, 2), repeat(calm, 50)...), 0},
		{"a busy tenant alone is not the edge's overload", repeat(tenantBusy, 5), 0},
		{"node pressure counts while the gateways fill the node", repeat(gatewaysBusy, 5), 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := NewOverload(OverloadConfig{Mode: OverloadOn, Thresholds: testThresholds(), CPUs: 4})
			for _, s := range tc.ticks {
				o.Update(s)
			}
			if got := o.Level(); got < tc.want-1e-9 || got > tc.want+1e-9 {
				t.Fatalf("level %.3f, want %.3f", got, tc.want)
			}
		})
	}
}

func repeat(s OverloadSignals, n int) []OverloadSignals {
	out := make([]OverloadSignals, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// newTestOverload is an Overload at level, with a fake clock and a rand that
// always draws r.
func newTestOverload(mode OverloadMode, level float64, r float64, clock *fakeClock) *Overload {
	th := testThresholds()
	th.Step = level
	o := NewOverload(OverloadConfig{Mode: mode, Thresholds: th, CPUs: 1, Now: clock.Now, Rand: func() float64 { return r }})
	o.Update(OverloadSignals{SchedDelayP99: time.Second})
	return o
}

func TestOverloadShedConnection(t *testing.T) {
	client := netip.MustParsePrefix("198.51.100.7/32")
	t.Run("observe measures and sheds nothing", func(t *testing.T) {
		o := newTestOverload(OverloadObserve, 1, 0, newFakeClock())
		if o.Level() != 1 || o.Shedding() != 0 {
			t.Fatalf("level %v, shedding %v; want 1 and 0", o.Level(), o.Shedding())
		}
		if _, shed := o.ShedConnection(client, false); shed {
			t.Fatal("observe mode reset a connection")
		}
	})
	t.Run("a nil overload sheds nothing", func(t *testing.T) {
		var o *Overload
		if _, shed := o.ShedConnection(client, false); shed || o.Shedding() != 0 {
			t.Fatal("a nil overload shed")
		}
	})
	t.Run("past the floor every drawn connection is reset", func(t *testing.T) {
		clock := newFakeClock()
		o := newTestOverload(OverloadOn, 1, 0, clock)
		floor := api.EdgeOverloadHandshakesFloorPerCPU
		for i := range floor {
			if reason, shed := o.ShedConnection(client, false); shed {
				t.Fatalf("connection %d inside the floor of %d was reset (%s)", i, floor, reason)
			}
		}
		if reason, shed := o.ShedConnection(client, false); !shed || reason != ReasonOverloadConnection {
			t.Fatalf("connection past the floor: %q, %v; want %q", reason, shed, ReasonOverloadConnection)
		}
		if _, shed := o.ShedConnection(client, true); shed {
			t.Fatal("a proxy's connection was reset; its clients are shed per request")
		}
		clock.advance(time.Second)
		if _, shed := o.ShedConnection(client, false); shed {
			t.Fatal("the floor did not refill")
		}
	})
	t.Run("a connection the draw spares is admitted", func(t *testing.T) {
		o := newTestOverload(OverloadOn, 0.5, 0.6, newFakeClock())
		for range 2 * api.EdgeOverloadHandshakesFloorPerCPU {
			if _, shed := o.ShedConnection(client, false); shed {
				t.Fatal("a connection drawn above the level was reset")
			}
		}
	})
}

// shedHarness serves Shed in front of a 204, for one client address.
type shedHarness struct {
	o       *Overload
	h       http.Handler
	known   *KnownHosts
	refused refusals
}

func newShedHarness(o *Overload) *shedHarness {
	s := &shedHarness{o: o, known: NewKnownHosts(16, nil)}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	clientAddr := func(r *http.Request) (netip.Addr, bool) {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		return ap.Addr(), err == nil
	}
	host := func(r *http.Request) (string, bool) { return r.Host, r.Host != "" }
	s.h = Shed(next, o, clientAddr, host, s.known, s.refused.add)
	return s
}

func (s *shedHarness) do(host, client string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	r.RemoteAddr = client + ":40000"
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

func (s *shedHarness) last() string {
	s.refused.mu.Lock()
	defer s.refused.mu.Unlock()
	if len(s.refused.reasons) == 0 {
		return ""
	}
	return s.refused.reasons[len(s.refused.reasons)-1]
}

func TestShedRefusesInOrder(t *testing.T) {
	t.Run("off and observe pass everything", func(t *testing.T) {
		for _, mode := range []OverloadMode{OverloadOff, OverloadObserve} {
			s := newShedHarness(newTestOverload(mode, 1, 0, newFakeClock()))
			if rec := s.do("unknown.gregale.dev", "198.51.100.7"); rec.Code != http.StatusNoContent {
				t.Fatalf("%s: status %d, want 204", mode, rec.Code)
			}
		}
	})
	t.Run("unknown hosts first", func(t *testing.T) {
		// At level 0.3 an unknown host is refused with probability 0.6 and a
		// known one not at all.
		s := newShedHarness(newTestOverload(OverloadOn, 0.3, 0.5, newFakeClock()))
		s.known.add("shop.gregale.dev")
		rec := s.do("unknown.gregale.dev", "198.51.100.7")
		if rec.Code != http.StatusServiceUnavailable || s.last() != ReasonOverloadUnknownHost {
			t.Fatalf("unknown host: %d %q, want 503 %q", rec.Code, s.last(), ReasonOverloadUnknownHost)
		}
		if rec := s.do("shop.gregale.dev", "198.51.100.7"); rec.Code != http.StatusNoContent {
			t.Fatalf("known host: status %d, want 204", rec.Code)
		}
	})
	t.Run("an address beyond its scaled share", func(t *testing.T) {
		// At level 1 the per-address rate is the floor, and nothing else is
		// drawn (r = 1 sits above every probability).
		s := newShedHarness(newTestOverload(OverloadOn, 1, 1, newFakeClock()))
		s.known.add("shop.gregale.dev")
		for i := range api.EdgeOverloadRequestsPerSecondPerAddrFloor {
			if rec := s.do("shop.gregale.dev", "198.51.100.7"); rec.Code != http.StatusNoContent {
				t.Fatalf("request %d inside the floor: %d %q", i, rec.Code, s.last())
			}
		}
		if rec := s.do("shop.gregale.dev", "198.51.100.7"); rec.Code != http.StatusServiceUnavailable || s.last() != ReasonOverloadAddress {
			t.Fatalf("past the floor: %d %q, want 503 %q", rec.Code, s.last(), ReasonOverloadAddress)
		}
		if rec := s.do("shop.gregale.dev", "203.0.113.9"); rec.Code != http.StatusNoContent {
			t.Fatalf("another address: status %d, want 204", rec.Code)
		}
	})
	t.Run("everything past the midpoint", func(t *testing.T) {
		// At level 0.8 any request is refused with probability 0.6.
		s := newShedHarness(newTestOverload(OverloadOn, 0.8, 0.5, newFakeClock()))
		s.known.add("shop.gregale.dev")
		if rec := s.do("shop.gregale.dev", "198.51.100.7"); rec.Code != http.StatusServiceUnavailable || s.last() != ReasonOverloadRequest {
			t.Fatalf("status %d %q, want 503 %q", rec.Code, s.last(), ReasonOverloadRequest)
		}
		below := newShedHarness(newTestOverload(OverloadOn, 0.5, 0.01, newFakeClock()))
		below.known.add("shop.gregale.dev")
		if rec := below.do("shop.gregale.dev", "198.51.100.7"); rec.Code != http.StatusNoContent {
			t.Fatalf("at the midpoint a known host's request was refused: %d %q", rec.Code, below.last())
		}
	})
}

func TestShedRefusalIsAnOverloadProblem(t *testing.T) {
	for _, r := range []float64{0, 0.5, 0.999} {
		s := newShedHarness(newTestOverload(OverloadOn, 1, r, newFakeClock()))
		rec := s.do("unknown.gregale.dev", "198.51.100.7")
		retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		if err != nil || retry < api.EdgeOverloadRetryAfterMin || retry > api.EdgeOverloadRetryAfterMax {
			t.Fatalf("Retry-After %q, want %d–%d", rec.Header().Get("Retry-After"), api.EdgeOverloadRetryAfterMin, api.EdgeOverloadRetryAfterMax)
		}
		if !strings.Contains(rec.Body.String(), `"edge_overloaded"`) {
			t.Fatalf("body %q, want the edge_overloaded problem", rec.Body.String())
		}
		if rec.Header().Get("Connection") != "" {
			t.Fatal("a first refusal closed the connection; refusals keep it open")
		}
	}
}

func TestShedCutsOffAnAddressThatKeepsSending(t *testing.T) {
	clock := newFakeClock()
	o := newTestOverload(OverloadOn, 1, 0, clock)
	s := newShedHarness(o)
	client := "198.51.100.7"
	key := Key(netip.MustParseAddr(client))
	for i := range api.EdgeOverloadRefusalsPerAddrMax {
		if rec := s.do("unknown.gregale.dev", client); rec.Header().Get("Connection") == "close" {
			t.Fatalf("refusal %d of %d closed the connection", i+1, api.EdgeOverloadRefusalsPerAddrMax)
		}
	}
	rec := s.do("unknown.gregale.dev", client)
	if rec.Header().Get("Connection") != "close" {
		t.Fatal("the refusal past the limit did not close the connection")
	}
	// Cut off: every request is refused, known host or not, and new
	// connections are reset before TLS.
	s.known.add("shop.gregale.dev")
	if rec := s.do("shop.gregale.dev", client); rec.Code != http.StatusServiceUnavailable || s.last() != ReasonOverloadCooloff || rec.Header().Get("Connection") != "close" {
		t.Fatalf("cut-off address: %d %q %q, want 503 %q with Connection: close", rec.Code, s.last(), rec.Header().Get("Connection"), ReasonOverloadCooloff)
	}
	if reason, shed := o.ShedConnection(key, false); !shed || reason != ReasonOverloadCooloff {
		t.Fatalf("cut-off address's connection: %q %v, want %q", reason, shed, ReasonOverloadCooloff)
	}
	if reason, shed := o.ShedConnection(key, true); !shed || reason != ReasonOverloadCooloff {
		t.Fatal("a cut-off address in a proxy range was admitted")
	}
	clock.advance(api.EdgeOverloadCooloff)
	if o.cool.active(key, clock.Now()) {
		t.Fatal("the cool-off did not end")
	}
}

func TestCooloffStaysWithinItsBound(t *testing.T) {
	clock := newFakeClock()
	c := &cooloff{limit: 0, d: time.Minute, max: 2, addrs: map[netip.Prefix]*cooloffEntry{}}
	a, b, d := netip.MustParsePrefix("192.0.2.1/32"), netip.MustParsePrefix("192.0.2.2/32"), netip.MustParsePrefix("192.0.2.3/32")
	if !c.refused(a, clock.Now()) || !c.refused(b, clock.Now()) {
		t.Fatal("a refusal past a zero limit did not cut off")
	}
	if c.refused(d, clock.Now()) || len(c.addrs) != 2 {
		t.Fatalf("a full set tracked a new address (%d tracked)", len(c.addrs))
	}
	clock.advance(time.Minute)
	if !c.refused(d, clock.Now()) {
		t.Fatal("expired entries were not swept to make room")
	}
}

func TestHandshakeGateResetsWhileShedding(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock()
	o := newTestOverload(OverloadOn, 1, 0, clock)
	for range api.EdgeOverloadHandshakesFloorPerCPU {
		o.floor.take(clock.Now())
	}
	var refused refusals
	gate := NewHandshakeGate(t.Context(), tls.NewListener(inner, testServerTLS(t)), 8, 5*time.Second, refused.add,
		func(net.Addr, error) {}).WithOverload(o)
	acceptAll(t, gate)
	if !refusedByPeer(t, inner.Addr().String()) {
		t.Fatal("a connection past the floor was handshaken while shedding")
	}
	refused.wait(t, ReasonOverloadConnection)
	if len(gate.slots) != 0 {
		t.Fatalf("%d handshake slots taken by a shed connection", len(gate.slots))
	}
}

func TestNodeSamplerSignals(t *testing.T) {
	start := time.Unix(1_000_000, 0)
	files := map[string]string{}
	set := func(pressure, a, b int) {
		files["/proc/pressure/cpu"] = "some avg10=1.00 avg60=0.50 avg300=0.10 total=" + strconv.Itoa(pressure) + "\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=7\n"
		files["/cg/a/cpu.stat"] = "usage_usec " + strconv.Itoa(a) + "\nuser_usec 1\nsystem_usec 1\n"
		files["/cg/b/cpu.stat"] = "usage_usec " + strconv.Itoa(b) + "\n"
	}
	hist := &metrics.Float64Histogram{Buckets: []float64{0, 0.001, 0.01, 0.1}, Counts: []uint64{0, 0, 0}}
	s := &NodeSampler{
		Window:       time.Second,
		PressurePath: "/proc/pressure/cpu",
		CgroupStats:  []string{"/cg/a/cpu.stat", "/cg/b/cpu.stat"},
		CPUs:         4,
		ReadFile: func(p string) ([]byte, error) {
			v, ok := files[p]
			if !ok {
				return nil, errors.New("missing")
			}
			return []byte(v), nil
		},
		Sched: func() *metrics.Float64Histogram {
			return &metrics.Float64Histogram{Buckets: hist.Buckets, Counts: append([]uint64(nil), hist.Counts...)}
		},
	}
	set(1_000_000, 5_000_000, 1_000_000)
	if got, err := s.Sample(start); err != nil || got != (OverloadSignals{}) {
		t.Fatalf("first sample %+v, %v; want zeros", got, err)
	}
	// One second later: 0.6 s of pressure, 2 CPU-seconds of gateway use on
	// 4 CPUs, and 98 of 100 new scheduling waits under 1 ms, 2 under 10 ms.
	set(1_600_000, 6_500_000, 1_500_000)
	hist.Counts = []uint64{98, 2, 0}
	got, err := s.Sample(start.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want := OverloadSignals{SchedDelayP99: 10 * time.Millisecond, NodeCPUPressure: 0.6, GatewayCPUShare: 0.5}
	if got.SchedDelayP99 != want.SchedDelayP99 || !near(got.NodeCPUPressure, want.NodeCPUPressure) || !near(got.GatewayCPUShare, want.GatewayCPUShare) {
		t.Fatalf("signals %+v, want %+v", got, want)
	}
	// A source it cannot read counts as zero and is reported.
	delete(files, "/cg/b/cpu.stat")
	got, err = s.Sample(start.Add(1100 * time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "/cg/b/cpu.stat") || got.GatewayCPUShare != 0 {
		t.Fatalf("missing cgroup: %+v, %v; want a zero share and an error naming it", got, err)
	}
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func TestHistogramQuantile(t *testing.T) {
	buckets := []float64{0, 0.001, 0.01, math.Inf(1)}
	for _, tc := range []struct {
		name      string
		base, cur []uint64
		want      time.Duration
	}{
		{"nothing new", []uint64{5, 5, 5}, []uint64{5, 5, 5}, 0},
		{"all fast", nil, []uint64{100, 0, 0}, time.Millisecond},
		{"tail in the unbounded bucket", nil, []uint64{90, 0, 10}, 10 * time.Millisecond},
		{"a counter went back", []uint64{5, 5, 5}, []uint64{4, 5, 5}, 0},
	} {
		if got := histogramQuantile(tc.base, tc.cur, buckets, 0.99); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGatewayCgroupStats(t *testing.T) {
	got, err := GatewayCgroupStats([]byte("0::/faas-cp.slice/faas-gatewayd-public.service\n"), "/sys/fs/cgroup", "faas-gatewayd-internal.service")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/sys/fs/cgroup/faas-cp.slice/faas-gatewayd-public.service/cpu.stat",
		"/sys/fs/cgroup/faas-cp.slice/faas-gatewayd-internal.service/cpu.stat",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paths %v, want %v", got, want)
	}
	for _, bad := range []string{"0::/\n", "12:cpu:/x\n"} {
		if _, err := GatewayCgroupStats([]byte(bad), "/sys/fs/cgroup"); err == nil {
			t.Errorf("%q gave paths, want an error", bad)
		}
	}
}

func TestPSIAndCPUStatParse(t *testing.T) {
	if _, err := psiSomeTotal([]byte("full avg10=0 total=3\n")); err == nil {
		t.Error("a pressure file with no some line parsed")
	}
	if v, err := cpuStatUsage([]byte("user_usec 1\nusage_usec 42\n")); err != nil || v != 42 {
		t.Errorf("usage %d, %v; want 42", v, err)
	}
}
