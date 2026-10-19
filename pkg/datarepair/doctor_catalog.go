package datarepair

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type doctorFunction struct {
	Name        string `json:"name"`
	Args        string `json:"args"`
	Result      string `json:"result"`
	Language    string `json:"language"`
	Body        string `json:"body"`
	Volatile    string `json:"volatile"`
	Definer     bool   `json:"definer"`
	Defaults    int    `json:"defaults"`
	DefaultExpr string `json:"default_expr"`
	Runtime     bool   `json:"runtime"`
}

// Definitions come from this binary's owner-install SQL, never the database.
// The last CREATE OR REPLACE wins, as it does in Schema().
func doctorFunctions() ([]doctorFunction, string) {
	pattern := regexp.MustCompile(`(?s)CREATE(?: OR REPLACE)? FUNCTION gregale_repair\.([a-z_]+)\((.*?)\) RETURNS ([^\n]+)\nLANGUAGE (.*?) AS \$\$(.*?)\$\$;`)
	byName := map[string]doctorFunction{}
	fingerprint := ""
	for _, m := range pattern.FindAllStringSubmatch(Schema(), -1) {
		// The optional repairable-transaction API qualifies its own contract;
		// legacy capture and executor readiness do not require these privileges.
		if m[1] == "capture_transaction" || m[1] == "protect_capture_receipt" {
			continue
		}
		f := doctorFunction{Name: m[1], Result: doctorType(m[3]), Language: strings.Fields(m[4])[0], Body: fmt.Sprintf("%x", sha256.Sum256([]byte(m[5]))), Volatile: "v", Definer: strings.Contains(m[4], "SECURITY DEFINER"), Runtime: true}
		if strings.Contains(m[4], "IMMUTABLE") {
			f.Volatile = "i"
		} else if strings.Contains(m[4], "STABLE") {
			f.Volatile = "s"
		}
		args := []string{}
		defaults := []string{}
		if strings.TrimSpace(m[2]) != "" {
			for _, arg := range strings.Split(m[2], ",") {
				fields := strings.Fields(arg)
				args = append(args, doctorType(fields[1]))
				if strings.Contains(arg, " DEFAULT ") {
					f.Defaults++
					_, value, _ := strings.Cut(arg, " DEFAULT ")
					// Current installation defaults are typed NULLs. A new
					// default form needs an explicit catalog contract first.
					if strings.TrimSpace(value) != "NULL" {
						return nil, ""
					}
					defaults = append(defaults, "NULL::"+doctorType(fields[1]))
				}
			}
		}
		f.Args = strings.Join(args, ", ")
		f.DefaultExpr = strings.Join(defaults, ", ")
		if strings.Contains(f.Name, "retention") || f.Name == "enroll" || f.Result == "trigger" {
			f.Runtime = false
		}
		byName[f.Name] = f
		if f.Name == "fingerprint" {
			// Substitute only this trusted embedded SQL parameter. Do not call
			// an installed fingerprint function, even if its name is familiar.
			fingerprint = regexp.MustCompile(`\btarget\b`).ReplaceAllStringFunc(m[5], func(string) string { return "$1::oid" })
		}
	}
	functions := make([]doctorFunction, 0, len(byName))
	for _, f := range byName {
		functions = append(functions, f)
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })
	return functions, fingerprint
}

func doctorType(t string) string {
	switch strings.TrimSpace(t) {
	case "timestamptz":
		return "timestamp with time zone"
	case "bool":
		return "boolean"
	default:
		return strings.TrimSpace(t)
	}
}

const doctorFunctionSQL = `WITH expected AS (
 SELECT * FROM jsonb_to_recordset($1::jsonb) AS x(name text,args text,result text,language text,body text,volatile text,definer boolean,defaults integer,default_expr text,runtime boolean)
), installed AS (
 SELECT p.*,l.lanname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='gregale_repair'
)
SELECT e.name,
 count(p.oid)=1 AND coalesce(bool_and(
  oidvectortypes(p.proargtypes)=e.args AND format_type(p.prorettype,NULL)=e.result AND p.lanname=e.language AND
  encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')=e.body AND p.provolatile::text=e.volatile AND p.prosecdef=e.definer AND
  p.proconfig=ARRAY['search_path=pg_catalog'] AND NOT p.proisstrict AND NOT p.proleakproof AND NOT p.proretset AND
  p.prokind='f' AND p.proparallel='u' AND p.prosupport=0 AND p.provariadic=0 AND p.proargmodes IS NULL AND p.pronargdefaults=e.defaults AND
  coalesce(pg_get_expr(p.proargdefaults,0),'')=e.default_expr),false),
 NOT e.runtime OR coalesce(bool_and(has_function_privilege(p.oid,'EXECUTE')),false)
FROM expected e LEFT JOIN installed p ON p.proname=e.name GROUP BY e.name,e.runtime ORDER BY e.name`

type doctorColumn struct {
	Table string `json:"table"`
	Name  string `json:"name"`
	Type  string `json:"type"`
}

func doctorColumns() []doctorColumn {
	tables := map[string]string{
		"enrollments":    "relation:regclass qualified_name:text enrollment_id:uuid fields:text[] keys:text[] fingerprint:text",
		"operations":     "id:uuid scope:jsonb release:text enrollment_id:uuid schema_fingerprint:text transaction_id:text finalized:boolean repair_of:uuid created_at:timestamptz payload_purged_at:timestamptz",
		"changes":        "operation_id:uuid row_identity:uuid key:jsonb before_values:jsonb after_values:jsonb before_version:bigint after_version:bigint",
		"plans":          "id:uuid source_operation_id:uuid scope:jsonb document:jsonb digest:text created_at:timestamptz expires_at:timestamptz rehearsed_at:timestamptz rehearsal:jsonb approved_at:timestamptz approved_by:text applied_at:timestamptz repair_operation_id:uuid payload_purged_at:timestamptz retained_digest:text check_failure:jsonb apply_checks:jsonb apply_resolution:jsonb replacement_plan_id:uuid",
		"retention_runs": "id:uuid scope:jsonb document:jsonb digest:text expires_at:timestamptz applied_at:timestamptz applied_by:text applied_role:text",
	}
	result := []doctorColumn{}
	for table, columns := range tables {
		for _, column := range strings.Fields(columns) {
			name, typ, _ := strings.Cut(column, ":")
			result = append(result, doctorColumn{table, name, doctorType(typ)})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Table+"."+result[i].Name < result[j].Table+"."+result[j].Name
	})
	return result
}

const doctorInstallationSQL = `WITH expected AS (
 SELECT * FROM jsonb_to_recordset($1::jsonb) AS x("table" text,name text,type text)
)
SELECT e."table",bool_and(c.oid IS NOT NULL AND c.relkind='r' AND NOT c.relispartition AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND
 NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid) AND
 NOT EXISTS(SELECT 1 FROM pg_rewrite WHERE ev_class=c.oid) AND
 a.attnum>0 AND NOT a.attisdropped AND a.atttypid IS NOT NULL AND format_type(a.atttypid,a.atttypmod)=e.type AND a.attgenerated='' AND a.attidentity='')
FROM expected e LEFT JOIN pg_namespace n ON n.nspname='gregale_repair' LEFT JOIN pg_class c ON c.relnamespace=n.oid AND c.relname=e."table"
LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attname=e.name GROUP BY e."table" ORDER BY e."table"`

const doctorInternalTriggersSQL = `WITH expected(rel,name,func,kind,deferred) AS (VALUES
 ('operations','gregale_repair_finalized','require_finalized',5,true),
 ('operations','gregale_repair_protect_operation','protect_operation',27,false),
 ('changes','gregale_repair_protect_change','protect_change',31,false))
SELECT e.rel,coalesce(t.tgtype=e.kind AND t.tgenabled IN ('O','A') AND t.tgnargs=0 AND t.tgattr=''::int2vector AND t.tgqual IS NULL AND
 t.tgfoid=p.oid AND t.tgdeferrable=e.deferred AND t.tginitdeferred=e.deferred AND t.tgoldtable IS NULL AND t.tgnewtable IS NULL,false)
FROM expected e LEFT JOIN pg_namespace n ON n.nspname='gregale_repair' LEFT JOIN pg_class c ON c.relnamespace=n.oid AND c.relname=e.rel
LEFT JOIN pg_trigger t ON t.tgrelid=c.oid AND t.tgname=e.name LEFT JOIN pg_proc p ON p.pronamespace=n.oid AND p.proname=e.func AND p.pronargs=0`

func doctorInspect(ctx context.Context, tx pgx.Tx, r *DoctorReport) error {
	var version int
	var installed bool
	if err := tx.QueryRow(ctx, `SELECT current_setting('server_version_num')::int,EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='gregale_repair')`).Scan(&version, &installed); err != nil {
		return err
	}
	r.finding("connection", "connected")
	if version < 160000 || version >= 170000 {
		r.finding("postgres", "postgres_unsupported")
		return nil
	}
	r.finding("postgres", "postgres16")
	if !installed {
		r.finding("installation", "schema_missing")
		return nil
	}
	ok, err := doctorInstallation(ctx, tx, r)
	if err != nil || !ok {
		return err
	}
	functions, fingerprint := doctorFunctions()
	if len(functions) == 0 || len(functions) > api.DataRepairDoctorMaxObjects || fingerprint == "" {
		return ErrUnavailable
	}
	missing, err := doctorInspectFunctions(ctx, tx, r, functions)
	if err != nil {
		return err
	}
	return doctorEnrollment(ctx, tx, r, fingerprint, missing)
}

func doctorInstallation(ctx context.Context, tx pgx.Tx, r *DoctorReport) (bool, error) {
	rows, err := tx.Query(ctx, doctorInstallationSQL, doctorJSON(doctorColumns()))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	objects := []string{}
	for rows.Next() {
		var name string
		var compatible bool
		if err := rows.Scan(&name, &compatible); err != nil {
			return false, err
		}
		if !compatible {
			objects = append(objects, "gregale_repair."+name)
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	rows.Close()
	triggers, err := tx.Query(ctx, doctorInternalTriggersSQL)
	if err != nil {
		return false, err
	}
	defer triggers.Close()
	for triggers.Next() {
		var name string
		var compatible bool
		if err := triggers.Scan(&name, &compatible); err != nil {
			return false, err
		}
		if !compatible && !slices.Contains(objects, "gregale_repair."+name) {
			objects = append(objects, "gregale_repair."+name)
		}
	}
	if err := triggers.Err(); err != nil {
		return false, err
	}
	if len(objects) > 0 {
		r.finding("installation", "schema_incompatible", objects...)
		return false, nil
	}
	r.finding("installation", "schema_current")
	return true, nil
}

func doctorInspectFunctions(ctx context.Context, tx pgx.Tx, r *DoctorReport, functions []doctorFunction) ([]string, error) {
	rows, err := tx.Query(ctx, doctorFunctionSQL, doctorJSON(functions))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changed, missing := []string{}, []string{}
	for rows.Next() {
		var name string
		var compatible, executable bool
		if err := rows.Scan(&name, &compatible, &executable); err != nil {
			return nil, err
		}
		if !compatible {
			changed = append(changed, "gregale_repair."+name)
		}
		if !executable {
			missing = append(missing, "gregale_repair."+name+": EXECUTE")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(changed) > 0 {
		r.finding("functions", "functions_incompatible", changed...)
	} else {
		r.finding("functions", "functions_current")
	}
	return missing, nil
}

const doctorPermissionsSQL = `WITH required(name,privilege) AS (VALUES
 ('enrollments','SELECT'),('operations','SELECT'),('operations','INSERT'),('operations','UPDATE'),
 ('changes','SELECT'),('changes','INSERT'),('changes','UPDATE'),('plans','SELECT'),('plans','INSERT'),('plans','UPDATE'))
SELECT 'gregale_repair: USAGE',has_schema_privilege(n.oid,'USAGE') FROM pg_namespace n WHERE n.nspname='gregale_repair'
UNION ALL SELECT 'gregale_repair.'||r.name||': '||r.privilege,has_table_privilege(c.oid,r.privilege)
FROM required r JOIN pg_namespace n ON n.nspname='gregale_repair' JOIN pg_class c ON c.relnamespace=n.oid AND c.relname=r.name`

func doctorPrivileges(ctx context.Context, tx pgx.Tx, r *DoctorReport, missing []string) (bool, error) {
	rows, err := tx.Query(ctx, doctorPermissionsSQL)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	registry := true
	for rows.Next() {
		var name string
		var granted bool
		if err := rows.Scan(&name, &granted); err != nil {
			return false, err
		}
		if !granted {
			missing = append(missing, name)
			if name == "gregale_repair: USAGE" || name == "gregale_repair.enrollments: SELECT" {
				registry = false
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		r.finding("permissions", "permissions_missing", missing...)
	} else {
		r.finding("permissions", "permissions_present")
	}
	return registry, nil
}

const doctorEnrollmentSQL = `SELECT e.relation::oid,e.enrollment_id::text,
 (SELECT array_agg(left(f,current_setting('max_identifier_length')::int)) FROM unnest(e.fields[1:$2]) f),
 (SELECT array_agg(left(f,current_setting('max_identifier_length')::int)) FROM unnest(e.keys[1:$2]) f),
 CASE WHEN octet_length(e.fingerprint)=64 THEN e.fingerprint ELSE '' END,
 coalesce(c.relkind='r' AND NOT c.relispartition AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND
 format('%I.%I',n.nspname,c.relname)=e.qualified_name AND
 NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid) AND
 cardinality(e.fields)>0 AND cardinality(e.keys)>0 AND cardinality(e.fields)+cardinality(e.keys)<=$2 AND
 NOT EXISTS(SELECT 1 FROM unnest(e.fields||e.keys) f LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attname=f AND a.attnum>0 AND NOT a.attisdropped
 WHERE a.attnum IS NULL OR a.attgenerated<>'' OR a.attidentity<>'') AND
 EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=c.oid AND attname='gregale_row_identity' AND atttypid='uuid'::regtype AND attnotnull AND NOT attisdropped) AND
 EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=c.oid AND attname='gregale_row_version' AND atttypid='int8'::regtype AND attnotnull AND NOT attisdropped),false)
FROM gregale_repair.enrollments e LEFT JOIN pg_class c ON c.oid=e.relation LEFT JOIN pg_namespace n ON n.oid=c.relnamespace WHERE e.qualified_name=$1`

func doctorEnrollment(ctx context.Context, tx pgx.Tx, r *DoctorReport, fingerprint string, missing []string) error {
	registry, err := doctorPrivileges(ctx, tx, r, missing)
	if err != nil || !registry {
		return err
	}
	var oid uint32
	var id, saved string
	var fields, keys []string
	var valid bool
	err = tx.QueryRow(ctx, doctorEnrollmentSQL, r.Table, api.DataRepairDoctorMaxObjects).Scan(&oid, &id, &fields, &keys, &saved, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		r.finding("enrollment", "not_enrolled")
		return nil
	}
	if err != nil {
		return err
	}
	if !valid || !validID(id) || !digestPattern.MatchString(saved) || len(fields)+len(keys) > api.DataRepairDoctorMaxObjects || !doctorFields(fields, keys) {
		r.finding("enrollment", "enrollment_invalid")
		return nil
	}
	r.finding("enrollment", "enrolled")
	var actual string
	if err := tx.QueryRow(ctx, fingerprint, oid).Scan(&actual); err != nil {
		return err
	}
	if actual != saved {
		r.finding("fingerprint", "fingerprint_changed")
	} else {
		r.finding("fingerprint", "fingerprint_matches")
	}
	if err := doctorTriggers(ctx, tx, r, oid); err != nil {
		return err
	}
	return doctorBusinessPrivileges(ctx, tx, r, oid, fields)
}

func doctorFields(fields, keys []string) bool {
	seen := map[string]bool{}
	for _, f := range append(slices.Clone(fields), keys...) {
		if f == "" || seen[f] || f == "gregale_row_identity" || f == "gregale_row_version" {
			return false
		}
		seen[f] = true
	}
	return true
}

const doctorTriggersSQL = `WITH expected(name,func,kind) AS (VALUES ('gregale_repair_guard','guard_row',31),('gregale_repair_capture','capture_row',17))
SELECT e.name,coalesce(t.tgtype=e.kind AND t.tgenabled IN ('O','A') AND t.tgnargs=0 AND t.tgattr=''::int2vector AND t.tgqual IS NULL AND
 t.tgconstraint=0 AND t.tgfoid=p.oid AND t.tgoldtable IS NULL AND t.tgnewtable IS NULL AND NOT t.tgisinternal AND
 current_setting('session_replication_role')='origin',false)
FROM expected e LEFT JOIN pg_trigger t ON t.tgrelid=$1 AND t.tgname=e.name
LEFT JOIN pg_namespace n ON n.nspname='gregale_repair' LEFT JOIN pg_proc p ON p.pronamespace=n.oid AND p.proname=e.func AND p.pronargs=0`

func doctorTriggers(ctx context.Context, tx pgx.Tx, r *DoctorReport, oid uint32) error {
	rows, err := tx.Query(ctx, doctorTriggersSQL, oid)
	if err != nil {
		return err
	}
	defer rows.Close()
	invalid := []string{}
	for rows.Next() {
		var name string
		var valid bool
		if err := rows.Scan(&name, &valid); err != nil {
			return err
		}
		if !valid {
			invalid = append(invalid, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(invalid) > 0 {
		r.finding("triggers", "triggers_invalid", invalid...)
	} else {
		r.finding("triggers", "triggers_enabled")
	}
	return nil
}

func doctorBusinessPrivileges(ctx context.Context, tx pgx.Tx, r *DoctorReport, oid uint32, fields []string) error {
	var schema, read, lock, write bool
	err := tx.QueryRow(ctx, `SELECT has_schema_privilege(c.relnamespace,'USAGE'),has_table_privilege(c.oid,'SELECT'),
	 has_table_privilege(c.oid,'UPDATE,DELETE,TRUNCATE'),
 (SELECT bool_and(has_column_privilege(c.oid,f,'UPDATE')) FROM unnest($2::text[]) f)
 FROM pg_class c WHERE c.oid=$1`, oid, fields).Scan(&schema, &read, &lock, &write)
	if err != nil {
		return err
	}
	missing := []string{}
	for _, f := range r.Findings {
		if f.ID == "permissions" {
			missing = append(missing, f.Objects...)
		}
	}
	for _, p := range []struct {
		ok   bool
		name string
	}{{schema, "table schema: USAGE"}, {read, "table: SELECT"}, {lock, "table: UPDATE, DELETE or TRUNCATE (enrollment lock)"}, {write, "declared fields: UPDATE"}} {
		if !p.ok {
			missing = append(missing, p.name)
		}
	}
	if len(missing) > 0 {
		r.finding("permissions", "permissions_missing", missing...)
	}
	return nil
}
