//go:build linux

// Package init — guest-init PID 1 binary. Reads the binary
// /etc/faas/app_passwd table the builder wrote at full-rootfs
// build time (M-3 commit 7 + commit 8, ADR-142 §Decision 3).
//
// The table layout is fixed:
//
//	per record, big-endian, contiguous, no padding:
//	  bytes 0..3   uint32  uid
//	  bytes 4..7   uint32  gid (unused today; gid is supplied
//	                        by the per-app manifest's
//	                        OverrideUserGid, see ADR-053 fourth
//	                        axis for M-4)
//	  byte  8      uint8   name length (0..255)
//	  bytes 9..9+N name (UTF-8, no NUL terminator)
//
// Records are sorted ascending by name so binary-search is
// O(log N) per lookup. Lookup runs once per guest boot (via
// lookupUID at supervisor bring-up time) — no per-request
// overhead.
//
// The reader is intentionally simple: a single open + read +
// binary-search. No mmap, no sysfs, no vsock. Falls back to
// DefaultAppUID on any error so a misbuilt / corrupt table
// degrades to today's behavior.
package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const maxImagePasswdBytes = 1 << 20

// readPasswdTable opens /etc/faas/app_passwd, binary-searches
// the entry for `name`, returns the resolved uid. Returns
// (0, false) on any error: missing file (legacy two-drive image),
// malformed file (build error), missing entry (named user not in
// the image's /etc/passwd), or read failure.
//
// Thread-safety: each call opens + reads + closes the file.
// lookupUID is called once per guest boot so the cost is
// bounded; mmap would be premature.
func readPasswdTable(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	body, err := os.ReadFile("/etc/faas/app_passwd")
	if err != nil {
		return 0, false
	}
	return searchPasswdTable(body, name)
}

// lookupUIDInRoot resolves a named user from an OCI image's own /etc/passwd.
// Full-rootfs sidecars run with their image root chrooted, so using the main
// image's /etc/faas/app_passwd table would resolve names against the wrong
// filesystem. Numeric UIDs use the same [0, 65534] trust boundary as the
// builder's ownership resolver. A missing, malformed, or out-of-range image
// passwd entry falls back to the platform default, matching lookupUID's
// legacy behavior.
func lookupUIDInRoot(root, name string) int {
	if name == api.DefaultAppUser {
		return api.DefaultAppUID
	}
	if uid, ok := numericUID(name); ok {
		return uid
	}
	if uid, ok := readImagePasswd(root, name); ok {
		return uid
	}
	return api.DefaultAppUID
}

func numericUID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	uid, err := strconv.ParseUint(name, 10, 32)
	if err != nil || uid > 65534 {
		return 0, false
	}
	return int(uid), true
}

func readImagePasswd(root, name string) (int, bool) {
	if root == "" {
		root = "/"
	}
	passwdPath, err := safeRootPath(root, filepath.Join("etc", "passwd"))
	if err != nil {
		return 0, false
	}
	//nolint:forbidigo // root is a guest-local, builder-mounted sidecar image.
	f, err := os.Open(passwdPath)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxImagePasswdBytes+1))
	if err != nil || len(body) > maxImagePasswdBytes {
		return 0, false
	}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 || fields[0] != name {
			continue
		}
		uid, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil || uid > 65534 {
			return 0, false
		}
		return int(uid), true
	}
	return 0, false
}

// searchPasswdTable is the binary-search core, separated from
// the file I/O so the table-lookup logic is testable without a
// /etc/faas mount. `body` must be the file contents; `name` is
// the lookup key. Returns (uid, true) on hit; (0, false) on
// miss or any parse error.
//
// Malformed records (length out of range, body shorter than the
// declared record size, name field that exceeds the body length)
// cause the search to abort and return (0, false) — same shape
// as a missing entry so the caller falls back to DefaultAppUID
// without distinguishing "user truly not in the table" from
// "table is corrupt". Both degrade to today's behavior.
func searchPasswdTable(body []byte, name string) (int, bool) {
	const recordHeader = 9 // 4 + 4 + 1
	if name == "" {
		return 0, false
	}

	// Build record boundaries before searching. A byte offset in the
	// middle of a variable-length record cannot be used as a binary-search
	// midpoint: advancing it to the next boundary can leave the search
	// interval unchanged and loop forever. The builder caps this table at
	// 256 entries, so this bounded index is cheap and keeps malformed input
	// fail-closed.
	type record struct {
		off  int
		name []byte
	}
	records := make([]record, 0, 16)
	for off := 0; off < len(body); {
		if off+recordHeader > len(body) {
			return 0, false
		}
		nameLen := int(body[off+8])
		end := off + recordHeader + nameLen
		if end > len(body) {
			return 0, false
		}
		records = append(records, record{off: off, name: body[off+9 : end]})
		off = end
	}
	if len(records) == 0 {
		return 0, false
	}

	key := []byte(name)
	i := sort.Search(len(records), func(i int) bool {
		return bytes.Compare(records[i].name, key) >= 0
	})
	if i >= len(records) || !bytes.Equal(records[i].name, key) {
		return 0, false
	}
	return int(binary.BigEndian.Uint32(body[records[i].off : records[i].off+4])), true
}
