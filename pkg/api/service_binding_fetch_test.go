package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// adr: 384 Exercise the standard client, not a copied list of forbidden ports.
func TestServiceBindingFetchCompatiblePort(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to exercise the WHATWG Fetch client")
	}
	for _, tc := range []struct {
		name         string
		port         int
		fetchAllowed bool
	}{
		{"legacy", ServiceBindingLegacyPort, false}, {"canonical", ServiceBindingPort, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := tc.port
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = fmt.Fprint(w, "binding-fetch-ok")
			})}
			t.Cleanup(func() { _ = server.Close() })
			go func() { _ = server.Serve(listener) }()
			binding := ServiceBindingEnvKey("worker")
			env := ServiceBindingEnv(nil, []AppServiceBinding{{Binding: binding, Service: "worker"}})
			if !strings.HasSuffix(env[binding], fmt.Sprintf(":%d", ServiceBindingPort)) {
				t.Fatalf("canonical binding = %s", env[binding])
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Only the host changes to reach the local fixture. The generated
			// canonical port is used without replacing the Fetch client.
			url := strings.Replace(env[binding], "worker.svc.gregale", "127.0.0.1", 1)
			if !tc.fetchAllowed {
				url = fmt.Sprintf("http://127.0.0.1:%d", port)
			}
			script := `fetch(process.argv[1], {signal: AbortSignal.timeout(2000)})
.then(async r => { if (r.status !== 200) throw new Error('status '+r.status); process.stdout.write(await r.text()) })
.catch(e => { process.stderr.write(String(e.cause?.message || e.message)); process.exitCode = 1 })`
			output, err := exec.CommandContext(ctx, node, "-e", script, url).CombinedOutput()
			if !tc.fetchAllowed {
				if err == nil || !strings.Contains(string(output), "bad port") || requests.Load() != 0 {
					t.Fatalf("legacy Fetch = %s/%v, requests=%d; want local bad-port refusal", output, err, requests.Load())
				}
			} else if err != nil || string(output) != "binding-fetch-ok" || requests.Load() != 1 {
				t.Fatalf("canonical Fetch = %s/%v, requests=%d", output, err, requests.Load())
			}
		})
	}
}
