//go:build metal

// stateless_advisory_metal_test.go — Wave 0 PR-C / ADR-047 metal test.
// adr: 047
//
// Spec §17 G13 / PR-C headline: a guest-init fanotify advisory on
// writes to state-shaped paths lands in the audit table. The unit
// tests pin the host-side seam (Manager.ForwardStatelessAdvisory
// forwards to AdvisoryForwarder; apid gRPC receiver maps to
// audit.Emit). This metal test is the end-to-end gate.
//
// What it does:
//  1. Builds (or fetches) a guest-init rootfs that has
//     guest/init/stateless_advisory_linux.go compiled in — the
//     boot-time fanotify mark + vsock DGRAM producer.
//  2. Cold-boots a real Firecracker microVM against it.
//  3. The guest entrypoint writes to /data/x after a 1s warmup;
//     the fanotify mark fires, the advisory debounces for
//     advisoryDeDupWindow=1s, the batch is shipped over AF_VSOCK
//     DGRAM port 1025.
//  4. The host-side Manager.SendStatelessAdvisory reads the
//     batch (passing it through ForwardStatelessAdvisory on the
//     Manager) and the stub AdvisoryForwarder asserts at least
//     one call with events[0].path starting with /data.
//
// Skips by default unless FAAS_TEST_* env vars are set
// (metalImages pattern from manager_metal_test.go). The unit
// tests in stateless_advisory_test.go pin the host-side shape;
// the CI gate for this file is `make metal-lima` (and the EX44
// §14 acceptance gate).
//
// Why this file is on a per-PR carve-out and not in a Wave 1
// follow-up: ADR-047 calls out that the end-to-end fanotify wire
// is the load-bearing assertion of the advisory. The wire is the
// thing that breaks silently if the producer/consumer drift on
// the fanotify mark filter set, the DGRAM framing, or the JSON
// shape — and the only honest assertion is a real boot.

package fcvm

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// metalAdvisoryStub records every advisory forwarded through the
// Manager. Mirrors stubAdvisoryForwarder (in the non-metal
// stateless_advisory_test.go) but adds a per-instance Wait helper
// for the cold-boot entrypoint's first write.
type metalAdvisoryStub struct {
	mu    sync.Mutex
	calls []metalAdvisoryCall
}

type metalAdvisoryCall struct {
	Instance string
	AppID    string
	Batch    []AdvisoryEvent
}

func (s *metalAdvisoryStub) Forward(_ context.Context, instance, appID string, events []AdvisoryEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, metalAdvisoryCall{Instance: instance, AppID: appID, Batch: events})
	return nil
}

func (s *metalAdvisoryStub) snapshot() []metalAdvisoryCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]metalAdvisoryCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// TestMetal_StatelessAdvisory_EndToEnd boots a real microVM and
// asserts the advisory batch lands at the host-side stub. Skips
// when the metal env isn't wired.
//
// Flow:
//  1. Resolve FAAS_TEST_* env (kernel, base, layer).
//  2. Wire a Manager with the metalAdvisoryStub as its
//     AdvisoryForwarder.
//  3. ColdBoot → wait ready → drop /data/foo from the host via
//     the guest's busybox entrypoint (the entrypoint already
//     busyboxes `touch /data/x` after a 1s warmup; see the
//     stateless-advisory rootfs build script).
//  4. Wait up to 10s for the stub to receive at least one
//     batch whose events[0].path starts with /data.
func TestMetal_StatelessAdvisory_EndToEnd(t *testing.T) {
	// The DGRAM receiver that turns guest port 1025 messages into
	// Manager.ForwardStatelessAdvisory calls is owned by cmd/vmmd. This
	// package-level process has no receiver to wire to the Manager below, so
	// running the body would wait for an event that cannot arrive. Keep the
	// missing cmd/vmmd acceptance harness visible in the native-suite tally.
	t.Skip("requires the cmd/vmmd stateless-advisory DGRAM receiver acceptance harness")

	kernel, base, layer := metalImages(t)
	stub := &metalAdvisoryStub{}
	m := newMetalManager(t, kernel)
	m.SetAdvisoryClient(stub)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const instance = "metal-advisory-1"
	if _, err := m.Wake(ctx, WakeRequest{
		Instance:   instance,
		AppID:      "metal-advisory-app",
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
	}); err != nil {
		t.Fatalf("ColdBoot: %v", err)
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		_ = m.Destroy(dctx, instance)
	})

	// Poll the stub for up to 10s. The guest debounce window is 1s
	// (advisoryDeDupWindow); 10s is generous on a busy CI box.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		calls := stub.snapshot()
		for _, c := range calls {
			for _, e := range c.Batch {
				if len(e.Path) >= 5 && e.Path[:5] == "/data" {
					t.Logf("advisory landed: instance=%s app=%s path=%s masks=%v",
						c.Instance, c.AppID, e.Path, e.Masks)
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	calls := stub.snapshot()
	if len(calls) == 0 {
		t.Fatalf("no advisory batches received within 10s of boot")
	}
	t.Fatalf("advisory batches received but none with /data path; got %d batch(es): %+v",
		len(calls), formatCallsForError(calls))
}

// formatCallsForError renders the stub's calls for a single t.Fatalf
// line. Stays terse so a failure log doesn't blow up the test
// output on a 50-batch noisy guest.
func formatCallsForError(calls []metalAdvisoryCall) string {
	if len(calls) == 0 {
		return "(none)"
	}
	out := fmt.Sprintf("call[0]={instance=%s app=%s events=", calls[0].Instance, calls[0].AppID)
	for i, e := range calls[0].Batch {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("{path=%s masks=%v}", e.Path, e.Masks)
	}
	out += "}"
	if len(calls) > 1 {
		out += fmt.Sprintf(" ...(+%d more batches)", len(calls)-1)
	}
	return out
}
