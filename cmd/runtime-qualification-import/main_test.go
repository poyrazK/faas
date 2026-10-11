package main

// adr: 740

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequalification"
	"github.com/onebox-faas/faas/pkg/state"
)

// Synthetic signed logs only test CLI validation/wiring; they are not native proof.
func cliEvidence(t *testing.T) (args []string, reportPath string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	now := time.Now().UTC()
	n := runtimequalification.Observation{RunID: uuid.NewString(), ReleaseID: strings.Repeat("a", 64), HostID: uuid.NewString(), KernelBootID: uuid.NewString(), SourceCommit: strings.Repeat("b", 40), DeploymentID: uuid.NewString(), LayerKey: "apps/fixture.ext4", BaseSHA256: strings.Repeat("c", 64), GuestInitSHA256: strings.Repeat("d", 64), LayerSHA256: strings.Repeat("e", 64), KernelSHA256: strings.Repeat("f", 64), FirecrackerSHA256: strings.Repeat("1", 64), OS: "linux", Architecture: "amd64", Virtualization: "none", KVM: true, ColdBoot: true, Ready: true, Retired: true}
	observation, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var metal bytes.Buffer
	for i, event := range []struct{ action, test, output string }{{"start", "", ""}, {"run", runtimequalification.NativeTest, ""}, {"output", runtimequalification.NativeTest, runtimequalification.ObservationMarker + string(observation) + "\n"}, {"pass", runtimequalification.NativeTest, ""}, {"pass", "", ""}} {
		e := map[string]any{"Time": now.Add(-time.Minute + time.Duration(i)*time.Millisecond), "Action": event.action, "Package": runtimequalification.NativePackage, "Test": event.test, "Output": event.output}
		if err := json.NewEncoder(&metal).Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	leak := []byte(runtimequalification.LeakcheckSuccess + "\n")
	report := runtimequalification.Report{Version: 1, Profile: state.RuntimeQualificationProfile, StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Second), Native: n, TestMetalSHA256: runtimequalification.SHA256(metal.Bytes()), LeakcheckSHA256: runtimequalification.SHA256(leak), TestMetalExitCode: &zero, LeakcheckExitCode: &zero}
	raw, err := runtimequalification.EncodeEnvelope(report, metal.Bytes(), leak, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	reportPath = filepath.Join(dir, "report.json")
	metalPath, leakPath := filepath.Join(dir, "metal.jsonl"), filepath.Join(dir, "leak.log")
	for path, body := range map[string][]byte{reportPath: raw, metalPath: metal.Bytes(), leakPath: leak} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return []string{"-report", reportPath, "-test-metal", metalPath, "-leakcheck", leakPath, "-public-key", hex.EncodeToString(pub), "-release", n.ReleaseID, "-host", n.HostID, "-source-commit", n.SourceCommit, "-run", n.RunID}, reportPath
}

func TestCLIRejectsUntrustedEvidenceBeforeOpeningResources(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, []string, string) []string
	}{
		{"missing flags", func(_ *testing.T, _ []string, _ string) []string { return nil }},
		{"invalid public key", func(_ *testing.T, args []string, _ string) []string { args[7] = "secret-not-a-public-key"; return args }},
		{"wrong target", func(_ *testing.T, args []string, _ string) []string { args[9] = strings.Repeat("2", 64); return args }},
		{"wrong host", func(_ *testing.T, args []string, _ string) []string { args[11] = uuid.NewString(); return args }},
		{"wrong source", func(_ *testing.T, args []string, _ string) []string { args[13] = strings.Repeat("2", 40); return args }},
		{"wrong run", func(_ *testing.T, args []string, _ string) []string { args[15] = uuid.NewString(); return args }},
		{"unsigned report", func(t *testing.T, args []string, path string) []string {
			if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			return args
		}},
		{"tampered log", func(t *testing.T, args []string, _ string) []string {
			if err := os.WriteFile(args[3], []byte("ok\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return args
		}},
		{"positional argument", func(_ *testing.T, args []string, _ string) []string { return append(args, "unexpected") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, path := cliEvidence(t)
			args = tc.edit(t, args, path)
			opened := false
			err := run(t.Context(), args, func(context.Context) (resources, error) { opened = true; return resources{}, nil }, &bytes.Buffer{})
			if err == nil || opened {
				t.Fatal("invalid input reached resources", err, opened)
			}
		})
	}
}

func TestCLIValidatedResourceLifecycleAndHelp(t *testing.T) {
	args, _ := cliEvidence(t)
	outage := errors.New("operator resources unavailable")
	opened := false
	err := run(t.Context(), args, func(context.Context) (resources, error) { opened = true; return resources{}, outage }, &bytes.Buffer{})
	if !opened || !errors.Is(err, outage) {
		t.Fatal("validated evidence did not reach operator factory", err)
	}
	closed := false
	err = run(t.Context(), args, func(context.Context) (resources, error) { return resources{close: func() { closed = true }}, nil }, &bytes.Buffer{})
	if err == nil || !closed {
		t.Fatal("import failure did not close resources", err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-help"}, nil, &out); !errors.Is(err, flag.ErrHelp) || !strings.Contains(out.String(), "public-key") {
		t.Fatal("operator help unavailable", err, out.String())
	}
}
