//go:build linux

package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

func (m *liveMigration) verificationMarker() dbmigration.Marker {
	return dbmigration.VerificationMarker(m.resource(), m.OperationID)
}

func (g *databaseGuest) queueMigrationVerification(ctx context.Context, m *liveMigration, req dbguest.Request) dbguest.Response {
	if m.Verification != nil && m.Verification.ID == req.VerificationID &&
		(m.Verification.State == dbmigration.Verifying && m.running || m.Verification.State == dbmigration.Verified) {
		return g.migrationResponse(m)
	}
	if !g.migrationFence.Load() || !m.running || m.StopRequested ||
		m.Preparation == nil || m.Preparation.State != dbmigration.Prepared ||
		g.migrationPrepareCancel != nil || m.TablesReady != m.Tables {
		r := g.migrationResponse(m)
		p := *r.MigrationProgress
		p.Verification = &dbmigration.Verification{ID: req.VerificationID, State: dbmigration.Blocked, Reason: "migration_preparation_required"}
		r.MigrationProgress = &p
		return r
	}
	system, err := g.migrationSourceIdentity(ctx, *req.Import)
	if err != nil || system != m.System {
		r := g.response()
		r.Error = dbguest.ErrCodeSourceDown
		return r
	}
	next := *m
	if m.Verification == nil || m.Verification.ID != req.VerificationID {
		token, err := uuid.NewRandom()
		if err != nil {
			r := g.response()
			r.Error = dbguest.ErrCodeNotReady
			return r
		}
		next.VerificationToken = token.String()
	}
	next.Verification = &dbmigration.Verification{ID: req.VerificationID, State: dbmigration.Verifying}
	if err := g.writeMigration(ctx, &next, false); err != nil {
		r := g.response()
		r.Error = dbguest.ErrCodeNotReady
		return r
	}
	*m = next
	return g.migrationResponse(m)
}

func (g *databaseGuest) updateMigrationVerification(ctx context.Context, m *liveMigration, v dbmigration.Verification) {
	if *m.Verification == v {
		return
	}
	next := *m
	next.Verification = &v
	if g.writeMigration(ctx, &next, false) == nil {
		*m = next
	}
}

func (g *databaseGuest) advanceMigrationVerification(ctx context.Context, m *liveMigration, src dbguest.ImportSource) {
	v := *m.Verification
	reason, blocked := g.verifyMigrationReplay(ctx, m, src, &v)
	v.Reason = reason
	if blocked {
		v.State = dbmigration.Blocked
	}
	g.updateMigrationVerification(ctx, m, v)
}

func (g *databaseGuest) verifyMigrationReplay(ctx context.Context, m *liveMigration, src dbguest.ImportSource, v *dbmigration.Verification) (string, bool) {
	if m.TablesReady != m.Tables || !g.migrationFence.Load() || m.Preparation == nil || m.Preparation.State != dbmigration.Prepared {
		return "migration_preparation_required", false
	}
	if !dbguest.ValidOperationID(m.VerificationToken) {
		return "verification_receipt_invalid", true
	}
	marker := m.verificationMarker()
	if reason, blocked := g.checkVerificationSchema(ctx, m, src); reason != "" {
		return reason, blocked
	}
	seqs, err := g.verificationSequences(ctx, src)
	if err != nil {
		return "source_sequence_inspection_unavailable", false
	}
	valuesSQL, err := dbmigration.SequenceValuesSQL(seqs)
	if err != nil {
		return "source_sequences_unsupported", true
	}
	// Prepared transactions can commit later, after the barrier. The
	// operator must drain ordinary writers and resolve any prepared ones.
	open, err := g.migrationSourceQuery(ctx, src, "SELECT EXISTS (SELECT FROM pg_catalog.pg_prepared_xacts WHERE database=current_database())")
	if err != nil {
		return "source_transaction_inspection_unavailable", false
	}
	if open != "f" {
		return "source_prepared_transactions", true
	}
	if reason, blocked := g.installVerificationMarker(ctx, m, src, marker); reason != "" {
		return reason, blocked
	}
	srcSeen, err := g.migrationSourceQuery(ctx, src, marker.SeenSQL(m.VerificationToken))
	if err != nil {
		return "source_marker_unavailable", false
	}
	if srcSeen != "t" {
		if _, err := g.migrationSourceQuery(ctx, src, marker.InsertSQL(m.VerificationToken)); err != nil {
			return "source_marker_write_unverified", false
		}
		return "waiting_for_marker_replay", false
	}
	seen, err := g.queryIn(ctx, m.Target, marker.SeenSQL(m.VerificationToken))
	if err != nil {
		return "target_marker_unavailable", false
	}
	if seen != "t" {
		return "waiting_for_marker_replay", false
	}
	// Fetch the flushed origin after the row is visible. Merely receiving a
	// WAL position or observing an idle sender does not prove replay.
	sub, err := g.migrationSubscription(ctx, m)
	lsn, valid := dbmigration.Position(sub.Durable)
	if err != nil || !valid || lsn == 0 || !sub.Exists || !sub.Enabled || !sub.MarkerReady || sub.Ready != m.Tables {
		return "target_replay_unverified", false
	}

	values, err := g.migrationSourceQuery(ctx, src, valuesSQL)
	var snapshot []dbmigration.SequenceValue
	if err != nil || json.Unmarshal([]byte(values), &snapshot) != nil {
		return "source_sequence_values_unavailable", false
	}
	if reason, blocked := g.syncVerificationSequences(ctx, m, seqs, snapshot); reason != "" {
		return reason, blocked
	}
	v.State, v.AppliedLSN, v.SequencesSynced, v.VerifiedAt = dbmigration.Verified, sub.Durable, len(seqs), time.Now().UTC()
	return "", false
}

func (g *databaseGuest) checkVerificationSchema(ctx context.Context, m *liveMigration, src dbguest.ImportSource) (string, bool) {
	inventory, reason := g.preparationSource(ctx, m, src)
	if reason != "" {
		return reason, reason == "source_schema_changed" || reason == "source_identity_changed"
	}
	p, indexes := inventory.Plan()
	if len(p.Blockers) != 0 || p.IndexesTotal != m.Preparation.IndexesReady {
		return "schema_preparation_stale", true
	}
	raw, err := g.queryIn(ctx, m.Target, dbmigration.InventoryWithMarkerSQL(m.verificationMarker()))
	var tables []dbmigration.Table
	if err != nil || json.Unmarshal([]byte(raw), &tables) != nil {
		return "target_schema_inspection_unavailable", false
	}
	if dbmigration.SchemaDigest(tables) != m.Schema {
		return "target_schema_changed", true
	}
	raw, err = g.queryIn(ctx, m.Target, dbmigration.PreparationIndexesSQL)
	var target []dbmigration.Index
	if err != nil || json.Unmarshal([]byte(raw), &target) != nil {
		return "target_index_inspection_unavailable", false
	}
	if !slices.EqualFunc(indexes, target, func(want, actual dbmigration.Index) bool {
		return actual.Valid && want.SameDefinition(actual)
	}) {
		return "target_indexes_changed", true
	}
	return "", false
}

func (g *databaseGuest) installVerificationMarker(ctx context.Context, m *liveMigration, src dbguest.ImportSource, marker dbmigration.Marker) (string, bool) {
	target, err := g.queryIn(ctx, m.Target, marker.TargetStateSQL())
	if err != nil {
		return "target_marker_unavailable", false
	}
	switch target {
	case "null":
		if m.VerificationMarkerReady {
			return "target_marker_lost", true
		}
		if _, err := g.queryIn(ctx, m.Target, marker.CreateSQL("")); err != nil {
			return "target_marker_setup_unverified", false
		}
	case "true":
	default:
		return "target_marker_conflict", true
	}
	source, err := g.migrationSourceQuery(ctx, src, marker.StateSQL())
	if err != nil {
		return "source_marker_unavailable", false
	}
	switch source {
	case "null":
		if m.VerificationMarkerReady {
			return "source_marker_lost", true
		}
		if _, err := g.migrationSourceQuery(ctx, src, marker.CreateSQL(m.resource())); err != nil {
			return "source_marker_setup_unverified", false
		}
	case "true":
	default:
		return "source_marker_conflict", true
	}
	publication, err := g.migrationPublication(ctx, m, src)
	if err != nil {
		return "source_publication_unavailable", false
	}
	tables, err := g.migrationInventory(ctx, src, m)
	if err != nil {
		return "source_inspection_unavailable", false
	}
	names := append(dbmigration.TableNames(tables), "public."+marker.Name)
	slices.Sort(names)
	if dbmigration.SchemaDigest(tables) != m.Schema || publication.Marker != m.OperationID ||
		!publication.Usable || !slices.Equal(publication.Tables, names) {
		return "source_marker_publication_unverified", true
	}
	sub, err := g.migrationSubscription(ctx, m)
	if err != nil || !sub.Exists || !sub.Enabled || sub.Ready != m.Tables {
		return "target_subscription_unavailable", false
	}
	if !sub.MarkerReady {
		if m.VerificationMarkerReady {
			return "target_marker_subscription_lost", true
		}
		if _, err := g.queryIn(ctx, m.Target, "ALTER SUBSCRIPTION "+dbmigration.Ident(m.resource())+
			" REFRESH PUBLICATION WITH (copy_data=false)"); err != nil {
			return "target_marker_subscription_unverified", false
		}
		sub, err = g.migrationSubscription(ctx, m)
		if err != nil || !sub.MarkerReady || sub.Ready != m.Tables {
			return "target_marker_subscription_unverified", false
		}
	}
	if !m.VerificationMarkerReady {
		next := *m
		next.VerificationMarkerReady = true
		if g.writeMigration(ctx, &next, false) != nil {
			return "migration_receipt_unavailable", false
		}
		*m = next
	}
	return "", false
}

func (g *databaseGuest) verificationSequences(ctx context.Context, src dbguest.ImportSource) ([]dbmigration.Sequence, error) {
	raw, err := g.migrationSourceQuery(ctx, src, dbmigration.SequenceInventorySQL)
	var sequences []dbmigration.Sequence
	if err == nil {
		err = json.Unmarshal([]byte(raw), &sequences)
	}
	return sequences, err
}

func (g *databaseGuest) syncVerificationSequences(ctx context.Context, m *liveMigration, source []dbmigration.Sequence, values []dbmigration.SequenceValue) (string, bool) {
	raw, err := g.queryIn(ctx, m.Target, dbmigration.SequenceInventorySQL)
	var target []dbmigration.Sequence
	if err != nil || json.Unmarshal([]byte(raw), &target) != nil {
		return "target_sequence_inspection_unavailable", false
	}
	if len(source) != len(values) {
		return "source_sequence_values_unverified", false
	}
	expected := make(map[string]dbmigration.Sequence, len(source))
	for _, seq := range source {
		expected[seq.Name] = seq
	}
	existing := make(map[string]bool, len(target))
	for _, seq := range target {
		if want, ok := expected[seq.Name]; !ok || want != seq {
			return "target_sequence_conflict", true
		}
		existing[seq.Name] = true
	}
	if len(source) == 0 {
		return "", false
	}
	actions := []string{"SET LOCAL synchronous_commit=on", "SET LOCAL ROLE " + dbmigration.Ident(m.Owner)}
	for i, seq := range source {
		if !existing[seq.Name] {
			ddl, err := seq.CreateSQL()
			if err != nil {
				return "source_sequences_unsupported", true
			}
			actions = append(actions, ddl)
		}
		sync, err := seq.SetValueSQL(values[i])
		if err != nil {
			return "source_sequence_values_unverified", true
		}
		actions = append(actions, sync)
	}
	if _, err := g.queryIn(ctx, m.Target, strings.Join(actions, "; ")); err != nil {
		return "target_sequence_sync_unverified", false
	}
	return "", false
}
