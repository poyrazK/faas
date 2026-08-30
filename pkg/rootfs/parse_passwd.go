package rootfs

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Resolver maps a named user (from a tar header's Uname field) to
// the (uid, gid) the chown path should apply (ADR-142 §Decision 1).
// Implementations are read-only and thread-safe; the full-rootfs
// build constructs one PasswdResolver from the image's merged
// /etc/passwd table.
//
// Resolve returns (uid, gid, true) when the name is known,
// (0, 0, false) otherwise. The fallback path's caller (preserveOwnership)
// is responsible for incrementing the unparseable_uid counter on miss
// and applying the existing inOwnershipRange clamp (ADR-136) to the
// returned uid/gid — Resolver does not enforce the [0, 65534] cap
// itself; out-of-range values are the caller's problem.
type Resolver interface {
	Resolve(name string) (uid, gid int, ok bool)
}

// PasswdEntry is one row in a parsed /etc/passwd table
// (ADR-142 §Decision 2). The standard 7-field colon-separated
// format: name:password:uid:gid:gecos:home:shell — only Name, Uid,
// Gid are projected; the rest are dropped (we never write a
// shadow record back to the image).
type PasswdEntry struct {
	Name string
	Uid  int
	Gid  int
}

// ParsePasswd parses a single /etc/passwd body. Empty lines and
// comments (`#`-prefixed) are skipped. Standard NSS `+`-entries are
// skipped (we don't model NSS sources — a hostile image declaring
// `+::::::` must not bypass our uid clamp). Returns the parsed
// map; an invalid uid/gid on a non-comment line is a hard error
// so a malicious image declaring uid=999999999 fails fast at build
// time rather than silently failing at runtime.
//
// ADR-142 §Decision 2: the parser is the image-shape input to
// PasswdResolver; the binary /etc/faas/app_passwd table the
// builder writes is a sorted, compact view of the same data.
func ParsePasswd(r io.Reader) (map[string]PasswdEntry, error) {
	out := make(map[string]PasswdEntry)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "+") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) != 7 {
			return nil, fmt.Errorf("rootfs: parse_passwd: malformed line %q (want 7 colon-separated fields)", line)
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("rootfs: parse_passwd: bad uid in line %q: %w", line, err)
		}
		gid, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, fmt.Errorf("rootfs: parse_passwd: bad gid in line %q: %w", line, err)
		}
		out[fields[0]] = PasswdEntry{Name: fields[0], Uid: uid, Gid: gid}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("rootfs: parse_passwd: read: %w", err)
	}
	return out, nil
}
