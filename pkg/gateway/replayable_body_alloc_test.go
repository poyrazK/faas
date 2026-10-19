// adr: 1736
package gateway

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// gatewayd-public buffers small bodies before the internal hop. The buffer
// must grow with the bytes the client actually sends: allocating the declared
// Content-Length up front let an unauthenticated client pin 64 KiB per
// connection, until ReadTimeout, by declaring a body and sending none of it.
func TestMakeBodyReplayableAllocatesOnlyReceivedBytes(t *testing.T) {
	const runs = 20
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("only ten b"))
		req.ContentLength = api.InternalHopReplayableBodyBytes
		if err := makeBodyReplayable(req); err == nil {
			t.Fatal("a body shorter than its Content-Length must be an error")
		}
	}
	runtime.ReadMemStats(&after)
	if perRun := (after.TotalAlloc - before.TotalAlloc) / runs; perRun >= uint64(api.InternalHopReplayableBodyBytes)/4 {
		t.Fatalf("allocated %d bytes per short request; want far less than the declared %d", perRun, api.InternalHopReplayableBodyBytes)
	}
}

func TestBufferReplayableServiceBodyAllocatesOnlyReceivedBytes(t *testing.T) {
	const runs = 20
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("only ten b"))
		req.ContentLength = api.ServiceProxyReplayBodyMaxBytes
		if bufferReplayableServiceBody(httptest.NewRecorder(), req) {
			t.Fatal("a body shorter than its Content-Length must be refused")
		}
	}
	runtime.ReadMemStats(&after)
	if perRun := (after.TotalAlloc - before.TotalAlloc) / runs; perRun >= uint64(api.ServiceProxyReplayBodyMaxBytes)/4 {
		t.Fatalf("allocated %d bytes per short request; want far less than the declared %d", perRun, api.ServiceProxyReplayBodyMaxBytes)
	}
}
