package rootfs

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestParseStagingPasswd_AlpineShape — alpine:latest ships a single
// /etc/passwd in its top-most layer; the helper returns a map
// with root + alpine + sshd + ... entries. Top-most-wins means the
// helper sees the merged view because the previous layer's entries
// would have been overwritten by the same file in the top-most
// layer (alpine rebuilds it whole).
func TestParseStagingPasswd_AlpineShape(t *testing.T) {
	staging := t.TempDir()
	passwd := "root:x:0:0:root:/root:/bin/sh\n" +
		"alpine:x:1000:1000:alpine:/home/alpine:/bin/sh\n" +
		"sshd:x:74:74:sshd:/var/empty:/sbin/nologin\n"
	if err := os.MkdirAll(filepath.Join(staging, "etc"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "etc", "passwd"), []byte(passwd), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := parseStagingPasswd(staging)
	if err != nil {
		t.Fatalf("parseStagingPasswd: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len(got) = %d; want 3 (root, alpine, sshd)", len(got))
	}
	if got["alpine"].Uid != 1000 || got["alpine"].Gid != 1000 {
		t.Errorf("alpine uid/gid = %d/%d; want 1000/1000", got["alpine"].Uid, got["alpine"].Gid)
	}
	if got["sshd"].Uid != 74 {
		t.Errorf("sshd uid = %d; want 74", got["sshd"].Uid)
	}
}

// TestParseStagingPasswd_DistrolessShape — distroless/static-debian12
// ships root + nonroot in a single layer; the helper returns both.
func TestParseStagingPasswd_DistrolessShape(t *testing.T) {
	staging := t.TempDir()
	// 7 colon-separated fields per record: name:password:uid:gid:gecos:home:shell.
	// We project only Name/UID/GID; the rest is dropped on parse.
	passwd := "root:x:0:0:root:/root:/sbin/nologin\n" +
		"nonroot:x:65532:65532:nonroot:/home/nonroot:/sbin/nologin\n"
	if err := os.MkdirAll(filepath.Join(staging, "etc"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "etc", "passwd"), []byte(passwd), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := parseStagingPasswd(staging)
	if err != nil {
		t.Fatalf("parseStagingPasswd: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d; want 2 (root, nonroot)", len(got))
	}
	if got["nonroot"].Uid != 65532 {
		t.Errorf("nonroot uid = %d; want 65532", got["nonroot"].Uid)
	}
}

// TestParseStagingPasswd_NoFileReturnsNil — images with no
// /etc/passwd at all (extremely rare) must NOT error; the helper
// returns (nil, nil) so the caller's merge is a no-op.
func TestParseStagingPasswd_NoFileReturnsNil(t *testing.T) {
	staging := t.TempDir() // empty — no etc/passwd
	got, err := parseStagingPasswd(staging)
	if err != nil {
		t.Errorf("parseStagingPasswd on empty staging: %v; want nil", err)
	}
	if got != nil {
		t.Errorf("parseStagingPasswd on empty staging: got %d entries; want 0", len(got))
	}
}

// TestParseStagingPasswd_NSSSkipsPlusEntries — standard `+`-prefixed
// NSS entries must NOT enter the map; they would otherwise let a
// hostile image bypass our uid clamp by declaring
// `+::::::/home/nobody:/sbin/nologin`.
func TestParseStagingPasswd_NSSSkipsPlusEntries(t *testing.T) {
	staging := t.TempDir()
	passwd := "root:x:0:0:root:/root:/bin/sh\n" +
		"+:::::::/home/nobody:/sbin/nologin\n" + // hostile — must skip
		"+@user:1000:1000:user:/home/user:/bin/sh\n" + // hostile — must skip
		"app:x:1000:1000:app:/home/app:/bin/sh\n"
	if err := os.MkdirAll(filepath.Join(staging, "etc"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "etc", "passwd"), []byte(passwd), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := parseStagingPasswd(staging)
	if err != nil {
		t.Fatalf("parseStagingPasswd: %v", err)
	}
	if _, ok := got["+"]; ok {
		t.Errorf("parseStagingPasswd included `+` entry; NSS must be skipped")
	}
	if _, ok := got["+@user"]; ok {
		t.Errorf("parseStagingPasswd included `+@user` entry; NSS must be skipped")
	}
	if got["app"].Uid != 1000 {
		t.Errorf("app uid = %d; want 1000", got["app"].Uid)
	}
}

// TestParseStagingPasswd_MalformedLineErrors — an invalid 7-field
// line is a hard error (a malicious image declaring uid=999999999
// or skipping fields must fail fast at build time).
func TestParseStagingPasswd_MalformedLineErrors(t *testing.T) {
	staging := t.TempDir()
	passwd := "root:x:0:0:root:/root:/bin/sh\n" +
		"onlythreefields:x:1000\n" // 3 fields instead of 7
	if err := os.MkdirAll(filepath.Join(staging, "etc"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "etc", "passwd"), []byte(passwd), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := parseStagingPasswd(staging)
	if err == nil {
		t.Errorf("parseStagingPasswd accepted a malformed line; want error")
	}
}

// TestWritePasswdTable_BinaryFormat — the on-disk binary file at
// /etc/faas/app_passwd must match the documented record layout:
//
//	4 bytes uid, 4 bytes gid, 1 byte name length, N bytes name,
//
// sorted ascending by name, no padding. The guest-init reader
// (commit 8) depends on this exact layout.
func TestWritePasswdTable_BinaryFormat(t *testing.T) {
	staging := t.TempDir()
	entries := map[string]PasswdEntry{
		"root":   {Name: "root", Uid: 0, Gid: 0},
		"alpine": {Name: "alpine", Uid: 1000, Gid: 1000},
		"sshd":   {Name: "sshd", Uid: 74, Gid: 74},
	}
	if err := writePasswdTable(staging, entries, 256); err != nil {
		t.Fatalf("writePasswdTable: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, passwdTablePath))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// 3 entries × (4+4+1 + len(name))
	wantSize := (4 + 4 + 1 + len("root")) +
		(4 + 4 + 1 + len("alpine")) +
		(4 + 4 + 1 + len("sshd"))
	if len(body) != wantSize {
		t.Fatalf("binary size = %d; want %d", len(body), wantSize)
	}
	// Walk records in order. Names must be sorted ASC.
	wantOrder := []string{"alpine", "root", "sshd"}
	off := 0
	for _, want := range wantOrder {
		if string(body[off+9:off+9+len(want)]) != want {
			t.Errorf("record at off=%d name = %q; want %q", off, string(body[off+9:off+9+len(want)]), want)
		}
		off += 4 + 4 + 1 + len(want)
	}
}

// TestWritePasswdTable_OverCapDropsExcess — when len(entries) >
// maxEntries, the binary file contains the first maxEntries
// records (sorted by name) and the metric counter fires once.
// /etc/passwd text form is unaffected.
func TestWritePasswdTable_OverCapDropsExcess(t *testing.T) {
	staging := t.TempDir()
	entries := make(map[string]PasswdEntry)
	names := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		n := string(rune('a' + i))
		entries[n] = PasswdEntry{Name: n, Uid: 1000 + i, Gid: 1000 + i}
		names = append(names, n)
	}
	sort.Strings(names)
	if err := writePasswdTable(staging, entries, 4); err != nil {
		t.Fatalf("writePasswdTable: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, passwdTablePath))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// 4 records × (4+4+1 + 1 byte name) = 4 × 10 = 40 bytes
	const wantSize = 4 * 10
	if len(body) != wantSize {
		t.Errorf("binary size = %d; want %d (over-cap should drop to 4)", len(body), wantSize)
	}
	// The first 4 entries by name: a, b, c, d.
	wantFirst := []string{"a", "b", "c", "d"}
	off := 0
	for _, want := range wantFirst {
		if string(body[off+9:off+10]) != want {
			t.Errorf("first-name at off=%d = %q; want %q", off, string(body[off+9:off+10]), want)
		}
		off += 10
	}
}

// TestWritePasswdTable_TextFormMirrorsBinary — the text form at
// /etc/passwd must list the SAME entries the binary file does,
// sorted the same way. The text file is for guest tooling that
// expects a real /etc/passwd; the binary file is for guest-init
// lookup. They MUST agree on the merged map.
func TestWritePasswdTable_TextFormMirrorsBinary(t *testing.T) {
	staging := t.TempDir()
	entries := map[string]PasswdEntry{
		"root": {Name: "root", Uid: 0, Gid: 0},
		"app":  {Name: "app", Uid: 1000, Gid: 1000},
	}
	if err := writePasswdTable(staging, entries, 256); err != nil {
		t.Fatalf("writePasswdTable: %v", err)
	}
	textBytes, err := os.ReadFile(filepath.Join(staging, "etc", "passwd"))
	if err != nil {
		t.Fatalf("ReadFile /etc/passwd: %v", err)
	}
	text := string(textBytes)
	// Names appear in sorted order: app, root.
	if i := indexOf(text, "app:x:1000:1000"); i < 0 {
		t.Errorf("text form missing 'app' line")
	}
	if i := indexOf(text, "root:x:0:0"); i < 0 {
		t.Errorf("text form missing 'root' line")
	}
}

// TestWritePasswdTable_EmptyMapNoTextFile — when entries is empty,
// /etc/passwd is NOT written (the guest has nothing to declare)
// but the binary table path is still created with a zero-byte
// file so guest-init's open() returns ENOENT for the legacy path
// and falls back to DefaultAppUID (M-3 commit 8 documents this).
func TestWritePasswdTable_EmptyMapNoTextFile(t *testing.T) {
	staging := t.TempDir()
	if err := writePasswdTable(staging, map[string]PasswdEntry{}, 256); err != nil {
		t.Fatalf("writePasswdTable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "etc", "passwd")); !os.IsNotExist(err) {
		t.Errorf("empty map wrote /etc/passwd; want absent (Stat err = %v)", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, passwdTablePath))
	if err != nil {
		t.Fatalf("ReadFile binary: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("empty map wrote binary table of %d bytes; want 0", len(body))
	}
}

// indexOf is a tiny helper to keep the test readable. Avoids
// importing strings just for Index.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
