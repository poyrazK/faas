// Tests for the type=0x05 workload-OOM envelope (Cluster C /
// ADR-121). Mirrors the parseFrameworkReadyDatagram test pattern
// from framework_ready_recv_test.go but for the new closed-set
// member. The wire shape is:
//
//	[1B type=0x05][json:{"peak_mb":N,"plan_mb":N}]
//
// Two failure classes are pinned:
//
//  1. Malformed JSON envelope → parse error (the dispatcher
//     never sees it).
//  2. Closed-set byte outside {0x01..0x06} → parse error
//     ("unknown msg sub-type 0xNN").
//
// The TypeLabel round-trip is also pinned so the Debug log
// classification tracks the constant.
//
// Build tag mirrors framework_ready_recv.go (linux-only —
// the wire-envelope types live behind the linux build tag
// because they're driven by the AF_VSOCK DGRAM listener).
//go:build linux

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseFrameworkReadyDatagram_WorkloadOOM_ValidBody asserts
// the type=0x05 envelope parses into the typed (peak_mb,
// plan_mb) tuple verbatim. The values flow end-to-end into
// the schedd's whycopy Observed closure
// (pkg/whycopy/whycopy.go::CodeAppRuntimeOOM); a future
// refactor that re-shaped the values would silently break the
// customer's templated ErrorWhy / ErrorFix prose.
func TestParseFrameworkReadyDatagram_WorkloadOOM_ValidBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                   string
		peakMB, planMB         int
		wantKind               parseFWKind
		wantPeakMB, wantPlanMB int
		wantTypeLabelSubstring string
	}{
		{"hobby_plan_over", 384, 256, parseFWReadyKindWorkloadOOM, 384, 256, "0x05"},
		{"pro_plan_under", 96, 512, parseFWReadyKindWorkloadOOM, 96, 512, "0x05"},
		{"free_plan_zero_peak", 0, 128, parseFWReadyKindWorkloadOOM, 0, 128, "0x05"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := append([]byte{VsockFrameworkReadyHostTypeWorkloadOOM}, []byte(makeOOMWire(tc.peakMB, tc.planMB))...)
			got, err := parseFrameworkReadyDatagram(body)
			if err != nil {
				t.Fatalf("parseFrameworkReadyDatagram(%v): unexpected err: %v", body, err)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %d, want %d", got.Kind, tc.wantKind)
			}
			if got.WorkloadOOM.PeakMB != tc.wantPeakMB {
				t.Errorf("WorkloadOOM.PeakMB = %d, want %d", got.WorkloadOOM.PeakMB, tc.wantPeakMB)
			}
			if got.WorkloadOOM.PlanMB != tc.wantPlanMB {
				t.Errorf("WorkloadOOM.PlanMB = %d, want %d", got.WorkloadOOM.PlanMB, tc.wantPlanMB)
			}
		})
	}
}

// TestParseFrameworkReadyDatagram_WorkloadOOM_MalformedJSON
// pins the dispatcher's contract: a non-JSON payload following
// the type byte is rejected with the "workload_oom" sentinel
// (mirroring the sidecar_init_exit / restart error tags). The
// loop's warn-log + drop pattern is unchanged.
func TestParseFrameworkReadyDatagram_WorkloadOOM_MalformedJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		body   []byte
		errSub string
	}{
		{
			name:   "non_json_payload",
			body:   append([]byte{VsockFrameworkReadyHostTypeWorkloadOOM}, []byte{0xFF, 0xFE, 0xFD}...),
			errSub: "workload_oom",
		},
		{
			name:   "json_missing_closing_brace",
			body:   append([]byte{VsockFrameworkReadyHostTypeWorkloadOOM}, []byte(`{"peak_mb":384`)...),
			errSub: "workload_oom",
		},
		{
			name:   "empty_body_after_type",
			body:   []byte{VsockFrameworkReadyHostTypeWorkloadOOM},
			errSub: "workload_oom",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFrameworkReadyDatagram(tc.body)
			if err == nil {
				t.Fatalf("parseFrameworkReadyDatagram(%v): err = nil, want error", tc.body)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.errSub)
			}
		})
	}
}

// TestParseFrameworkReadyDatagram_TypeClosedSet pins the
// 6-value closed-set discipline: {0x01..0x06} parse OK;
// anything else returns the "unknown msg sub-type" sentinel.
// Mirrors TestParseFrameworkReadyDatagram's earlier
// closed-set guard. Future event classes add a byte + a
// switch case + a test in lockstep — this tripwire catches a
// forgotten case.
func TestParseFrameworkReadyDatagram_TypeClosedSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body []byte
	}{
		{"type_0x00", []byte{0x00}},
		{"type_0xFF", []byte{0xFF, 0x00, 0x00}},
		{"type_0x07_then_payload", []byte{0x07, '{', '}'}},
		{"type_0x10_then_long_payload", append([]byte{0x10}, []byte(strings.Repeat("x", 32))...)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFrameworkReadyDatagram(tc.body)
			if err == nil {
				t.Fatalf("parseFrameworkReadyDatagram(%v) = nil err, want unknown-type error", tc.body)
			}
			if !strings.Contains(err.Error(), "unknown msg sub-type") {
				t.Errorf("err = %q, want substring 'unknown msg sub-type'", err.Error())
			}
		})
	}
}

// TestTypeLabel_WorkloadOOM pins the diagnostic label for
// the new closed-set member. TypeLabel is for Debug logs;
// the loop never dispatches on it (the dispatch is the
// parseFWKind switch), but the label is the operator-
// facing identifier and breaking it would silently gut
// the on-call's fleet-debug experience.
func TestTypeLabel_WorkloadOOM(t *testing.T) {
	t.Parallel()
	m := parseFWReadyMsg{Kind: parseFWReadyKindWorkloadOOM}
	got := m.TypeLabel()
	if !strings.Contains(got, "workload_oom") {
		t.Errorf("TypeLabel = %q, want substring 'workload_oom'", got)
	}
	if !strings.Contains(got, "0x05") {
		t.Errorf("TypeLabel = %q, want substring '0x05'", got)
	}
}

// TestParseFrameworkReadyDatagram_WorkloadOOM_BodyCap
// (review finding #5) pins the host-side body-cap
// enforcement: a workload-OOM envelope larger than
// VsockWorkloadOOMMaxBody (256 bytes) is rejected with
// the "workload_oom: body too large" sentinel. The
// guest-side EmitWorkloadOOM clamps to
// workloadOOMEmitMaxBody = 256 before the socket opens;
// the host-side cap is the tripwire that catches a
// hostile or buggy guest that bypasses the guest-side
// clamp. The read buffer is frameworkReadyMaxDatagram
// = 1024 (large enough for ALL types); without the
// per-type cap, a 1 KiB workload-OOM payload would
// silently flow through to the dispatcher.
func TestParseFrameworkReadyDatagram_WorkloadOOM_BodyCap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		bodyLen   int // bytes after the type byte
		wantError bool
	}{
		{"at_cap_minus_one", int(VsockWorkloadOOMMaxBody) - 1, false},
		{"at_cap", int(VsockWorkloadOOMMaxBody), false},
		{"at_cap_plus_one", int(VsockWorkloadOOMMaxBody) + 1, true},
		{"double_cap", int(VsockWorkloadOOMMaxBody) * 2, true},
		{"read_buffer_max", frameworkReadyMaxDatagram - 1, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Build a body of EXACTLY tc.bodyLen bytes
			// after the type byte. The cap is enforced
			// on `len(rest)` where `rest := b[1:]`, so
			// the wire packet is 1 + tc.bodyLen bytes
			// total. For the under-cap cases we leave
			// the JSON malformed (the cap check runs
			// BEFORE unmarshal, so under-cap is what
			// matters); for the over-cap cases the
			// cap-check trips before unmarshal, so
			// malformed content is fine.
			packet := make([]byte, 0, 1+tc.bodyLen)
			packet = append(packet, VsockFrameworkReadyHostTypeWorkloadOOM)
			packet = append(packet, []byte(strings.Repeat("x", tc.bodyLen))...)
			_, err := parseFrameworkReadyDatagram(packet)
			if tc.wantError {
				if err == nil {
					t.Fatalf("body %d bytes: err = nil, want body-cap error", tc.bodyLen)
				}
				if !strings.Contains(err.Error(), "body too large") {
					t.Errorf("body %d bytes: err = %q, want substring 'body too large'", tc.bodyLen, err.Error())
				}
				if !strings.Contains(err.Error(), "workload_oom") {
					t.Errorf("body %d bytes: err = %q, want substring 'workload_oom'", tc.bodyLen, err.Error())
				}
				return
			}
			// Under-cap cases: the JSON unmarshal will
			// fail (the body is all 'x' bytes), so we
			// assert the error is the unmarshal one,
			// not the body-cap one. The tripwire is
			// "did the cap check pass" — a future
			// refactor that bumps the cap or the
			// payload size below the cap shouldn't
			// trip the cap guard.
			if err == nil {
				t.Fatalf("body %d bytes: err = nil, want unmarshal failure (under-cap path)", tc.bodyLen)
			}
			if strings.Contains(err.Error(), "body too large") {
				t.Errorf("body %d bytes: err = %q, contains 'body too large' (should be under cap)", tc.bodyLen, err.Error())
			}
			if !strings.Contains(err.Error(), "workload_oom") {
				t.Errorf("body %d bytes: err = %q, want substring 'workload_oom'", tc.bodyLen, err.Error())
			}
		})
	}
}

// makeOOMWire constructs a JSON envelope for the type=0x05
// body. Used by the closed-set + payload tests above.
func makeOOMWire(peakMB, planMB int) string {
	b, err := json.Marshal(workloadOOMWire{PeakMB: peakMB, PlanMB: planMB})
	if err != nil {
		// json.Marshal on a struct of two ints can't fail;
		// panic is appropriate here — the test code itself
		// is broken.
		panic(err)
	}
	return string(b)
}
