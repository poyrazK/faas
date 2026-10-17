package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

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
// transaction the script's own COMMIT ends, and gateCommit writes the
// epilogue (the source's PUBLIC privileges and the import's lineage) just
// before that COMMIT, so a finished copy cannot exist without its
// operation ID, and a failure anywhere (including a script cut short,
// whose COMMIT is withheld) leaves the target as it was.

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

// importScriptPrologue opens the restore's transaction and makes owner own
// what the script creates. The script's own BEGIN only warns inside it.
func importScriptPrologue(owner string) string {
	return fmt.Sprintf("BEGIN;\nSET ROLE %s;\n", owner)
}

// importScriptEpilogue runs just before the script's COMMIT (gateCommit):
// the source's PUBLIC privileges, then the import's lineage on target, so
// both commit with the copy and only with it.
func importScriptEpilogue(target, operationID string, publicACL []string) string {
	var b strings.Builder
	for _, statement := range publicACL {
		b.WriteString(statement)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "COMMENT ON DATABASE %s IS '%s%s';\n", target, importLineagePrefix, operationID)
	return b.String()
}

// errScriptUnfinished is a script that ended before pg_restore's end
// marker: it was cut short, so its COMMIT is withheld.
var errScriptUnfinished = errors.New("the restore script ended before its end marker")

// gateCommit copies pg_restore's script from src to dst, holding back its
// final COMMIT until the end marker (\unrestrict with this run's key,
// which no dump can contain) arrives, and writing epilogue just before
// that COMMIT. pg_restore --single-transaction ends its script with
// "COMMIT;", comment or blank lines, and the marker. A "COMMIT;" line
// followed by anything else (a COPY row, say) is passed on unchanged. A
// script that ends without the marker never gets its COMMIT, so psql's
// transaction rolls back.
func gateCommit(dst io.Writer, src io.Reader, key, epilogue string) error {
	marker := []byte(`\unrestrict ` + key)
	in := bufio.NewReaderSize(src, 64<<10)
	var held [][]byte
	flush := func() error {
		for _, line := range held {
			if _, err := dst.Write(line); err != nil {
				return err
			}
		}
		held = held[:0]
		return nil
	}
	for {
		line, err := in.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A long line is never COMMIT, a comment we hold, or the
			// marker: pass it on in pieces.
			if ferr := flush(); ferr != nil {
				return ferr
			}
			for errors.Is(err, bufio.ErrBufferFull) {
				if _, werr := dst.Write(line); werr != nil {
					return werr
				}
				line, err = in.ReadSlice('\n')
			}
			if _, werr := dst.Write(line); werr != nil {
				return werr
			}
			if err != nil {
				return scriptEnd(err)
			}
			continue
		}
		if len(line) > 0 {
			text := bytes.TrimRight(line, "\r\n")
			switch {
			case bytes.Equal(text, []byte("COMMIT;")):
				if ferr := flush(); ferr != nil {
					return ferr
				}
				held = append(held, slices.Clone(line))
			case len(held) > 0 && (len(text) == 0 || bytes.HasPrefix(text, []byte("--"))):
				held = append(held, slices.Clone(line))
			case len(held) > 0 && bytes.Equal(text, marker):
				if _, werr := io.WriteString(dst, epilogue); werr != nil {
					return werr
				}
				if ferr := flush(); ferr != nil {
					return ferr
				}
				if _, werr := dst.Write(line); werr != nil {
					return werr
				}
				_, cerr := io.Copy(dst, in)
				return cerr
			default:
				if ferr := flush(); ferr != nil {
					return ferr
				}
				if _, werr := dst.Write(line); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			return scriptEnd(err)
		}
	}
}

func scriptEnd(err error) error {
	if errors.Is(err, io.EOF) {
		return errScriptUnfinished
	}
	return err
}

// The source's PUBLIC privileges. pg_dump runs with --no-privileges,
// because its grants name the source's roles, which the target does not
// have. Grants to PUBLIC are portable, and on Gregale they are how an app
// reaches anything its runtime credential is not granted, such as a
// function (both drivers revoke EXECUTE on new functions from PUBLIC). So
// the import carries them over: for every user object with explicit
// privileges, and every routine (whose default on the target differs from
// PostgreSQL's), PUBLIC ends with exactly the source's privileges.
//
// Only privileges that read, write or run are carried; never CREATE, nor
// TRIGGER or MAINTAIN, so PUBLIC (every runtime credential) still cannot
// create objects. Extension members are left to their extension. The
// statements come from the source, at the dump's trust: they run as the
// owner, in psql's restricted mode, inside the restore's transaction.
const importPublicACLQuery = `SET search_path = '';
WITH obj AS (
  SELECT CASE c.relkind WHEN 'S' THEN 'SEQUENCE' ELSE 'TABLE' END AS kind, c.oid::regclass::text AS ident,
         coalesce(c.relacl, acldefault(CASE c.relkind WHEN 'S' THEN 's' ELSE 'r' END::"char", c.relowner)) AS acl,
         c.relacl IS NOT NULL AS explicit, 'pg_catalog.pg_class'::regclass AS cls, c.oid
    FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
   WHERE c.relkind IN ('r', 'p', 'v', 'm', 'S') AND n.nspname <> 'information_schema' AND n.nspname !~ '^pg_'
  UNION ALL
  SELECT 'ROUTINE', p.oid::regprocedure::text, coalesce(p.proacl, acldefault('f', p.proowner)), true, 'pg_catalog.pg_proc'::regclass, p.oid
    FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
   WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_'
  UNION ALL
  SELECT 'SCHEMA', pg_catalog.quote_ident(n.nspname), coalesce(n.nspacl, acldefault('n', n.nspowner)), n.nspacl IS NOT NULL,
         'pg_catalog.pg_namespace'::regclass, n.oid
    FROM pg_catalog.pg_namespace n
   WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_'
  UNION ALL
  SELECT 'TYPE', pg_catalog.format_type(t.oid, NULL), coalesce(t.typacl, acldefault('T', t.typowner)), t.typacl IS NOT NULL,
         'pg_catalog.pg_type'::regclass, t.oid
    FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
   WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_'
     AND (t.typrelid = 0 OR (SELECT r.relkind FROM pg_catalog.pg_class r WHERE r.oid = t.typrelid) = 'c')
     AND NOT EXISTS (SELECT FROM pg_catalog.pg_type e WHERE e.typarray = t.oid)
), keep AS (
  SELECT o.kind, o.ident,
         (SELECT pg_catalog.string_agg(DISTINCT a.privilege_type, ', ' ORDER BY a.privilege_type) FROM pg_catalog.aclexplode(o.acl) a
           WHERE a.grantee = 0 AND a.privilege_type = ANY (CASE o.kind
             WHEN 'TABLE' THEN ARRAY['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES']
             WHEN 'SEQUENCE' THEN ARRAY['USAGE', 'SELECT', 'UPDATE']
             WHEN 'ROUTINE' THEN ARRAY['EXECUTE']
             ELSE ARRAY['USAGE'] END)) AS privs
    FROM obj o
   WHERE o.explicit
     AND NOT EXISTS (SELECT FROM pg_catalog.pg_depend d WHERE d.classid = o.cls AND d.objid = o.oid AND d.deptype = 'e')
)
SELECT v.statement FROM keep k, LATERAL (VALUES
  (1, 'REVOKE ALL ON ' || k.kind || ' ' || k.ident || ' FROM PUBLIC;'),
  (2, CASE WHEN k.privs IS NOT NULL THEN 'GRANT ' || k.privs || ' ON ' || k.kind || ' ' || k.ident || ' TO PUBLIC;' END)) AS v(step, statement)
WHERE v.statement IS NOT NULL
ORDER BY k.kind, k.ident, v.step`

// publicACLStatement is one statement importPublicACLQuery produces.
var publicACLStatement = regexp.MustCompile(`(?s)^(?:REVOKE ALL ON (?:TABLE|SEQUENCE|ROUTINE|SCHEMA|TYPE) .+ FROM PUBLIC|GRANT (?:SELECT|INSERT|UPDATE|DELETE|TRUNCATE|REFERENCES|USAGE|EXECUTE)(?:, (?:SELECT|INSERT|UPDATE|DELETE|TRUNCATE|REFERENCES|USAGE|EXECUTE))* ON (?:TABLE|SEQUENCE|ROUTINE|SCHEMA|TYPE) .+ TO PUBLIC);$`)

// maxPublicACLBytes bounds what the source may return for its privileges.
const maxPublicACLBytes = 64 << 20

// publicACLStatements splits importPublicACLQuery's NUL-separated output
// and refuses anything that is not one of its statements.
func publicACLStatements(out []byte) ([]string, error) {
	var statements []string
	for _, record := range bytes.Split(out, []byte{0}) {
		statement := strings.TrimRight(string(record), "\n")
		if statement == "" {
			continue
		}
		if !publicACLStatement.MatchString(statement) {
			return nil, fmt.Errorf("source privileges: unexpected statement %.80q", statement)
		}
		statements = append(statements, statement)
	}
	return statements, nil
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
	importSourceSizeQuery  = `SELECT pg_catalog.pg_database_size(pg_catalog.current_database())`
	importTargetUsedQuery  = `SELECT (SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%')
 + (SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname NOT IN ('public', 'pg_catalog', 'information_schema') AND nspname NOT LIKE 'pg\_%')
 + (SELECT count(*) FROM pg_catalog.pg_extension WHERE extname <> 'plpgsql')`
)

// The dump and the restored copy share the target volume, and a full
// volume stops PostgreSQL itself. An import starts only when the source
// fits beside the floor, and is stopped if free space falls to the floor
// anyway.

// importSpaceFloor is the free space an import always leaves on a volume
// of total bytes, so the database can still write WAL and roll the
// restore back: 5%, at least 128 MiB.
func importSpaceFloor(total uint64) uint64 { return max(total/20, 128<<20) }

// importSpaceNeeded is the free space importing a source of sourceBytes
// needs over the floor: twice the source. Just before the restore commits
// the volume holds the restored copy (about the source's size: the dump
// has no bloat, and indexes are rebuilt), the dump beside it (compressed
// table data, well under the source), and the restore's WAL and index
// build files (WAL is recycled past max_wal_size once archived; a build
// spills at most its table).
func importSpaceNeeded(sourceBytes, floor uint64) uint64 {
	return 2*sourceBytes + floor
}

// errImportVolumeFull stops a copy that brought free space to the floor.
var errImportVolumeFull = errors.New("the target volume reached its free-space floor during the copy")

// watchImportSpace stops the copy, with errImportVolumeFull, once free
// reports no more than floor; it checks every interval until ctx ends. A
// failed probe is retried on the next tick.
func watchImportSpace(ctx context.Context, stop context.CancelCauseFunc, floor uint64, interval time.Duration, free func() (uint64, error)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if left, err := free(); err == nil && left <= floor {
			stop(errImportVolumeFull)
			return
		}
	}
}

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
