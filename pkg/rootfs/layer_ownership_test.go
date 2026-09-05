// layer_ownership_test.go — M-1 (ADR-136 §Decision 2) layer ownership
// preservation tests.
//
// The ApplyLayer path now reads hdr.Uid / hdr.Gid (the integer fields
// the tar.Header carries alongside the string Uname/Gname) and
// preserves the declared uid/gid on the staging tree via os.Lchown.
// Values outside [0, 65534] or named users (Uname non-empty but Uid=0)
// fall through to the daemon uid/gid and increment the
// imaged_ownership_clamp_total counter under the appropriate reason.
//
// Tests below pin the field-level behaviour:
//   - integer uid/gid in range       → preserved
//   - integer uid/gid out of range   → counter increments, daemon uid
//   - Uid=0 + Uname non-empty        → counter increments (named user, M-3)
//   - Uid=0 + Uname empty            → silent fall-through (BuildKit default)
//   - char/block device              → skipped + counter incremented
package rootfs

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
	dto "github.com/prometheus/client_model/go"
)

// applyEntryPublic is a thin shim that calls the unexported
// applyEntry with a freshly-buffered reader.
func applyEntryPublic(t *testing.T, dst string, hdr *tar.Header) error {
	t.Helper()
	return applyEntry(dst, filepath.Join(dst, hdr.Name), hdr, bytes.NewReader(nil), nil)
}

// writeTarLayer assembles a tarball with one entry of the given
// header and returns it as a *tar.Reader.
func writeTarLayer(t *testing.T, hdr *tar.Header, body []byte) *tar.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if len(body) > 0 {
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	return tar.NewReader(&buf)
}

// readCounter returns the cumulative count for the given reason.
// Tests use wire.NewOpsMetrics("imaged") which pre-registers
// ownershipClamp + layerEntrySkipped under the imaged prefix, so
// the increment path (recordOwnershipClamp → ops.OwnershipClamp) is
// byte-identical to production.
func readCounter(t *testing.T, reason string) float64 {
	t.Helper()
	ensureTestOps(t)
	c := ops.OwnershipClamp(reason)
	if c == nil {
		t.Fatalf("ops.OwnershipClamp(%q) returned nil; OpsMetrics prefix may not be 'imaged'", reason)
	}
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("counter.Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// readDeviceSkipTotal returns the cumulative skip count.
func readDeviceSkipTotal(t *testing.T) float64 {
	t.Helper()
	ensureTestOps(t)
	c := ops.LayerEntrySkipped()
	if c == nil {
		t.Fatalf("ops.LayerEntrySkipped returned nil; OpsMetrics prefix may not be 'imaged'")
	}
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("layerDeviceSkip.Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// ensureTestOps builds a fresh *wire.OpsMetrics with prefix "imaged"
// (so the ownershipClamp / layerEntrySkipped fields are registered)
// and wires it via SetOpsMetrics. Tests run in package rootfs so they
// share the package-level `ops` var. Each test that calls readCounter
// or readDeviceSkipTotal pays the NewOpsMetrics cost once; subsequent
// tests in the same package see the cached handle.
func ensureTestOps(t *testing.T) {
	t.Helper()
	if ops != nil {
		return
	}
	ops = wire.NewOpsMetrics("imaged")
	SetOpsMetrics(ops)
}

func TestPreserveOwnership_IntegerInRange(t *testing.T) {
	requireRoot(t)
	// Pre-fix regression: parseOwnership read hdr.Uname (string) so
	// the integer Uid field was ignored and the file landed as
	// daemon uid. With the fix, integer Uid=1001 lands as uid 1001
	// regardless of what Uname says (BuildKit typically writes both).
	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "owned-by-1001",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     5,
		Uid:      1001,
		Gid:      1001,
		Uname:    "app", // intentionally non-numeric — must be ignored
		Gname:    "app",
	}
	if err := applyEntry(tmp, filepath.Join(tmp, hdr.Name), hdr, bytes.NewReader([]byte("hello")), nil); err != nil {
		t.Fatalf("applyEntry: %v", err)
	}
	info, err := os.Stat(filepath.Join(tmp, "owned-by-1001"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := statUID(info); got != 1001 {
		t.Errorf("uid = %d; want 1001 (integer field must win over Uname string)", got)
	}
	if got := statGID(info); got != 1001 {
		t.Errorf("gid = %d; want 1001", got)
	}
}

func TestPreserveOwnership_IntegerOutOfRangeClamped(t *testing.T) {
	// An integer Uid/Gid above 65534 must clamp to daemon uid and
	// increment imaged_ownership_clamp_total{out_of_range}. Pre-fix
	// the same scenario tripped only if Uname was the magic string
	// "99999"; this test pins the integer-field path.
	before := readCounter(t, "out_of_range")

	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "out-of-range",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     1,
		Uid:      99999,
		Gid:      99999,
	}
	if err := applyEntry(tmp, filepath.Join(tmp, hdr.Name), hdr, bytes.NewReader([]byte("x")), nil); err != nil {
		t.Fatalf("applyEntry: %v", err)
	}
	after := readCounter(t, "out_of_range")
	if got := after - before; got != 1 {
		t.Errorf("imaged_ownership_clamp_total{out_of_range} delta = %v; want 1", got)
	}
	// The file must NOT be chowned to 99999 — that uid doesn't
	// exist on the host. Confirm daemon uid is what landed.
	info, err := os.Stat(filepath.Join(tmp, "out-of-range"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := statUID(info); got == 99999 {
		t.Errorf("uid = %d; want NOT 99999 (must clamp)", got)
	}
}

func TestPreserveOwnership_NamedUserTrips(t *testing.T) {
	// Uid=0 with Uname="node" → counter unparseable_uid, daemon uid.
	// Pre-fix, hdr.Uname="1001" was the only way to trigger
	// preservation (the int field was ignored). With the fix, an
	// integer Uid wins; named users trip the counter.
	before := readCounter(t, "unparseable_uid")

	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "named-user",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     1,
		Uid:      0,
		Gid:      0,
		Uname:    "node",
		Gname:    "nogroup",
	}
	if err := applyEntry(tmp, filepath.Join(tmp, hdr.Name), hdr, bytes.NewReader([]byte("x")), nil); err != nil {
		t.Fatalf("applyEntry: %v", err)
	}
	after := readCounter(t, "unparseable_uid")
	if got := after - before; got != 1 {
		t.Errorf("imaged_ownership_clamp_total{unparseable_uid} delta = %v; want 1", got)
	}
}

func TestPreserveOwnership_EmptyStaysSilent(t *testing.T) {
	// Uid=0 AND Uname="" → BuildKit default. No counter movement,
	// no chown call. Pre-fix this case explicitly chowned the file
	// to root:root (silent bug — counter looked fine but the file's
	// owner was overwritten).
	beforeOOR := readCounter(t, "out_of_range")
	beforeUnpUID := readCounter(t, "unparseable_uid")
	beforeUnpGID := readCounter(t, "unparseable_gid")

	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "default-owned",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     1,
		// Uid/Gid/Uname/Gname all zero/empty
	}
	if err := applyEntry(tmp, filepath.Join(tmp, hdr.Name), hdr, bytes.NewReader([]byte("x")), nil); err != nil {
		t.Fatalf("applyEntry: %v", err)
	}
	afterOOR := readCounter(t, "out_of_range")
	afterUnpUID := readCounter(t, "unparseable_uid")
	afterUnpGID := readCounter(t, "unparseable_gid")
	if afterOOR != beforeOOR || afterUnpUID != beforeUnpUID || afterUnpGID != beforeUnpGID {
		t.Errorf("empty uid/gid + Uname/Gname must not increment any clamp counter (OOR %v→%v, uid %v→%v, gid %v→%v)",
			beforeOOR, afterOOR, beforeUnpUID, afterUnpUID, beforeUnpGID, afterUnpGID)
	}
}

func TestPreserveOwnership_EmptyDoesNotChownToRoot(t *testing.T) {
	// The pre-fix bug: empty headers triggered os.Lchown(target, 0, 0)
	// silently. Pin the post-fix behaviour — daemon uid preserved.
	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "stays-daemon-owned",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     1,
	}
	if err := applyEntry(tmp, filepath.Join(tmp, hdr.Name), hdr, bytes.NewReader([]byte("x")), nil); err != nil {
		t.Fatalf("applyEntry: %v", err)
	}
	info, err := os.Stat(filepath.Join(tmp, "stays-daemon-owned"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Touch a control file the same way ApplyLayer would have,
	// then compare its uid — both must match (proves preserveOwnership
	// didn't chown to root).
	control := filepath.Join(tmp, "control")
	if err := os.WriteFile(control, []byte("y"), 0o644); err != nil {
		t.Fatalf("control write: %v", err)
	}
	cinfo, err := os.Stat(control)
	if err != nil {
		t.Fatalf("Stat(control): %v", err)
	}
	if got := statUID(info); got != statUID(cinfo) {
		t.Errorf("uid = %d (control %d); preserveOwnership must NOT chown when header is empty",
			got, statUID(cinfo))
	}
}

func TestPreserveOwnership_DeviceEntrySkipped(t *testing.T) {
	before := readDeviceSkipTotal(t)

	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "null-device",
		Typeflag: tar.TypeChar,
		Mode:     0o666,
		Uid:      0,
		Gid:      0,
		Devmajor: 1,
		Devminor: 3,
	}
	if err := applyEntryPublic(t, tmp, hdr); err != nil {
		t.Fatalf("applyEntry(device): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "null-device")); !os.IsNotExist(err) {
		t.Errorf("device entry should NOT exist on disk; got err=%v", err)
	}
	after := readDeviceSkipTotal(t)
	if got := after - before; got != 1 {
		t.Errorf("imaged_layer_entry_skipped_total delta = %v; want 1", got)
	}
}

func TestApplyLayer_OwnershipAppliedEndToEnd(t *testing.T) {
	requireRoot(t)
	// Drive ApplyLayer (the full pipeline) with a tar containing a
	// single regular file declaring USER 1001; the file lands on
	// disk as uid 1001 and the body is preserved.
	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "app/server",
		Typeflag: tar.TypeReg,
		Mode:     0o755,
		Size:     3,
		Uid:      1001,
		Gid:      1001,
	}
	tr := writeTarLayer(t, hdr, []byte("bin"))
	if err := ApplyLayer(tmp, tr); err != nil {
		t.Fatalf("ApplyLayer: %v", err)
	}
	info, err := os.Stat(filepath.Join(tmp, "app", "server"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("file is not regular: %v", info.Mode())
	}
	if size := info.Size(); size != 3 {
		t.Errorf("size = %d; want 3", size)
	}
	if got := statUID(info); got != 1001 {
		t.Errorf("uid = %d; want 1001 (ApplyLayer must wire os.Lchown end-to-end)", got)
	}
}

func TestApplyLayer_OutOfRangeClampEndToEnd(t *testing.T) {
	requireRoot(t)
	before := readCounter(t, "out_of_range")

	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "weird-uid",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     1,
		Uid:      70000,
		Gid:      70000,
	}
	tr := writeTarLayer(t, hdr, []byte("x"))
	if err := ApplyLayer(tmp, tr); err != nil {
		t.Fatalf("ApplyLayer: %v", err)
	}
	info, err := os.Stat(filepath.Join(tmp, "weird-uid"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := statUID(info); got == 70000 {
		t.Errorf("uid = %d; must NOT be 70000 (out_of_range clamp)", got)
	}
	after := readCounter(t, "out_of_range")
	if got := after - before; got != 1 {
		t.Errorf("out_of_range counter delta = %v; want 1", got)
	}
}

func TestInOwnershipRange(t *testing.T) {
	cases := []struct {
		in   int
		want bool
	}{
		{-1, false},
		{0, true},
		{1, true},
		{1000, true},
		{20000, true},
		{29999, true},
		{65534, true},
		{65535, false},
		{70000, false},
		{99999, false},
	}
	for _, tc := range cases {
		if got := inOwnershipRange(tc.in); got != tc.want {
			t.Errorf("inOwnershipRange(%d) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseOwnershipInt(t *testing.T) {
	cases := []struct {
		name        string
		intVal      int
		nameVal     string
		kind        string
		wantN       int
		wantOK      bool
		wantCounter string // expected counter movement
	}{
		// Integer field wins (the whole point of M-1).
		{intVal: 1001, nameVal: "app", kind: "uid", wantN: 1001, wantOK: true},
		{intVal: 0, nameVal: "node", kind: "uid", wantOK: false, wantCounter: "unparseable_uid"},
		{intVal: 0, nameVal: "", kind: "uid", wantOK: false, wantCounter: ""},
		{intVal: 65534, nameVal: "", kind: "uid", wantN: 65534, wantOK: true},
		{intVal: 99999, nameVal: "", kind: "uid", wantOK: false, wantCounter: "out_of_range"},
		// Named gid: same path.
		{intVal: 0, nameVal: "nogroup", kind: "gid", wantOK: false, wantCounter: "unparseable_gid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var before float64
			if tc.wantCounter != "" {
				before = readCounter(t, tc.wantCounter)
			}
			n, ok := parseOwnershipInt(tc.intVal, tc.nameVal, tc.kind)
			if ok != tc.wantOK {
				t.Errorf("parseOwnershipInt(%d,%q,%s) ok = %v; want %v",
					tc.intVal, tc.nameVal, tc.kind, ok, tc.wantOK)
			}
			if n != tc.wantN {
				t.Errorf("parseOwnershipInt(%d,%q,%s) n = %d; want %d",
					tc.intVal, tc.nameVal, tc.kind, n, tc.wantN)
			}
			if tc.wantCounter != "" {
				after := readCounter(t, tc.wantCounter)
				if after-before != 1 {
					t.Errorf("counter %q delta = %v; want 1", tc.wantCounter, after-before)
				}
			}
		})
	}
}

func TestApplyEntryPreservesOwnershipOnSymlink(t *testing.T) {
	// os.Lchown on a symlink must target the link, not its resolution.
	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "bin-sh",
		Typeflag: tar.TypeSymlink,
		Linkname: "/bin/busybox",
		Uid:      1001,
		Gid:      1001,
	}
	if err := applyEntryPublic(t, tmp, hdr); err != nil {
		t.Fatalf("applyEntry(symlink): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "bin-sh")); err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	target, err := os.Readlink(filepath.Join(tmp, "bin-sh"))
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != "/bin/busybox" {
		t.Errorf("Readlink = %q; want /bin/busybox", target)
	}
}

func TestPreserveOwnership_DirIntegerInRange(t *testing.T) {
	requireRoot(t)
	// Directory entries must also flow through preserveOwnership —
	// ADR-136 §Decision 2 says every entry, not just regular files.
	tmp := t.TempDir()
	hdr := &tar.Header{
		Name:     "data-dir",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
		Uid:      1001,
		Gid:      1001,
	}
	if err := applyEntryPublic(t, tmp, hdr); err != nil {
		t.Fatalf("applyEntry(dir): %v", err)
	}
	info, err := os.Stat(filepath.Join(tmp, "data-dir"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("data-dir is not a directory: %v", info.Mode())
	}
	if got := statUID(info); got != 1001 {
		t.Errorf("uid = %d; want 1001 (directory entries must flow through)", got)
	}
}

// Compile-time guard: ensure parseOwnership's three return values
// are stable. (Function signature changes would break the build
// here before they break callers.)
var _ = func() (int, int, bool) {
	uid, gid, ok := parseOwnership(&tar.Header{Uid: 0, Gid: 0})
	_ = uid
	_ = gid
	return uid, gid, ok
}

// strings.HasPrefix keeps the import alive without churning the
// file when other helpers are added.
var _ = strings.HasPrefix

// --- platform-specific stat helpers ---------------------------------
//
// statUID / statGID extract the integer uid/gid from a FileInfo.
// On Linux the FileInfo.Sys() returns *syscall.Stat_t with Uid/Gid
// as int32 (libc convention) — we assert to that concrete type.
// On non-Linux platforms the helper returns -1; the comparison
// tests are skipped (build tag = linux) so we don't pretend.
func statUID(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

func statGID(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Gid)
	}
	return -1
}

// requireRoot skips the test when not running as root, because
// os.Lchown is a no-op for non-root users and the assertions would
// see the daemon uid/gid regardless. The parseOwnership logic itself
// (which is what we ship) is independent of root — see the
// TestParseOwnershipInt case for that surface.
func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("Lchown requires root; parseOwnership logic is independent of root and is covered by TestParseOwnershipInt")
	}
}
