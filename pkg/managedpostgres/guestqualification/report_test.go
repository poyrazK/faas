package guestqualification

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureReport() Report {
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	report := Report{Version: Version, SuiteVersion: Version, RootTest: SuiteContract().Root, HostClass: "native", HostOS: "linux", HostArch: "amd64", SourceClean: true, PostgresMajor: 17, StartedAt: start, CompletedAt: start.Add(time.Minute), ExpiresAt: start.Add(time.Hour), LeakCheckBefore: true, LeakCheckAfter: true, ResultsSHA256: strings.Repeat("a", 64), Runtime: RuntimeManifest{Version: Version, SourceRevision: strings.Repeat("b", 40), Artifacts: make(map[string]string)}}
	for _, name := range SuiteContract().Artifacts {
		report.Runtime.Artifacts[name] = strings.Repeat("c", 64)
	}
	report.Runtime.Artifacts["probe_app_layer"] = strings.Repeat("d", 64)
	report.Runtime.Artifacts["probe_source_archive"] = strings.Repeat("e", 64)
	report.Runtime.Artifacts["python312_app_layer"] = strings.Repeat("0", 64)
	report.Runtime.Artifacts["python312_source_archive"] = strings.Repeat("1", 64)
	for _, name := range SuiteContract().Phases {
		report.Checks = append(report.Checks, PhaseCheck{Name: name, Passed: true})
	}
	report.EvidenceSHA256 = EvidenceDigest(report)
	return report
}

func TestGuestReadinessRejectsIncompleteAndMismatchedEvidence(t *testing.T) {
	report := fixtureReport()
	now := report.CompletedAt.Add(time.Minute)
	if got := Evaluate(&report, &report.Runtime, 17, now); !got.Ready {
		t.Fatalf("valid evidence: %+v", got)
	}
	for _, tc := range []struct {
		name, reason string
		mutate       func(*Report)
	}{
		{"nested", "guest_host_not_native", func(r *Report) { r.HostClass = "nested-diagnostic" }},
		{"wrong architecture", "guest_host_not_native", func(r *Report) { r.HostArch = "arm64" }},
		{"dirty", "guest_source_dirty", func(r *Report) { r.SourceClean = false }},
		{"expired", "guest_report_expired", func(r *Report) { r.ExpiresAt = now }},
		{"future", "guest_window_invalid", func(r *Report) { r.CompletedAt = now.Add(time.Hour) }},
		{"TTL", "guest_window_invalid", func(r *Report) { r.ExpiresAt = r.CompletedAt.Add(MaxTTL + time.Second) }},
		{"major", "guest_postgres_major_mismatch", func(r *Report) { r.PostgresMajor = 16 }},
		{"revision", "guest_runtime_mismatch", func(r *Report) { r.Runtime.SourceRevision = strings.Repeat("d", 40) }},
		{"app layer replaced", "guest_runtime_mismatch", func(r *Report) { r.Runtime.Artifacts["node22_app_layer"] = strings.Repeat("f", 64) }},
		{"missing app layer", "guest_runtime_invalid", func(r *Report) { delete(r.Runtime.Artifacts, "node22_app_layer") }},
		{"source replaced", "guest_runtime_mismatch", func(r *Report) { r.Runtime.Artifacts["node22_source_archive"] = strings.Repeat("f", 64) }},
		{"missing source", "guest_runtime_invalid", func(r *Report) { delete(r.Runtime.Artifacts, "probe_source_archive") }},
		{"reused app layer", "guest_runtime_invalid", func(r *Report) { r.Runtime.Artifacts["node22_app_layer"] = r.Runtime.Artifacts["probe_app_layer"] }},
		{"reused source", "guest_runtime_invalid", func(r *Report) {
			r.Runtime.Artifacts["node22_source_archive"] = r.Runtime.Artifacts["probe_source_archive"]
		}},
		{"v2 report", "guest_contract_invalid", func(r *Report) { r.Version = 2 }},
		{"v2 suite", "guest_contract_invalid", func(r *Report) { r.SuiteVersion = 2 }},
		{"v2 manifest", "guest_runtime_invalid", func(r *Report) { r.Runtime.Version = 2 }},
		{"missing Python layer", "guest_runtime_invalid", func(r *Report) { delete(r.Runtime.Artifacts, "python312_app_layer") }},
		{"missing Python source", "guest_runtime_invalid", func(r *Report) { delete(r.Runtime.Artifacts, "python312_source_archive") }},
		{"reused Python layer", "guest_runtime_invalid", func(r *Report) { r.Runtime.Artifacts["python312_app_layer"] = r.Runtime.Artifacts["node22_app_layer"] }},
		{"reused Python source", "guest_runtime_invalid", func(r *Report) {
			r.Runtime.Artifacts["python312_source_archive"] = r.Runtime.Artifacts["probe_source_archive"]
		}},
		{"old report", "guest_contract_invalid", func(r *Report) { r.Version = 1 }},
		{"old manifest", "guest_runtime_invalid", func(r *Report) { r.Runtime.Version = 1 }},
		{"extra artifact", "guest_runtime_invalid", func(r *Report) { r.Runtime.Artifacts["unqualified"] = strings.Repeat("d", 64) }},
		{"old suite", "guest_contract_invalid", func(r *Report) { r.SuiteVersion = 1 }},
		{"missing phase", "guest_phases_incomplete", func(r *Report) { r.Checks = r.Checks[:len(r.Checks)-1] }},
		{"failed phase", "guest_phases_incomplete", func(r *Report) { r.Checks[0].Passed = false }},
		{"duplicate phase", "guest_phases_incomplete", func(r *Report) { r.Checks[0] = r.Checks[1] }},
		{"reordered phases", "guest_phases_incomplete", func(r *Report) { r.Checks[0], r.Checks[1] = r.Checks[1], r.Checks[0] }},
		{"before leaks", "guest_host_cleanup_unverified", func(r *Report) { r.LeakCheckBefore = false }},
		{"after leaks", "guest_host_cleanup_unverified", func(r *Report) { r.LeakCheckAfter = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal(report)
			var candidate Report
			if err := json.Unmarshal(data, &candidate); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&candidate)
			candidate.EvidenceSHA256 = EvidenceDigest(candidate)
			got := Evaluate(&candidate, &report.Runtime, 17, now)
			if got.Ready || !containsReason(got.Reasons, tc.reason) {
				t.Fatalf("readiness: %+v", got)
			}
		})
	}
	report.Checks[0].Passed = false
	if got := Evaluate(&report, &report.Runtime, 17, now); !containsReason(got.Reasons, "guest_evidence_digest_mismatch") {
		t.Fatalf("tampering accepted: %+v", got)
	}
	if got := Evaluate(nil, nil, 17, now); got.Ready || !containsReason(got.Reasons, "guest_report_missing") || !containsReason(got.Reasons, "runtime_manifest_missing") {
		t.Fatalf("missing evidence: %+v", got)
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func TestPythonGuestReportIsVerifiedByGo(t *testing.T) {
	report := fixtureReport()
	contract := SuiteContract()
	var stream bytes.Buffer
	encoder := json.NewEncoder(&stream)
	second := 0
	emit := func(test, action, output string) {
		second++
		event := map[string]any{"Package": contract.Package, "Time": report.StartedAt.Add(time.Duration(second) * time.Second), "Action": action}
		if test != "" {
			event["Test"] = test
		}
		if output != "" {
			event["Output"] = output
		}
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	emit("", "start", "")
	emit(contract.Root, "run", "")
	for _, phase := range contract.Phases {
		emit(contract.Root+"/"+phase, "run", "")
		emit(contract.Root+"/"+phase, "pass", "")
	}
	emit(contract.Root, "output", "private-connection-password-must-not-enter-report")
	identity, _ := json.Marshal(Identity{Runtime: report.Runtime, PostgresMajor: report.PostgresMajor})
	line := "    native_test.go:1: " + IdentityPrefix + string(identity) + "\n"
	for len(line) > 1024 {
		emit(contract.Root, "output", line[:1024])
		line = line[1024:]
	}
	emit(contract.Root, "output", line)
	emit(contract.Root, "pass", "")
	emit("", "pass", "")
	dir := t.TempDir()
	input, output := filepath.Join(dir, "events.jsonl"), filepath.Join(dir, "report.json")
	if err := os.WriteFile(input, stream.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join("..", "..", "..", "scripts", "ci", "managed-postgres-native-verdict.py")
	args := []string{script, input, "--report", output, "--host-class", "native", "--source-clean", "--leakcheck-before", "--leakcheck-after"}
	if data, err := exec.Command("python3", args...).CombinedOutput(); err != nil {
		t.Fatalf("report producer: %v %s", err, data)
	}
	produced, err := Load[Report](output)
	if err != nil {
		t.Fatal(err)
	}
	if got := Evaluate(&produced, &report.Runtime, report.PostgresMajor, produced.CompletedAt.Add(time.Minute)); !got.Ready {
		t.Fatalf("cross-language report: %+v", got)
	}
	data, _ := os.ReadFile(output)
	if bytes.Contains(data, []byte("private-connection-password")) {
		t.Fatal("raw output entered report")
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("report not private")
	}
	if _, err := exec.Command("python3", args...).CombinedOutput(); err == nil {
		t.Fatal("producer replaced existing evidence")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(data, after) {
		t.Fatal("existing evidence changed")
	}
}

func TestGuestDocumentLoaderRejectsUnknownTrailingAndOversizedData(t *testing.T) {
	for _, data := range []string{`{"provider_password":"secret"}`, `{} {}`, strings.Repeat(" ", (1<<20)+1)} {
		p := filepath.Join(t.TempDir(), "artifact.json")
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load[Report](p); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
}
