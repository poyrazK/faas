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
		// hunt #10: device kicks on every rc.257 restore.
		"2026-10-14T08:12:32.964273610 [" + id + ":main] [Vsock:vsock] notifying tx queue",
		"2026-10-14T08:12:32.964280138 [" + id + ":main] [Balloon:balloon] notifying queues",
		"2026-10-14T08:12:32.964289134 [" + id + ":main] [Block:layer] notifying queues",
		"2026-10-14T08:12:32.964295007 [" + id + ":main] [Net:eth0] notifying queues",
		"2026-10-14T08:12:32.964295007 [" + id + ":main] [Rng:rng] notifying queues",
		"2026-10-14T08:12:32.964295007 [" + id + ":main] [Vsock:vsock] signaling event queue",
		"2026-10-14T08:12:32.964301857 [" + id + ":main] kick pmem base.",
	} {
		if !firecrackerControlLine([]byte(line)) {
			t.Errorf("production control line was not filtered: %q", line)
		}
	}
	for _, line := range []string{
		"2026-09-22T18:27:44Z request handled in 3ms",
		"2026-09-22T18:27:44Z [worker:main] processed job 42",
		"[worker:main] notifying 3 subscribers",
		"crashing on purpose",
		"guest-init: app crash-looped after 3 restart(s)",
	} {
		if firecrackerControlLine([]byte(line)) {
			t.Errorf("customer line was filtered: %q", line)
		}
	}
}

// hunt #8: job task logs began with Firecracker's start banner and
// guest-init's boot stages; only the workload's output belongs there.
func TestCustomerConsoleDropsStartBannerAndBootStages(t *testing.T) {
	if !firecrackerControlLine([]byte("2026-10-08T21:58:54.575241699 [d0426906-03df-43bf-99bf-eb060fa6fd99:main] Successfully started microvm that was configured from one single json\n")) {
		t.Error("Firecracker start banner reached the customer log")
	}
	for line, want := range map[string]bool{
		"guest-init: stage boot\n":                                         true,
		"guest-init: stage pivot\n":                                        true,
		"guest-init: app crash-looped after 3 restart(s): exit status 1\n": false,
		"job-ok\n": false,
	} {
		if got := guestInitStageLine([]byte(line)); got != want {
			t.Errorf("guestInitStageLine(%q) = %v, want %v", line, got, want)
		}
	}
}

// adr: 983 — Firecracker 1.17 logs vCPU-thread events and the guest's PCI
// probes, and a 6.18 guest reports the missing PCI config space on every boot.
// None of it is the customer's output; the console file keeps it.
func TestCustomerConsoleDropsVcpuLogsAndPCIConfigSpaceNotice(t *testing.T) {
	ring := logbuf.New(64 << 10)
	w := &customerConsoleWriter{ring: ring}
	for _, line := range []string{
		"2026-10-11T20:25:18.032196598 [m0-hello:fc_vcpu 0] vcpu: IO write @ 0xcf8:0x4 failed: Failed to find address range.\n",
		"2026-10-11T20:25:17.987772734 [m0-hello:fc_vcpu 1] Received a VcpuEvent::Resume message with immediate_exit enabled. immediate_exit was disabled before proceeding\n",
		"[    0.054718] PCI: Fatal: No config space access function found\n",
		"PCI: Fatal: No config space access function found\n",
		"app ready\n",
		"[    1.200000] PCI: Fatal: something the app caused\n",
	} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	lines := ring.Snapshot(1)
	if len(lines) != 2 || !strings.Contains(lines[0].Line, "app ready") || !strings.Contains(lines[1].Line, "something the app caused") {
		t.Fatalf("customer lines = %+v, want only the app line and the unrelated kernel error", lines)
	}
}

// production-us hunt #9 (H9-108): the serial console's CRLF left a '\r' at
// the end of every customer log line in the logs API and log drains.
func TestCustomerConsoleStripsTheConsoleCarriageReturn(t *testing.T) {
	ring := logbuf.New(64 << 10)
	w := &customerConsoleWriter{ring: ring}
	for _, chunk := range []string{"{\"msg\":\"ready\"}\r\n", "progress 50%\r\r\n", "plain\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	lines := ring.Snapshot(1)
	if len(lines) != 3 || lines[0].Line != `{"msg":"ready"}` || lines[1].Line != "progress 50%\r" || lines[2].Line != "plain" {
		t.Fatalf("customer lines = %+v, want the console CR stripped and the app's own CR kept", lines)
	}
}

// H10-15: on rc.257 every customer's logs and log drains began with
// Firecracker's API-socket line and guest-init's own startup and warning
// lines, including three "WARN stateless advisory fanotify_mark failed".
func TestCustomerConsoleDropsGuestInitPlatformLines(t *testing.T) {
	ring := logbuf.New(64 << 10)
	w := &customerConsoleWriter{ring: ring}
	for _, line := range []string{
		"2026-10-14T00:58:49.583701763 [4f7d6569-3265-464f-96be-9b199d019b24:main] Listening on API socket (\"api.sock\").\n",
		"2026/10/14 00:58:50 INFO workload identity proxy started endpoint=http://127.0.0.1:2773/oidc/token\n",
		"2026/10/14 00:58:50 INFO event publish proxy started endpoint=http://169.254.169.254/v1/events:publish\n",
		"2026/10/14 00:58:50 INFO outbound relay started endpoint=http://127.0.0.1:2774\n",
		"2026/10/14 00:58:50 WARN stateless advisory fanotify_mark failed (non-fatal) path=/data err=\"invalid argument\"\n",
		// H10-56: boot characterization and port normalization.
		"2026/10/14 15:46:06 WARN portnorm DNAT install failed rule=\"-t nat -A OUTPUT -p tcp --dport 8080 -j REDIRECT --to-ports 7000\"\n",
		"2026/10/14 15:46:06 WARN portnorm forward failed; host will see listen mismatch port=7000\n",
		"2026/10/14 15:46:06 INFO characterization complete mode=forward port=7000 class=http exit=-1 shipped=true\n",
		// Every boot's proxy and environment lines.
		"2026/10/14 19:44:14 INFO tail_events proxy started socket=/run/guest-init/tail-events.sock vsock_port=1027\n",
		"2026/10/14 19:44:14 INFO framework_ready stream proxy started vsock_port=1027\n",
		"2026/10/14 19:44:14 INFO secrets loaded count=3\n",
		"2026/10/14 19:44:14 INFO api_env loaded count=10\n",
		"2026-10-14T07:14:45.514973340 [c26f50da-1f34-494c-9ecc-acd797734558:main] Vmm is stopping.\n",
		"2026-10-14T07:14:45.515204584 [c26f50da-1f34-494c-9ecc-acd797734558:main] Killing vCPU threads\n",
		"2026/10/14 00:58:51 INFO order created id=42\n",
		// A job task's log ended with the exit delivery and the kernel halt.
		"2026/10/14 09:23:13 INFO job-exit shipped exit_code=3 error_class=failed signal=0 attempt=1\n",
		"[    2.497231] reboot: Power off not available: System halted instead\n",
		"workload identity proxy started is fine from an app without the slog stamp\n",
		"hello-gregale listening on :3000\n",
	} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, l := range ring.Snapshot(1) {
		got = append(got, strings.TrimSpace(l.Line))
	}
	want := []string{
		"2026/10/14 00:58:51 INFO order created id=42",
		"workload identity proxy started is fine from an app without the slog stamp",
		"hello-gregale listening on :3000",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("customer lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A command's last output without a newline reaches the ring when the VM
// exits; Flush is idempotent and still filters platform lines.
func TestCustomerConsoleWriterFlushCommitsFinalPartialLine(t *testing.T) {
	ring := logbuf.New(64 << 10)
	w := &customerConsoleWriter{ring: ring}
	if _, err := w.Write([]byte("first\r\ndone")); err != nil {
		t.Fatal(err)
	}
	if lines := ring.Snapshot(1); len(lines) != 1 {
		t.Fatalf("before flush = %+v, want only the complete line", lines)
	}
	for i := 0; i < 2; i++ {
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	lines := ring.Snapshot(1)
	if len(lines) != 2 || lines[0].Line != "first" || lines[1].Line != "done" {
		t.Fatalf("after flush = %+v, want first, done", lines)
	}
	if _, err := w.Write([]byte("guest-init: stage pivot")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := len(ring.Snapshot(1)); got != 2 {
		t.Fatalf("flushed platform line reached the customer log: %d lines", got)
	}
}

// A guest that never writes a newline cannot grow the writer past the ring's
// partial-line cap, and later lines still arrive.
func TestCustomerConsoleWriterBoundsPartialLine(t *testing.T) {
	ring := logbuf.New(64 << 10)
	w := &customerConsoleWriter{ring: ring}
	chunk := []byte(strings.Repeat("x", 64<<10))
	for written := 0; written <= 3*logbuf.MaxPartialLineBytes; written += len(chunk) {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if len(w.pending) > logbuf.MaxPartialLineBytes {
			t.Fatalf("pending grew to %d bytes", len(w.pending))
		}
	}
	if _, err := w.Write([]byte("\nafter\n")); err != nil {
		t.Fatal(err)
	}
	lines := ring.Snapshot(1)
	if len(lines) == 0 || lines[len(lines)-1].Line != "after" {
		t.Fatalf("an over-long partial line blocked later output: %d lines", len(lines))
	}
}
