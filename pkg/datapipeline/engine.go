package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

// TargetSQL is private worker-only access. Credentials and provider identities
// must never be returned by the customer HTTP API or delivered to an app.
type TargetSQL func(context.Context, string, string, func(*pgx.Conn, string) error) error

type Engine struct {
	RefreshSchema func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error)
	Policy        commit.NetworkPolicy
	Target        TargetSQL
	Capacity      *CapacityObserver
	Admit         func(context.Context, Pipeline) error
	// Test-only seam: production always uses Commit's verified/pinned dialer.
	openSource func(context.Context, string) (*pgxpool.Pool, error)
	pinSource  func(context.Context, string) (string, error)
}
type column struct {
	Name, Type                 string
	NotNull, Key               bool
	Modifier                   int32
	Ordinal                    int32 `json:",omitempty"`
	Default, Missing, Identity bool  `json:",omitempty"`
	Unsupported                bool  `json:",omitempty"`
}
type relation struct {
	Name        string
	Columns     []column
	OID         uint32 `json:",omitempty"`
	Unsupported bool   `json:",omitempty"`
}
type blocked struct {
	code   string
	resync bool
}

func (e *blocked) Error() string { return e.code }

func (e *Engine) Reconcile(ctx context.Context, p Pipeline, raw string, current func(context.Context) error) Observation {
	ob := Observation{VerificationProgress: p.VerificationProgress, State: "blocked", Reason: "database_unavailable", Verification: p.Verification, Initialized: p.Initialized}
	if p.VerifiedAt != "" {
		ob.VerifiedAt, _ = time.Parse(time.RFC3339Nano, p.VerifiedAt)
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
		applied, err := applyReaders(ctx, p, target, current)
		if err != nil {
			return err
		}
		if applied != nil {
			ob.ReaderChange, ob.ReaderOnly = applied, true
			// Acknowledge the policy receipt before ordinary reconciliation can
			// retire this generation and delete it. A lost acknowledgement must
			// always leave an authoritative receipt for the next worker.
			ob.State, ob.Reason, ob.TablesReady = p.State, p.Reason, p.TablesReady
			if p.DesiredState == "paused" {
				ob.State = "paused"
			}
			return nil
		}
		source, err := e.connectSource(ctx, raw)
		if err != nil {
			var authentication *pgconn.PgError
			if (errors.Is(err, commit.ErrConnectionAuthentication) || (errors.As(err, &authentication) && (authentication.Code == "28P01" || authentication.Code == "28000"))) && p.DesiredState == "running" {
				return &blocked{"source_authentication_failed", false}
			}

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
		if p.DesiredState == "running" && e.Capacity != nil && !activeTableCancellation(p) {
			job := ConnectionCheck{AccountID: p.AccountID, DataConnectionCheck: api.DataConnectionCheck{ConnectionID: p.SourceConnectionID, ConnectionRevision: p.SourceRevision}}
			_, sourceErr := e.Capacity.source(ctx, job, raw, source)
			_, targetErr := e.Capacity.target(ctx, p.AccountID, p.DatabaseID, target)
			if sourceErr != nil || targetErr != nil || e.Admit == nil {
				return capacityBlock(connectioncapacity.ErrUnknown)
			}
			if err = e.Admit(ctx, p); err != nil {
				return capacityBlock(err)
			}
		}
		return e.reconcile(ctx, p, raw, source, target, owner, &ob, current)
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

func (e *Engine) reconcile(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, owner string, ob *Observation, current func(context.Context) error) (result error) {
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
	if !activeTableCancellation(p) && p.VerificationProgress != nil && p.VerificationProgress.State == "cancelled" {
		c, err := loadVerification(ctx, target, p)
		if err != nil {
			return err
		}
		if c != nil && (c.Progress.ID == p.VerificationProgress.ID || p.VerificationProgress.IntentID != "" && c.Progress.IntentID == p.VerificationProgress.IntentID) {
			if err = cleanupVerification(ctx, p, target); err != nil {
				return err
			}
		}
	}
	if !activeTableCancellation(p) && p.VerificationProgress != nil && p.VerificationProgress.State == "limited" {
		c, err := loadVerification(ctx, target, p)
		if err != nil {
			return err
		}
		if c != nil && c.Progress.ID == p.VerificationProgress.ID && c.Progress.State == "limited" && c.Progress.Phase != "stopped" {
			relations := make([]relation, len(p.Tables))
			for i, name := range p.Tables {
				relations[i].Name = name
			}
			if err = retireVerification(ctx, p, target, relations, c, current); err != nil {
				return err
			}
			ob.VerificationProgress = &c.Progress
		}
	}
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
	if handled, err := e.reconcileLiveRepair(ctx, p, raw, source, target, owner, ob, current); err != nil || handled {
		return err
	}
	if activeTableCancellation(p) {
		if p.SchemaChange.Cancellation.Kind == "live" {
			return e.reconcileTableRemoval(ctx, p, raw, source, target, owner, ob, current)
		}
		return e.reconcileTableCancellation(ctx, p, raw, source, target, owner, ob, current)
	}
	if tableWorkflow(p) {
		return e.reconcileTables(ctx, p, raw, source, target, owner, ob, current)
	}
	relations, hash, err := inspectSource(ctx, p, source)
	if err != nil {
		return err
	}
	pinned, err := e.sourceConninfo(ctx, raw)
	if err != nil {
		return err
	}
	if err := admitNativeCopy(ctx, p, source, target); err != nil {
		return err
	}
	activeSchema, hold, err := reconcileSchema(ctx, p, source, target, owner, relations, hash, ob, current)
	if err != nil || hold {
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
	if err := observeAppliedProgress(ctx, p, source, target, ob); err != nil {
		return err
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
	if activeSchema {
		ready, err := e.finishSchema(ctx, p, source, target, relations, ob, current)
		if err != nil || !ready {
			return err
		}
	}
	// Retry quarantine before reopening reads if its first transaction failed
	// or its reply was lost. Catalog mismatch metadata must never become live
	// merely because the ordinary verification request has already finished.
	if !activeSchema && p.VerificationProgress != nil && p.VerificationProgress.State == "mismatch" {
		return quarantineLiveMismatch(ctx, p, source, target, relations, ob, current)
	}
	if !activeSchema {
		if err := allowMirrorReads(ctx, p, target, true); err != nil {
			return err
		}
	}
	ob.State, ob.Reason = "live", ""
	if p.VerifyRequested {
		ready, err := verifyResumable(ctx, p, source, target, relations, ob, current)
		if err != nil && !verificationTransient(err) {
			return err
		}
		if ready {
			ready, err = sealVerification(ctx, p, source, target, relations, ob, current, nil)
		}
		if err != nil && !verificationTransient(err) {
			return err
		}
		ob.Verification = "pending"
		if ready && ob.VerificationProgress != nil {
			ob.Verification = "passed"
			ob.VerifiedAt = ob.VerificationProgress.CompletedAt
		}
		if ob.VerificationProgress != nil && ob.VerificationProgress.State == "limited" {
			ob.Verification = "inconclusive"
		}
		if ob.VerificationProgress != nil && ob.VerificationProgress.State == "mismatch" {
			ob.Verification = "mismatch"
			return quarantineLiveMismatch(ctx, p, source, target, relations, ob, current)
		}
	}
	return nil
}

func inspectSource(ctx context.Context, p Pipeline, source *pgxpool.Pool) ([]relation, string, error) {
	checks, err := inspectSourceSettings(ctx, source)
	if err != nil {
		return nil, "", err
	}
	for _, check := range checks {
		if check.State == "blocked" {
			return nil, "", &blocked{check.Code, false}
		}
	}
	result := make([]relation, 0, len(p.Tables))
	for _, name := range p.Tables {
		r, table, err := inspectSourceTable(ctx, source, name)
		if err != nil {
			return nil, "", err
		}
		if !table.Supported {
			if !p.Initialized {
				return nil, "", &blocked{table.Issues[0].Code, false}
			}
			for _, issue := range table.Issues {
				if issue.Column == "" {
					r.Unsupported = true
				}
			}
		}
		result = append(result, r)
	}
	return result, relationHash(result), nil
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
	occupied, err := targetOccupied(ctx, target)
	if err != nil {
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
		if err = createMirrorTable(ctx, p, tx, owner, r); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
		return err
	}
	if err = ensureSchemaReceipt(ctx, tx); err != nil {
		return err
	}
	spec, _ := json.Marshal(relations)
	if _, err = tx.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors(pipeline_id,generation,schema_hash,barrier,tables,schema_spec) VALUES($1,$2,$3,'0/0',$4,$5)`, p.ID, p.Generation, hash, p.Tables, spec); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ensurePublication(ctx context.Context, p Pipeline, source *pgxpool.Pool) error {
	name := pipelineName(p)
	var comment string
	err := source.QueryRow(ctx, `SELECT coalesce(obj_description(oid,'pg_publication'),'') FROM pg_publication WHERE pubname=$1`, name).Scan(&comment)
	if errors.Is(err, pgx.ErrNoRows) {
		if p.Initialized {
			return &blocked{"replication_state_lost", true}
		}
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
	err = source.QueryRow(ctx, `SELECT count(*)=$2 AND bool_and(schemaname='public' AND tablename=ANY($3::text[]) AND rowfilter IS NULL
 AND attnames=(SELECT array_agg(a.attname ORDER BY a.attnum) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=schemaname AND c.relname=tablename AND a.attnum>0 AND NOT a.attisdropped)) FROM pg_publication_tables WHERE pubname=$1`, name, len(p.Tables), p.Tables).Scan(&matches)
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
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = updateMirrorReads(ctx, p, tx, allowed); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func updateMirrorReads(ctx context.Context, p Pipeline, tx receiptSQL, allowed bool) error {
	if err := updateTableReads(ctx, p, tx, allowed); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET readable=$3 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, allowed)
	return err
}

// A table-only gate must not change the receipt's shared readable flag.
func updateTableReads(ctx context.Context, p Pipeline, tx receiptSQL, allowed bool) error {
	// Approved table selection can precede remote CREATE TABLE. Closing reads
	// must still gate every existing owned table when a new one is absent.
	var owned []string
	if err := tx.QueryRow(ctx, `SELECT coalesce(array_agg(c.relname::text ORDER BY c.relname COLLATE "C"),'{}')
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'
 AND c.relname=ANY($1::text[]) AND coalesce(obj_description(c.oid,'pg_class'),'')=$2`, p.Tables, marker(p)).Scan(&owned); err != nil {
		return err
	}
	if allowed && len(owned) != len(p.Tables) {
		return ErrConflict
	}
	predicate := "false"
	if allowed {
		readers, _, _, err := readerAuthority(ctx, p, tx, false)
		if err != nil {
			return err
		}
		predicate = readerPredicate(readers)
	}
	for _, table := range owned {
		if _, err := tx.Exec(ctx, "ALTER POLICY gregale_pipeline_read ON "+tableIdent(table)+" USING ("+predicate+")"); err != nil {
			return err
		}
	}
	return nil
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
	if err := cleanupVerification(ctx, p, target); err != nil {
		return err
	}
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
