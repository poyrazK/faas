package dbmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const MaxTables = 64

type Progress struct {
	Verification     *Verification `json:"verification,omitempty"`
	Preparation      *Preparation  `json:"preparation,omitempty"`
	OperationID      string        `json:"operation_id,omitempty"`
	State            string        `json:"state"`
	Reason           string        `json:"reason,omitempty"`
	Tables           int           `json:"tables"`
	TablesReady      int           `json:"tables_ready"`
	ApplyLagBytes    int64         `json:"apply_lag_bytes"`
	RetainedWALBytes int64         `json:"retained_wal_bytes"`
}

const (
	None           = "none"
	Preparing      = "preparing"
	Copying        = "copying"
	CatchingUp     = "catching_up"
	Live           = "live"
	Paused         = "paused"
	ResyncRequired = "resync_required"
	Stopped        = "stopped"
)

func (p Progress) Active() bool {
	return p.State == Preparing || p.State == Copying || p.State == CatchingUp || p.State == Live
}

type Table struct {
	Supported bool     `json:"supported"`
	Name      string   `json:"name"`
	Columns   []Column `json:"columns"`
	Key       []string `json:"key"`
}
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Mod      int    `json:"mod"`
	Required bool   `json:"required"`
}

// InventorySQL provides the ordinary tables selected by preflight. The
// caller checks the complete inventory against that preflight before use.
const inventoryRows = `
SELECT coalesce(json_agg(row_to_json(t) ORDER BY t.name COLLATE "C"),'[]'::json)
FROM (
 SELECT c.relname::text AS name, c.supported,
 (SELECT json_agg(json_build_object('name',a.attname,'type',typ.typname,'mod',a.atttypmod,
                            'required',a.attnotnull) ORDER BY a.attnum)
 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type typ ON typ.oid=a.atttypid
 WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped) AS columns,
 (SELECT json_agg(a.attname ORDER BY keys.position) FROM pg_catalog.pg_index i
 CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS keys(attnum,position)
 JOIN pg_catalog.pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=keys.attnum
 WHERE i.indrelid=c.oid AND i.indisprimary AND i.indisvalid AND i.indimmediate
 AND keys.position<=i.indnkeyatts) AS key
 FROM relations c`

const inventoryEnd = ` ORDER BY c.nspname COLLATE "C", c.relname COLLATE "C" LIMIT 65
) t`

const InventorySQL = sourceRelations + inventoryRows + inventoryEnd

func InventoryWithMarkerSQL(marker Marker) string {
	return sourceRelations + inventoryRows +
		" WHERE NOT (c.nspname='public' AND c.relname=" + Literal(marker.Name) +
		" AND coalesce(obj_description(c.oid,'pg_class'),'')=" + Literal(marker.Tag) + ")" + inventoryEnd
}

var errInventory = errors.New("invalid migration table inventory")

// SchemaSQL builds the fenced target's replication tables from bounded
// catalog data. Application constraints and defaults belong to cutover.
func SchemaSQL(tables []Table) (string, error) {
	if len(tables) == 0 || len(tables) > MaxTables {
		return "", errInventory
	}
	var sql strings.Builder
	var previous string
	for _, table := range tables {
		if !table.Supported || !safeTable.MatchString("public."+table.Name) || table.Name <= previous ||
			strings.HasPrefix(table.Name, "gregale_") || len(table.Key) == 0 ||
			len(table.Columns) == 0 || len(table.Columns) > 128 {
			return "", errInventory
		}
		previous = table.Name
		names := make(map[string]bool, len(table.Columns))
		columns := make([]string, 0, len(table.Columns))
		for _, col := range table.Columns {
			if !safeTable.MatchString("public."+col.Name) || names[col.Name] {
				return "", errInventory
			}
			names[col.Name] = true
			typ, ok := columnType(col)
			if !ok {
				return "", errInventory
			}
			def := Ident(col.Name) + " " + typ
			if col.Required {
				def += " NOT NULL"
			}
			columns = append(columns, def)
		}
		keys := make([]string, 0, len(table.Key))
		for _, key := range table.Key {
			if !names[key] {
				return "", errInventory
			}
			delete(names, key)
			keys = append(keys, Ident(key))
		}
		fmt.Fprintf(&sql, "CREATE TABLE public.%s (%s, PRIMARY KEY (%s));\n",
			Ident(table.Name), strings.Join(columns, ", "), strings.Join(keys, ", "))
	}
	return sql.String(), nil
}

func columnType(col Column) (string, bool) {
	switch col.Type {
	case "bool", "int2", "int4", "int8", "float4", "float8", "text", "bytea", "uuid", "json", "jsonb", "date", "inet", "cidr", "macaddr", "macaddr8", "interval":
		return "pg_catalog." + Ident(col.Type), col.Mod == -1
	case "varchar", "bpchar":
		name := "pg_catalog." + Ident(col.Type)
		if col.Mod == -1 {
			return name, true
		}
		return fmt.Sprintf("%s(%d)", name, col.Mod-4), col.Mod >= 5 && col.Mod <= 1048580
	case "timestamp", "timestamptz", "time", "timetz":
		name := "pg_catalog." + Ident(col.Type)
		if col.Mod == -1 {
			return name, true
		}
		return fmt.Sprintf("%s(%d)", name, col.Mod), col.Mod >= 0 && col.Mod <= 6
	case "numeric":
		if col.Mod == -1 {
			return "pg_catalog.numeric", true
		}
		if col.Mod < 4 {
			return "", false
		}
		precision, scale := (col.Mod-4)>>16, (col.Mod-4)&2047
		if scale >= 1024 {
			scale -= 2048
		}
		return fmt.Sprintf("pg_catalog.numeric(%d,%d)", precision, scale), precision >= 1 && precision <= 1000 && scale >= -1000 && scale <= 1000
	default:
		return "", false
	}
}

func SchemaDigest(tables []Table) string {
	b, _ := json.Marshal(tables)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TableNames(tables []Table) []string {
	out := make([]string, 0, len(tables))
	for _, table := range tables {
		out = append(out, "public."+table.Name)
	}
	return out
}

func Ident(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\"" }

func Literal(s string) string {
	return "E'" + strings.NewReplacer("\\", "\\\\", "'", "''").Replace(s) + "'"
}

func ResourceName(operation, pin string) string {
	sum := sha256.Sum256([]byte(operation + ":" + pin))
	return "gregale_m_" + hex.EncodeToString(sum[:16])
}

type Slot struct {
	Exists      bool   `json:"exists"`
	Plugin      string `json:"plugin"`
	Database    string `json:"database"`
	Confirmed   string `json:"confirmed"`
	Restart     string `json:"restart"`
	WALStatus   string `json:"wal_status"`
	Application string `json:"application"`
	Sender      string `json:"sender"`
	Retained    int64  `json:"retained"`
	Current     string `json:"current"`
	Active      bool   `json:"active"`
}

func SlotSQL(slot string) string {
	return `SELECT coalesce((SELECT json_build_object('exists',true,'plugin',s.plugin,'database',s.database,
	 'confirmed',s.confirmed_flush_lsn::text,'restart',s.restart_lsn::text,'wal_status',s.wal_status,
	 'sender',coalesce(r.backend_start::text||':'||r.pid::text,''), 'application',coalesce(r.application_name,''),
	 'retained',greatest(pg_wal_lsn_diff(pg_current_wal_lsn(),s.restart_lsn),0)::bigint,
	 'current',pg_current_wal_lsn()::text,'active',s.active)
	 FROM pg_catalog.pg_replication_slots s LEFT JOIN pg_catalog.pg_stat_replication r ON s.active_pid=r.pid
	 WHERE s.slot_name=` + Literal(slot) + `),'null'::json)`
}

// Position parses PostgreSQL's 64-bit WAL pointer without signed truncation.
func Position(lsn string) (uint64, bool) {
	h, l, ok := strings.Cut(lsn, "/")
	if !ok {
		return 0, false
	}
	hi, eh := strconv.ParseUint(h, 16, 32)
	lo, el := strconv.ParseUint(l, 16, 32)
	return hi<<32 | lo, eh == nil && el == nil
}

func BytesBehind(current, applied string) int64 {
	latest, lok := Position(current)
	done, dok := Position(applied)
	if !lok || !dok || latest < done {
		return 0
	}
	return int64(min(latest-done, uint64(1<<62)))
}

func IsContinuous(slot Slot, database, floor, prior, durableApplied string) bool {
	confirmed, cok := Position(slot.Confirmed)
	created, fok := Position(floor)
	if slot.Plugin != "pgoutput" || slot.Database != database ||
		!slices.Contains([]string{"reserved", "extended"}, slot.WALStatus) || !cok || !fok || confirmed < created {
		return false
	}
	if prior == "" {
		prior = floor
	}
	saved, ok := Position(prior)
	if !ok || confirmed < saved {
		return false
	}
	applied, observed := Position(durableApplied)
	return confirmed == saved || observed && confirmed <= applied
}

// PublicationSQL verifies the exact unfiltered table set and the source
// login's ownership before the worker uses or removes a publication.
func PublicationSQL(name string) string {
	return `SELECT coalesce((SELECT json_build_object(
 'exists',true,'marker',coalesce(obj_description(p.oid,'pg_publication'),''),
 'tables',(SELECT json_agg(schemaname||'.'||tablename ORDER BY (schemaname||'.'||tablename) COLLATE "C")
     FROM pg_catalog.pg_publication_tables t WHERE t.pubname=p.pubname),
 'usable',pg_catalog.pg_get_userbyid(p.pubowner)=current_user AND NOT p.puballtables
   AND p.pubinsert AND p.pubupdate AND p.pubdelete AND p.pubtruncate AND NOT p.pubviaroot
   AND NOT EXISTS (SELECT FROM pg_catalog.pg_publication_rel WHERE prpubid=p.oid AND (prattrs IS NOT NULL OR prqual IS NOT NULL)))
 FROM pg_catalog.pg_publication p WHERE p.pubname=` + Literal(name) + `),'null'::json)`
}

func CreatePublicationSQL(resource, operation string, tables []Table) string {
	name := Ident(resource)
	names := make([]string, 0, len(tables))
	for _, table := range tables {
		names = append(names, "ONLY public."+Ident(table.Name))
	}
	return "CREATE PUBLICATION " + name + " FOR TABLE " + strings.Join(names, ", ") +
		"; COMMENT ON PUBLICATION " + name + " IS " + Literal(operation)
}

func CreateSubscriptionSQL(name, dsn string) string {
	return "CREATE SUBSCRIPTION " + Ident(name) + " CONNECTION " + Literal(dsn) +
		" PUBLICATION " + Ident(name) + " WITH (create_slot=false, enabled=false, copy_data=true, synchronous_commit=local, run_as_owner=false, password_required=false)"
}

func SubscriptionSQL(name string) string { return SubscriptionWithMarkerSQL(name, "") }

func SubscriptionWithMarkerSQL(name, marker string) string {
	ordinary, markerReady := "", "false"
	if marker != "" {
		rel := "pg_catalog.to_regclass(" + Literal("public."+Ident(marker)) + ")"
		ordinary = " AND r.srrelid IS DISTINCT FROM " + rel
		markerReady = "EXISTS (SELECT FROM pg_catalog.pg_subscription_rel r WHERE r.srsubid=s.oid AND r.srsubstate='r' AND r.srrelid=" + rel + ")"
	}
	return `SELECT coalesce((SELECT json_build_object(
 'exists',true,'enabled',s.subenabled,
 'applying',EXISTS (SELECT FROM pg_catalog.pg_stat_subscription st WHERE st.subid=s.oid AND st.relid IS NULL AND st.pid IS NOT NULL),
 'ready',(SELECT count(*) FROM pg_catalog.pg_subscription_rel r WHERE r.srsubid=s.oid AND r.srsubstate='r'` + ordinary + `),
 'tables',(SELECT count(*) FROM pg_catalog.pg_subscription_rel r WHERE r.srsubid=s.oid` + ordinary + `),
 'marker_ready',` + markerReady + `,
 'durable',coalesce((SELECT pg_catalog.pg_replication_origin_progress(o.external_id, true)::text FROM pg_catalog.pg_replication_origin_status o WHERE o.external_id='pg_'||s.oid),''))
 FROM pg_catalog.pg_subscription s JOIN pg_catalog.pg_database d ON d.oid=s.subdbid
 WHERE d.datname=current_database() AND s.subname=` + Literal(name) + `),'null'::json)`
}
