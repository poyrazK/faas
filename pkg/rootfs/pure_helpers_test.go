// pure_helpers_test.go — fill pkg/rootfs coverage of the tiny
// pure helpers and the small filesystem walks reachable without
// mkfs.ext4 or a real build root.
//
// Targets:
//   - Builder.WithSigner (the setter)
//   - ErrTarballExceedsCap.Error / Is
//   - CheckCapForStaging (the staging-aware cap check)
//   - DirSize / InspectStaging (the t.TempDir-backed walks)
//   - MkfsCommand (the canonical argv shape)
//
// Whitebox `package rootfs`.
package rootfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- Builder.WithSigner ---------------------------------------

// fakeSigner is a Signer that records the calls. We don't need a
// real signer to exercise WithSigner — the contract is "setter
// returns the receiver; field is assigned."
type fakeSigner struct{ signed int }

func (s *fakeSigner) Sign(_ context.Context, _, _ string) error {
	s.signed++
	return nil
}

func TestWithSigner_SetsAndReturnsReceiver(t *testing.T) {
	b := &Builder{}
	var s Signer = &fakeSigner{}
	if got := b.WithSigner(s); got != b {
		t.Error("WithSigner did not return receiver")
	}
	if b.signer != s {
		t.Error("signer not set")
	}
}

func TestWithSigner_NilClears(t *testing.T) {
	b := &Builder{signer: &fakeSigner{}}
	b.WithSigner(nil)
	if b.signer != nil {
		t.Errorf("WithSigner(nil): signer = %v, want nil", b.signer)
	}
}

// --- ErrTarballExceedsCap --------------------------------------

func TestErrTarballExceedsCap_Error(t *testing.T) {
	e := &ErrTarballExceedsCap{
		WrittenBytes: 100,
		EntryBytes:   50,
		CapBytes:     200,
	}
	msg := e.Error()
	// Error message must surface all three sizes so the operator
	// sees the running total vs. the entry about to overflow vs.
	// the configured cap.
	for _, want := range []string{"100", "50", "200"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want substring %q", msg, want)
		}
	}
}

func TestErrTarballExceedsCap_IsSelf(t *testing.T) {
	e := &ErrTarballExceedsCap{WrittenBytes: 1, EntryBytes: 1, CapBytes: 1}
	if !errors.Is(e, &ErrTarballExceedsCap{}) {
		t.Error("Is: same-type target should match")
	}
}

func TestErrTarballExceedsCap_IsUnrelated(t *testing.T) {
	e := &ErrTarballExceedsCap{}
	if errors.Is(e, errors.New("other")) {
		t.Error("Is: unrelated error should NOT match")
	}
}

// --- CheckCapForStaging ---------------------------------------

// Under-cap staging returns nil error and the padded MB count.
func TestCheckCapForStaging_UnderCap(t *testing.T) {
	stats := SmallFileStats{ContentBytes: 100 * mib, SmallRatio: 0}
	l := api.Limits{AppLayerMaxMB: 256}
	sizeMB, err := CheckCapForStaging(l, stats)
	if err != nil {
		t.Fatalf("under cap: err = %v, want nil", err)
	}
	if sizeMB <= 0 {
		t.Errorf("under cap: sizeMB = %d, want positive", sizeMB)
	}
	if sizeMB > l.AppLayerMaxMB {
		t.Errorf("under cap: sizeMB = %d exceeds cap %d", sizeMB, l.AppLayerMaxMB)
	}
}

// Over-cap staging surfaces *api.Problem with the cap and
// observed size attached.
func TestCheckCapForStaging_OverCapReturnsProblem(t *testing.T) {
	stats := SmallFileStats{
		ContentBytes: int64(256) * mib, // 256 MB content
		SmallRatio:   0.9,              // big slack → bigger padded size
	}
	l := api.Limits{AppLayerMaxMB: 128}
	_, err := CheckCapForStaging(l, stats)
	if err == nil {
		t.Fatal("over cap: err = nil, want problem")
	}
	var p *api.Problem
	if !errors.As(err, &p) {
		t.Errorf("over cap: err = %v, want *api.Problem", err)
	}
}

// --- DirSize / InspectStaging ---------------------------------

// DirSize on an empty directory returns zero.
func TestDirSize_EmptyDir(t *testing.T) {
	d := t.TempDir()
	got, err := DirSize(d)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 0 {
		t.Errorf("empty dir: got %d, want 0", got)
	}
}

// DirSize counts only regular files; directory entries don't
// contribute (their on-disk size is metadata only).
func TestDirSize_SumsRegularFilesOnly(t *testing.T) {
	d := t.TempDir()
	// 100-byte file at root + 200-byte file in subdir.
	if err := os.WriteFile(filepath.Join(d, "a.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(d, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "sub", "b.txt"), make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DirSize(d)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 300 {
		t.Errorf("got %d, want 300", got)
	}
}

// DirSize on a missing root surfaces a walk error.
func TestDirSize_MissingRootErrors(t *testing.T) {
	if _, err := DirSize("/no/such/path/anywhere"); err == nil {
		t.Error("missing root: got nil err, want error")
	}
}

// InspectStaging returns ContentBytes + smallRatio for a tree
// with one big file and one small file.
func TestInspectStaging_ComputesRatio(t *testing.T) {
	d := t.TempDir()
	// 100 bytes (small) and 10000 bytes (big).
	if err := os.WriteFile(filepath.Join(d, "small"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "big"), make([]byte, 10000), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := InspectStaging(d)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if stats.ContentBytes != 10100 {
		t.Errorf("ContentBytes = %d, want 10100", stats.ContentBytes)
	}
	want := 0.5 // 1 of 2 files small
	if stats.SmallRatio != want {
		t.Errorf("SmallRatio = %v, want %v", stats.SmallRatio, want)
	}
}

// InspectStaging on an empty tree returns ContentBytes=0 and
// SmallRatio=0 (no division-by-zero, the n>0 guard).
func TestInspectStaging_EmptyTreeZeroRatio(t *testing.T) {
	d := t.TempDir()
	stats, err := InspectStaging(d)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if stats.ContentBytes != 0 {
		t.Errorf("ContentBytes = %d, want 0", stats.ContentBytes)
	}
	if stats.SmallRatio != 0 {
		t.Errorf("SmallRatio = %v, want 0 (no division-by-zero)", stats.SmallRatio)
	}
}

// --- MkfsCommand golden shape ---------------------------------

// MkfsCommand returns the canonical argv: mkfs.ext4 -F -L applayer
// -d <staging> <out> <sizeM>. The shape is load-bearing —
// cmd/imaged's exec.Command path expects this exact ordering.
func TestMkfsCommand_Shape(t *testing.T) {
	got := MkfsCommand("/srv/staging", "/srv/app.ext4", 256)
	want := []string{
		"mkfs.ext4",
		"-F",
		"-L", "applayer",
		"-d", "/srv/staging",
		"/srv/app.ext4",
		"256M",
	}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// adr: 908 — a base gets 4 KiB blocks and an even-MiB size, so it can be a
// DAX pmem root, and the pmem label only when its build installed
// guest-init; app layers keep MkfsCommand's shape.
func TestBaseMkfsCommand_Shape(t *testing.T) {
	for _, tc := range []struct {
		size      int
		pmemReady bool
		wantSize  string
		wantLabel string
	}{
		{256, true, "256M", BasePmemLabel},
		{257, true, "258M", BasePmemLabel},
		{256, false, "256M", "applayer"},
	} {
		got := BaseMkfsCommand("/srv/staging", "/srv/base.ext4", tc.size, tc.pmemReady)
		wantArgv := []string{"mkfs.ext4", "-F", "-b", "4096", "-L", tc.wantLabel, "-d", "/srv/staging", "/srv/base.ext4", tc.wantSize}
		if strings.Join(got, " ") != strings.Join(wantArgv, " ") {
			t.Errorf("%+v: argv = %v, want %v", tc, got, wantArgv)
		}
	}
	if layer := MkfsCommand("/s", "/o", 257); strings.Contains(strings.Join(layer, " "), "-b") || layer[len(layer)-1] != "257M" {
		t.Errorf("MkfsCommand changed: %v", layer)
	}
}
