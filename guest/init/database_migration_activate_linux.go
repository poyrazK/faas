//go:build linux

package main

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

const (
	migrationSealing       = "sealing"
	migrationSealed        = "sealed"
	migrationOpened        = "opened"
	migrationMarkerArchive = "gregale_migration_internal"
)

type migrationActivation struct {
	SwitchID string
	Phase    string
	Roles    []string
}

func (g *databaseGuest) restoreMigrationAdmission(m *liveMigration) {
	if m == nil || m.Activation == nil {
		return
	}
	switch m.Activation.Phase {
	case migrationSealed:
		g.migrationAdmission.CompareAndSwap(nil, &migrationAdmission{database: m.Target, roles: slices.Clone(m.Activation.Roles)})
		g.primarySessions.resume()
	case migrationOpened:
		g.migrationAdmission.Store(nil)
		g.primarySessions.resume()
		g.migrationFence.Store(false)
	}
}

func (g *databaseGuest) controlMigrationActivation(req dbguest.Request) dbguest.Response {
	r := g.response()
	if r.State != dbguest.StateReady || !g.migrationMu.TryLock() {
		r.Error = dbguest.ErrCodeNotReady
		return r
	}
	defer g.migrationMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if g.migration == nil {
		var err error
		g.migration, err = g.readMigration(ctx)
		if err != nil {
			r.Error = dbguest.ErrCodeNotReady
			return r
		}
	}
	m, a := g.migration, req.Activation
	if m == nil || m.OperationID != a.OperationID {
		r.Error = dbguest.ErrCodeImportOperationMismatch
		return r
	}
	if m.Activation != nil && m.Activation.SwitchID != a.SwitchID {
		if req.Op == dbguest.OpMigrationAbortActivation {
			return g.migrationResponse(m)
		}
		r.Error = dbguest.ErrCodeImportOperationMismatch
		return r
	}
	switch req.Op {
	case dbguest.OpMigrationSeal:
		r.Error = g.sealVerifiedMigration(ctx, m, a)
	case dbguest.OpMigrationOpen:
		r.Error = g.openVerifiedMigration(ctx, m)
	case dbguest.OpMigrationAbortActivation:
		r.Error = g.abortVerifiedMigration(ctx, m, a.SwitchID)
	}
	if r.Error != "" {
		return r
	}
	return g.migrationResponse(m)
}

func (g *databaseGuest) sealVerifiedMigration(ctx context.Context, m *liveMigration, a *dbmigration.Activation) string {
	if a.SourcePin != m.Pin || a.SourceDatabase != m.Source ||
		!dbmigration.FreshVerification(m.Verification, a.SwitchID, time.Now()) ||
		m.State == dbmigration.ResyncRequired || m.StopRequested || !m.VerificationMarkerReady ||
		g.migrationPrepareCancel != nil || !g.migrationFence.Load() {
		return dbguest.ErrCodeImportOperationMismatch
	}
	roles := slices.Clone(a.Roles)
	slices.Sort(roles)
	if m.Activation != nil {
		if !slices.Equal(m.Activation.Roles, roles) {
			return dbguest.ErrCodeImportOperationMismatch
		}
		if m.Activation.Phase == migrationSealed {
			g.restoreMigrationAdmission(m)
			return ""
		}
	} else {
		sub, err := g.migrationSubscription(ctx, m)
		if err != nil {
			return dbguest.ErrCodeNotReady
		}
		durable, ok := dbmigration.Position(sub.Durable)
		verified, valid := dbmigration.Position(m.Verification.AppliedLSN)
		seen, err := g.queryIn(ctx, m.Target, m.verificationMarker().SeenSQL(m.VerificationToken))
		if err != nil {
			return dbguest.ErrCodeNotReady
		}
		if !sub.Exists || !sub.MarkerReady || sub.Ready != m.Tables || !ok || !valid || durable != verified || seen != "t" {
			return dbguest.ErrCodeImportOperationMismatch
		}
		next := *m
		next.Activation = &migrationActivation{SwitchID: a.SwitchID, Phase: migrationSealing, Roles: roles}
		next.State, next.Reason, next.running = dbmigration.Paused, "activation_sealing", false
		if err := g.writeMigration(ctx, &next, false); err != nil {
			return dbguest.ErrCodeNotReady
		}
		*m = next
		g.migrationRunning.Store(false)
		g.migrationFeed.Store(nil)
		_ = os.Remove(migrationPassfile)
	}
	if !g.migrationSubscriberStopped(ctx, m) {
		return dbguest.ErrCodeNotReady
	}
	sub, err := g.migrationSubscription(ctx, m)
	if err != nil {
		return dbguest.ErrCodeNotReady
	}
	if sub.Durable != m.Verification.AppliedLSN {
		return dbguest.ErrCodeImportOperationMismatch
	}
	if !g.moveVerificationMarker(ctx, m, true) {
		return dbguest.ErrCodeNotReady
	}
	next := *m
	next.Activation = &migrationActivation{SwitchID: a.SwitchID, Phase: migrationSealed, Roles: roles}
	next.Reason = "activation_sealed"
	if err := g.writeMigration(ctx, &next, false); err != nil {
		return dbguest.ErrCodeNotReady
	}
	*m = next
	g.restoreMigrationAdmission(m)
	return ""
}

func (g *databaseGuest) openVerifiedMigration(ctx context.Context, m *liveMigration) string {
	if m.Activation == nil {
		return dbguest.ErrCodeImportOperationMismatch
	}
	if m.Activation.Phase == migrationOpened {
		g.restoreMigrationAdmission(m)
		return ""
	}
	if m.Activation.Phase != migrationSealed || !g.migrationSubscriberStopped(ctx, m) {
		return dbguest.ErrCodeNotReady
	}
	next := *m
	next.Activation = &migrationActivation{SwitchID: m.Activation.SwitchID, Phase: migrationOpened}
	next.State, next.Reason = dbmigration.Activated, ""
	if err := g.writeMigration(ctx, &next, false); err != nil {
		return dbguest.ErrCodeNotReady
	}
	*m = next
	g.restoreMigrationAdmission(m)
	return ""
}

func (g *databaseGuest) abortVerifiedMigration(ctx context.Context, m *liveMigration, switchID string) string {
	if m.Verification == nil || m.Verification.ID != switchID {
		return ""
	}
	next := *m
	if m.Activation != nil {
		if m.Activation.Phase == migrationOpened {
			return dbguest.ErrCodeImportOperationMismatch
		}
		g.setMigrationFence()
		if !g.moveVerificationMarker(ctx, m, false) {
			return dbguest.ErrCodeNotReady
		}
		next.Activation = nil
		next.State, next.Reason = dbmigration.Paused, "resume_required"
	}
	// Clear the proof even if cancellation reaches the guest before Seal.
	// A delayed request for this same switch can then no longer seal it.
	v := *m.Verification
	v.State, v.Reason, v.VerifiedAt = dbmigration.Blocked, "activation_cancelled", time.Time{}
	v.AppliedLSN, v.SequencesSynced = "", 0
	next.Verification = &v
	if err := g.writeMigration(ctx, &next, false); err != nil {
		return dbguest.ErrCodeNotReady
	}
	*m = next
	return ""
}

func (g *databaseGuest) migrationSubscriberStopped(ctx context.Context, m *liveMigration) bool {
	name := dbmigration.Literal(m.resource())
	if err := g.setMigrationSubscription(ctx, m, false); err != nil {
		return false
	}
	for {
		out, err := g.queryIn(ctx, m.Target, "SELECT NOT EXISTS (SELECT FROM pg_catalog.pg_stat_subscription WHERE subname="+name+" AND pid IS NOT NULL)")
		if err != nil {
			return false
		}
		if out == "t" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// The marker participates in replay but is infrastructure, so move it
// outside public before verifying staged app roles against customer tables.
func (g *databaseGuest) moveVerificationMarker(ctx context.Context, m *liveMigration, archive bool) bool {
	name := dbmigration.Ident(m.verificationMarker().Name)
	original, archived := "public."+name, migrationMarkerArchive+"."+name
	from, to, schema := original, archived, migrationMarkerArchive
	if !archive {
		from, to, schema = archived, original, "public"
	}
	tag := dbmigration.Literal(m.verificationMarker().Tag)
	owned := func(relation string) string {
		return "obj_description(to_regclass(" + dbmigration.Literal(relation) + "),'pg_class') = " + tag
	}
	q := "SELECT CASE WHEN " + owned(to) + " AND to_regclass(" +
		dbmigration.Literal(from) + ") IS NULL THEN 'done' WHEN " + owned(from) +
		" AND to_regclass(" + dbmigration.Literal(to) + ") IS NULL THEN 'move' ELSE 'conflict' END"
	state, err := g.queryIn(ctx, m.Target, q)
	if err != nil || state == "conflict" {
		return false
	}
	if state == "done" {
		return true
	}
	sql := "ALTER TABLE " + from + " SET SCHEMA " + schema
	if archive {
		sql = "CREATE SCHEMA IF NOT EXISTS " + migrationMarkerArchive + "; REVOKE ALL ON SCHEMA " +
			migrationMarkerArchive + " FROM PUBLIC; " + sql
	}
	_, err = g.queryIn(ctx, m.Target, sql)
	return err == nil
}
