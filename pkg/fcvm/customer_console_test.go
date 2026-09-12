// spec: §12.3 — the customer log surface contains tenant stdout/stderr and
// must not expose Firecracker control-plane output.
package fcvm

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

func TestCustomerConsoleWriterExcludesFirecrackerControlLogs(t *testing.T) {
	ring := logbuf.New(64 << 10)
	w := &customerConsoleWriter{ring: ring}
	chunks := []string{
		"[instance:main] Running Firecracker v1.7.0\napp ready\n",
		"[instance:fc_", // verify split writes cannot bypass classification
		"api] The API server received a Put request on \"/snapshot/load\" with body \"backend_path=snap-in-mem\"\n",
		"customer payload mentions Firecracker and /snapshot/load safely\n",
		"[instance:vmm] restored snapshot\nrequest handled\n",
	}
	for _, chunk := range chunks {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	lines := ring.Snapshot(1)
	if len(lines) != 3 {
		t.Fatalf("customer lines = %+v, want 3", lines)
	}
	joined := lines[0].Line + lines[1].Line + lines[2].Line
	for _, internal := range []string{"fc_api", "backend_path=snap-in-mem", "[instance:vmm]"} {
		if strings.Contains(joined, internal) {
			t.Fatalf("customer log exposed %q: %q", internal, joined)
		}
	}
	for _, customer := range []string{"app ready", "customer payload mentions Firecracker", "request handled"} {
		if !strings.Contains(joined, customer) {
			t.Fatalf("customer line %q was lost: %q", customer, joined)
		}
	}
}

func TestFirecrackerControlLineDoesNotFilterKernelOrArbitraryAppPrefixes(t *testing.T) {
	for _, line := range []string{
		"[    0.123] Linux version 6.1\n",
		"[my-app:main] application started\n",
		"plain application log\n",
	} {
		if firecrackerControlLine([]byte(line)) {
			t.Fatalf("guest line classified as Firecracker control output: %q", line)
		}
	}
}
