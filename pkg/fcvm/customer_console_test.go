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
		"[instance:main] Host CPU vendor ID: GenuineIntel\n",
		"[instance:main] Snapshot CPU vendor ID: GenuineIntel\n",
		"[instance:main] Device kick on virtio-net\n",
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

func TestFirecrackerControlLineRecognizesRestoreDiagnostics(t *testing.T) {
	for _, line := range []string{
		"[2026-09-16T03:20:00Z:main] Host CPU vendor ID: GenuineIntel\n",
		"[2026-09-16T03:20:00Z:main] Snapshot CPU vendor ID: GenuineIntel\n",
		"[2026-09-16T03:20:00Z:main] Device kick on virtio-net\n",
		"[2026-09-16T03:20:00Z:main] The API server received a Put request\n",
		"[2026-09-16T03:20:00Z:fc_api] 'load snapshot' API request took 11325 us.\n",
	} {
		if !firecrackerControlLine([]byte(line)) {
			t.Errorf("restore diagnostic was not filtered: %q", line)
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

// Issue #3362: production Firecracker lines carry a wall-clock token before
// the "[instance:origin]" header, and every one of them reached the customer
// log. These are verbatim lines from a production restore and exit.
func TestFirecrackerControlLineRecognizesTimestampedProductionLines(t *testing.T) {
	const id = "70a2fc33-74d4-4013-91c7-2b883d61ca6a"
	for _, line := range []string{
		"2026-09-22T18:27:44.308421861 [" + id + ":main] Running Firecracker v1.7.0",
		"2026-09-22T18:27:44.312061439 [" + id + ":fc_api] The API server received a Put request on \"/snapshot/load\"",
		"2026-09-22T18:27:44.312235528 [" + id + ":main] [DevPreview] Virtual machine snapshots is in development preview.",
		"2026-09-22T18:27:44.312581881 [" + id + ":main] Host CPU vendor ID: [71, 101, 110]",
		"2026-09-22T18:27:44.318663910 [" + id + ":main] Artificially kick devices.",
		"2026-09-22T18:27:44.318679858 [" + id + ":main] kick net eth0.",
		"2026-09-22T18:27:44.318710356 [" + id + ":main] kick entropy rng.",
		"2026-09-22T18:27:44.318719225 [" + id + ":main] kick block base.",
		"2026-09-22T18:27:44.319312144 [" + id + ":main] [DevPreview] Virtual machine snapshots is in development preview - 'load snapshot' VMM action took 7038 us.",
		"2026-09-22T18:27:44.320377143 [" + id + ":fc_api] 'load snapshot' API request took 8347 us.",
		"2026-09-22T19:35:17.807045958 [" + id + ":main] Firecracker exiting successfully. exit_code=0",
	} {
		if !firecrackerControlLine([]byte(line)) {
			t.Errorf("production control line was not filtered: %q", line)
		}
	}
	for _, line := range []string{
		"2026-09-22T18:27:44Z request handled in 3ms",
		"2026-09-22T18:27:44Z [worker:main] processed job 42",
		"crashing on purpose",
		"guest-init: app crash-looped after 3 restart(s)",
	} {
		if firecrackerControlLine([]byte(line)) {
			t.Errorf("customer line was filtered: %q", line)
		}
	}
}
