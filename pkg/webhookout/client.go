package webhookout

import (
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/oci"
)

// The process-wide delivery clients. Every Dispatcher built without a
// caller client sends through one of these, so all deliveries in a process
// share one connection pool. Building a Dispatcher per delivery (schedd's
// app webhooks) or per request (apid and meterd test sends) used to build a
// fresh transport each time; the response body is drained, so each
// keep-alive connection then idled in a transport nothing could reach
// until the receiver closed it, holding a file descriptor and two
// goroutines per delivery.
//
// The guarded client is the production one. The loopback client exists
// only for FAAS_EGRESS_ALLOW_LOOPBACK=1 test runs; it is a separate client
// so a process never mixes guarded and unguarded connections in one pool.
var (
	sharedGuardedOnce  sync.Once
	sharedGuarded      *http.Client
	sharedLoopbackOnce sync.Once
	sharedLoopback     *http.Client
)

// SharedHTTPClient returns the process-wide delivery client: SSRF-guarded
// at dial time, with bounded idle connections (see oci.EgressIdleConnTimeout).
// The FAAS_EGRESS_ALLOW_LOOPBACK=1 test escape hatch is read on every call,
// as NewDispatcher always did, so a test that sets it still gets a client
// that can reach 127.0.0.1.
//
// The returned client has no Timeout and follows redirects. Callers must not
// mutate it: copy the struct to set a Timeout or CheckRedirect. A copy keeps
// the shared Transport, which is what shares the pool.
func SharedHTTPClient() *http.Client {
	if oci.EgressAllowLoopbackFromEnv() {
		sharedLoopbackOnce.Do(func() { sharedLoopback = oci.NewEgressHTTPClientAllowLoopback() })
		if sharedLoopback != nil {
			return sharedLoopback
		}
	}
	sharedGuardedOnce.Do(func() { sharedGuarded = oci.NewEgressHTTPClient() })
	return sharedGuarded
}
