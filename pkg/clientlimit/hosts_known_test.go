// adr: 20261103163954
package clientlimit

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A flood of random names cannot push a real host out of the table: only a
// host that served an app's answer gets its own bucket; every other name
// shares one unknown-host bucket.
func TestHostsRandomNamesCannotEvictKnownHosts(t *testing.T) {
	clock := newFakeClock()
	var refused refusals
	h := Hosts(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "shop.gregale.dev" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound) // routes nowhere
	}), RequestLimits{PerSecond: 1, Burst: 3, TrackedAddrsMax: 4},
		func(r *http.Request) (string, bool) { return r.Host, r.Host != "" }, refused.add, clock.Now)
	do := func(host string) int {
		req := httptest.NewRequest(http.MethodGet, "https://x/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := do("shop.gregale.dev"); got != http.StatusOK {
		t.Fatalf("first request = %d", got)
	}
	clock.advance(time.Minute) // shop's bucket refills; a sweep would drop it
	limited := 0
	for i := range 1000 {
		if do(fmt.Sprintf("r%d.gregale.dev", i)) == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited < 990 {
		t.Fatalf("random names refused %d of 1000, want them to share one bucket", limited)
	}
	for i := range 3 {
		if got := do("shop.gregale.dev"); got != http.StatusOK {
			t.Fatalf("known host request %d during the flood = %d, want it served from its own bucket", i, got)
		}
	}
}

// Only an app's answer proves a host: the edge's misses, redirects and
// refusals do not.
func TestServedByApp(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusOK: true, http.StatusNoContent: true, http.StatusSwitchingProtocols: true, http.StatusNotModified: true,
		0: false, http.StatusNotFound: false, http.StatusMisdirectedRequest: false, http.StatusServiceUnavailable: false,
		http.StatusPermanentRedirect: false, http.StatusTooManyRequests: false,
	} {
		if got := servedByApp(status); got != want {
			t.Errorf("servedByApp(%d) = %v, want %v", status, got, want)
		}
	}
}

type hijackRecorder struct{ *httptest.ResponseRecorder }

func (hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }

// The recorder keeps hijack reachable (WebSocket upgrades through the edge)
// and counts a hijacked connection as a protocol switch.
func TestHostStatusRecorderHijack(t *testing.T) {
	rec := &hostStatusRecorder{ResponseWriter: hijackRecorder{httptest.NewRecorder()}}
	if _, _, err := http.NewResponseController(rec).Hijack(); err != nil {
		t.Fatalf("hijack through the recorder: %v", err)
	}
	if rec.status() != http.StatusSwitchingProtocols {
		t.Fatalf("status after hijack = %d", rec.status())
	}
}
