//go:build linux

// Tests for the binary /etc/faas/app_passwd reader. The
// table-lookup core is testable without a real /etc mount —
// searchPasswdTable takes the file body as a []byte.
package main

import (
	"encoding/binary"
	"testing"
)

// makePasswdRow builds a single record per the documented
// layout (writePasswdTable in pkg/rootfs/build_base.go).
func makePasswdRow(uid, gid uint32, name string) []byte {
	out := make([]byte, 9+len(name))
	binary.BigEndian.PutUint32(out[0:4], uid)
	binary.BigEndian.PutUint32(out[4:8], gid)
	out[8] = byte(len(name))
	copy(out[9:], name)
	return out
}

func TestReadPasswdTable_HitMiss(t *testing.T) {
	// Sorted by name (per the builder's contract): alpine,
	// root, sshd.
	body := append([]byte{},
		append(makePasswdRow(1000, 1000, "alpine"),
			append(makePasswdRow(0, 0, "root"),
				makePasswdRow(74, 74, "sshd")...)...)...,
	)
	if uid, ok := searchPasswdTable(body, "alpine"); !ok || uid != 1000 {
		t.Errorf("alpine: got (%d, %v); want (1000, true)", uid, ok)
	}
	if uid, ok := searchPasswdTable(body, "root"); !ok || uid != 0 {
		t.Errorf("root: got (%d, %v); want (0, true)", uid, ok)
	}
	if uid, ok := searchPasswdTable(body, "sshd"); !ok || uid != 74 {
		t.Errorf("sshd: got (%d, %v); want (74, true)", uid, ok)
	}
	// Miss: "ghost" is NOT in the table; expect (0, false).
	if uid, ok := searchPasswdTable(body, "ghost"); ok || uid != 0 {
		t.Errorf("ghost: got (%d, %v); want (0, false)", uid, ok)
	}
	// Empty lookup key: returns (0, false).
	if uid, ok := searchPasswdTable(body, ""); ok || uid != 0 {
		t.Errorf("empty name: got (%d, %v); want (0, false)", uid, ok)
	}
}

func TestReadPasswdTable_MalformedFileFallback(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"truncated header", []byte{0, 0, 0}},             // 3 bytes — shorter than the 9-byte header
		{"truncated name field", makePasswdRow(0, 0, "")}, // length byte = 0 → next record immediately
		// Over-declared name length:
		{"name-len-exceeds-body", []byte{
			0, 0, 0, 0, 0, 0, 0, 0, // uid/gid
			255, // name length 255, but body ends here
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := searchPasswdTable(tc.body, "anything"); ok {
				t.Errorf("searchPasswdTable(%q) returned ok; want false on malformed body", tc.name)
			}
		})
	}
}

func TestReadPasswdTable_EmptyBody(t *testing.T) {
	// No entries — every lookup misses.
	if _, ok := searchPasswdTable(nil, "anyone"); ok {
		t.Errorf("empty body returned ok; want false")
	}
	if _, ok := searchPasswdTable([]byte{}, "anyone"); ok {
		t.Errorf("zero-length body returned ok; want false")
	}
}

func TestReadPasswdTable_SingleEntry(t *testing.T) {
	body := makePasswdRow(65532, 65532, "nonroot")
	if uid, ok := searchPasswdTable(body, "nonroot"); !ok || uid != 65532 {
		t.Errorf("nonroot: got (%d, %v); want (65532, true)", uid, ok)
	}
	if _, ok := searchPasswdTable(body, "root"); ok {
		t.Errorf("root miss: got ok; want false")
	}
}

// TestReadPasswdTable_MidRecordBoundaryRegression — when `mid`
// lands inside a record (not at a record boundary), the search
// must compare against the record CONTAINING mid's predecessor
// boundary, not the record that starts at-or-after mid. The first
// implementation read the comparison record at off>=mid and used
// that record's nameLen to narrow the search, which (a) missed
// the first record on single-record tables and (b) infinite-looped
// when off landed exactly at mid for the second record of a
// 2-record table. This regression pins both failures.
//
// 3 records sorted ascending: alpine(uid=1000), root(uid=0),
// sshd(uid=74). Body size = 15+13+13 = 41 bytes. mid values:
//   - lo=0, hi=41 → mid=20 → mid is inside the root record
//     (15..28). The previous record (alpine) must compare as
//     "alpine vs root" on a hit-or-narrow; the next record
//     (sshd) must NOT be the comparison target.
//   - lo=0, hi=28 → mid=14 → mid is inside the alpine record
//     (0..15). alpine is the comparison target.
func TestReadPasswdTable_MidRecordBoundaryRegression(t *testing.T) {
	body := append([]byte{},
		append(makePasswdRow(1000, 1000, "alpine"),
			append(makePasswdRow(0, 0, "root"),
				makePasswdRow(74, 74, "sshd")...)...)...,
	)
	// Hit cases.
	if uid, ok := searchPasswdTable(body, "alpine"); !ok || uid != 1000 {
		t.Errorf("alpine: got (%d, %v); want (1000, true)", uid, ok)
	}
	if uid, ok := searchPasswdTable(body, "root"); !ok || uid != 0 {
		t.Errorf("root: got (%d, %v); want (0, true)", uid, ok)
	}
	if uid, ok := searchPasswdTable(body, "sshd"); !ok || uid != 74 {
		t.Errorf("sshd: got (%d, %v); want (74, true)", uid, ok)
	}
	// Miss cases — must NOT infinite-loop.
	for _, name := range []string{"aardvark", "ghost", "zebra"} {
		if uid, ok := searchPasswdTable(body, name); ok || uid != 0 {
			t.Errorf("%q: got (%d, %v); want (0, false)", name, uid, ok)
		}
	}
}

// TestLookupUID_DefaultUser — the lookupUID default-user
// short-circuit (today-equivalent behavior).
func TestLookupUID_DefaultUser(t *testing.T) {
	if got := lookupUID("app"); got != 1000 {
		t.Errorf("lookupUID(\"app\") = %d; want 1000 (DefaultAppUID)", got)
	}
}

// TestLookupUID_FallbackWhenNoTable — when /etc/faas/app_passwd
// is absent (legacy two-drive image), lookupUID falls back to
// DefaultAppUID regardless of the input name.
func TestLookupUID_FallbackWhenNoTable(t *testing.T) {
	// The test runs in a hermetic temp-dir-less environment
	// that has no /etc/faas/app_passwd, so any lookup miss
	// must surface as DefaultAppUID.
	if got := lookupUID("ghost-user"); got != 1000 {
		t.Errorf("lookupUID(\"ghost-user\") = %d; want 1000 (DefaultAppUID fallback)", got)
	}
}
