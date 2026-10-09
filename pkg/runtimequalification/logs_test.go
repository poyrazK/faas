package runtimequalification

// adr: 740

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeCoverageCannotBeSkippedOrBorrowed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*evidenceFixture)
	}{
		{"valid", func(*evidenceFixture) {}},
		{"missing run", func(f *evidenceFixture) { f.events = append(f.events[:1], f.events[2:]...) }},
		{"skipped", func(f *evidenceFixture) { f.events[3].Action = "skip" }},
		{"failed", func(f *evidenceFixture) { f.events[3].Action = "fail" }},
		{"other test", func(f *evidenceFixture) { f.events[1].Test = "TestMetalHelloBoot" }},
		{"other package", func(f *evidenceFixture) { f.events[4].Package = "another/package" }},
		{"missing package verdict", func(f *evidenceFixture) { f.events = f.events[:4] }},
		{"missing observation", func(f *evidenceFixture) { f.events[2].Output = "ok\n" }},
		{"borrowed observation", func(f *evidenceFixture) { f.report.Native.RunID = "different" }},
		{"duplicate observation", func(f *evidenceFixture) {
			f.events = append(f.events[:3], append([]testEvent{f.events[2]}, f.events[3:]...)...)
		}},
		{"duplicate run", func(f *evidenceFixture) {
			f.events = append(f.events[:2], append([]testEvent{f.events[1]}, f.events[2:]...)...)
		}},
		{"after verdict", func(f *evidenceFixture) { f.events = append(f.events, f.events[4]) }},
		{"out of order", func(f *evidenceFixture) { f.events[3].Time = f.events[0].Time.Add(-time.Second) }},
		{"outside interval", func(f *evidenceFixture) { f.events[0].Time = f.report.StartedAt.Add(-time.Second) }},
		{"failed build", func(f *evidenceFixture) { f.events[0].FailedBuild = NativePackage }},
		{"oversized event", func(f *evidenceFixture) {
			f.events[2].Output = string(bytes.Repeat([]byte("x"), api.RuntimeQualificationEventMaxBytes))
		}},
		{"leak", func(f *evidenceFixture) { f.leak = append([]byte("LEAK: cgroup\n"), f.leak...) }},
		{"non Linux no-op", func(f *evidenceFixture) { f.leak = append([]byte("not Linux — skipping\n"), f.leak...) }},
		{"duplicate leak success", func(f *evidenceFixture) { f.leak = append(f.leak, f.leak...) }},
		{"missing leak verdict", func(f *evidenceFixture) { f.leak = []byte("exit 0\n") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			tc.edit(f)
			f.metal = marshalEvents(t, f.events)
			f.report.TestMetalSHA256 = SHA256(f.metal)
			f.report.LeakcheckSHA256 = SHA256(f.leak)
			if err := VerifyLogs(f.report, f.metal, f.leak); (err == nil) != (tc.name == "valid") {
				t.Fatal(err)
			}
		})
	}
}

func TestLogDigestAndJSONIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*evidenceFixture)
	}{
		{"tampered metal", func(f *evidenceFixture) { f.metal = append(f.metal, ' ') }},
		{"tampered leak", func(f *evidenceFixture) { f.leak = append(f.leak, ' ') }},
		{"non JSON", func(f *evidenceFixture) { f.metal = []byte("ok\n"); f.report.TestMetalSHA256 = SHA256(f.metal) }},
		{"unknown event field", func(f *evidenceFixture) {
			f.metal = append([]byte(`{"Unknown":true,`), f.metal[1:]...)
			f.report.TestMetalSHA256 = SHA256(f.metal)
		}},
		{"duplicate event field", func(f *evidenceFixture) {
			f.metal = append([]byte(`{"Action":"start",`), f.metal[1:]...)
			f.report.TestMetalSHA256 = SHA256(f.metal)
		}},
		{"truncated event", func(f *evidenceFixture) {
			f.metal = f.metal[:len(f.metal)-3]
			f.report.TestMetalSHA256 = SHA256(f.metal)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			tc.edit(f)
			if err := VerifyLogs(f.report, f.metal, f.leak); err == nil {
				t.Fatal("invalid log accepted")
			}
		})
	}
}

// The real converter exercises Go's output fragmentation without running a VM.
func TestNativeObservationSurvivesRealGoJSONChunking(t *testing.T) {
	f := newEvidenceFixture(t)
	observation, err := json.Marshal(f.report.Native)
	if err != nil {
		t.Fatal(err)
	}
	trace := fmt.Sprintf("=== RUN   %s\n    native.go:1: %s%s\n--- PASS: %s (0.00s)\nPASS\n", NativeTest, ObservationMarker, observation, NativeTest)
	f.report.StartedAt = time.Now().UTC()
	cmd := exec.CommandContext(t.Context(), "go", "tool", "test2json", "-t", "-p", NativePackage)
	cmd.Stdin = strings.NewReader(trace)
	f.metal, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	f.report.CompletedAt = time.Now().UTC()
	f.report.TestMetalSHA256 = SHA256(f.metal)
	if bytes.Count(f.metal, []byte(`"Action":"output"`)) < 3 {
		t.Fatal("fixture did not exercise fragmented output")
	}
	if err := VerifyLogs(f.report, f.metal, f.leak); err != nil {
		t.Fatal("actual Go JSON rejected", err, string(f.metal))
	}
}
