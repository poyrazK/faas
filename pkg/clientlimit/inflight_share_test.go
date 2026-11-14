package clientlimit

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
)

// ADR-20261107180000 (R8-9): one client address holds at most its share of
// the forwards, so another address is still served while it holds them.
func TestInFlightCapsEachAddress(t *testing.T) {
	var refused refusals
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	clientAddr := func(r *http.Request) (netip.Addr, bool) {
		a, err := netip.ParseAddr(r.Header.Get("X-Test-Client"))
		return a, err == nil
	}
	h := InFlight(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), 8, 2, clientAddr, refused.add)
	request := func(client string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "https://shop.gregale.dev/upload", nil)
		r.Header.Set("X-Test-Client", client)
		return r
	}

	var wg sync.WaitGroup
	// Two addresses of one IPv6 /64 are one client.
	for _, client := range []string{"2001:db8::1", "2001:db8::2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), request(client))
		}()
		<-entered
	}
	third := httptest.NewRecorder()
	h.ServeHTTP(third, request("2001:db8::3"))
	if third.Code != http.StatusServiceUnavailable {
		t.Fatalf("a third forward from the /64 = %d, want 503", third.Code)
	}
	refused.wait(t, ReasonForwardInFlightPerAddress)

	other := httptest.NewRecorder()
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(other, request("198.51.100.7"))
	}()
	<-entered
	close(release)
	wg.Wait()
	if other.Code != http.StatusNoContent {
		t.Fatalf("another address = %d, want it served", other.Code)
	}
}
