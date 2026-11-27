// Package runrelease provides read-only local Run release and rollback checks.
// It grants no runtime admission, journal repair or artifact deletion authority.
package runrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	PlanSchema   = "gregale.run-release-plan/v1"
	ReaderSchema = "gregale.run-release-reader/v1"
	ResultSchema = "gregale.run-release-check/v1"
)

// Reader is emitted by the corresponding vmmd binary, then pinned in the
// operator's reviewed plan. A digest binds bytes, not publisher authenticity:
// the normal signed release installation remains the trust boundary.
type Reader struct {
	Schema                  string `json:"schema"`
	BinarySHA256            string `json:"binary_sha256"`
	OS                      string `json:"os"`
	Architecture            string `json:"architecture"`
	NativeLaunchOwner       int    `json:"native_launch_owner"`
	TemplateFlight          int    `json:"template_flight"`
	RuntimeCaptureAuthority int    `json:"runtime_capture_authority"`
	KVMOnlyDeviceScope      int    `json:"kvm_only_device_scope"`
	PublicationIntent       int    `json:"publication_intent"`
}

func Capabilities(binarySHA, goos, arch string) Reader {
	return Reader{ReaderSchema, binarySHA, goos, arch, 3, 1, 2, 2, 2}
}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Binary struct {
	File   File `json:"file"`
	Reader File `json:"reader"`
}

// Plan paths are absolute. Config pins cover the candidate and the proposed
// rollback configuration, never a mutable unrecorded "previous" file.
type Plan struct {
	Schema          string          `json:"schema"`
	SourceCommit    string          `json:"source_commit"`
	Candidate       Binary          `json:"candidate"`
	Rollback        Binary          `json:"rollback"`
	CandidateConfig File            `json:"candidate_config"`
	RollbackConfig  File            `json:"rollback_config"`
	Artifacts       map[string]File `json:"artifacts"`
	Acceptance      File            `json:"acceptance"`
}

// Config contains only release-relevant effective vmmd values. cmd/vmmd uses
// its real LoadConfig, including environment overrides, to construct it.
type Config struct {
	NativeRecovery       bool   `json:"native_process_recovery"`
	LaunchOwners         bool   `json:"native_launch_owner_records"`
	RuntimeCapture       bool   `json:"native_runtime_capture"`
	QualificationCapture bool   `json:"native_qualification_capture"`
	AdoptionRecord       bool   `json:"adoption_record"`
	AdoptRunning         bool   `json:"adopt_running_vms"`
	PreparedNetworks     int    `json:"prepared_networks"`
	PublicationRoot      string `json:"native_snapshot_publication_root"`
	ImageRoot            string `json:"native_image_staging_root"`
	SnapshotRoot         string `json:"native_runtime_snapshot_root"`
	ResourceRoot         string `json:"resource_journal_dir"`
	VolatileRoot         string `json:"resource_journal_volatile_dir"`
	VolatileWrites       bool   `json:"resource_journal_volatile_writes"`
}

type Host struct {
	OS             string `json:"os"`
	Architecture   string `json:"architecture"`
	Virtualization string `json:"virtualization"`
	KVM            bool   `json:"kvm"`
	CgroupV2       bool   `json:"cgroup_v2"`
}

type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type Result struct {
	Schema string `json:"schema"`
	Action string `json:"action"`
	Passed bool   `json:"passed"`
	// A passing preflight is not a runtime qualification or an enablement token.
	RuntimeAdmissionGranted     bool           `json:"runtime_admission_granted"`
	ProductionEnablementChecked bool           `json:"production_enablement_checked"`
	PlanSHA256                  string         `json:"plan_sha256"`
	Host                        Host           `json:"host"`
	Checks                      []Check        `json:"checks"`
	Retained                    map[string]int `json:"retained_entries"`
	Steps                       []string       `json:"operator_steps"`
}

type Inputs struct {
	Plan                  Plan
	PlanSHA256            string
	Candidate             Config
	Rollback              Config
	Host                  Host
	ExecutingBinarySHA256 string
	NativeJournalRoot     string
}

func (r *Result) add(name string, err error, success string) {
	c := Check{Name: name, Passed: err == nil, Detail: success}
	if err != nil {
		c.Detail = err.Error()
		r.Passed = false
	}
	r.Checks = append(r.Checks, c)
}

func require(ok bool, message string) error {
	if !ok {
		return errors.New(message)
	}
	return nil
}

// Evaluate never opens a daemon connection, takes an owner lock, runs a binary,
// creates a directory or changes state. Run it for each node behind its existing
// rollout/drain barrier; the result is a point-in-time check, not a fleet lease.
func Evaluate(ctx context.Context, action string, in Inputs) Result {
	r := Result{Schema: ResultSchema, Action: action, Passed: true, PlanSHA256: in.PlanSHA256, Host: in.Host, Checks: []Check{}, Retained: map[string]int{}}
	if action != "preflight" && action != "rollback" {
		r.add("action", errors.New("expected preflight or rollback"), "")
		return r
	}
	r.add("plan", in.Plan.Validate(), "reviewed plan has complete source, binary, configuration and artifact pins")
	if !r.Passed {
		return r
	}
	for _, item := range []struct {
		name   string
		binary Binary
	}{{"candidate", in.Plan.Candidate}, {"rollback", in.Plan.Rollback}} {
		r.add(item.name+"_reader", verifyBinary(ctx, item.binary), "pinned Linux amd64 reader supports Run native template formats")
	}
	// Both commands run in the candidate reader. Rollback never executes an old
	// binary just to ask it a question: an old unrecognised verb can start a daemon.
	r.add("executing_reader", require(in.ExecutingBinarySHA256 == in.Plan.Candidate.File.SHA256, "run this check with the plan's pinned candidate vmmd"), "executing binary matches candidate")
	for _, item := range []struct {
		name string
		file File
	}{{"candidate_config", in.Plan.CandidateConfig}, {"rollback_config", in.Plan.RollbackConfig}} {
		_, err := verifyFile(ctx, item.file, false)
		r.add(item.name, err, "configuration bytes match reviewed plan")
	}
	r.add("candidate_configuration", validateConfig(in.Candidate, true), "native capture and original launch ownership are enabled without adoption")
	r.add("rollback_configuration", validateConfig(in.Rollback, false), "rollback stops capture writers and retains native ownership readers")
	r.add("retained_roots", require(sameRoots(in.Candidate, in.Rollback), "rollback must retain all native and resource journal roots and volatile-write settings"), "rollback retains all journal and snapshot roots")
	r.ProductionEnablementChecked = action == "preflight"
	if action == "preflight" {
		r.add("host", require(in.Host.OS == "linux" && in.Host.Architecture == "amd64" && in.Host.Virtualization == "none" && in.Host.KVM && in.Host.CgroupV2, "requires nonvirtualized Linux amd64, accessible KVM and cgroups v2"), "supported native KVM host")
	} else {
		r.add("host", require(in.Host.OS == "linux" && in.Host.Architecture == "amd64" && in.Host.KVM && in.Host.CgroupV2, "rollback requires Linux amd64, accessible KVM and cgroups v2"), "local KVM host; this writer-disabled rollback does not enable production")
	}
	for _, name := range artifactNames() {
		err := verifyArtifact(ctx, in.Plan.Artifacts[name], name == "firecracker" || name == "jailer" || name == "jail_helper")
		r.add("artifact_"+name, err, "artifact bytes match reviewed release pin")
	}
	if action == "preflight" {
		r.add("native_acceptance", verifyAcceptance(ctx, in.Plan), "native lifecycle evidence matches source, artifacts and complete Run coverage")
	} else {
		_, err := verifyFile(ctx, in.Plan.Acceptance, false)
		r.add("retained_acceptance", err, "retained evidence matches plan; rollback does not promote it to production qualification")
	}
	roots := map[string]string{"native_journal": in.NativeJournalRoot, "publications": in.Candidate.PublicationRoot, "images": in.Candidate.ImageRoot, "snapshots": in.Candidate.SnapshotRoot, "resource_journal": in.Candidate.ResourceRoot}
	if in.Candidate.VolatileRoot != "" {
		roots["volatile_resource_journal"] = in.Candidate.VolatileRoot
	}
	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		// Before first native enablement the launch journal may not exist.
		// Absence grants no legacy-reader downgrade permission.
		count, err := inventory(ctx, roots[name], name == "native_journal")
		r.Retained[name] = count
		r.add("inventory_"+name, err, "read-only entry inventory; no journal schema or physical-retirement claim")
	}
	if action == "rollback" {
		r.Steps = []string{"Disable schedd FAAS_EXECUTION_TEMPLATE_CAPTURE; preserve its existing platform account and catalog.", "Use the existing node drain and operation-completion barrier; let the current compatible vmmd retire work and capture flights.", "Re-run this rollback check under the rollout lock immediately before replacement; abort if it fails.", "Install only the pinned compatible rollback binary and configuration using the signed release installer; retain native flags, roots and audit records.", "Run the existing base-adoption, restart/reconnect, leak and usage checks before undraining; use cold fallback when a template is incompatible."}
	} else {
		r.Steps = []string{"Install compatible readers on every compute node and retain a compatible rollback artifact before enabling writers.", "Run this preflight on each target with its effective deployment environment and approved signed release artifacts.", "Keep existing image admission and native runtime qualification gates; a preflight result grants neither.", "Enable the schedd template producer only after the existing deployment and acceptance gates pass; retain the reviewed plan and JSON result."}
	}
	return r
}

func artifactNames() []string {
	return []string{"kernel", "base", "guest_executor", "firecracker", "jailer", "jail_helper"}
}

func (p Plan) Validate() error {
	if p.Schema != PlanSchema || !validHex(p.SourceCommit, 40) && !validHex(p.SourceCommit, 64) {
		return errors.New("invalid plan schema or source commit")
	}
	if len(p.Artifacts) != len(artifactNames()) {
		return errors.New("exactly kernel, base, guest_executor, firecracker, jailer and jail_helper artifact pins required")
	}
	files := []File{p.Candidate.File, p.Candidate.Reader, p.Rollback.File, p.Rollback.Reader, p.CandidateConfig, p.RollbackConfig, p.Acceptance}
	for _, name := range artifactNames() {
		files = append(files, p.Artifacts[name])
	}
	for _, file := range files {
		if !cleanPath(file.Path) || !validHex(file.SHA256, 64) {
			return errors.New("all inputs require canonical absolute paths and lowercase SHA-256 pins")
		}
	}
	return nil
}

func cleanPath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != string(filepath.Separator)
}
func validHex(value string, size int) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(value) == size && hex.EncodeToString(b) == value
}

func validateConfig(c Config, capture bool) error {
	if !c.NativeRecovery || !c.LaunchOwners || c.RuntimeCapture != capture || c.QualificationCapture || c.AdoptionRecord || c.AdoptRunning || c.PreparedNetworks != 0 {
		return fmt.Errorf("requires native recovery, launch owners, runtime_capture=%t, no qualification/adoption and prepared_networks=0", capture)
	}
	roots := []string{c.PublicationRoot, c.ImageRoot, c.SnapshotRoot, c.ResourceRoot}
	if c.VolatileRoot != "" {
		roots = append(roots, c.VolatileRoot)
	}
	for i, p := range roots {
		if !cleanPath(p) {
			return errors.New("native and resource roots must be explicit canonical absolute paths")
		}
		for _, other := range roots[:i] {
			if p == other || strings.HasPrefix(p, other+string(filepath.Separator)) || strings.HasPrefix(other, p+string(filepath.Separator)) {
				return errors.New("native and resource roots must not overlap")
			}
		}
	}
	return nil
}

func sameRoots(a, b Config) bool {
	return a.PublicationRoot == b.PublicationRoot && a.ImageRoot == b.ImageRoot && a.SnapshotRoot == b.SnapshotRoot && a.ResourceRoot == b.ResourceRoot && a.VolatileRoot == b.VolatileRoot && a.VolatileWrites == b.VolatileWrites
}

func verifyBinary(ctx context.Context, b Binary) error {
	if _, err := verifyFile(ctx, b.File, true); err != nil {
		return err
	}
	body, err := verifyFile(ctx, b.Reader, false)
	if err != nil {
		return err
	}
	var r Reader
	if err := Decode(body, &r); err != nil {
		return errors.New("invalid reader capability manifest")
	}
	want := Capabilities(b.File.SHA256, "linux", "amd64")
	if r != want {
		return errors.New("reader is missing required native formats or does not name its pinned Linux amd64 binary; retain a compatible rollback release")
	}
	return nil
}

// Payload-free summaries are reviewed and hash-pinned independently by the
// operator. They are not self-authenticating image provenance. Actual release
// signatures and ADR-739 runtime admission remain mandatory elsewhere.
func verifyAcceptance(ctx context.Context, p Plan) error {
	body, err := verifyFile(ctx, p.Acceptance, false)
	if err != nil {
		return err
	}
	var evidence map[string]any
	if err := DecodeObject(body, &evidence); err != nil {
		return errors.New("invalid native acceptance summary")
	}
	if evidence["schema"] != "gregale.run-native-recovery-summary/v1" || evidence["functional_pass"] != true || evidence["bare_metal_host"] != true || evidence["p0_3_complete"] != true || evidence["runtime_release_eligibility"] != true || evidence["source_recovery_profile"] != "native_process_recovery_owner_v3" {
		return errors.New("requires reviewed native Run release evidence; functional-only or virtualized evidence cannot enable production")
	}
	source, _ := evidence["source"].(map[string]any)
	if source["snapshot_commit"] != p.SourceCommit {
		return errors.New("native acceptance source differs from release plan")
	}
	host, _ := evidence["host"].(map[string]any)
	if host["architecture"] != "x86_64" || host["virtualization"] != "none" || host["kvm"] != true {
		return errors.New("native acceptance host is unsupported")
	}
	pins, _ := evidence["release_artifact_sha256"].(map[string]any)
	if pins["vmmd"] != p.Candidate.File.SHA256 {
		return errors.New("native acceptance does not name the exact candidate vmmd binary")
	}
	for _, name := range artifactNames() {
		if pins[name] != p.Artifacts[name].SHA256 {
			return fmt.Errorf("native acceptance lacks matching %s release pin", name)
		}
	}
	checks, _ := evidence["checks"].(map[string]any)
	cohort, _ := checks["template-clones"].(map[string]any)
	passed, _ := cohort["passed_tests"].([]any)
	set := map[string]bool{}
	for _, test := range passed {
		if s, ok := test.(string); ok {
			set[s] = true
		}
	}
	for _, test := range RequiredScenarios() {
		if !set[test] {
			return fmt.Errorf("native acceptance is missing required scenario %s", test)
		}
	}
	exits, _ := evidence["process_exits"].(map[string]any)
	for _, name := range []string{"native_recovery", "leakcheck_before", "leakcheck_after"} {
		if exits[name] != float64(0) {
			return errors.New("native acceptance requires successful process and leak exits")
		}
	}
	ledger, _ := evidence["execution_ledger_assertions"].(map[string]any)
	for key, want := range map[string]float64{"total_customer_runs": 27, "native_recovery_customer_runs": 11, "template_cohort_customer_runs": 9, "warm_oom_customer_runs": 7, "usage_entries_per_customer_run": 1, "retained_payload_rows": 0, "template_account_runs": 0} {
		if ledger[key] != want {
			return errors.New("native acceptance ledger coverage is incomplete")
		}
	}
	if evidence["native_kvm_only_device_receipts"] != float64(27) || evidence["post_capture_crash_leakchecks"] != float64(5) || evidence["pre_teardown_leakchecks"] != float64(3) {
		return errors.New("native device or within-fixture cleanup coverage is incomplete")
	}
	return nil
}

// RequiredScenarios is deliberately explicit: an aggregate count cannot stand
// in for a missing crash, publication, isolation or OOM journey.
func RequiredScenarios() []string {
	groups := []struct {
		root  string
		cases []string
	}{
		{"TestSourceNativeRecoveryMetal", []string{"crash_before_boot", "crash_after_pristine_ack", "crash_after_pause", "crash_during_publication", "crash_after_capture", "schedd_crash_during_capture", "published_template_survives_owner_restart", "vmmd_restart_never_reexecutes_running_work", "schedd_restart_never_reexecutes_running_work", "payload_erasure_and_single_usage_entry"}},
		{"TestSourceTemplateClonesMetal", []string{"automatic_capture_and_publication", "four_live_clones_are_isolated", "cancelling_one_clone_spares_its_peer", "corrupt_drive_cold_fallback", "corrupt_backing_cold_fallback", "failed_capture_never_publishes", "idempotent_publication_after_restart", "catalog_retention_and_orphan_gc", "template_unchanged_and_usage_settled"}},
		{"TestSourceWarmRestoreOOMMetal", []string{"payload_free_capture", "repeated_restores_are_clean", "cold_real_oom", "warm_real_oom_and_recovery", "corrupt_memory_cold_fallback", "corrupt_vmstate_cold_fallback", "payload_erasure_and_single_usage_entry"}},
	}
	var tests []string
	for _, g := range groups {
		for _, name := range g.cases {
			tests = append(tests, g.root+"/"+name)
		}
	}
	return tests
}

func Digest(ctx context.Context, path string) (string, error) {
	f, err := openInput(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return digestFile(ctx, f)
}

func digestFile(ctx context.Context, f *os.File) (string, error) {
	before, err := f.Stat()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return "", errors.New("input changed while hashing")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyFile(ctx context.Context, pin File, executable bool) ([]byte, error) {
	f, err := openInput(pin.Path)
	if err != nil {
		return nil, fmt.Errorf("cannot read pinned input: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("pinned binary is not executable")
	}
	if !executable && info.Size() > api.RunReleaseManifestMaxBytes {
		return nil, errors.New("control input exceeds release manifest bound")
	}
	digest, err := digestFile(ctx, f)
	if err != nil {
		return nil, err
	}
	if digest != pin.SHA256 {
		return nil, errors.New("input SHA-256 differs from reviewed pin")
	}
	if executable {
		return nil, nil
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(f, api.RunReleaseManifestMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > api.RunReleaseManifestMaxBytes || SHA256(body) != pin.SHA256 {
		return nil, errors.New("control input changed while reading")
	}
	return body, nil
}

func SHA256(body []byte) string { digest := sha256.Sum256(body); return hex.EncodeToString(digest[:]) }
