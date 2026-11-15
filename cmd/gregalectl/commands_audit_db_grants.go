// commands_audit_db_grants.go — read-only evidence for narrowing each daemon
// role's table grants (ADR-20261023104734 step 3).
//
// For every database role that ran statements against the faas database, the
// report lists the tables those statements read and wrote, taken from
// pg_stat_statements, next to the privileges the role holds today. A table
// the role is granted but never touched is a narrowing candidate; a table it
// touched without a grant should not exist. Nothing is changed.
//
// The table extraction reads the statement text rather than parsing it (the
// SQL parser in go.mod needs cgo, and release builds are static). It is
// deliberately generous: any identifier that names a public table counts as a
// read, so a narrowed grant built from this report errs towards granting.
// It cannot see tables written inside functions or triggers, and
// pg_stat_statements keeps only its most frequent statements, so the ADR
// unions this with the tables each binary's queries name and ships narrowed
// grants behind a switch with a permission-denied alert.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const auditDBGrantsSub = "db-grants"

// Privileges a statement can need on a table, in report order.
const (
	privSelect = "SELECT"
	privInsert = "INSERT"
	privUpdate = "UPDATE"
	privDelete = "DELETE"
)

var dbGrantPrivileges = []string{privSelect, privInsert, privUpdate, privDelete}

// dbGrantTablesSQL lists the faas database's public tables and partitioned
// tables; partitions themselves are reached through their parent.
const dbGrantTablesSQL = `
SELECT c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm') AND NOT c.relispartition
ORDER BY c.relname`

// dbGrantRolesSQL lists the login roles a daemon can use: every non-superuser
// login except the database owner and the operator running the report.
const dbGrantRolesSQL = `
SELECT r.rolname
FROM pg_roles r
WHERE r.rolcanlogin AND NOT r.rolsuper AND r.rolname <> current_user
  AND NOT EXISTS (SELECT 1 FROM pg_database d WHERE d.datname = current_database() AND d.datdba = r.oid)
ORDER BY r.rolname`

// dbGrantPrivilegesSQL returns every privilege a role holds on a public
// table, directly or through membership.
const dbGrantPrivilegesSQL = `
SELECT c.relname, p.priv
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN unnest(ARRAY['SELECT', 'INSERT', 'UPDATE', 'DELETE']) AS p(priv)
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm') AND NOT c.relispartition
  AND has_table_privilege($1, c.oid, p.priv)`

// dbGrantStatementsSQL reads pg_stat_statements in the maintenance database,
// which records statements from every database, keeping the faas database's.
const dbGrantStatementsSQL = `
SELECT r.rolname, s.query, s.calls
FROM pg_stat_statements s
JOIN pg_roles r ON r.oid = s.userid
JOIN pg_database d ON d.oid = s.dbid
WHERE d.datname = $1`

type dbGrantStatement struct {
	Role  string
	Query string
	Calls int64
}

type dbGrantTable struct {
	Table      string   `json:"table"`
	Observed   []string `json:"observed,omitempty"`
	Granted    []string `json:"granted,omitempty"`
	Unused     []string `json:"unused,omitempty"`
	Ungranted  []string `json:"ungranted,omitempty"`
	Statements int      `json:"statements,omitempty"`
	Calls      int64    `json:"calls,omitempty"`
}

type dbGrantRole struct {
	Role       string         `json:"role"`
	Statements int            `json:"statements"`
	Calls      int64          `json:"calls"`
	Tables     []dbGrantTable `json:"tables"`
	// GrantedTables and ObservedTables count tables with any privilege.
	GrantedTables  int `json:"granted_tables"`
	ObservedTables int `json:"observed_tables"`
	// UnusedGrants is the number of (table, privilege) pairs granted but
	// never observed: what narrowing would remove.
	UnusedGrants int `json:"unused_grants"`
	// UngrantedUses must be zero; a non-zero count means the role ran a
	// statement it no longer has the grant for, or the extraction over-read.
	UngrantedUses int `json:"ungranted_uses"`
}

type dbGrantReport struct {
	Database string        `json:"database"`
	Roles    []dbGrantRole `json:"roles"`
	Caveats  []string      `json:"caveats"`
}

var dbGrantCaveats = []string{
	"pg_stat_statements keeps only its most frequent statements: a table missing from observed may still be used on a rare path",
	"tables written inside functions and triggers are not seen",
	"any identifier naming a table counts as a read, so observed may include a table the statement does not read",
	"sequences are not reported: a narrowed INSERT grant also needs USAGE on the table's sequences",
}

func cmdAuditDBGrants(args []string) int {
	fs := flag.NewFlagSet(auditDBGrantsSub, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	dsn := fs.String("pg-dsn", "", "faas database DSN (default: $FAAS_PG_DSN or $DATABASE_URL)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	statsDSN := fs.String("stats-dsn", "", "DSN of the database holding pg_stat_statements (default: --pg-dsn with dbname=postgres)")
	roles := fs.String("role", "", "comma-separated roles to report (default: every runtime login role)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit db-grants: positional arguments are not accepted")
		return 2
	}
	ctx := context.Background()
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit db-grants:", err)
		return 3
	}
	defer pool.Close()
	stats, err := openDBGrantStatsPool(pool, *statsDSN)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit db-grants:", err)
		return 3
	}
	defer stats.Close()
	report, err := collectDBGrantReport(ctx, pool, stats, splitDBGrantRoles(*roles))
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit db-grants:", err)
		return 3
	}
	if err := emitDBGrantReport(osStdout, report, jsonOutput); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit db-grants:", err)
		return 3
	}
	return 0
}

func splitDBGrantRoles(raw string) []string {
	var out []string
	for _, role := range strings.Split(raw, ",") {
		if role = strings.TrimSpace(role); role != "" {
			out = append(out, role)
		}
	}
	return out
}

// openDBGrantStatsPool opens the database holding pg_stat_statements: the
// explicit DSN, or the faas DSN pointed at the postgres maintenance database.
func openDBGrantStatsPool(faas *pgxpool.Pool, explicit string) (*pgxpool.Pool, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return openPgPoolFromDSN(explicit)
	}
	cfg := faas.Config().Copy()
	cfg.ConnConfig.Database = "postgres"
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("open pg_stat_statements database: %w", err)
	}
	return pool, nil
}

func collectDBGrantReport(ctx context.Context, pool, stats *pgxpool.Pool, onlyRoles []string) (dbGrantReport, error) {
	database := pool.Config().ConnConfig.Database
	tables, err := queryStrings(ctx, pool, dbGrantTablesSQL)
	if err != nil {
		return dbGrantReport{}, fmt.Errorf("list tables: %w", err)
	}
	roles := onlyRoles
	if len(roles) == 0 {
		if roles, err = queryStrings(ctx, pool, dbGrantRolesSQL); err != nil {
			return dbGrantReport{}, fmt.Errorf("list roles: %w", err)
		}
	}
	granted := map[string]map[string]map[string]bool{}
	for _, role := range roles {
		rows, err := pool.Query(ctx, dbGrantPrivilegesSQL, role)
		if err != nil {
			return dbGrantReport{}, fmt.Errorf("privileges of %s: %w", role, err)
		}
		privs := map[string]map[string]bool{}
		for rows.Next() {
			var table, priv string
			if err := rows.Scan(&table, &priv); err != nil {
				rows.Close()
				return dbGrantReport{}, fmt.Errorf("privileges of %s: %w", role, err)
			}
			if privs[table] == nil {
				privs[table] = map[string]bool{}
			}
			privs[table][priv] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return dbGrantReport{}, fmt.Errorf("privileges of %s: %w", role, err)
		}
		granted[role] = privs
	}
	statements, err := queryDBGrantStatements(ctx, stats, database)
	if err != nil {
		return dbGrantReport{}, fmt.Errorf("read pg_stat_statements (needs pg_read_all_stats or superuser): %w", err)
	}
	return buildDBGrantReport(database, tables, roles, granted, statements), nil
}

func queryStrings(ctx context.Context, pool *pgxpool.Pool, sql string) ([]string, error) {
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func queryDBGrantStatements(ctx context.Context, pool *pgxpool.Pool, database string) ([]dbGrantStatement, error) {
	rows, err := pool.Query(ctx, dbGrantStatementsSQL, database)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbGrantStatement
	for rows.Next() {
		var s dbGrantStatement
		if err := rows.Scan(&s.Role, &s.Query, &s.Calls); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// buildDBGrantReport joins the observed accesses with the held privileges.
// granted maps role → table → privilege.
func buildDBGrantReport(database string, tables, roles []string, granted map[string]map[string]map[string]bool, statements []dbGrantStatement) dbGrantReport {
	known := make(map[string]bool, len(tables))
	for _, t := range tables {
		known[t] = true
	}
	type use struct {
		privs      map[string]bool
		statements int
		calls      int64
	}
	observed := map[string]map[string]*use{}
	totals := map[string][2]int64{}
	for _, s := range statements {
		access := statementTableAccess(s.Query, known)
		t := totals[s.Role]
		t[0]++
		t[1] += s.Calls
		totals[s.Role] = t
		for table, privs := range access {
			if observed[s.Role] == nil {
				observed[s.Role] = map[string]*use{}
			}
			u := observed[s.Role][table]
			if u == nil {
				u = &use{privs: map[string]bool{}}
				observed[s.Role][table] = u
			}
			for p := range privs {
				u.privs[p] = true
			}
			u.statements++
			u.calls += s.Calls
		}
	}
	report := dbGrantReport{Database: database, Caveats: dbGrantCaveats}
	for _, role := range roles {
		r := dbGrantRole{Role: role, Statements: int(totals[role][0]), Calls: totals[role][1]}
		names := map[string]bool{}
		for t := range granted[role] {
			names[t] = true
		}
		for t := range observed[role] {
			names[t] = true
		}
		sorted := make([]string, 0, len(names))
		for t := range names {
			sorted = append(sorted, t)
		}
		sort.Strings(sorted)
		for _, table := range sorted {
			held := granted[role][table]
			var seen map[string]bool
			row := dbGrantTable{Table: table}
			if u := observed[role][table]; u != nil {
				seen = u.privs
				row.Statements, row.Calls = u.statements, u.calls
				r.ObservedTables++
			}
			if len(held) > 0 {
				r.GrantedTables++
			}
			for _, p := range dbGrantPrivileges {
				switch {
				case held[p] && seen[p]:
					row.Granted = append(row.Granted, p)
					row.Observed = append(row.Observed, p)
				case held[p]:
					row.Granted = append(row.Granted, p)
					row.Unused = append(row.Unused, p)
				case seen[p]:
					row.Observed = append(row.Observed, p)
					row.Ungranted = append(row.Ungranted, p)
				}
			}
			r.UnusedGrants += len(row.Unused)
			r.UngrantedUses += len(row.Ungranted)
			r.Tables = append(r.Tables, row)
		}
		report.Roles = append(report.Roles, r)
	}
	return report
}

var (
	// A table name, optionally schema-qualified and quoted.
	sqlIdent = `(?:"?public"?\s*\.\s*)?"?([a-z_][a-z0-9_$]*)"?`
	// Write targets: the table named right after these keywords.
	sqlInsertTarget = regexp.MustCompile(`(?i)\binsert\s+into\s+` + sqlIdent)
	sqlUpdateTarget = regexp.MustCompile(`(?i)\bupdate\s+(?:only\s+)?` + sqlIdent)
	sqlDeleteTarget = regexp.MustCompile(`(?i)\bdelete\s+from\s+(?:only\s+)?` + sqlIdent)
	sqlMergeTarget  = regexp.MustCompile(`(?i)\bmerge\s+into\s+` + sqlIdent)
	sqlUpsert       = regexp.MustCompile(`(?i)\bon\s+conflict\b[^;]*?\bdo\s+update\b`)
	sqlRowLock      = regexp.MustCompile(`(?i)\bfor\s+(?:no\s+key\s+)?update\b|\bfor\s+(?:key\s+)?share\b`)
	sqlWord         = regexp.MustCompile(`"([^"]+)"|([A-Za-z_][A-Za-z0-9_$]*)`)
	// String literals and comments carry no table references.
	sqlNoise = regexp.MustCompile(`(?s)'(?:[^']|'')*'|--[^\n]*|/\*.*?\*/`)
)

// statementTableAccess returns, for one statement, each known table it names
// and the privileges that needs. Every named table needs SELECT (a WHERE,
// RETURNING or join reads it); write targets add their verb; an upsert needs
// UPDATE on its insert target; a row lock (FOR UPDATE/SHARE) needs UPDATE on
// the tables it reads.
func statementTableAccess(query string, known map[string]bool) map[string]map[string]bool {
	text := sqlNoise.ReplaceAllString(query, " ")
	out := map[string]map[string]bool{}
	add := func(table, priv string) {
		if !known[table] {
			return
		}
		if out[table] == nil {
			out[table] = map[string]bool{}
		}
		out[table][priv] = true
	}
	for _, m := range sqlWord.FindAllStringSubmatch(text, -1) {
		word := m[1]
		if word == "" {
			word = strings.ToLower(m[2])
		}
		add(word, privSelect)
	}
	targets := func(re *regexp.Regexp, priv string) []string {
		var names []string
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			name := strings.ToLower(m[1])
			add(name, priv)
			names = append(names, name)
		}
		return names
	}
	inserted := targets(sqlInsertTarget, privInsert)
	targets(sqlUpdateTarget, privUpdate)
	targets(sqlDeleteTarget, privDelete)
	for _, name := range targets(sqlMergeTarget, privInsert) {
		add(name, privUpdate)
		add(name, privDelete)
	}
	if sqlUpsert.MatchString(text) {
		for _, name := range inserted {
			add(name, privUpdate)
		}
	}
	if sqlRowLock.MatchString(text) {
		for table := range out {
			add(table, privUpdate)
		}
	}
	return out
}

func emitDBGrantReport(w io.Writer, report dbGrantReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	if _, err := fmt.Fprintf(w, "Database %s: observed table use (pg_stat_statements) vs held grants\n\n", report.Database); err != nil {
		return err
	}
	for _, r := range report.Roles {
		if _, err := fmt.Fprintf(w, "%s: %d statements, %d calls; %d tables observed of %d granted; %d unused grants; %d ungranted uses\n",
			r.Role, r.Statements, r.Calls, r.ObservedTables, r.GrantedTables, r.UnusedGrants, r.UngrantedUses); err != nil {
			return err
		}
		for _, t := range r.Tables {
			if len(t.Observed) == 0 && len(t.Ungranted) == 0 {
				continue
			}
			line := fmt.Sprintf("  %-48s %s", t.Table, strings.Join(t.Observed, ","))
			if len(t.Ungranted) > 0 {
				line += "  UNGRANTED " + strings.Join(t.Ungranted, ",")
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "Caveats:"); err != nil {
		return err
	}
	for _, c := range report.Caveats {
		if _, err := fmt.Fprintln(w, "  - "+c); err != nil {
			return err
		}
	}
	return nil
}
