package runrelease

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fixture struct {
	in       Inputs
	evidence map[string]any
	root     string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{root: root}
	write := func(name string, body []byte, mode os.FileMode) File {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
		return File{path, SHA256(body)}
	}
	candidate := write("candidate", []byte("candidate bytes"), 0o700)
	rollback := write("rollback", []byte("rollback bytes"), 0o700)
	reader := func(name string, binary File) File {
		body, err := json.Marshal(Capabilities(binary.SHA256, "linux", "amd64"))
		if err != nil {
			t.Fatal(err)
		}
		return write(name, body, 0o600)
	}
	plan := Plan{Schema: PlanSchema, SourceCommit: strings.Repeat("a", 40), Candidate: Binary{candidate, reader("candidate-reader.json", candidate)}, Rollback: Binary{rollback, reader("rollback-reader.json", rollback)}, CandidateConfig: write("candidate.toml", []byte("candidate config"), 0o600), RollbackConfig: write("rollback.toml", []byte("rollback config"), 0o600), Artifacts: map[string]File{}}
	for _, name := range artifactNames() {
		plan.Artifacts[name] = write(name, []byte(name+" release"), 0o700)
	}
	pins := map[string]any{"vmmd": candidate.SHA256}
	for k, p := range plan.Artifacts {
		pins[k] = p.SHA256
	}
	tests := []any{}
	for _, s := range RequiredScenarios() {
		tests = append(tests, s)
	}
	f.evidence = map[string]any{
		"schema": "gregale.run-native-recovery-summary/v1", "functional_pass": true, "bare_metal_host": true, "p0_3_complete": true, "runtime_release_eligibility": true, "source_recovery_profile": "native_process_recovery_owner_v3",
		"source": map[string]any{"snapshot_commit": plan.SourceCommit}, "host": map[string]any{"architecture": "x86_64", "virtualization": "none", "kvm": true}, "release_artifact_sha256": pins,
		"checks": map[string]any{"template-clones": map[string]any{"passed_tests": tests}}, "process_exits": map[string]any{"native_recovery": 0, "leakcheck_before": 0, "leakcheck_after": 0},
		"execution_ledger_assertions":     map[string]any{"total_customer_runs": 27, "native_recovery_customer_runs": 11, "template_cohort_customer_runs": 9, "warm_oom_customer_runs": 7, "usage_entries_per_customer_run": 1, "retained_payload_rows": 0, "template_account_runs": 0},
		"native_kvm_only_device_receipts": 27, "post_capture_crash_leakchecks": 5, "pre_teardown_leakchecks": 3,
	}
	body, err := json.Marshal(f.evidence)
	if err != nil {
		t.Fatal(err)
	}
	plan.Acceptance = write("acceptance.json", body, 0o600)
	makeDir := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg := Config{NativeRecovery: true, LaunchOwners: true, RuntimeCapture: true, PublicationRoot: makeDir("publications"), ImageRoot: makeDir("images"), SnapshotRoot: makeDir("snapshots"), ResourceRoot: makeDir("resources"), VolatileRoot: makeDir("volatile")}
	rb := cfg
	rb.RuntimeCapture = false
	f.in = Inputs{Plan: plan, PlanSHA256: strings.Repeat("b", 64), Candidate: cfg, Rollback: rb, Host: Host{"linux", "amd64", "none", true, true}, ExecutingBinarySHA256: candidate.SHA256, NativeJournalRoot: makeDir("native-journal")}
	return f
}

func (f *fixture) saveEvidence(t *testing.T) {
	t.Helper()
	body, err := json.Marshal(f.evidence)
	if err != nil {
		t.Fatal(err)
	}
	f.replace(t, &f.in.Plan.Acceptance, body)
}
func (f *fixture) replace(t *testing.T, pin *File, body []byte) {
	t.Helper()
	if err := os.WriteFile(pin.Path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	pin.SHA256 = SHA256(body)
}
func failed(r Result, name string) bool {
	for _, c := range r.Checks {
		if c.Name == name {
			return !c.Passed
		}
	}
	return false
}

func TestReleasePreflightAndRollbackAreReadOnly(t *testing.T) {
	f := newFixture(t)
	// Audit records remain even when a guest was physically retired. Neither
	// action may delete them to manufacture compatibility with an older reader.
	path := filepath.Join(f.in.NativeJournalRoot, "retained-incomplete.json")
	if err := os.WriteFile(path, []byte("retained audit bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"preflight", "rollback"} {
		r := Evaluate(t.Context(), action, f.in)
		if !r.Passed || r.RuntimeAdmissionGranted || r.Retained["native_journal"] != 1 || len(r.Steps) == 0 {
			t.Fatalf("%s: %+v", action, r)
		}
	}
	after, err := os.ReadDir(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("check created files")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "retained audit bytes" {
		t.Fatal("audit record changed", err)
	}
}

func TestReleaseRejectsUnsafeReadersConfigurationsAndPins(t *testing.T) {
	cases := []struct {
		name, check string
		change      func(*testing.T, *fixture)
	}{
		{"legacy rollback", "rollback_reader", func(t *testing.T, f *fixture) {
			r := Capabilities(f.in.Plan.Rollback.File.SHA256, "linux", "amd64")
			r.TemplateFlight = 0
			b, _ := json.Marshal(r)
			f.replace(t, &f.in.Plan.Rollback.Reader, b)
		}},
		{"wrong binary reader", "candidate_reader", func(t *testing.T, f *fixture) {
			r := Capabilities(strings.Repeat("f", 64), "linux", "amd64")
			b, _ := json.Marshal(r)
			f.replace(t, &f.in.Plan.Candidate.Reader, b)
		}},
		{"future unknown reader", "rollback_reader", func(t *testing.T, f *fixture) {
			r := Capabilities(f.in.Plan.Rollback.File.SHA256, "linux", "amd64")
			r.RuntimeCaptureAuthority = 3
			b, _ := json.Marshal(r)
			f.replace(t, &f.in.Plan.Rollback.Reader, b)
		}},
		{"wrong executing binary", "executing_reader", func(_ *testing.T, f *fixture) { f.in.ExecutingBinarySHA256 = strings.Repeat("c", 64) }},
		{"changed artifact", "artifact_base", func(t *testing.T, f *fixture) {
			if err := os.WriteFile(f.in.Plan.Artifacts["base"].Path, []byte("different"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"changed config", "candidate_config", func(t *testing.T, f *fixture) {
			if err := os.WriteFile(f.in.Plan.CandidateConfig.Path, []byte("different"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"rollback recovery disabled", "rollback_configuration", func(_ *testing.T, f *fixture) { f.in.Rollback.NativeRecovery = false }},
		{"rollback writer enabled", "rollback_configuration", func(_ *testing.T, f *fixture) { f.in.Rollback.RuntimeCapture = true }},
		{"rollback root removed", "retained_roots", func(_ *testing.T, f *fixture) { f.in.Rollback.SnapshotRoot = "" }},
		{"volatile write mismatch", "retained_roots", func(_ *testing.T, f *fixture) { f.in.Rollback.VolatileWrites = true }},
		{"adoption mixed", "candidate_configuration", func(_ *testing.T, f *fixture) { f.in.Candidate.AdoptionRecord = true }},
		{"prepared network cache", "candidate_configuration", func(_ *testing.T, f *fixture) { f.in.Candidate.PreparedNetworks = 1 }},
		{"overlapping roots", "candidate_configuration", func(_ *testing.T, f *fixture) { f.in.Candidate.ImageRoot = f.in.Candidate.SnapshotRoot }},
		{"no KVM", "host", func(_ *testing.T, f *fixture) { f.in.Host.KVM = false }},
		{"old cgroups", "host", func(_ *testing.T, f *fixture) { f.in.Host.CgroupV2 = false }},
		{"missing inventory", "inventory_publications", func(t *testing.T, f *fixture) {
			if err := os.Remove(f.in.Candidate.PublicationRoot); err != nil {
				t.Fatal(err)
			}
		}},
		{"inventory symlink", "inventory_native_journal", func(t *testing.T, f *fixture) {
			if err := os.Symlink(f.in.Plan.Acceptance.Path, filepath.Join(f.in.NativeJournalRoot, "link")); err != nil {
				t.Fatal(err)
			}
		}},
		{"unknown artifact", "plan", func(_ *testing.T, f *fixture) { f.in.Plan.Artifacts["extra"] = f.in.Plan.Artifacts["base"] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.change(t, f)
			for _, action := range []string{"preflight", "rollback"} {
				r := Evaluate(t.Context(), action, f.in)
				if r.Passed || !failed(r, tc.check) {
					t.Fatalf("%s did not reject %s: %+v", action, tc.check, r)
				}
			}
		})
	}
}

func TestReleaseNativeAcceptanceCannotBorrowFunctionalEvidence(t *testing.T) {
	for _, field := range []string{"functional_pass", "bare_metal_host", "p0_3_complete", "runtime_release_eligibility"} {
		t.Run(field, func(t *testing.T) {
			f := newFixture(t)
			f.evidence[field] = false
			f.saveEvidence(t)
			if !failed(Evaluate(t.Context(), "preflight", f.in), "native_acceptance") {
				t.Fatal("functional evidence enabled release")
			}
		})
	}
	for _, test := range RequiredScenarios() {
		t.Run(test, func(t *testing.T) {
			f := newFixture(t)
			checks := f.evidence["checks"].(map[string]any)
			cohort := checks["template-clones"].(map[string]any)
			tests := cohort["passed_tests"].([]any)
			kept := []any{}
			for _, s := range tests {
				if s != test {
					kept = append(kept, s)
				}
			}
			cohort["passed_tests"] = kept
			f.saveEvidence(t)
			if !failed(Evaluate(t.Context(), "preflight", f.in), "native_acceptance") {
				t.Fatal("missing scenario accepted")
			}
		})
	}
	for _, field := range []string{"vmmd", "kernel", "base", "guest_executor", "firecracker", "jailer", "jail_helper"} {
		t.Run("pin/"+field, func(t *testing.T) {
			f := newFixture(t)
			f.evidence["release_artifact_sha256"].(map[string]any)[field] = strings.Repeat("0", 64)
			f.saveEvidence(t)
			if !failed(Evaluate(t.Context(), "preflight", f.in), "native_acceptance") {
				t.Fatal("foreign release artifact accepted")
			}
		})
	}
}

func TestReleaseRollbackRemainsAvailableWithoutEnablingProduction(t *testing.T) {
	f := newFixture(t)
	f.in.Host.Virtualization = "google"
	f.evidence["bare_metal_host"] = false
	f.evidence["p0_3_complete"] = false
	f.evidence["runtime_release_eligibility"] = false
	f.saveEvidence(t)
	preflight := Evaluate(t.Context(), "preflight", f.in)
	if preflight.Passed || !failed(preflight, "host") || !failed(preflight, "native_acceptance") {
		t.Fatal("functional host enabled production")
	}
	rollback := Evaluate(t.Context(), "rollback", f.in)
	if !rollback.Passed || rollback.ProductionEnablementChecked || rollback.RuntimeAdmissionGranted {
		t.Fatalf("compatible writer-disabled rollback unavailable: %+v", rollback)
	}
}

func TestReleaseStrictJSONAndBoundedControlInputs(t *testing.T) {
	for _, body := range []string{`{"schema":"a","schema":"b"}`, `{"schema":"a","SCHEMA":"b"}`, `{"schema":"a","unknown":true}`, `{} {}`, `[]`, "{\"schema\":\"\xff\"}", strings.Repeat("[", api.RunReleaseJSONMaxDepth+1) + "0" + strings.Repeat("]", api.RunReleaseJSONMaxDepth+1)} {
		var p Plan
		if err := Decode([]byte(body), &p); err == nil {
			t.Fatalf("accepted invalid JSON %q", body)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f := newFixture(t)
	if Evaluate(ctx, "preflight", f.in).Passed {
		t.Fatal("canceled check passed")
	}
	big := []byte(strings.Repeat("x", api.RunReleaseManifestMaxBytes+1))
	f.replace(t, &f.in.Plan.Acceptance, big)
	if !failed(Evaluate(t.Context(), "preflight", f.in), "native_acceptance") {
		t.Fatal("oversized evidence accepted")
	}
	// Rootfs and kernel artifacts are streamed, not limited like JSON controls.
	base := f.in.Plan.Artifacts["base"]
	f.replace(t, &base, big)
	f.in.Plan.Artifacts["base"] = base
	if failed(Evaluate(t.Context(), "preflight", f.in), "artifact_base") {
		t.Fatal("large rootfs was treated as a manifest")
	}
}

func TestReleaseRefusesSymlinkAndWritableInput(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(f.root, "alias")
	if err := os.Symlink(f.in.Plan.Acceptance.Path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); err == nil {
		t.Fatal("symlink input accepted")
	}
	if err := os.Chmod(f.in.Plan.Acceptance.Path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(f.in.Plan.Acceptance.Path); err == nil {
		t.Fatal("writable input accepted")
	}
}

func TestReleaseAbsentJournalDoesNotPermitLegacyRollback(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(f.in.NativeJournalRoot); err != nil {
		t.Fatal(err)
	}
	if !Evaluate(t.Context(), "preflight", f.in).Passed {
		t.Fatal("fresh native journal must not be created by preflight")
	}
	if _, err := os.Lstat(f.in.NativeJournalRoot); !os.IsNotExist(err) {
		t.Fatal("preflight created native journal", err)
	}
	r := Capabilities(f.in.Plan.Rollback.File.SHA256, "linux", "amd64")
	r.RuntimeCaptureAuthority = 1
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	f.replace(t, &f.in.Plan.Rollback.Reader, body)
	if !failed(Evaluate(t.Context(), "rollback", f.in), "rollback_reader") {
		t.Fatal("empty journal granted downgrade")
	}
}
