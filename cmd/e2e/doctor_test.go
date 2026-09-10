// doctor_test.go — non-metal CI-safe acceptance for the
// `gregalectl doctor` flow (PR-4 / ADR-110).
//
// This is the e2e surface for PR-4. The commander reads the
// cluster-shipped release bundle's three surfaces and reports
// drift; it never writes. The tests drive the binary as a
// subprocess so we exercise the public surface (flag.Parse,
// exit codes, JSON wire shape) end-to-end.
//
//	TestDoctor_Healthy              — healthy single-box cluster
//	TestDoctor_ManifestHashDrift    — corrupt compute_nodes.manifest_hash
//	TestDoctor_OrphanReleaseID      — release_id points at a missing SHA
//	TestDoctor_OnDiskOnlyNoDB       — DB down, on-disk checks still work
//	TestDoctor_FailOnWarn           — warn vs error threshold
//	TestDoctor_NodeFilter           — --node filter narrows findings
//	TestDoctor_Deep_DriftPerNode    — --deep detects on-disk tampering
//
// Skipped automatically when FAAS_PG_DSN is unset. Every test
// deletes its own release_bundles + compute_nodes rows + temp
// /opt/faas tree so reruns are deterministic.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// doctorSetup is the canonical fixture builder. Two compute_nodes
// (A, B) point at the same release; the on-disk bundle is canonical
// (every catalog daemon + a fresh manifest). Returns the
// per-test working dir, the binary, the pool, the dsn, and the
// git_sha + manifest_hash the test fixture installs.
type doctorSetup struct {
	workdir      string
	releasesRoot string
	binPath      string
	gitSHA       string
	manifestHash string
	pool         *pgxpool.Pool
	dsn          string
	nodeA        string
	nodeB        string
}

func newDoctorSetup(t *testing.T) *doctorSetup {
	t.Helper()
	dsn := os.Getenv("FAAS_PG_DSN")
	if dsn == "" {
		t.Skip("FAAS_PG_DSN not set; skipping doctor e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	workdir := t.TempDir()
	binDir := filepath.Join(workdir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		body := []byte("fake-" + name)
		if err := os.WriteFile(filepath.Join(binDir, name), body, 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	optRoot := filepath.Join(workdir, "opt", "faas")
	releasesRoot := filepath.Join(optRoot, "releases")
	if err := os.MkdirAll(releasesRoot, 0o755); err != nil {
		t.Fatalf("mkdir releases: %v", err)
	}

	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	manifestHash := "sha256:" + strings.Repeat("a", 64)
	bin := buildGregaleCtl(t)

	// Build the bundle on disk + INSERT into release_bundles.
	// Quiet the test noise by routing stdout/stderr to the
	// captured buffers, but the contents don't matter to the
	// doctor tests.
	runCmd := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "FAAS_PG_DSN="+dsn)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else {
				t.Fatalf("exec: %v", err)
			}
		}
		return code, stdout.String(), stderr.String()
	}

	if code, _, errOut := runCmd(
		"release", "bundle",
		"--bin-dir="+binDir,
		"--git-sha="+gitSHA,
		"--manifest-hash="+manifestHash,
		"--releases-root="+releasesRoot,
	); code != 0 {
		t.Fatalf("release bundle: exit=%d stderr=%q", code, errOut)
	}
	nodeA := "test-node-doctor-a"
	nodeB := "test-node-doctor-b"
	for _, node := range []string{nodeA, nodeB} {
		if code, _, errOut := runCmd(
			"release", "install",
			"--git-sha="+gitSHA,
			"--releases-root="+releasesRoot,
			"--node="+node,
		); code != 0 {
			t.Fatalf("release install %s: exit=%d stderr=%q", node, code, errOut)
		}
	}

	return &doctorSetup{
		workdir:      workdir,
		releasesRoot: releasesRoot,
		binPath:      bin,
		gitSHA:       gitSHA,
		manifestHash: manifestHash,
		pool:         pool,
		dsn:          dsn,
		nodeA:        nodeA,
		nodeB:        nodeB,
	}
}

// cleanup deletes the per-test release_bundles + compute_nodes rows
// and closes the pool. Wired to t.Cleanup so test failures don't
// leak rows across runs.
func (s *doctorSetup) cleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.pool.Exec(ctx, `delete from release_bundles where git_sha = $1`, s.gitSHA); err != nil {
			t.Logf("cleanup: delete release_bundles: %v", err)
		}
		if _, err := s.pool.Exec(ctx, `delete from compute_nodes where name in ($1, $2)`,
			s.nodeA, s.nodeB); err != nil {
			t.Logf("cleanup: delete compute_nodes: %v", err)
		}
		s.pool.Close()
	})
}

// runDoctor drives the doctor command and returns exit code +
// stdout + stderr. Optional env-override map is appended LAST so
// it can clobber FAAS_PG_DSN (used by the no-DB tests).
func (s *doctorSetup) runDoctor(t *testing.T, envOverrides map[string]string, args ...string) (int, string, string) {
	t.Helper()
	bin := s.binPath
	cmd := exec.Command(bin, args...)
	env := append(os.Environ(), "FAAS_PG_DSN="+s.dsn)
	for k, v := range envOverrides {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("exec: %v", err)
		}
	}
	return code, stdout.String(), stderr.String()
}

// doctorReport is the JSON wire shape of `gregalectl doctor --json`.
// Mirrors cmd/gregalectl/commands_doctor.go without importing it
// (cmd/gregalectl is a main package; cmd/e2e is a test package).
// The shape is the same.
type doctorReport struct {
	ReleasesRoot  string        `json:"releases_root"`
	NodeFilter    string        `json:"node_filter"`
	ReleaseFilter string        `json:"release_filter"`
	StartedAt     time.Time     `json:"started_at"`
	FinishedAt    time.Time     `json:"finished_at"`
	Healthy       bool          `json:"healthy"`
	Counts        doctorCounts  `json:"counts"`
	Findings      []doctorFound `json:"findings"`
	Checks        []doctorCheck `json:"checks"`
}

type doctorCounts struct {
	OK    int `json:"ok"`
	Warn  int `json:"warn"`
	Error int `json:"error"`
	Total int `json:"total"`
}

type doctorFound struct {
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Target   string `json:"target"`
	Message  string `json:"message"`
	Detail   string `json:"detail"`
}

type doctorCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Notes      int    `json:"notes"`
}

// findCheck returns the persisted per-check summary by name, or
// zero-value if the check was skipped / not in the report.
func (r doctorReport) findCheck(name string) (doctorCheck, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctorCheck{}, false
}

func TestDoctor_Healthy(t *testing.T) {
	s := newDoctorSetup(t)
	s.cleanup(t)

	code, out, errOut := s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--json",
	)
	if code != 0 {
		t.Fatalf("doctor: exit=%d stderr=%q", code, errOut)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v (stdout=%q)", err, out)
	}
	if !rep.Healthy {
		t.Errorf("healthy = false, want true (counts: %+v)", rep.Counts)
	}
	if rep.Counts.Error != 0 {
		t.Errorf("counts.error = %d, want 0", rep.Counts.Error)
	}
	for _, want := range []string{"symlink", "bundle", "lockstep", "nodes", "bundle-orphans"} {
		if _, ok := rep.findCheck(want); !ok {
			t.Errorf("missing check %q in report", want)
		}
	}
	// node-hashes is --deep; should NOT appear in this report.
	if _, ok := rep.findCheck("node-hashes"); ok {
		t.Errorf("node-hashes check ran without --deep; want skipped")
	}
}

func TestDoctor_ManifestHashDrift(t *testing.T) {
	s := newDoctorSetup(t)
	s.cleanup(t)

	// Corrupt compute_nodes.manifest_hash on nodeA only.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	badHash := "sha256:" + strings.Repeat("z", 64)
	if _, err := s.pool.Exec(ctx, `update compute_nodes set manifest_hash = $1 where name = $2`,
		badHash, s.nodeA); err != nil {
		t.Fatalf("update compute_nodes: %v", err)
	}

	// Doctor without --deep does NOT re-verify manifest_hash — it
	// only checks the active release against the bundle. The
	// corruption is on a node that already shipped; the check
	// reports it as ok because the node is on the active release
	// and the manifest_hash check only fires when a node's
	// release_id matches the active symlink. Use --deep.
	// nodeA's release_id matches the active release, so on
	// checking the manifest_hash against the bundle row we'd
	// see drift. But that check is --deep-only.
	// The non-deep "nodes" check only validates:
	//   - release_id non-empty + valid shape
	//   - release_id points at a release_bundles row
	//   - manifest_hash matches the bundle row when active
	// The third check fires here because nodeA is on the active
	// release. So we expect exit 3 + a "manifest_hash drift"
	// finding WITHOUT --deep.
	code, out, errOut := s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--json",
	)
	if code != 3 {
		t.Errorf("doctor: exit=%d, want 3 (drift). stderr=%q out=%q", code, errOut, out)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v", err)
	}
	if rep.Healthy {
		t.Errorf("healthy = true, want false (drift detected)")
	}
	// The finding should be on the "nodes" check.
	hit := false
	for _, f := range rep.Findings {
		if f.Check == "nodes" && f.Severity == "error" && strings.Contains(f.Message, "manifest_hash") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("no nodes error finding with 'manifest_hash' in message; got findings=%+v", rep.Findings)
	}
}

func TestDoctor_OrphanReleaseID(t *testing.T) {
	s := newDoctorSetup(t)
	s.cleanup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Set nodeA's release_id to a SHA that has no release_bundles row.
	orphan := "ffffffffffffffffffffffffffffffffffffffff"
	if _, err := s.pool.Exec(ctx, `update compute_nodes set release_id = $1 where name = $2`,
		orphan, s.nodeA); err != nil {
		t.Fatalf("update compute_nodes: %v", err)
	}

	code, out, errOut := s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--json",
	)
	if code != 3 {
		t.Errorf("doctor: exit=%d, want 3 (drift). stderr=%q out=%q", code, errOut, out)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v", err)
	}
	hit := false
	for _, f := range rep.Findings {
		if f.Check == "nodes" && f.Severity == "error" && strings.Contains(f.Message, "orphan release_id") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("no 'orphan release_id' finding; got findings=%+v", rep.Findings)
	}
}

func TestDoctor_OnDiskOnlyNoDB(t *testing.T) {
	// Lay out a fresh box: bundle on disk, no DB. The on-disk
	// checks (symlink + bundle + lockstep) should run; the DB
	// checks emit a warn and skip.
	dsn := os.Getenv("FAAS_PG_DSN")
	if dsn == "" {
		t.Skip("FAAS_PG_DSN not set; skipping doctor e2e")
	}
	workdir := t.TempDir()
	binDir := filepath.Join(workdir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		body := []byte("fake-" + name)
		if err := os.WriteFile(filepath.Join(binDir, name), body, 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	optRoot := filepath.Join(workdir, "opt", "faas")
	releasesRoot := filepath.Join(optRoot, "releases")
	if err := os.MkdirAll(releasesRoot, 0o755); err != nil {
		t.Fatalf("mkdir releases: %v", err)
	}
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	manifestHash := "sha256:" + strings.Repeat("a", 64)
	bin := buildGregaleCtl(t)

	runCmd := func(envOverrides map[string]string, args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "FAAS_PG_DSN="+dsn)
		for k, v := range envOverrides {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		return code, stdout.String(), stderr.String()
	}

	// Build the bundle — DB is up so the INSERT works.
	if code, _, errOut := runCmd(nil,
		"release", "bundle",
		"--bin-dir="+binDir,
		"--git-sha="+gitSHA,
		"--manifest-hash="+manifestHash,
		"--releases-root="+releasesRoot,
	); code != 0 {
		t.Fatalf("release bundle: %d %q", code, errOut)
	}
	// Install — DB is up.
	if code, _, errOut := runCmd(nil,
		"release", "install",
		"--git-sha="+gitSHA,
		"--releases-root="+releasesRoot,
		"--node=test-node-doctor-no-db",
	); code != 0 {
		t.Fatalf("release install: %d %q", code, errOut)
	}
	// Cleanup the DB rows we just created.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return
		}
		defer pool.Close()
		_, _ = pool.Exec(ctx, `delete from release_bundles where git_sha = $1`, gitSHA)
		_, _ = pool.Exec(ctx, `delete from compute_nodes where name = $1`, "test-node-doctor-no-db")
	})

	// Now run doctor with FAAS_PG_DSN empty.
	code, out, errOut := runCmd(
		map[string]string{"FAAS_PG_DSN": ""},
		"doctor",
		"--releases-root="+releasesRoot,
		"--json",
	)
	if code != 0 {
		t.Errorf("doctor: exit=%d, want 0 (no drift on disk). stderr=%q out=%q", code, errOut, out)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v", err)
	}
	if !rep.Healthy {
		t.Errorf("healthy = false, want true (only on-disk checks ran; db is just a warning)")
	}
	// Should have a "db" warn finding.
	hit := false
	for _, f := range rep.Findings {
		if f.Check == "db" && f.Severity == "warn" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("no db warn finding; got findings=%+v", rep.Findings)
	}
}

func TestDoctor_FailOnWarn(t *testing.T) {
	s := newDoctorSetup(t)
	s.cleanup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Insert a separate release_bundles row for a SHA that has
	// NO on-disk tree. Leave applied_at NULL so it's a
	// bundle-orphan (warning, not error).
	orphanSHA := "ffffffffffffffffffffffffffffffffffffffff"
	orphanMH := "sha256:" + strings.Repeat("e", 64)
	hashes := map[string]string{}
	for _, name := range manifest.SortedHostKeys() {
		hashes[name] = "sha256:" + strings.Repeat("0", 64)
	}
	// We can't synthesise valid per-daemon hashes for the
	// orphan row; the doctor only reads the row's git_sha +
	// applied_at for the bundle-orphans check, so the hash
	// content doesn't matter.
	hashJSON, _ := json.Marshal(hashes)
	if _, err := s.pool.Exec(ctx, `
		insert into release_bundles (git_sha, manifest_hash, daemon_hashes, created_at)
		values ($1, $2, $3::jsonb, now())
	`, orphanSHA, orphanMH, string(hashJSON)); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `delete from release_bundles where git_sha = $1`, orphanSHA)
	})

	// --fail-on=warn — the bundle-orphan warning escalates to exit 3.
	code, _, _ := s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--fail-on=warn",
	)
	if code != 3 {
		t.Errorf("doctor --fail-on=warn: exit=%d, want 3 (warn escalated)", code)
	}
	// --fail-on=error — the warning is below threshold; exit 0.
	code, _, _ = s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--fail-on=error",
	)
	if code != 0 {
		t.Errorf("doctor --fail-on=error: exit=%d, want 0 (warn below threshold)", code)
	}
}

func TestDoctor_NodeFilter(t *testing.T) {
	s := newDoctorSetup(t)
	s.cleanup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Corrupt nodeA's release_id to be orphan, so the filtered
	// run surfaces a finding on nodeA only.
	orphan := "ffffffffffffffffffffffffffffffffffffffff"
	if _, err := s.pool.Exec(ctx, `update compute_nodes set release_id = $1 where name = $2`,
		orphan, s.nodeA); err != nil {
		t.Fatalf("update compute_nodes: %v", err)
	}

	// --node=B should NOT see the orphan-finding on nodeA.
	code, out, errOut := s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeB,
		"--json",
	)
	if code != 0 {
		t.Errorf("doctor --node=B: exit=%d, want 0 (nodeA is filtered out). stderr=%q", code, errOut)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v", err)
	}
	for _, f := range rep.Findings {
		if f.Target == s.nodeA {
			t.Errorf("finding references filtered node %s", s.nodeA)
		}
	}

	// --node=A SHOULD see the orphan-finding.
	code, out, _ = s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--json",
	)
	if code != 3 {
		t.Errorf("doctor --node=A: exit=%d, want 3 (orphan release_id)", code)
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v", err)
	}
	hit := false
	for _, f := range rep.Findings {
		if f.Check == "nodes" && f.Target == s.nodeA {
			hit = true
		}
	}
	if !hit {
		t.Errorf("no nodes finding on nodeA; got findings=%+v", rep.Findings)
	}
}

func TestDoctor_Deep_DriftPerNode(t *testing.T) {
	s := newDoctorSetup(t)
	s.cleanup(t)

	// Healthy --deep run.
	code, out, errOut := s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--deep",
		"--json",
	)
	if code != 0 {
		t.Fatalf("doctor --deep healthy: exit=%d stderr=%q", code, errOut)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v", err)
	}
	if _, ok := rep.findCheck("node-hashes"); !ok {
		t.Errorf("node-hashes check missing from --deep run")
	}

	// Tamper with the vmmd binary on disk.
	binPath := filepath.Join(s.releasesRoot, s.gitSHA, releaseinstall.BinDirName, "vmmd")
	if err := os.WriteFile(binPath, []byte("TAMPERED"), 0o755); err != nil {
		t.Fatalf("tamper vmmd: %v", err)
	}

	// --deep run should detect the drifted binary.
	code, out, _ = s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--deep",
		"--json",
	)
	if code != 3 {
		t.Errorf("doctor --deep tampered: exit=%d, want 3. out=%q", code, out)
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v", err)
	}
	hit := false
	for _, f := range rep.Findings {
		if f.Check == "node-hashes" && strings.Contains(f.Detail, "vmmd") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("no node-hashes finding mentioning vmmd; got findings=%+v", rep.Findings)
	}
}

func TestDoctor_Deep_SkipsUnavailableHistoryByDefault(t *testing.T) {
	s := newDoctorSetup(t)
	s.cleanup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	legacySHA := "123456789abcdef0123456789abcdef012345678"
	if _, err := s.pool.Exec(ctx, `
		insert into release_bundles (git_sha, manifest_hash, daemon_hashes, applied_at)
		select $1, manifest_hash, daemon_hashes, now()
		from release_bundles where git_sha = $2`, legacySHA, s.gitSHA); err != nil {
		t.Fatalf("insert legacy release bundle: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `
		update compute_nodes
		set release_id = $1, lifecycle = 'unavailable'
		where name = $2`, legacySHA, s.nodeA); err != nil {
		t.Fatalf("make node unavailable: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = s.pool.Exec(cleanupCtx, `delete from compute_nodes where name = $1`, s.nodeA)
		_, _ = s.pool.Exec(cleanupCtx, `delete from release_bundles where git_sha = $1`, legacySHA)
	})

	code, out, errOut := s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--deep",
		"--json",
	)
	if code != 0 {
		t.Fatalf("doctor --deep default fleet: exit=%d stderr=%q out=%q", code, errOut, out)
	}
	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode default report: %v", err)
	}
	for _, finding := range report.Findings {
		if finding.Check == "node-hashes" && finding.Target == s.nodeA {
			t.Fatalf("default deep audit inspected unavailable node: %+v", finding)
		}
	}

	code, out, _ = s.runDoctor(t, nil,
		"doctor",
		"--releases-root="+s.releasesRoot,
		"--node="+s.nodeA,
		"--deep",
		"--json",
	)
	if code != 3 {
		t.Fatalf("doctor --deep explicit unavailable node: exit=%d, want 3; out=%q", code, out)
	}
}
