//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

const migrationSocketDir = "/run/gregale-migration"
const migrationFeedSocketDir = "/run/gregale-subscriber"
const migrationPassfile = migrationSocketDir + "/pgpass"
const migrationReceiptExists = "SELECT to_regclass('gregale_migration.receipt') IS NOT NULL"

type liveMigration struct {
	dbmigration.Progress
	Pin, System, Source, User, Target, Owner, Schema string
	Floor, Confirmed, Sender                         string
	SlotIntent                                       bool
	StopRequested                                    bool
	PreparePending                                   *dbmigration.Index
	VerificationToken                                string
	VerificationMarkerReady                          bool
	Activation                                       *migrationActivation
	running                                          bool
}

func (m *liveMigration) resource() string {
	return dbmigration.ResourceName(m.OperationID, m.Pin)
}

func (g *databaseGuest) setMigrationFence() {
	g.migrationFence.Store(true)
	g.migrationAdmission.Store(nil)
	g.primarySessions.end()
	g.replicas.end()
}

func (g *databaseGuest) restoreMigrationFence() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	found, err := g.queryIn(ctx, "postgres", migrationReceiptExists)
	if err != nil || (found != "t" && found != "f") {
		return false
	}
	if found == "t" {
		g.setMigrationFence()
		m, err := g.readMigration(ctx)
		if err != nil {
			return false
		}
		g.restoreMigrationAdmission(m)
	}
	return true
}

func (g *databaseGuest) readMigration(ctx context.Context) (*liveMigration, error) {
	found, err := g.queryIn(ctx, "postgres", migrationReceiptExists)
	if err != nil || found == "f" {
		return nil, err
	}
	raw, err := g.queryIn(ctx, "postgres", "SELECT record FROM gregale_migration.receipt WHERE singleton")
	if err != nil {
		return nil, err
	}
	var m liveMigration
	if err := json.Unmarshal([]byte(raw), &m); err != nil || !dbguest.ValidOperationID(m.OperationID) {
		return nil, errors.New("invalid migration receipt")
	}
	return &m, nil
}

func (g *databaseGuest) writeMigration(ctx context.Context, m *liveMigration, create bool) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	value := dbmigration.Literal(string(raw)) + "::jsonb"
	sql := "UPDATE gregale_migration.receipt SET record=" + value + " WHERE singleton"
	if create {
		sql = `CREATE SCHEMA gregale_migration; REVOKE ALL ON SCHEMA gregale_migration FROM PUBLIC;
CREATE TABLE gregale_migration.receipt (singleton boolean PRIMARY KEY CHECK (singleton), record jsonb NOT NULL);
INSERT INTO gregale_migration.receipt VALUES (true, ` + value + ")"
	}
	_, err = g.queryIn(ctx, "postgres", sql)
	return err
}

func (g *databaseGuest) publishMigrationProgress(m *liveMigration) *dbmigration.Progress {
	p := dbmigration.Progress{State: dbmigration.None}
	if m != nil {
		p = m.Progress
		if p.Preparation != nil && p.Preparation.State == dbmigration.Preparing && g.migrationPrepareCancel == nil {
			interrupted := *p.Preparation
			interrupted.State, interrupted.Reason = dbmigration.Paused, "preparation_interrupted"
			p.Preparation = &interrupted
		}
		if p.Verification != nil && p.Verification.State == dbmigration.Verifying &&
			(!m.running || m.StopRequested || m.State == dbmigration.ResyncRequired) {
			waiting := *p.Verification
			waiting.State, waiting.Reason = dbmigration.Paused, "migration_not_active"
			p.Verification = &waiting
		}
		if !m.running && p.Active() {
			p.State, p.Reason = dbmigration.Paused, "resume_required"
		}
	}
	g.migrationPublic.Store(&p)
	return &p
}

func (g *databaseGuest) migrationResponse(m *liveMigration) dbguest.Response {
	r := g.response()
	r.MigrationProgress = g.publishMigrationProgress(m)
	return r
}

func (g *databaseGuest) liveMigrationStatus() dbguest.Response {
	if !g.migrationMu.TryLock() {
		r := g.response()
		r.MigrationProgress = g.migrationPublic.Load()
		if r.MigrationProgress == nil {
			r.Error = dbguest.ErrCodeNotReady
		}
		return r
	}
	defer g.migrationMu.Unlock()
	if g.migration == nil && g.currentState() == dbguest.StateReady {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		m, err := g.readMigration(ctx)
		if err != nil {
			r := g.response()
			r.Error = dbguest.ErrCodeNotReady
			return r
		}
		g.migration = m
	}
	return g.migrationResponse(g.migration)
}

func (g *databaseGuest) controlLiveMigration(req dbguest.Request) dbguest.Response {
	if !g.migrationMu.TryLock() {
		r := g.response()
		r.Error = dbguest.ErrCodeNotReady
		return r
	}
	defer g.migrationMu.Unlock()
	r := g.response()
	src := *req.Import
	if r.State != dbguest.StateReady {
		r.Error = dbguest.ErrCodeNotReady
		return r
	}
	if req.MigrationPin == "" || len(src.SkipExtensions) != 0 || strings.ContainsRune(src.Password, '\r') {
		r.Error = dbguest.ErrCodeInvalidRequest
		return r
	}
	if status := g.importResponse(); status.Import.State == dbguest.ImportRunning {
		r.Error = dbguest.ErrCodeImportOperationMismatch
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
	defer cancel()
	if g.migration == nil {
		m, err := g.readMigration(ctx)
		if err != nil {
			r.Error = dbguest.ErrCodeNotReady
			return r
		}
		g.migration = m
	}
	m := g.migration
	if m != nil && (m.OperationID != src.OperationID || m.Pin != req.MigrationPin ||
		m.Source != src.Database || m.User != src.User || m.Target != src.TargetDatabase || m.Owner != src.Owner) {
		r.Error = dbguest.ErrCodeImportOperationMismatch
		return r
	}
	if m == nil && req.Op != dbguest.OpMigrationStart {
		r.Error = dbguest.ErrCodeImportOperationMismatch
		return r
	}
	if m != nil && m.Activation != nil {
		if req.Op != dbguest.OpMigrationStop || m.Activation.Phase != migrationOpened {
			r.Error = dbguest.ErrCodeImportOperationMismatch
			return r
		}
		if m.StopRequested && m.Reason == "" {
			return g.migrationResponse(m)
		}
	}
	if req.Op == dbguest.OpMigrationVerify {
		return g.queueMigrationVerification(ctx, m, req)
	}
	if req.Op == dbguest.OpMigrationPrepare {
		return g.queueMigrationPreparation(ctx, m, src)
	}
	if m != nil && (m.State == dbmigration.Stopped || m.State == dbmigration.ResyncRequired && req.Op != dbguest.OpMigrationStop) {
		return g.migrationResponse(m)
	}
	if m != nil && (m.StopRequested || m.running && m.State != dbmigration.Paused) && req.Op == dbguest.OpMigrationStart {
		return g.migrationResponse(m)
	}
	g.migrationOnce.Do(func() {
		g.migrationBridgeErr = g.listenSourceBridge(migrationSocketDir, dbguest.MigrationPort)
		if g.migrationBridgeErr == nil {
			g.migrationBridgeErr = g.listenGuardedSourceBridge(migrationFeedSocketDir, dbguest.MigrationPort, g.admitMigrationFeed)
		}
	})
	if g.migrationBridgeErr != nil {
		r.Error = dbguest.ErrCodeNotReady
		return r
	}
	if req.Op == dbguest.OpMigrationStop {
		return g.stopLiveMigration(ctx, m, src)
	}
	system, err := g.migrationSourceIdentity(ctx, src)
	if err != nil {
		r.Error = dbguest.ErrCodeSourceDown
		return r
	}
	if m != nil && m.System != system {
		g.invalidateMigration(ctx, m, "source_identity_changed")
		m.running = false
		g.migrationRunning.Store(false)
		return g.migrationResponse(m)
	}
	if m == nil {
		var blocked string
		m, blocked = g.prepareLiveMigration(ctx, src, req.MigrationPin, system)
		if blocked != "" {
			r = g.migrationResponse(nil)
			r.MigrationProgress = &dbmigration.Progress{OperationID: src.OperationID, State: dbmigration.Paused, Reason: blocked}
			return r
		}
		g.migration = m
	}
	if err := g.writeMigrationPassfile(src); err != nil {
		r.Error = dbguest.ErrCodeNotReady
		return r
	}
	if g.migrationFeed.Load() != nil {
		g.offerMigrationFeed(m, src)
	}
	m.running = true
	m.State, m.Reason = dbmigration.Preparing, ""
	g.migrationGeneration++
	g.migrationRunning.Store(true)
	go g.runLiveMigration(m, src, g.migrationGeneration)
	return g.migrationResponse(m)
}

func (g *databaseGuest) prepareLiveMigration(ctx context.Context, src dbguest.ImportSource, pin, system string) (*liveMigration, string) {
	check := g.inspectMigration(ctx, src, g.migrationSourceQuery)
	if check.Outcome != dbmigration.Compatible {
		return nil, check.Reasons[0]
	}
	tables, err := g.migrationInventory(ctx, src, nil)
	if err != nil || len(tables) != int(check.TableCount) {
		return nil, "source_schema_unsupported"
	}
	if _, err := dbmigration.SchemaSQL(tables); err != nil {
		return nil, "source_schema_unsupported"
	}
	g.setMigrationFence()
	// Recheck after closing customer sessions and waiting for their backends
	// to terminate. The maintenance session is in a separate database.
	drained, err := g.queryIn(ctx, "postgres",
		"SELECT coalesce(bool_and(pg_terminate_backend(pid, 3000)), true) FROM pg_catalog.pg_stat_activity WHERE backend_type='client backend' AND datname="+dbmigration.Literal(src.TargetDatabase))
	if err != nil || drained != "t" {
		g.releaseUncommittedFence()
		return nil, "target_sessions_active"
	}
	if err := g.checkImportTargetEmpty(ctx, src.TargetDatabase, false); err != nil {
		g.releaseUncommittedFence()
		return nil, "target_not_empty"
	}
	m := &liveMigration{
		Progress: dbmigration.Progress{OperationID: src.OperationID, State: dbmigration.Preparing, Tables: len(tables)},
		Pin:      pin, System: system, Source: src.Database, User: src.User, Target: src.TargetDatabase,
		Owner: src.Owner, Schema: dbmigration.SchemaDigest(tables),
	}
	if err := g.writeMigration(ctx, m, true); err != nil {
		g.releaseUncommittedFence()
		return nil, "migration_receipt_unavailable"
	}
	return m, ""
}

func (g *databaseGuest) releaseUncommittedFence() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	found, err := g.queryIn(ctx, "postgres", migrationReceiptExists)
	if err == nil && found == "f" {
		g.primarySessions.resume()
		g.migrationFence.Store(false)
	}
}

func migrationDSN(src dbguest.ImportSource) string {
	return fmt.Sprintf("host=%s port=5432 dbname=%s user=%s sslmode=disable channel_binding=disable connect_timeout=5 application_name=gregale_migration",
		migrationSocketDir, src.Database, src.User)
}

func (g *databaseGuest) migrationSourceQuery(ctx context.Context, src dbguest.ImportSource, sql string) (string, error) {
	raw, err := g.sourceRecordsDSN(ctx, src, migrationDSN(src), sql, 2<<20)
	return strings.TrimSpace(strings.TrimSuffix(string(raw), "\x00")), err
}

func (g *databaseGuest) migrationSourceIdentity(ctx context.Context, src dbguest.ImportSource) (string, error) {
	raw, err := g.sourceRecordsDSN(ctx, src, migrationDSN(src)+" replication=database", "IDENTIFY_SYSTEM", 512)
	parts := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "|")
	if err != nil || len(parts) != 4 || parts[3] != src.Database {
		return "", errors.New("source replication identity unavailable")
	}
	system, se := strconv.ParseUint(parts[0], 10, 64)
	timeline, te := strconv.ParseUint(parts[1], 10, 32)
	if se != nil || te != nil || system == 0 || timeline == 0 {
		return "", errors.New("invalid source replication identity")
	}
	return parts[0] + ":" + parts[1], nil
}

func (g *databaseGuest) writeMigrationPassfile(src dbguest.ImportSource) error {
	f, err := os.CreateTemp(migrationSocketDir, ".pgpass-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	escaped := strings.NewReplacer("\\", "\\\\", ":", "\\:").Replace(src.Password)
	if _, err := fmt.Fprintf(f, "*:5432:%s:%s:%s\n", src.Database, src.User, escaped); err != nil {
		return err
	}
	if err := f.Chown(g.uid, g.gid); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), migrationPassfile)
}

func (g *databaseGuest) migrationInventory(ctx context.Context, src dbguest.ImportSource, m *liveMigration) ([]dbmigration.Table, error) {
	sql := dbmigration.InventorySQL
	if m != nil && m.Verification != nil {
		sql = dbmigration.InventoryWithMarkerSQL(m.verificationMarker())
	}
	raw, err := g.migrationSourceQuery(ctx, src, sql)
	if err != nil {
		return nil, err
	}
	var tables []dbmigration.Table
	err = json.Unmarshal([]byte(raw), &tables)
	return tables, err
}

func (g *databaseGuest) migrationSlot(ctx context.Context, m *liveMigration, src dbguest.ImportSource) (dbmigration.Slot, error) {
	raw, err := g.migrationSourceQuery(ctx, src, dbmigration.SlotSQL(m.resource()))
	var slot dbmigration.Slot
	if err == nil {
		err = json.Unmarshal([]byte(raw), &slot)
	}
	return slot, err
}

func (g *databaseGuest) migrationSchema(ctx context.Context, m *liveMigration, tables []dbmigration.Table) error {
	tag := "gregale:migration:v1:" + m.OperationID
	target := dbmigration.Literal(m.Target)
	marker, err := g.queryIn(ctx, m.Target, "SELECT shobj_description(oid, 'pg_database') FROM pg_catalog.pg_database WHERE datname="+target)
	if err != nil || marker == tag {
		return err
	}
	if err := g.checkImportTargetEmpty(ctx, m.Target, false); err != nil {
		return err
	}
	schema, err := dbmigration.SchemaSQL(tables)
	if err != nil {
		return err
	}
	// A full inventory can exceed Linux's per-argument limit. Send the
	// generated DDL over stdin and commit its marker with the tables.
	args := databasePsqlArgs(g.major)
	for i, arg := range args {
		if arg == "-d" {
			args[i+1] = m.Target
			break
		}
	}
	cmd := g.asSuperuser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Stdin = strings.NewReader("BEGIN; SET LOCAL ROLE " + dbmigration.Ident(m.Owner) + "; " + schema +
		"RESET ROLE; COMMENT ON DATABASE " + dbmigration.Ident(m.Target) + " IS " + dbmigration.Literal(tag) + "; COMMIT;\n")
	return cmd.Run()
}

type publicationState struct {
	Exists bool
	Marker string
	Tables []string
	Usable bool
}

func (g *databaseGuest) migrationPublication(ctx context.Context, m *liveMigration, src dbguest.ImportSource) (publicationState, error) {
	raw, err := g.migrationSourceQuery(ctx, src, dbmigration.PublicationSQL(m.resource()))
	var p publicationState
	if err == nil {
		err = json.Unmarshal([]byte(raw), &p)
	}
	return p, err
}

func (g *databaseGuest) ensureMigrationPublication(ctx context.Context, m *liveMigration, src dbguest.ImportSource, tables []dbmigration.Table) bool {
	p, err := g.migrationPublication(ctx, m, src)
	if err != nil {
		return false
	}
	if p.Marker == m.OperationID && p.Usable {
		names := dbmigration.TableNames(tables)
		if !m.VerificationMarkerReady && slices.Equal(p.Tables, names) {
			return true
		}
		if m.Verification != nil {
			names = append(names, "public."+m.verificationMarker().Name)
			slices.Sort(names)
			if slices.Equal(p.Tables, names) {
				return true
			}
		}
	}
	if m.SlotIntent || p.Exists {
		return false
	}
	_, err = g.migrationSourceQuery(ctx, src, dbmigration.CreatePublicationSQL(m.resource(), m.OperationID, tables))
	return err == nil
}

func (g *databaseGuest) initializeMigrationSlot(ctx context.Context, m *liveMigration, src dbguest.ImportSource, slot dbmigration.Slot) string {
	if slot.Exists {
		g.invalidateMigration(ctx, m, "source_slot_unverified")
		return m.Reason
	}
	m.SlotIntent = true
	if err := g.writeMigration(ctx, m, false); err != nil {
		return "migration_receipt_unavailable"
	}
	floor, err := g.migrationSourceQuery(ctx, src, "SELECT lsn::text FROM pg_catalog.pg_create_logical_replication_slot("+
		dbmigration.Literal(m.resource())+", 'pgoutput')")
	if _, ok := dbmigration.Position(floor); err != nil || !ok {
		return "source_slot_creation_unverified"
	}
	m.Floor, m.Confirmed = floor, floor
	if err := g.writeMigration(ctx, m, false); err != nil {
		m.Floor, m.Confirmed = "", ""
		return "migration_receipt_unavailable"
	}
	return ""
}

func (g *databaseGuest) invalidateMigration(ctx context.Context, m *liveMigration, reason string) {
	g.cancelMigrationPreparation()
	// Removing the ephemeral credential also prevents fresh connections if
	// PostgreSQL is temporarily unable to disable the existing subscriber.
	g.migrationFeed.Store(nil)
	_ = os.Remove(migrationPassfile)
	local, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = g.setMigrationSubscription(local, m, false)
	m.State, m.Reason = dbmigration.ResyncRequired, reason
	_ = g.writeMigration(local, m, false)
}

func (g *databaseGuest) setMigrationSubscription(ctx context.Context, m *liveMigration, enable bool) error {
	action := " DISABLE"
	if enable {
		action = " ENABLE"
	}
	_, err := g.queryIn(ctx, m.Target, "ALTER SUBSCRIPTION "+dbmigration.Ident(m.resource())+action)
	return err
}

func (g *databaseGuest) stopLiveMigration(ctx context.Context, m *liveMigration, src dbguest.ImportSource) dbguest.Response {
	g.cancelMigrationPreparation()
	// Preserve the fence even when stopped. The target may contain only a
	// subset of the source and a separate cutover must verify completeness.
	m.running = false
	m.StopRequested = true
	g.migrationRunning.Store(false)
	if m.State != dbmigration.ResyncRequired && m.State != dbmigration.Activated {
		m.State = dbmigration.Paused
	}
	m.Reason = "source_cleanup_unverified"
	g.migrationFeed.Store(nil)
	_ = os.Remove(migrationPassfile)
	if err := g.writeMigration(ctx, m, false); err != nil {
		_ = g.setMigrationSubscription(ctx, m, false)
		return g.migrationResponse(m)
	}
	sub, err := g.migrationSubscription(ctx, m)
	if err != nil {
		return g.migrationResponse(m)
	}
	if sub.Exists {
		if g.setMigrationSubscription(ctx, m, false) != nil {
			return g.migrationResponse(m)
		}
		sub, err = g.migrationSubscription(ctx, m)
		if err != nil {
			return g.migrationResponse(m)
		}
	}
	system, err := g.migrationSourceIdentity(ctx, src)
	if err != nil || system != m.System {
		return g.migrationResponse(m)
	}
	if err := g.writeMigrationPassfile(src); err != nil {
		return g.migrationResponse(m)
	}
	defer os.Remove(migrationPassfile)
	slot, err := g.migrationSlot(ctx, m, src)
	if err != nil || slot.Exists && (m.State == dbmigration.ResyncRequired ||
		!dbmigration.IsContinuous(slot, src.Database, m.Floor, m.Confirmed, sub.Durable)) {
		return g.migrationResponse(m)
	}
	name := dbmigration.Ident(m.resource())
	if sub.Exists {
		if !slot.Exists {
			if _, err := g.queryIn(ctx, m.Target, "ALTER SUBSCRIPTION "+name+" SET (slot_name=NONE)"); err != nil {
				return g.migrationResponse(m)
			}
		}
		g.migrationFeed.Store(&migrationFeed{receipt: *m, source: src, lost: new(atomic.Bool), cleanup: true, cleanupDurable: sub.Durable})
		defer g.migrationFeed.Store(nil)
		if _, err := g.queryIn(ctx, m.Target, "DROP SUBSCRIPTION "+name); err != nil {
			return g.migrationResponse(m)
		}
	} else if slot.Exists {
		if _, err := g.migrationSourceQuery(ctx, src, "SELECT pg_catalog.pg_drop_replication_slot("+
			dbmigration.Literal(m.resource())+")"); err != nil {
			return g.migrationResponse(m)
		}
	}
	p, err := g.migrationPublication(ctx, m, src)
	if err != nil {
		return g.migrationResponse(m)
	}
	if p.Exists {
		if p.Marker != m.OperationID || !p.Usable {
			return g.migrationResponse(m)
		}
		if _, err := g.migrationSourceQuery(ctx, src, "DROP PUBLICATION "+name); err != nil {
			return g.migrationResponse(m)
		}
	}
	if m.Verification != nil {
		marker := m.verificationMarker()
		state, err := g.migrationSourceQuery(ctx, src, marker.StateSQL())
		if err != nil || state != "true" && state != "null" {
			return g.migrationResponse(m)
		}
		if state == "true" {
			if _, err := g.migrationSourceQuery(ctx, src, marker.DropSQL()); err != nil {
				return g.migrationResponse(m)
			}
		}
	}
	if m.Activation != nil && m.Activation.Phase == migrationOpened {
		m.State = dbmigration.Activated
	} else {
		m.State = dbmigration.Stopped
	}
	m.Reason = ""
	m.RetainedWALBytes = 0
	if err := g.writeMigration(ctx, m, false); err != nil {
		if m.State != dbmigration.Activated {
			m.State = dbmigration.Paused
		}
		m.Reason = "migration_receipt_unavailable"
	}
	return g.migrationResponse(m)
}
