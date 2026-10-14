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

// importRestoreArgs restores the filtered dump into target in one
// transaction as owner, so owner owns every object and a failure leaves
// target as it was.
func importRestoreArgs(major int, target, owner string) []string {
	return []string{
		databaseBinDir(major) + "/pg_restore",
		"--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges",
		"--role=" + owner,
		"--use-list=" + databaseImportList,
		"-h", databaseSocketDir, "-U", databaseOSUser,
		"--dbname=" + target,
		databaseImportDump,
	}
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
