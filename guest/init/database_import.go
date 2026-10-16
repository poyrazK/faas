package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Importing another PostgreSQL database (ADR-1027 migration off Neon). The
// guest dumps the source through a socket guest-init serves: each
// connection goes to the host's import relay, which reaches the source over
// TLS it verifies itself. The guest sends plaintext and never learns where
// the source is.
const (
	databaseImportSocketDir = "/run/gregale-import"
	// databaseImportDump holds the dump on the volume, outside the cluster.
	databaseImportDump = databaseMountPoint + "/import.dump"
	databaseImportList = databaseMountPoint + "/import.list"
)

// importConnString reaches the source through the import socket. Every
// value is a validated identifier; the password travels in PGPASSWORD.
func importConnString(src dbguest.ImportSource) string {
	return fmt.Sprintf("host=%s port=5432 dbname=%s user=%s sslmode=disable connect_timeout=30 application_name=gregale_import",
		databaseImportSocketDir, src.Database, src.User)
}

// importDumpArgs takes a consistent custom-format dump of the source
// without ownership, grants or the objects a non-superuser cannot restore.
func importDumpArgs(major int, conn string) []string {
	return []string{
		databaseBinDir(major) + "/pg_dump",
		"--format=custom", "--no-owner", "--no-privileges", "--no-publications", "--no-subscriptions",
		"--no-security-labels", "--no-tablespaces",
		"--file=" + databaseImportDump,
		"--dbname=" + conn,
	}
}

// The restore is pg_restore writing the filtered dump as SQL in one
// transaction, run by psql as databaseImportRole. The script comes from
// whatever server the source is, so nothing in it may act beyond the
// target's owner: the session is never the superuser (a RESET ROLE in the
// script lands on databaseImportRole, a member of the owner and nothing
// more), and pg_restore's \restrict key, which the dump never sees, makes
// psql refuse every meta-command in the script. The prologue opens the
// transaction the script's own COMMIT ends and records the import's
// lineage in it, so a finished copy cannot exist without its operation ID
// and a failure anywhere (a script that stops short never reaches its
// COMMIT) leaves the target as it was.

// databaseImportRole is the role an import's restore runs as: LOGIN, no
// attributes, and a member of the target's owner. Only the postgres OS
// user reaches it (databaseIdent), and that user is the superuser anyway.
const databaseImportRole = "gregale_import"

// importRoleStatement creates databaseImportRole and makes it a member of
// owner, which it refuses to be when owner holds any role attribute.
func importRoleStatement(owner string) string {
	return fmt.Sprintf(`DO $gregale$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '%[1]s') THEN
    CREATE ROLE %[1]s WITH LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '%[2]s'
      AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls) THEN
    RAISE EXCEPTION 'gregale: import owner %[2]s is missing or privileged';
  END IF;
  GRANT %[2]s TO %[1]s;
END $gregale$`, databaseImportRole, owner)
}

// importScriptArgs writes the filtered dump as one transaction of SQL to
// stdout, behind restrictKey.
func importScriptArgs(major int, restrictKey string) []string {
	return []string{
		databaseBinDir(major) + "/pg_restore",
		"--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges",
		"--restrict-key=" + restrictKey,
		"--use-list=" + databaseImportList,
		"--file=-",
		databaseImportDump,
	}
}

// importApplyArgs runs a script from stdin in target as databaseImportRole,
// stopping at its first error.
func importApplyArgs(major int, target string) []string {
	return []string{
		databaseBinDir(major) + "/psql",
		"-X", "-q", "-w", "-v", "ON_ERROR_STOP=1",
		"-h", databaseSocketDir, "-U", databaseImportRole,
		"-d", target, "-f", "-",
	}
}

// importScriptPrologue opens the restore's transaction, makes owner own
// what the script creates, and records operationID on target. The script's
// own BEGIN only warns inside it.
func importScriptPrologue(target, owner, operationID string) string {
	return fmt.Sprintf("BEGIN;\nSET ROLE %s;\nCOMMENT ON DATABASE %s IS '%s%s';\n", owner, target, importLineagePrefix, operationID)
}

// importLineagePrefix starts the target database's comment that records a
// finished import. pg_dump leaves database comments out (without
// --create), so the record never travels into another copy.
const importLineagePrefix = "gregale:import:v1:"

// importLineage reads a recorded operation ID from a database comment.
func importLineage(comment string) (string, bool) {
	id, ok := strings.CutPrefix(strings.TrimSpace(comment), importLineagePrefix)
	if !ok || !dbguest.ValidOperationID(id) {
		return "", false
	}
	return id, true
}

// importLineageQuery lists recorded imports, one "database|comment" per
// line.
const importLineageQuery = `SELECT datname || '|' || shobj_description(oid, 'pg_database') FROM pg_catalog.pg_database
 WHERE shobj_description(oid, 'pg_database') LIKE 'gregale:import:v1:%'`

// recordedImports parses importLineageQuery's output.
func recordedImports(out string) map[string]string {
	found := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		database, comment, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		if id, ok := importLineage(comment); ok {
			found[database] = id
		}
	}
	return found
}

// tocExtensionEntry matches an extension's TOC lines in pg_restore -l
// output: the extension and its comment.
var tocExtensionEntry = regexp.MustCompile(`^\d+; \d+ \d+ (?:EXTENSION - (\S+)|COMMENT - EXTENSION (\S+))(?:\s|$)`)

// filterImportTOC drops plpgsql, which the target has (owned by the
// superuser, so its comment cannot be restored as owner), and the skipped
// extensions.
func filterImportTOC(list string, skip []string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(list, "\n") {
		if m := tocExtensionEntry.FindStringSubmatch(strings.TrimRight(line, "\n")); m != nil {
			name := m[1] + m[2]
			if name == "plpgsql" || slices.Contains(skip, name) {
				continue
			}
		}
		b.WriteString(line)
	}
	return b.String()
}

// Import checks, as psql -tA queries.
const (
	importSourceMajorQuery = `SELECT current_setting('server_version_num')::int / 10000`
	importExtensionsQuery  = `SELECT coalesce(string_agg(extname, ',' ORDER BY extname), '') FROM pg_catalog.pg_extension WHERE extname <> 'plpgsql'`
	importTrustedQuery     = `SELECT coalesce(string_agg(DISTINCT name, ','), '') FROM pg_catalog.pg_available_extension_versions WHERE trusted`
	importTargetUsedQuery  = `SELECT (SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%')
 + (SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname NOT IN ('public', 'pg_catalog', 'information_schema') AND nspname NOT LIKE 'pg\_%')
 + (SELECT count(*) FROM pg_catalog.pg_extension WHERE extname <> 'plpgsql')`
)

// missingExtensions lists the source's extensions, less the skipped ones,
// that the target cannot create as a non-superuser (not trusted, or not
// installed). Both lists are comma-separated.
func missingExtensions(source, trusted string, skip []string) []string {
	have := strings.Split(trusted, ",")
	var missing []string
	for _, ext := range strings.Split(source, ",") {
		if ext == "" || slices.Contains(skip, ext) || slices.Contains(have, ext) {
			continue
		}
		missing = append(missing, ext)
	}
	return missing
}
