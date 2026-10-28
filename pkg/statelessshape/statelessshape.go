// Package statelessshape decides whether a source tree carries a database
// store. A scale-to-zero microVM loses local writes when it parks, so a
// database file shipped with the source is data the customer will lose
// (spec §17 G13). The check is shared by apid's accept-time scan and the
// CLI's doctor preflight so both sides agree on what is refused.
//
// Only database-store files under a top-level data/ or db/ directory are
// refused (ADR-20261026152731). Source that merely lives in those
// directories, such as db/migrations/*.sql, db/schema.ts or data/seed.json,
// is ordinary stateless code.
package statelessshape

import (
	"bytes"
	"path"
	"strings"
)

// HeaderBytes is how many leading bytes of a file DatabaseFile needs to
// recognise a store by its signature.
const HeaderBytes = 16

// sqliteMagic opens every SQLite 3 database file, whatever its name.
var sqliteMagic = []byte("SQLite format 3\x00")

// storeExtensions are file extensions written by embedded or server
// databases. A SQLite companion file (-wal, -shm, -journal) is matched
// through the extension of the database it belongs to.
var storeExtensions = map[string]bool{
	".sqlite": true, ".sqlite3": true, ".db": true, ".db3": true,
	".s3db": true, ".sl3": true, ".duckdb": true, ".mdb": true,
	".accdb": true, ".rdb": true, ".aof": true, ".wt": true,
	".ibd": true, ".myd": true, ".myi": true,
}

// storeNames are files that only appear inside a server database's data
// directory (PostgreSQL, MySQL/InnoDB, MongoDB/WiredTiger, Redis).
var storeNames = map[string]bool{
	"pg_version": true, "postmaster.pid": true, "postmaster.opts": true,
	"ibdata1": true, "ib_logfile0": true, "ib_logfile1": true,
	"wiredtiger": true, "wiredtiger.wt": true, "mongod.lock": true,
	"dump.rdb": true, "appendonly.aof": true,
}

// IsStateDir reports whether a source-root directory name is one whose
// database-store contents mark a deploy as stateful.
func IsStateDir(name string) bool {
	return name == "data" || name == "db"
}

// StateDirOf returns the state directory that rel (slash-separated, relative
// to the selected source root) sits under, or "" when it is elsewhere.
func StateDirOf(rel string) string {
	first, rest, ok := strings.Cut(strings.TrimPrefix(rel, "./"), "/")
	if !ok || rest == "" || !IsStateDir(first) {
		return ""
	}
	return first
}

// DatabaseFile reports whether the regular file rel under a state directory
// is a database store, judged by its name or by header (its first
// HeaderBytes bytes; a shorter or nil header only disables the signature
// test).
func DatabaseFile(rel string, header []byte) bool {
	if StateDirOf(rel) == "" {
		return false
	}
	base := strings.ToLower(path.Base(rel))
	if storeNames[base] {
		return true
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if storeExtensions[path.Ext(base)] {
		return true
	}
	return bytes.HasPrefix(header, sqliteMagic)
}

// Reason is the customer-facing explanation for a refused file.
func Reason(rel string) string {
	return rel + " is a database file inside " + StateDirOf(rel) +
		"/ — this platform is stateless and loses local writes when an app parks; keep data in managed Postgres or object storage, or remove the file from the source"
}
