//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

func (g *databaseGuest) queueMigrationPreparation(ctx context.Context, m *liveMigration, src dbguest.ImportSource) dbguest.Response {
	if g.migrationPrepareCancel != nil {
		return g.migrationResponse(m)
	}
	if !g.migrationFence.Load() || !m.running || m.StopRequested || m.Floor == "" ||
		m.TablesReady != m.Tables || (m.State != dbmigration.Live && m.State != dbmigration.CatchingUp) {
		r := g.migrationResponse(m)
		p := *r.MigrationProgress
		p.Preparation = &dbmigration.Preparation{State: dbmigration.Paused, Reason: "migration_catch_up_required"}
		r.MigrationProgress = &p
		return r
	}
	next := *m
	next.Preparation = &dbmigration.Preparation{State: dbmigration.Preparing}
	if err := g.writeMigration(ctx, &next, false); err != nil {
		r := g.response()
		r.Error = dbguest.ErrCodeNotReady
		return r
	}
	*m = next
	work, cancel := context.WithCancel(context.Background())
	g.migrationPrepareCancel = cancel
	go g.runMigrationPreparation(work, next, src)
	return g.migrationResponse(m)
}

func (g *databaseGuest) cancelMigrationPreparation() {
	if g.migrationPrepareCancel != nil {
		g.migrationPrepareCancel()
	}
}

func (g *databaseGuest) runMigrationPreparation(ctx context.Context, snapshot liveMigration, src dbguest.ImportSource) {
	p := g.prepareMigrationIndexes(ctx, &snapshot, src)
	g.migrationMu.Lock()
	defer g.migrationMu.Unlock()
	interrupted := ctx.Err() != nil
	g.cancelMigrationPreparation()
	g.migrationPrepareCancel = nil
	m := g.migration
	if m == nil || m.OperationID != snapshot.OperationID {
		return
	}
	if interrupted || m.StopRequested || m.State == dbmigration.ResyncRequired {
		p.State = dbmigration.Paused
		if p.Reason == "" {
			p.Reason = "preparation_interrupted"
		}
	}
	local, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !g.persistPreparation(local, m, p, m.PreparePending) {
		// The last committed state remains retryable even if the final
		// write failed. Report the problem locally until the next retry.
		p.State, p.Reason = dbmigration.Paused, "migration_receipt_unavailable"
		m.Preparation = &p
		g.publishMigrationProgress(m)
	}
}

// persistPreparation runs under migrationMu. The pending index is committed
// before issuing DDL so retries can distinguish the worker's invalid index
// from a pre-existing object with the same name.
func (g *databaseGuest) persistPreparation(ctx context.Context, m *liveMigration, p dbmigration.Preparation, pending *dbmigration.Index) bool {
	next := *m
	next.Preparation, next.PreparePending = &p, pending
	if g.writeMigration(ctx, &next, false) != nil {
		return false
	}
	*m = next
	g.publishMigrationProgress(m)
	return true
}

func (g *databaseGuest) recordPreparation(ctx context.Context, m *liveMigration, p dbmigration.Preparation, pending *dbmigration.Index) bool {
	g.migrationMu.Lock()
	defer g.migrationMu.Unlock()
	local, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m != nil && g.migration == m && !m.StopRequested && m.State != dbmigration.ResyncRequired &&
		g.persistPreparation(local, m, p, pending)
}

func (g *databaseGuest) preparationSource(ctx context.Context, m *liveMigration, src dbguest.ImportSource) (dbmigration.PreparationInventory, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var inv dbmigration.PreparationInventory
	system, err := g.migrationSourceIdentity(ctx, src)
	if err != nil {
		return inv, "source_inspection_unavailable"
	}
	if system != m.System {
		return inv, "source_identity_changed"
	}
	tables, err := g.migrationInventory(ctx, src, m)
	if err != nil {
		return inv, "source_inspection_unavailable"
	}
	if dbmigration.SchemaDigest(tables) != m.Schema {
		return inv, "source_schema_changed"
	}
	raw, err := g.migrationSourceQuery(ctx, src, "SET search_path = pg_catalog; "+dbmigration.InspectPreparationSQL)
	if err != nil || json.Unmarshal([]byte(raw), &inv) != nil {
		return inv, "source_inspection_unavailable"
	}
	return inv, ""
}

func (g *databaseGuest) prepareMigrationIndexes(ctx context.Context, snapshot *liveMigration, src dbguest.ImportSource) dbmigration.Preparation {
	before, reason := g.preparationSource(ctx, snapshot, src)
	if reason != "" {
		return dbmigration.Preparation{State: dbmigration.Paused, Reason: reason}
	}
	p, plan := before.Plan()
	if len(before.Indexes) > dbmigration.MaxPreparationIndexes {
		p.State = dbmigration.Blocked
		return p
	}

	local, cancel := context.WithTimeout(ctx, 10*time.Second)
	raw, err := g.queryIn(local, snapshot.Target, dbmigration.PreparationIndexesSQL)
	cancel()
	var existing []dbmigration.Index
	if err != nil || json.Unmarshal([]byte(raw), &existing) != nil {
		p.State, p.Reason = dbmigration.Paused, "target_index_inspection_unavailable"
		return p
	}
	desired := make(map[string]dbmigration.Index, len(plan))
	for _, idx := range plan {
		desired[idx.Name] = idx
	}
	pending := snapshot.PreparePending
	if pending != nil && !pending.SameDefinition(desired[pending.Name]) {
		pending = nil
	}
	if pending != nil {
		// Resolve an interrupted build before recording another intent.
		for n, idx := range plan {
			if idx.SameDefinition(*pending) {
				plan[0], plan[n] = plan[n], plan[0]
				break
			}
		}
	}
	present := make(map[string]dbmigration.Index, len(existing))
	for _, idx := range existing {
		if !idx.SameDefinition(desired[idx.Name]) || !idx.Valid && (pending == nil || !idx.SameDefinition(*pending)) {
			p.State = dbmigration.Blocked
			p.Blockers = append(p.Blockers, dbmigration.PreparationBlocker{Code: "target_index_conflict", Count: 1})
			return p
		}
		present[idx.Name] = idx
	}

	// Only this goroutine builds indexes. Replication uses its own worker
	// and can keep applying during PostgreSQL's concurrent index scans.
	g.migrationMu.Lock()
	m := g.migration
	g.migrationMu.Unlock()
	if !g.recordPreparation(ctx, m, p, pending) {
		p.State, p.Reason = dbmigration.Paused, "migration_receipt_unavailable"
		return p
	}
	for _, idx := range plan {
		existing, exists := present[idx.Name]
		if !exists || !existing.Valid {
			if reason := g.buildMigrationIndex(ctx, m, snapshot, p, idx, exists); reason != "" {
				p.State, p.Reason = dbmigration.Paused, reason
				return p
			}
			pending = nil
		}
		if pending != nil && idx.SameDefinition(*pending) {
			pending = nil
		}
		p.IndexesReady++
		if !g.recordPreparation(ctx, m, p, pending) {
			p.State, p.Reason = dbmigration.Paused, "migration_receipt_unavailable"
			return p
		}
	}

	after, reason := g.preparationSource(ctx, snapshot, src)
	if reason != "" || !samePreparationInventory(before, after) {
		if reason == "" {
			reason = "source_schema_changed"
		}
		p.State, p.Reason = dbmigration.Paused, reason
		return p
	}
	p.State = dbmigration.Prepared
	if len(p.Blockers) > 0 {
		p.State = dbmigration.Blocked
	}
	return p
}

func samePreparationInventory(a, b dbmigration.PreparationInventory) bool {
	// Relation sizes may change with source writes during the build.
	for n := range a.Indexes {
		a.Indexes[n].Bytes = 0
	}
	for n := range b.Indexes {
		b.Indexes[n].Bytes = 0
	}
	return reflect.DeepEqual(a, b)
}

func (g *databaseGuest) buildMigrationIndex(ctx context.Context, m, snapshot *liveMigration, p dbmigration.Preparation, index dbmigration.Index, dropInvalid bool) string {
	total, free, err := importVolume()
	reserve := max(importSpaceFloor(total), storageWritableAbove(total))
	if err != nil || free <= reserve || uint64(index.Bytes) > (free-reserve)/2 { //nolint:gosec // nonnegative inspected size
		return "target_index_storage_insufficient"
	}
	sql, err := index.BuildSQL()
	if err != nil {
		return "source_schema_changed"
	}
	if !g.recordPreparation(ctx, m, p, &index) {
		return "migration_receipt_unavailable"
	}
	job, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if dropInvalid {
		if err := g.execMigrationIndexSQL(job, snapshot, "DROP INDEX CONCURRENTLY public."+dbmigration.Ident(index.Name)+";"); err != nil {
			return "target_index_cleanup_failed"
		}
	}
	if err := g.execMigrationIndexSQL(job, snapshot, sql); err != nil {
		return "target_index_build_failed"
	}
	return ""
}

func (g *databaseGuest) execMigrationIndexSQL(ctx context.Context, m *liveMigration, sql string) error {
	args := databasePsqlArgs(g.major)
	for i, arg := range args {
		if arg == "-d" {
			args[i+1] = m.Target
			break
		}
	}
	cmd := g.asSuperuser(exec.CommandContext(ctx, args[0], args[1:]...))
	cmd.Stdin = strings.NewReader("SET ROLE " + dbmigration.Ident(m.Owner) + ";\n" + sql + "\n")
	return cmd.Run()
}
