package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
)

// TargetSQL is private worker-only access. Credentials and provider identities
// must never be returned by the customer HTTP API or delivered to an app.
type TargetSQL func(context.Context, string, string, func(*pgx.Conn, string) error) error

type Engine struct {
	Policy commit.NetworkPolicy
	Target TargetSQL
	// Test-only seam: production always uses Commit's verified/pinned dialer.
	openSource func(context.Context, string) (*pgxpool.Pool, error)
	pinSource  func(context.Context, string) (string, error)
}
type column struct {
	Name, Type   string
	NotNull, Key bool
	Modifier     int32
}
type relation struct {
	Name    string
	Columns []column
}
type blocked struct {
	code   string
	resync bool
}

func (e *blocked) Error() string { return e.code }

func (e *Engine) Reconcile(ctx context.Context, p Pipeline, raw string, current func(context.Context) error) Observation {
	ob := Observation{State: "blocked", Reason: "database_unavailable", Verification: p.Verification, Initialized: p.Initialized}
	if p.VerifiedAt != "" {
		ob.VerifiedAt, _ = time.Parse(time.RFC3339Nano, p.VerifiedAt)
	}
	open := e.openSource
	if open == nil {
		open = func(ctx context.Context, raw string) (*pgxpool.Pool, error) {
			return commit.OpenPool(ctx, raw, e.Policy)
		}
	}
	if e.Target == nil {
		return ob
	}
	err := e.Target(ctx, p.AccountID, p.DatabaseID, func(target *pgx.Conn, owner string) error {
		// Session locks serialize remote commands even when a worker's catalog
		// lease expires. Recheck the catalog intent *after* obtaining the lock.
		if _, err := target.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,51732))`, p.ID); err != nil {
			return err
		}
		defer func() {
			unlock, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_, _ = target.Exec(unlock, `SELECT pg_advisory_unlock(hashtextextended($1,51732))`, p.ID)
		}()
		if err := current(ctx); err != nil {
			return err
		}
		if p.DesiredState == "paused" {
			if err := setSubscriptionEnabled(ctx, p, target, false); err != nil {
				return err
			}
		}
		if p.DesiredState == "deleted" {
			if err := setSubscriptionEnabled(ctx, p, target, false); err != nil {
				return err
			}
		}
		source, err := open(ctx, raw)
		if err != nil {
			if p.DesiredState == "paused" {
				ob.State, ob.Reason = "paused", "source_unavailable"
				if p.State == "resync_required" {
					ob.State, ob.Reason = p.State, p.Reason
				}
				return nil
			}
			return err
		}
		defer source.Close()
		return e.reconcile(ctx, p, raw, source, target, owner, &ob)
	})
	if err != nil {
		var b *blocked
		if errors.As(err, &b) {
			ob.Reason = b.code
			ob.State = "blocked"
			if b.resync {
				ob.State = "resync_required"
			}
		} else {
			ob.State = "blocked"
			if p.DesiredState == "deleted" {
				ob.State = "deleting"
			}
			ob.Reason = "database_unavailable"
		}
	}
	return ob
}

func pipelineName(p Pipeline) string {
	return "gregale_sync_" + strings.ReplaceAll(p.ID, "-", "") + "_" + strconv.FormatInt(p.Generation, 10)
}
func marker(p Pipeline) string {
	return "gregale.pipeline.v1:" + p.AccountID + ":" + p.ID + ":" + strconv.FormatInt(p.Generation, 10)
}
func ident(name string) string      { return pgx.Identifier{name}.Sanitize() }
func tableIdent(name string) string { return pgx.Identifier{"public", name}.Sanitize() }

// Values in external DDL cannot use bind parameters. This is the same explicit
// customer-DDL boundary as managedpostgres credentials, not control-plane SQL.
func literal(value string) string {
	return "E'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "''") + "'"
}

func (e *Engine) reconcile(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, owner string, ob *Observation) (result error) {
	defer func() {
		var b *blocked
		if errors.As(result, &b) && p.Initialized && slices.Contains([]string{"source_table_unsupported", "source_column_unsupported", "source_primary_key_required"}, b.code) {
			b.resync = true
		}
		if errors.As(result, &b) && b.resync {
			_ = allowMirrorReads(ctx, p, target, false)
			retained := p
			retained.DesiredState = "deleted"
			_ = cleanupGeneration(ctx, retained, source, target)
		}
	}()
	if p.DesiredState == "deleted" {
		if p.Generation > 1 {
			previous := p
			previous.Generation--
			if err := cleanupGeneration(ctx, previous, source, target); err != nil {
				return err
			}
		}
		if err := cleanupGeneration(ctx, p, source, target); err != nil {
			ob.State = "deleting"
			return err
		}
		ob.State, ob.Reason = "deleted", ""
		if ob.Verification == "pending" {
			ob.Verification = "unknown"
		}
		return nil
	}
	if p.Generation > 1 {
		previous := p
		previous.Generation--
		if err := cleanupGeneration(ctx, previous, source, target); err != nil {
			return err
		}
	}
	if p.State == "resync_required" {
		return &blocked{p.Reason, true}
	}
	if p.DesiredState == "paused" {
		if err := setSubscriptionEnabled(ctx, p, target, false); err != nil {
			return err
		}
		ob.State, ob.Reason = "paused", ""
		return checkRetention(ctx, p, source, target, ob)
	}
	relations, hash, err := inspectSource(ctx, p, source)
	if err != nil {
		return err
	}
	pinned, err := e.sourceConninfo(ctx, raw)
	if err != nil {
		return err
	}
	if err := prepareMirror(ctx, p, target, owner, relations, hash); err != nil {
		return err
	}
	if err := ensurePublication(ctx, p, source); err != nil {
		return err
	}
	if err := ensureSubscription(ctx, p, source, target, pinned); err != nil {
		return err
	}
	ob.Initialized = true
	if err := checkRetention(ctx, p, source, target, ob); err != nil {
		return err
	}
	var ready int
	err = target.QueryRow(ctx, `SELECT count(*) FROM pg_subscription_rel r JOIN pg_subscription s ON s.oid=r.srsubid WHERE s.subname=$1 AND r.srsubstate='r'`, pipelineName(p)).Scan(&ready)
	if err != nil {
		return err
	}
	ob.TablesReady = ready
	if ready != len(p.Tables) {
		ob.State, ob.Reason = "copying", ""
		return nil
	}
	var caught bool
	var barrier string
	if err = target.QueryRow(ctx, `SELECT barrier::text FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&barrier); err != nil {
		return err
	}
	err = source.QueryRow(ctx, `SELECT coalesce(confirmed_flush_lsn >= $2::pg_lsn AND active AND pg_wal_lsn_diff(pg_current_wal_lsn(),confirmed_flush_lsn)<=$3,false) FROM pg_replication_slots WHERE slot_name=$1`, pipelineName(p), barrier, api.DataPipelineLiveLagMaxBytes).Scan(&caught)
	if err != nil {
		return err
	}
	var receiveFresh bool
	if err := target.QueryRow(ctx, `SELECT coalesce(bool_or(pid IS NOT NULL AND last_msg_receipt_time>=clock_timestamp()-($2::bigint*interval '1 second')),false) FROM pg_stat_subscription WHERE subname=$1 AND relid IS NULL`, pipelineName(p), int64(api.DataPipelineObservationMaxAge.Seconds())).Scan(&receiveFresh); err != nil {
		return err
	}
	if !caught || !receiveFresh {
		ob.State, ob.Reason = "catching_up", "waiting_for_replication"
		return nil
	}
	if err := allowMirrorReads(ctx, p, target, true); err != nil {
		return err
	}
	ob.State, ob.Reason = "live", ""
	if p.VerifyRequested {
		result, err := verifyMirror(ctx, p, source, target, relations)
		if err != nil {
			return err
		}
		ob.Verification = result
		ob.VerifiedAt = time.Now().UTC()
		if result == "mismatch" {
			_ = setSubscriptionEnabled(ctx, p, target, false)
			_ = allowMirrorReads(ctx, p, target, false)
			return &blocked{"replica_mismatch", true}
		}
	}
	return nil
}

func inspectSource(ctx context.Context, p Pipeline, source *pgxpool.Pool) ([]relation, string, error) {
	var version int
	if err := source.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version); err != nil {
		return nil, "", err
	}
	if version < 160000 || version >= 170000 {
		return nil, "", &blocked{"source_postgres_16_required", false}
	}
	var level string
	if err := source.QueryRow(ctx, `SHOW wal_level`).Scan(&level); err != nil {
		return nil, "", err
	}
	if level != "logical" {
		return nil, "", &blocked{"source_logical_replication_required", false}
	}
	var retention int64
	if err := source.QueryRow(ctx, `SELECT pg_size_bytes(current_setting('max_slot_wal_keep_size'))`).Scan(&retention); err != nil {
		return nil, "", err
	}
	if retention <= 0 || retention > api.DataPipelineRetainedWALMaxBytes {
		return nil, "", &blocked{"source_wal_retention_limit_required", false}
	}
	var canPublish bool
	if err := source.QueryRow(ctx, `SELECT has_database_privilege(current_database(),'CREATE')`).Scan(&canPublish); err != nil {
		return nil, "", err
	}
	if !canPublish {
		return nil, "", &blocked{"source_publication_permission_required", false}
	}

	var canReplicate bool
	if err := source.QueryRow(ctx, `SELECT rolreplication OR rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&canReplicate); err != nil {
		return nil, "", err
	}
	if !canReplicate {
		return nil, "", &blocked{"source_replication_permission_required", false}
	}
	result := make([]relation, 0, len(p.Tables))
	for _, name := range p.Tables {
		var supported bool
		err := source.QueryRow(ctx, `SELECT c.relkind='r' AND NOT c.relispartition AND NOT c.relrowsecurity AND c.relreplident='d' AND has_table_privilege(c.oid,'SELECT') AND pg_has_role(c.relowner,'USAGE') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1`, name).Scan(&supported)
		if err != nil || !supported {
			return nil, "", &blocked{"source_table_unsupported", false}
		}
		rows, err := source.Query(ctx, `SELECT a.attname,a.atttypmod,a.attnotnull,
 EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=a.attrelid AND i.indisprimary AND a.attnum=ANY(i.indkey)),
 t.typname,nt.nspname,a.attgenerated::text
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace nt ON nt.oid=t.typnamespace
 WHERE n.nspname='public' AND c.relname=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT $2`, name, api.DataPipelineColumnsMax+1)
		if err != nil {
			return nil, "", err
		}
		r := relation{Name: name}
		hasKey := false
		for rows.Next() {
			var col column
			var typ, namespace, generated string
			if err := rows.Scan(&col.Name, &col.Modifier, &col.NotNull, &col.Key, &typ, &namespace, &generated); err != nil {
				rows.Close()
				return nil, "", err
			}
			if namespace != "pg_catalog" || generated != "" || !supportedType(typ) {
				rows.Close()
				return nil, "", &blocked{"source_column_unsupported", false}
			}
			col.Type = typ
			// SQL type names come from a fixed allowlist, never source-provided
			// format_type output. This source is a customer trust boundary.
			if typ == "varchar" || typ == "bpchar" {
				base := "character varying"
				if typ == "bpchar" {
					base = "character"
				}
				if typ == "varchar" && col.Modifier == -1 {
					col.Type = "character varying"
				} else if col.Modifier < 5 || col.Modifier > 1048580 {
					rows.Close()
					return nil, "", &blocked{"source_column_unsupported", false}
				}
				if col.Modifier >= 5 {
					col.Type = base + "(" + strconv.FormatInt(int64(col.Modifier-4), 10) + ")"
				}
			}
			r.Columns = append(r.Columns, col)
			hasKey = hasKey || col.Key
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, "", err
		}
		if !hasKey || len(r.Columns) > api.DataPipelineColumnsMax {
			return nil, "", &blocked{"source_primary_key_required", false}
		}
		result = append(result, r)
	}
	encoded, _ := json.Marshal(result)
	sum := sha256.Sum256(encoded)
	return result, hex.EncodeToString(sum[:]), nil
}
func supportedType(typ string) bool {
	return slices.Contains([]string{"int2", "int4", "int8", "float4", "float8", "numeric", "bool", "text", "varchar", "bpchar", "uuid", "timestamp", "timestamptz", "date", "time", "timetz", "interval", "json", "jsonb", "bytea", "inet", "cidr", "macaddr", "macaddr8"}, typ)
}

// Pin hostaddr in libpq's subscriber connection as well as verifying the TLS
// hostname. Native PostgreSQL connections do not pass through Gregale's dialer.
func (e *Engine) sourceConninfo(ctx context.Context, raw string) (string, error) {
	if e.pinSource != nil {
		return e.pinSource(ctx, raw)
	}
	u, err := url.Parse(raw)
	if err != nil || commit.ValidateConnection(raw) != nil || !e.Policy.Hosts[u.Hostname()] {
		return "", ErrInvalid
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil {
		return "", ErrUnavailable
	}
	slices.SortFunc(addresses, func(a, b netip.Addr) int { return a.Compare(b) })
	for _, address := range addresses {
		ip := address.Unmap()
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		for _, prefix := range e.Policy.Prefixes {
			if prefix.Contains(ip) {
				q := u.Query()
				q.Set("hostaddr", netip.Addr(ip).String())
				q.Set("application_name", "gregale-data-pipeline")
				q.Set("sslrootcert", "system")
				u.RawQuery = q.Encode()
				return u.String(), nil
			}
		}
	}
	return "", ErrUnavailable
}

func prepareMirror(ctx context.Context, p Pipeline, target *pgx.Conn, owner string, relations []relation, hash string) error {
	if owner == "" {
		return ErrInvalid
	}
	// The dedicated target must have no unrelated application relations. The
	// private receipt permits retries without adopting arbitrary existing tables.
	var receipt bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&receipt); err != nil {
		return err
	}
	if !receipt && p.Initialized {
		return &blocked{"destination_replication_state_lost", true}
	}
	if receipt {
		var generation int64
		var retiring bool
		var saved string
		err := target.QueryRow(ctx, `SELECT generation,schema_hash,retiring FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&generation, &saved, &retiring)
		if err == nil {
			if generation == p.Generation && saved == hash {
				if retiring {
					return &blocked{"replication_state_lost", true}
				}
				return nil
			}
			return &blocked{"source_schema_changed", true}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if p.Initialized {
			return &blocked{"destination_replication_state_lost", true}
		}
	}
	var occupied bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f')) OR EXISTS(SELECT 1 FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace WHERE n.nspname='public')`).Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return &blocked{"destination_not_empty", false}
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, sql := range []string{`CREATE SCHEMA IF NOT EXISTS gregale_pipeline`, `REVOKE ALL ON SCHEMA gregale_pipeline FROM PUBLIC`,
		`CREATE TABLE IF NOT EXISTS gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint NOT NULL,schema_hash text NOT NULL,barrier pg_lsn NOT NULL,initialized boolean NOT NULL DEFAULT false,tables text[] NOT NULL,readable boolean NOT NULL DEFAULT false,retiring boolean NOT NULL DEFAULT false)`} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
		return err
	}
	for _, r := range relations {
		parts := make([]string, 0, len(r.Columns)+1)
		keys := []string{}
		for _, c := range r.Columns {
			declaration := ident(c.Name) + " " + c.Type
			if c.NotNull {
				declaration += " NOT NULL"
			}
			parts = append(parts, declaration)
			if c.Key {
				keys = append(keys, ident(c.Name))
			}
		}
		parts = append(parts, "PRIMARY KEY ("+strings.Join(keys, ",")+")")
		for _, sql := range []string{
			"CREATE TABLE " + tableIdent(r.Name) + " (" + strings.Join(parts, ",") + ")",
			"ALTER TABLE " + tableIdent(r.Name) + " OWNER TO " + ident(owner),
			"ALTER TABLE " + tableIdent(r.Name) + " ENABLE ROW LEVEL SECURITY",
			"CREATE POLICY gregale_pipeline_read ON " + tableIdent(r.Name) + " FOR SELECT USING (false)",
			"COMMENT ON TABLE " + tableIdent(r.Name) + " IS " + literal(marker(p)),
		} {
			if _, err = tx.Exec(ctx, sql); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors(pipeline_id,generation,schema_hash,barrier,tables) VALUES($1,$2,$3,'0/0',$4)`, p.ID, p.Generation, hash, p.Tables); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ensurePublication(ctx context.Context, p Pipeline, source *pgxpool.Pool) error {
	name := pipelineName(p)
	var comment string
	err := source.QueryRow(ctx, `SELECT coalesce(obj_description(oid,'pg_publication'),'') FROM pg_publication WHERE pubname=$1`, name).Scan(&comment)
	if errors.Is(err, pgx.ErrNoRows) {
		conn, err := source.Acquire(ctx)
		if err != nil {
			return err
		}
		defer conn.Release()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		tables := make([]string, 0, len(p.Tables))
		for _, table := range p.Tables {
			tables = append(tables, tableIdent(table))
		}
		if _, err = tx.Exec(ctx, "CREATE PUBLICATION "+ident(name)+" FOR TABLE "+strings.Join(tables, ",")); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "COMMENT ON PUBLICATION "+ident(name)+" IS "+literal(marker(p))); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if comment != marker(p) {
		return &blocked{"source_publication_conflict", false}
	}
	var fullChanges bool
	if err = source.QueryRow(ctx, `SELECT NOT puballtables AND pubinsert AND pubupdate AND pubdelete AND pubtruncate AND NOT pubviaroot FROM pg_publication WHERE pubname=$1`, name).Scan(&fullChanges); err != nil {
		return err
	}
	if !fullChanges {
		return &blocked{"source_publication_changed", true}
	}
	var matches bool
	err = source.QueryRow(ctx, `SELECT count(*)=$2 AND bool_and(schemaname='public' AND tablename=ANY($3::text[]) AND rowfilter IS NULL) FROM pg_publication_tables WHERE pubname=$1`, name, len(p.Tables), p.Tables).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return &blocked{"source_schema_changed", true}
	}
	return nil
}

func ensureSubscription(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, conninfo string) error {
	name := pipelineName(p)
	var initialized bool
	if err := target.QueryRow(ctx, `SELECT initialized FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&initialized); err != nil {
		return err
	}
	var exists bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()))`, name).Scan(&exists); err != nil {
		return err
	}
	var slot bool
	if err := source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1 AND plugin='pgoutput' AND database=current_database() AND coalesce(wal_status,'')<>'lost')`, name).Scan(&slot); err != nil {
		return err
	}
	if (initialized && !exists) || (exists && !slot) {
		return &blocked{"replication_state_lost", true}
	}
	if !slot {
		if _, err := source.Exec(ctx, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, name); err != nil {
			return err
		}
	}
	if !exists {
		var barrier string
		if err := source.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&barrier); err != nil {
			return err
		}
		if _, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET barrier=$3::pg_lsn WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, barrier); err != nil {
			return err
		}
		// Slot creation is separate so source and destination can safely be
		// different databases on the same PostgreSQL server as well.
		sql := "CREATE SUBSCRIPTION " + ident(name) + " CONNECTION " + literal(conninfo) + " PUBLICATION " + ident(name) + " WITH (create_slot=false,slot_name=" + literal(name) + ",copy_data=true)"
		if _, err := target.Exec(ctx, sql); err != nil {
			return err
		}
		if _, err := target.Exec(ctx, "COMMENT ON SUBSCRIPTION "+ident(name)+" IS "+literal(marker(p))); err != nil {
			return err
		}
	}
	// CREATE SUBSCRIPTION cannot be transactional. Validate the immutable
	// remote identity before recovering a lost creation acknowledgement.
	var slotName, actualConnection string
	var publications []string
	if err := target.QueryRow(ctx, `SELECT subslotname,subpublications,subconninfo FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, name).Scan(&slotName, &publications, &actualConnection); err != nil {
		return err
	}
	if slotName != name || !slices.Equal(publications, []string{name}) {
		return &blocked{"destination_subscription_conflict", false}
	}
	if actualConnection != conninfo {
		if _, err := target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(name)+" CONNECTION "+literal(conninfo)); err != nil {
			return err
		}
	}
	if err := setSubscriptionEnabled(ctx, p, target, true); err != nil {
		return err
	}
	_, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET initialized=true WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation)
	return err
}

func setSubscriptionEnabled(ctx context.Context, p Pipeline, target *pgx.Conn, enabled bool) error {
	var current bool
	err := target.QueryRow(ctx, `SELECT subenabled FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, pipelineName(p)).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current == enabled {
		return nil
	}
	action := "DISABLE"
	if enabled {
		action = "ENABLE"
	}
	_, err = target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(p))+" "+action)
	return err
}

func allowMirrorReads(ctx context.Context, p Pipeline, target *pgx.Conn, allowed bool) error {
	var current bool
	if err := target.QueryRow(ctx, `SELECT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&current); err != nil {
		return err
	}
	if current == allowed {
		return nil
	}
	subjects := make([]string, 0, len(p.ReadSubjects))
	for _, subject := range p.ReadSubjects {
		subjects = append(subjects, literal(subject))
	}
	predicate := "false"
	if allowed {
		predicate = "(coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb->>'sub') = ANY(ARRAY[" + strings.Join(subjects, ",") + "]::text[])"
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, table := range p.Tables {
		if _, err := tx.Exec(ctx, "ALTER POLICY gregale_pipeline_read ON "+tableIdent(table)+" USING ("+predicate+")"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET readable=$3 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, allowed); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func checkRetention(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, ob *Observation) error {
	var retained int64
	var lost bool
	err := source.QueryRow(ctx, `SELECT coalesce(greatest(pg_wal_lsn_diff(pg_current_wal_lsn(),restart_lsn),0),0)::bigint,coalesce(wal_status='lost',false) FROM pg_replication_slots WHERE slot_name=$1`, pipelineName(p)).Scan(&retained, &lost)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	ob.RetainedWALBytes = &retained
	if lost {
		return &blocked{"replication_state_lost", true}
	}
	if retained > api.DataPipelineRetainedWALMaxBytes {
		return &blocked{"retained_wal_limit", true}
	}
	return nil
}

func cleanupGeneration(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn) error {
	var ledger bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&ledger); err != nil {
		return err
	}
	if ledger {
		if _, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET retiring=true WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation); err != nil {
			return err
		}
	}
	var comment string
	publicationErr := source.QueryRow(ctx, `SELECT coalesce(obj_description(oid,'pg_publication'),'') FROM pg_publication WHERE pubname=$1`, pipelineName(p)).Scan(&comment)
	if publicationErr != nil && !errors.Is(publicationErr, pgx.ErrNoRows) {
		return publicationErr
	}
	if publicationErr == nil && comment != marker(p) {
		return &blocked{"source_publication_conflict", false}
	}
	var subscription bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()))`, pipelineName(p)).Scan(&subscription); err != nil {
		return err
	}
	if subscription {
		if err := setSubscriptionEnabled(ctx, p, target, false); err != nil {
			return err
		}
		var slot bool
		if err := source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1 AND database=current_database())`, pipelineName(p)).Scan(&slot); err != nil {
			return err
		}
		if !slot {
			// Native DROP SUBSCRIPTION cleans table-sync slots as well as its main
			// slot. Restore a missing main slot only for this disabled, durably
			// retiring generation, then let Postgres perform the complete cleanup.
			if _, err := source.Exec(ctx, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, pipelineName(p)); err != nil {
				return err
			}
		}
		if _, err := target.Exec(ctx, "DROP SUBSCRIPTION "+ident(pipelineName(p))); err != nil {
			return err
		}
	}
	var slot bool
	if err := source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1 AND database=current_database())`, pipelineName(p)).Scan(&slot); err != nil {
		return err
	}
	if slot {
		if _, err := source.Exec(ctx, `SELECT pg_drop_replication_slot($1)`, pipelineName(p)); err != nil {
			return err
		}
	}
	if publicationErr == nil {
		if _, err := source.Exec(ctx, "DROP PUBLICATION "+ident(pipelineName(p))); err != nil {
			return err
		}
	}

	// A resync explicitly replaces only tables carrying this generation's
	// ownership marker. Deleting a pipeline retains its destination data.
	if p.DesiredState != "deleted" {
		tx, err := target.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for _, table := range p.Tables {
			var owned bool
			if err := tx.QueryRow(ctx, `SELECT coalesce(obj_description(to_regclass($1),'pg_class'),'')=$2`, tableIdent(table), marker(p)).Scan(&owned); err != nil {
				return err
			}
			if owned {
				if _, err := tx.Exec(ctx, "DROP TABLE "+tableIdent(table)); err != nil {
					return err
				}
			}
		}
		var ledger bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&ledger); err != nil {
			return err
		}
		if ledger {
			if _, err := tx.Exec(ctx, `DELETE FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	return nil
}

// A bounded digest check is conclusive only if the source's WAL position is
// unchanged over both reads and replication has acknowledged that position.
// Busy sources and oversized tables are inconclusive, never reported corrupt.
func verifyMirror(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation) (string, error) {
	var before, after string
	if err := source.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&before); err != nil {
		return "", err
	}
	var caught bool
	if err := source.QueryRow(ctx, `SELECT coalesce(confirmed_flush_lsn >= $2::pg_lsn,false) FROM pg_replication_slots WHERE slot_name=$1`, pipelineName(p), before).Scan(&caught); err != nil {
		return "", err
	}
	if !caught {
		return "inconclusive", nil
	}
	matches := true
	for _, r := range relations {
		columns, keys := []string{}, []string{}
		for _, c := range r.Columns {
			columns = append(columns, ident(c.Name))
			if c.Key {
				key := ident(c.Name)
				if c.Type == "text" || strings.HasPrefix(c.Type, "character") {
					key += ` COLLATE "pg_catalog"."C"`
				}
				keys = append(keys, key)
			}
		}
		query := "SELECT substring(pg_catalog.convert_to(pg_catalog.to_jsonb(t)::text,'UTF8') FROM 1 FOR $2) FROM (SELECT " + strings.Join(columns, ",") + " FROM " + tableIdent(r.Name) + " ORDER BY " + strings.Join(keys, ",") + " LIMIT $1) t"
		a, err := digestRows(ctx, source, query)
		if err != nil {
			return "", err
		}
		b, err := digestRows(ctx, target, query)
		if err != nil {
			return "", err
		}
		if a == "" || b == "" {
			return "inconclusive", nil
		}
		matches = matches && a == b
	}
	if err := source.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&after); err != nil {
		return "", err
	}
	if before != after {
		return "inconclusive", nil
	}
	if !matches {
		return "mismatch", nil
	}
	return "passed", nil
}

type queryRows interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func digestRows(ctx context.Context, db queryRows, query string) (string, error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Canonical output avoids false mismatches from source role defaults.
	if _, err = tx.Exec(ctx, `SET LOCAL timezone='UTC'; SET LOCAL datestyle='ISO, YMD'; SET LOCAL intervalstyle='postgres'; SET LOCAL extra_float_digits=3`); err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx, query, api.DataPipelineVerificationRowsMax+1, api.DataPipelineVerificationBytesMax+1)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	count, bytes := 0, 0
	for rows.Next() {
		var row []byte
		if err := rows.Scan(&row); err != nil {
			return "", err
		}
		count++
		bytes += len(row)
		if count > api.DataPipelineVerificationRowsMax || bytes > api.DataPipelineVerificationBytesMax {
			return "", nil
		}
		_, _ = fmt.Fprintf(h, "%d:", len(row))
		_, _ = h.Write(row)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
