// commands_pki_test.go - pins the load-bearing contracts of the
// operator-side PKI introspection leaf (commands_pki.go::cmdPKIList).
//
// The current contracts under test:
//
//   - `pki list` is read-only: it never writes to the FS and must
//     succeed with exit 0 even on a bare rootDir (no CA, no leaves).
//     CI gates rely on this so the pre-init wire shape is stable.
//   - `pki list --json` emits a pkiListReport with the pinned field
//     set {box_role, daemon, ca:{path,mode,serial,not_after,present},
//     leaves:[{directory,filename,cn,sans,...}]}. Missing files
//     report present=false; present files report mode/serial/not_after.
//   - `pki list --daemon <name>` narrows leaves to the directory
//     (with the egress cross-directory carve-out, identical to the
//     rotate path) and the JSON shape echoes daemon="<name>".
//   - `pki list --json` exit code is 0 on a marshal+write success
//     and the report is parseable by encoding/json.Unmarshal.
//   - The dispatcher error message lists the new subcommand
//     ("known: init, status, list, rotate") so operator muscle
//     memory stops typo'ing.
//
// Tests drive the full CLI path with `--json` (capturing stdout via
// the osStdout hook) so the wire shape is locked end-to-end rather
// than only the inspector helper. This mirrors the sign_keys + host_age
// pattern; see commands_sign_keys_test.go:TestCmdSignKeysStatus_JSON_*.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/pki"
)

func TestPKIIdentityCanonicalizesComputeNodeAndTransportSAN(t *testing.T) {
	identity, err := pkiIdentity(&pkiFlags{
		boxRole:      "compute-only",
		nodeCN:       "fsn-3",
		transportSAN: "fsn-3.gregale.dev",
	})
	if err != nil {
		t.Fatalf("pkiIdentity: %v", err)
	}
	if identity.nodeCN != "fsn-3.faas" {
		t.Fatalf("node CN = %q, want fsn-3.faas", identity.nodeCN)
	}
	if len(identity.transportSAN.DNSNames) != 1 || identity.transportSAN.DNSNames[0] != "fsn-3.gregale.dev" {
		t.Fatalf("transport SAN = %#v, want fsn-3.gregale.dev", identity.transportSAN)
	}
}

func TestPKIIdentityRejectsNodeIdentityOnControlPlane(t *testing.T) {
	if _, err := pkiIdentity(&pkiFlags{boxRole: "control-plane", nodeCN: "fsn-1"}); err == nil {
		t.Fatal("pkiIdentity accepted a compute node identity on control-plane")
	}
}

func TestPKIRenewalRecreatesSecureExportParentBeforeExport(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "pki_renew.yml"))
	if err != nil {
		t.Fatalf("read pki renewal playbook: %v", err)
	}
	text := string(body)
	remove := strings.Index(text, "pki renewal — remove prior target export material")
	recreate := strings.Index(text, "pki renewal — recreate target export staging parent")
	export := strings.Index(text, "pki renewal — export active trust material without ca.key")
	if remove < 0 || recreate < 0 || export < 0 || !(remove < recreate && recreate < export) {
		t.Fatalf("renewal export staging order is unsafe: remove=%d recreate=%d export=%d", remove, recreate, export)
	}
	stagingTask := text[recreate:export]
	for _, required := range []string{
		`path: "{{ faas_pki_remote_export }}"`,
		"state: directory",
		"owner: root",
		"group: root",
		`mode: "0700"`,
		"when: faas_pki_action_required and not faas_pki_resume_activation",
	} {
		if !strings.Contains(stagingTask, required) {
			t.Errorf("renewal export staging task missing %q", required)
		}
	}
}

func TestRenderPKIMetricsExportsBoundedDaemonExpiryAndRenewalState(t *testing.T) {
	rootDir := seedPKIRootDir(t)
	statePath := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(statePath, []byte(`{"last_success_unix":101,"last_failure_unix":99,"partial":false}`), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	now := time.Unix(1234, 0)
	body, err := renderPKIMetrics(rootDir, "control-plane", "fsn-1", statePath, now)
	if err != nil {
		t.Fatalf("renderPKIMetrics: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		`faas_internal_mtls_leaf_earliest_expiry_timestamp_seconds{host="fsn-1",box_role="control-plane",daemon="apid"}`,
		`faas_internal_mtls_expiry_metrics_last_success_timestamp_seconds{host="fsn-1",box_role="control-plane"} 1234`,
		`faas_internal_mtls_renewal_last_success_timestamp_seconds{host="fsn-1",box_role="control-plane"} 101`,
		`faas_internal_mtls_renewal_last_failure_timestamp_seconds{host="fsn-1",box_role="control-plane"} 99`,
		`faas_internal_mtls_renewal_partial{host="fsn-1",box_role="control-plane"} 0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("metrics missing %q\n%s", want, got)
		}
	}
}

func TestRenderPKIMetricsFailsClosedOnMissingActiveLeaf(t *testing.T) {
	rootDir := seedPKIRootDir(t)
	role := pki.RolesForBox("compute-only")[0]
	certPath, _ := pki.LeafPaths(rootDir, role)
	if err := os.Remove(certPath); err != nil {
		t.Fatalf("remove active leaf: %v", err)
	}
	if _, err := renderPKIMetrics(rootDir, "compute-only", "fsn-2.faas", "", time.Now()); err == nil {
		t.Fatal("renderPKIMetrics accepted a missing active leaf")
	}
}

func TestWritePKIMetricsAtomicReplacesExistingTextfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector", "pki.prom")
	if err := writePKIMetricsAtomic(path, []byte("old\n")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writePKIMetricsAtomic(path, []byte("new\n")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(body) != "new\n" {
		t.Fatalf("output = %q, want new", body)
	}
}

// captureOsStdoutPKI swaps the package-level osStdout for a buffer
// and returns a restore closure. Local to this file so we can grow
// the buffer type independently from the sign_keys + host_age
// capture helpers (they were copy-pasted across two test files
// already; duplicating once more keeps the tests in their own
// namespace rather than introducing a shared test-helper package).
func captureOsStdoutPKI(t *testing.T) (*pkiBuffer, func()) {
	t.Helper()
	old := osStdout
	buf := &pkiBuffer{}
	osStdout = buf
	return buf, func() { osStdout = old }
}

type pkiBuffer struct {
	data []byte
}

func (b *pkiBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *pkiBuffer) Bytes() []byte  { return b.data }
func (b *pkiBuffer) String() string { return string(b.data) }

// writeFakeCert writes a self-signed ECDSA P-256 cert + key as
// PEM at certPath / keyPath. Mirrors pkg/pki's own self-signed
// helper but stays local so the test doesn't depend on
// pki.EnsureLeaf (which writes the actual CA-chained leaf and
// is overkill for an introspection-only test).
func writeFakeCert(t *testing.T, certPath, keyPath, cn string, sans []string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     sans,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

// seedPKIRootDir writes a minimal CA + one leaf per role in the
// role set. Uses pki.Roles() (the full superset, not box-filtered)
// so the test sees every directory + exercises the dispatcher's
// egress carve-out + daemon filter paths. Returns the rootDir.
func seedPKIRootDir(t *testing.T) string {
	t.Helper()
	rootDir := t.TempDir()

	// CA: pkg/pki.CARoot(rootDir) → <rootDir>/ca/ca.crt + ca.key
	caCert, caKey := pki.CARoot(rootDir)
	if err := os.MkdirAll(filepath.Dir(caCert), 0o755); err != nil {
		t.Fatalf("mkdir ca dir: %v", err)
	}
	writeFakeCert(t, caCert, caKey, "gregale-test-ca", nil)

	// One leaf per role (covers all directories the text path prints).
	for _, role := range pki.Roles() {
		certPath, keyPath := pki.LeafPaths(rootDir, role)
		if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
			t.Fatalf("mkdir leaf dir: %v", err)
		}
		writeFakeCert(t, certPath, keyPath, "leaf-"+role.CommonName, []string{role.CommonName + ".test"})
	}
	return rootDir
}

// TestInspectPKI_AllLeavesAndCA pins the text-path JSON shape
// independently from the CLI driver: it asserts the inspector
// returns one leaf per role in the full set + a populated CA,
// with every present=true and a non-zero serial + not_after.
//
// The test does not pin the exact leaf count (role set is a
// pki-internal constant that may grow over time) but it does
// pin "all leaves are present" so a missing leaf would fail
// loudly rather than silently dropping a directory.
func TestInspectPKI_AllLeavesAndCA(t *testing.T) {
	rootDir := seedPKIRootDir(t)

	rep := inspectPKI(rootDir, "", "")
	if !rep.CA.Present {
		t.Errorf("CA.Present = false, want true (seedPKIRootDir wrote the CA)")
	}
	if rep.CA.Serial == "" {
		t.Errorf("CA.Serial empty, want non-zero")
	}
	if rep.CA.NotAfter == "" {
		t.Errorf("CA.NotAfter empty, want non-zero")
	}
	if rep.BoxRole != "" {
		t.Errorf("BoxRole = %q, want empty (no --box-role filter)", rep.BoxRole)
	}
	if rep.Daemon != "" {
		t.Errorf("Daemon = %q, want empty (no --daemon filter)", rep.Daemon)
	}
	if len(rep.Leaves) == 0 {
		t.Fatal("Leaves empty, want one entry per role")
	}
	for i, l := range rep.Leaves {
		if !l.Present {
			t.Errorf("Leaves[%d] (%s/%s) Present = false, want true", i, l.Directory, l.Filename)
		}
		if l.Serial == "" {
			t.Errorf("Leaves[%d] (%s/%s) Serial empty, want non-zero", i, l.Directory, l.Filename)
		}
		if l.CN == "" {
			t.Errorf("Leaves[%d] (%s/%s) CN empty, want non-zero", i, l.Directory, l.Filename)
		}
	}
}

// TestInspectPKI_PreInitShape pins the missing-files wire shape.
// A bare rootDir produces present=false everywhere + non-empty
// paths so the operator can see WHAT would be inspected (mirrors
// the pre-init contract from sign_keys_test.go).
func TestInspectPKI_PreInitShape(t *testing.T) {
	rootDir := t.TempDir()

	rep := inspectPKI(rootDir, "", "")
	if rep.CA.Present {
		t.Errorf("CA.Present = true with no CA file, want false")
	}
	if rep.CA.Path == "" {
		t.Errorf("CA.Path empty, want non-empty (path echoes the location that WOULD be inspected)")
	}
	if len(rep.Leaves) == 0 {
		t.Fatal("Leaves empty, want one entry per role even when files are missing")
	}
	for i, l := range rep.Leaves {
		if l.Present {
			t.Errorf("Leaves[%d] (%s/%s) Present = true with no file, want false", i, l.Directory, l.Filename)
		}
		if l.Path == "" {
			t.Errorf("Leaves[%d] (%s/%s) Path empty, want non-empty", i, l.Directory, l.Filename)
		}
	}
}

// TestInspectPKI_DaemonFilter pins the --daemon narrowing: only
// leaves whose role matches the daemon (per roleMatchesDaemon,
// with the egress cross-directory carve-out) appear in the
// leaves slice, AND Daemon echoes the filter on the report.
//
// We pick "apid" as the daemon name because it's in the
// canonical role set (per pki.Roles()) and doesn't trigger
// the egress carve-out — keeping the assertion narrowly typed.
func TestInspectPKI_DaemonFilter(t *testing.T) {
	rootDir := seedPKIRootDir(t)

	rep := inspectPKI(rootDir, "apid", "")
	if rep.Daemon != "apid" {
		t.Errorf("Daemon = %q, want %q", rep.Daemon, "apid")
	}
	if len(rep.Leaves) == 0 {
		t.Fatal("Leaves empty, want one entry for the apid directory")
	}
	for i, l := range rep.Leaves {
		if l.Directory != "apid" {
			t.Errorf("Leaves[%d] Directory = %q, want %q (--daemon filter must narrow to one directory)", i, l.Directory, "apid")
		}
	}
}

// TestCmdPKIList_JSON_HappyPath drives the full CLI path with
// --json, captures stdout via the osStdout hook, and asserts the
// JSON document unmarshals into the pinned shape. This is the
// wire-format guarantee that CI gates rely on; if the schema
// drifts (e.g. a stray struct tag rename), this test fails fast.
func TestCmdPKIList_JSON_HappyPath(t *testing.T) {
	rootDir := seedPKIRootDir(t)

	out, restore := captureOsStdoutPKI(t)
	code := cmdPKIList([]string{
		"--root-dir=" + rootDir,
		"--json",
	})
	restore()
	if code != 0 {
		t.Fatalf("cmdPKIList(--json) = %d, want 0 (raw: %q)", code, out.String())
	}
	var rep pkiListReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal stdout: %v (raw: %q)", err, out.String())
	}
	if !rep.CA.Present {
		t.Errorf("CA.Present = false in JSON, want true")
	}
	if rep.CA.Serial == "" {
		t.Errorf("CA.Serial empty in JSON, want non-zero")
	}
	if len(rep.Leaves) == 0 {
		t.Fatal("Leaves empty in JSON, want one entry per role")
	}
	for i, l := range rep.Leaves {
		if !l.Present {
			t.Errorf("Leaves[%d] (%s/%s) Present = false in JSON, want true", i, l.Directory, l.Filename)
		}
	}
}

// TestCmdPKIList_JSON_PreInit pins the pre-init JSON wire shape:
// bare rootDir + --json must NOT error, must emit present=false
// on the CA + every leaf, and the paths must still be populated
// so the operator can see what WOULD be inspected.
func TestCmdPKIList_JSON_PreInit(t *testing.T) {
	rootDir := t.TempDir()

	out, restore := captureOsStdoutPKI(t)
	code := cmdPKIList([]string{
		"--root-dir=" + rootDir,
		"--json",
	})
	restore()
	if code != 0 {
		t.Fatalf("cmdPKIList(--json, pre-init) = %d, want 0 (list is a read path; missing files are not an error) (raw: %q)", code, out.String())
	}
	var rep pkiListReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal stdout: %v (raw: %q)", err, out.String())
	}
	if rep.CA.Present {
		t.Errorf("CA.Present = true in pre-init JSON, want false")
	}
	if rep.CA.Path == "" {
		t.Errorf("CA.Path empty in pre-init JSON, want non-empty")
	}
	if len(rep.Leaves) == 0 {
		t.Fatal("Leaves empty in pre-init JSON, want one entry per role")
	}
	for i, l := range rep.Leaves {
		if l.Present {
			t.Errorf("Leaves[%d] Present = true in pre-init JSON, want false", i)
		}
		if l.Path == "" {
			t.Errorf("Leaves[%d] Path empty in pre-init JSON, want non-empty", i)
		}
	}
}

// TestCmdPKIList_JSON_DaemonFilter drives the --daemon narrowing
// end-to-end via the CLI (vs the inspector-only test above) so
// the JSON wire shape with daemon != "" is also pinned.
func TestCmdPKIList_JSON_DaemonFilter(t *testing.T) {
	rootDir := seedPKIRootDir(t)

	out, restore := captureOsStdoutPKI(t)
	code := cmdPKIList([]string{
		"--root-dir=" + rootDir,
		"--daemon=apid",
		"--json",
	})
	restore()
	if code != 0 {
		t.Fatalf("cmdPKIList(--daemon=apid,--json) = %d, want 0 (raw: %q)", code, out.String())
	}
	var rep pkiListReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal stdout: %v (raw: %q)", err, out.String())
	}
	if rep.Daemon != "apid" {
		t.Errorf("Daemon = %q in JSON, want %q", rep.Daemon, "apid")
	}
	if len(rep.Leaves) == 0 {
		t.Fatal("Leaves empty with --daemon=apid, want one entry")
	}
	for i, l := range rep.Leaves {
		if l.Directory != "apid" {
			t.Errorf("Leaves[%d] Directory = %q, want %q (--daemon filter must echo in JSON)", i, l.Directory, "apid")
		}
	}
}

// TestCmdPKIList_PositionalArgRejected pins the usage-gate: any
// positional arg is a usage error (exit 1). The dispatcher relies
// on fs.NArg() to surface this; a future refactor that drops the
// check would silently accept the arg.
func TestCmdPKIList_PositionalArgRejected(t *testing.T) {
	out, restore := captureOsStdoutPKI(t)
	code := cmdPKIList([]string{"--root-dir=/tmp", "extra-positional"})
	restore()
	if code == 0 {
		t.Errorf("cmdPKIList(extra positional) = 0, want non-zero (usage error); stdout: %q", out.String())
	}
}

// TestCmdPKIDispatch_ListsNewVerb pins the dispatcher's error
// message mentions "list" so operators stop typo'ing. The
// dispatcher hands unknown verbs to cmdPKI which emits the
// "known:" hint; this test catches a future refactor that drops
// the new verb from the message.
func TestCmdPKIDispatch_ListsNewVerb(t *testing.T) {
	// Snapshot stderr (the dispatcher prints the hint there).
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	code := cmdPKI([]string{"bogus"})
	w.Close()
	os.Stderr = oldStderr
	if code == 0 {
		t.Fatalf("cmdPKI(bogus) = 0, want non-zero (unknown verb is a usage error)")
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "init, status, list, rotate") {
		t.Errorf("dispatcher hint = %q, want it to contain %q", got, "init, status, list, rotate")
	}
}

// TestCmdPKI_NoArgs pins the empty-args branch (commands_pki.go:71-74) —
// exit 1 with a usage hint.
func TestCmdPKI_NoArgs(t *testing.T) {
	if rc := cmdPKI(nil); rc != 1 {
		t.Errorf("cmdPKI(nil) = %d, want 1 (usage error)", rc)
	}
}

// TestCmdPKIDispatch_Routing pins the dispatcher's routing for
// every known verb. Each subtest feeds an invalid flag into the
// routed leaf — flag.Parse fails BEFORE the leaf touches the FS,
// so the test exercises only the dispatch + flag-parse path.
func TestCmdPKIDispatch_Routing(t *testing.T) {
	cases := []struct {
		name string
		verb string
	}{
		{name: "init", verb: "init"},
		{name: "status", verb: "status"},
		{name: "list", verb: "list"},
		{name: "rotate", verb: "rotate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := cmdPKI([]string{tc.verb, "--not-a-flag"})
			if code != 1 {
				t.Errorf("cmdPKI(%s --not-a-flag) = %d, want 1 (flag.Parse error)", tc.verb, code)
			}
		})
	}
}

// TestCmdPKIInit_InvalidFlag pins the flag.Parse error branch
// of cmdPKIInit (commands_pki.go:143-145) — exit 1.
func TestCmdPKIInit_InvalidFlag(t *testing.T) {
	if code := cmdPKIInit([]string{"--not-a-flag"}); code != 1 {
		t.Errorf("cmdPKIInit(--not-a-flag) = %d, want 1", code)
	}
}

// TestCmdPKIInit_ExtraPositional pins the NArg != 0 branch
// (commands_pki.go:146-149) — exit 1 with a usage hint.
func TestCmdPKIInit_ExtraPositional(t *testing.T) {
	stderr := captureStderrPKI(t, func() {
		if code := cmdPKIInit([]string{"extra-positional"}); code != 1 {
			t.Errorf("cmdPKIInit(extra) = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "usage:") {
		t.Errorf("cmdPKIInit stderr missing usage hint (got %q)", stderr)
	}
}

// TestCmdPKIStatus_InvalidFlag pins the flag.Parse error branch
// of cmdPKIStatus (commands_pki.go:173-175).
func TestCmdPKIStatus_InvalidFlag(t *testing.T) {
	if code := cmdPKIStatus([]string{"--not-a-flag"}); code != 1 {
		t.Errorf("cmdPKIStatus(--not-a-flag) = %d, want 1", code)
	}
}

// TestCmdPKIStatus_ExtraPositional pins the NArg != 0 branch
// (commands_pki.go:176-179).
func TestCmdPKIStatus_ExtraPositional(t *testing.T) {
	stderr := captureStderrPKI(t, func() {
		if code := cmdPKIStatus([]string{"extra-positional"}); code != 1 {
			t.Errorf("cmdPKIStatus(extra) = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "usage:") {
		t.Errorf("cmdPKIStatus stderr missing usage hint (got %q)", stderr)
	}
}

// TestCmdPKIRotate_InvalidFlag pins the flag.Parse error branch
// of cmdPKIRotate (commands_pki.go:209-211).
func TestCmdPKIRotate_InvalidFlag(t *testing.T) {
	if code := cmdPKIRotate([]string{"--not-a-flag"}); code != 1 {
		t.Errorf("cmdPKIRotate(--not-a-flag) = %d, want 1", code)
	}
}

// TestCmdPKIRotate_ExtraPositional pins the NArg != 0 branch
// (commands_pki.go:212-215).
func TestCmdPKIRotate_ExtraPositional(t *testing.T) {
	stderr := captureStderrPKI(t, func() {
		if code := cmdPKIRotate([]string{"extra-positional"}); code != 1 {
			t.Errorf("cmdPKIRotate(extra) = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "usage:") {
		t.Errorf("cmdPKIRotate stderr missing usage hint (got %q)", stderr)
	}
}

// TestRotateRestartHint pins the per-daemon restart hint
// (commands_pki.go:260-287) — exit 0 of the rotation has the
// right systemctl invocation for every known daemon. The
// empty-daemon branch emits the whole-fleet hint.
func TestRotateRestartHint(t *testing.T) {
	cases := []struct {
		daemon string
		want   string
	}{
		{daemon: "", want: "systemctl kill -s HUP"},
		{daemon: "egress", want: "systemctl reload faas-gatewayd-internal"},
		{daemon: "meterd", want: "systemctl reload faas-meterd"},
		{daemon: "schedd", want: "systemctl kill -s HUP faas-schedd"},
		{daemon: "vmmd", want: "systemctl kill -s HUP faas-vmmd"},
		{daemon: "apid", want: "systemctl kill -s HUP faas-apid"},
		{daemon: "githubd", want: "systemctl reload faas-githubd"},
		{daemon: "builderd", want: "systemctl reload faas-builderd"},
		{daemon: "unknown-daemon", want: "systemctl kill -s HUP faas-{schedd,vmmd,apid}"}, // fallthrough
	}
	for _, tc := range cases {
		t.Run(tc.daemon, func(t *testing.T) {
			got := rotateRestartHint(tc.daemon)
			if !strings.Contains(got, tc.want) {
				t.Errorf("rotateRestartHint(%q) = %q, want substring %q", tc.daemon, got, tc.want)
			}
		})
	}
}

// TestIsErrLeafNotExpiringSoon pins the sentinel-matcher helper
// (commands_pki.go:465-467) — string-compare without errors.Is
// because the sentinel may be wrapped by EnsureLeaf.
func TestIsErrLeafNotExpiringSoon(t *testing.T) {
	if !isErrLeafNotExpiringSoon(pki.ErrLeafNotExpiringSoon) {
		t.Errorf("bare sentinel should match")
	}
	if !isErrLeafNotExpiringSoon(fmt.Errorf("wrap: %w", pki.ErrLeafNotExpiringSoon)) {
		t.Errorf("wrapped sentinel should match")
	}
	if isErrLeafNotExpiringSoon(nil) {
		t.Errorf("nil should NOT match")
	}
	if isErrLeafNotExpiringSoon(errors.New("other error")) {
		t.Errorf("unrelated error should NOT match")
	}
}

// TestReportLeafStatusFiltered_DaemonFilter pins the text-path
// counterpart of inspectPKI's daemon narrowing — every printed
// row's directory label should match the --daemon filter (with
// the egress cross-directory carve-out).
func TestReportLeafStatusFiltered_DaemonFilter(t *testing.T) {
	rootDir := seedPKIRootDir(t)
	var buf bytes.Buffer
	reportLeafStatusFiltered(&buf, rootDir, "apid", "")
	out := buf.String()
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "apid ") {
			t.Errorf("expected every row to start with 'apid ', got %q", line)
		}
	}
}

// captureStderrPKI is the file-local stderr capture helper. Mirrors
// the precedent at commands_release_sbom_gate_test.go:107-123 and
// commands_compute_nodes_test.go:63-79. Kept local to this file so
// the buffer type stays in the test author's namespace (no shared
// testutil package — see commands_pki_test.go:46-51 comment).
func captureStderrPKI(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	_ = w.Close()
	var out []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	_ = r.Close()
	return string(out)
}
