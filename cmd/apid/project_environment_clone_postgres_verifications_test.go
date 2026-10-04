//go:build !no_pg

// adr: 569
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

type verificationWorkerFailureStore struct {
	*importWorkerFailureStore
	loseVerificationReserve, loseVerificationClaim, loseMatch, loseClosure bool
}

func (s *verificationWorkerFailureStore) ReserveProjectEnvironmentClonePostgresVerification(ctx context.Context, l state.ProjectEnvironmentCloneLease, r state.ProjectEnvironmentClonePostgresVerificationRequest) (state.ProjectEnvironmentClonePostgresVerification, bool, error) {
	v, first, err := s.PgStore.ReserveProjectEnvironmentClonePostgresVerification(ctx, l, r)
	if err == nil && s.loseVerificationReserve {
		s.loseVerificationReserve = false
		return state.ProjectEnvironmentClonePostgresVerification{}, false, managedpostgres.ErrUnavailable
	}
	return v, first, err
}

func (s *verificationWorkerFailureStore) ClaimProjectEnvironmentClonePostgresVerification(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32) (state.ProjectEnvironmentClonePostgresVerification, bool, error) {
	v, first, err := s.PgStore.ClaimProjectEnvironmentClonePostgresVerification(ctx, l, id, oid)
	if err == nil && s.loseVerificationClaim {
		s.loseVerificationClaim = false
		return state.ProjectEnvironmentClonePostgresVerification{}, false, managedpostgres.ErrUnavailable
	}
	return v, first, err
}

func (s *verificationWorkerFailureStore) RecordProjectEnvironmentClonePostgresVerificationMatch(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, proof copycontents.SealedMatch) (state.ProjectEnvironmentClonePostgresVerification, error) {
	v, err := s.PgStore.RecordProjectEnvironmentClonePostgresVerificationMatch(ctx, l, id, oid, proof)
	if err == nil && s.loseMatch {
		s.loseMatch = false
		return state.ProjectEnvironmentClonePostgresVerification{}, managedpostgres.ErrUnavailable
	}
	return v, err
}

func (s *verificationWorkerFailureStore) RecordProjectEnvironmentClonePostgresVerificationClosure(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, proof state.ProjectEnvironmentClonePostgresVerificationCompletion) (state.ProjectEnvironmentClonePostgresVerification, error) {
	v, err := s.PgStore.RecordProjectEnvironmentClonePostgresVerificationClosure(ctx, l, id, oid, proof)
	if err == nil && s.loseClosure {
		s.loseClosure = false
		// The worker must return zero even if a failing store returns a committed row.
		return v, managedpostgres.ErrUnavailable
	}
	return v, err
}

type verificationWorkerFixture struct {
	f          *importWorkerFixture
	store      *verificationWorkerFailureStore
	cfg        copycontents.Config
	dataReads  int
	childTrace pgx.QueryTracer
}

type verificationWorkerWindow struct {
	allow        bool
	state, owner string
	imported     string
	openedAt     time.Time
	closedAt     *time.Time
}

func (v *verificationWorkerFixture) window(t *testing.T) verificationWorkerWindow {
	t.Helper()
	f, x := v.f, v.f.db.x
	cfg := x.targetRoot.Config().Copy()
	cfg.Database = x.target.DatabaseName
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.WithoutCancel(t.Context()))
	var w verificationWorkerWindow
	if err := conn.QueryRow(t.Context(), `SELECT d.datallowconn,w.state,w.owner_id::text,w.import_owner_id::text,w.opened_at,w.closed_at
 FROM gregale_copy_database_verification.windows w JOIN pg_database d ON d.oid=w.target_oid WHERE w.source_oid=$1::oid`, f.sourceOID).
		Scan(&w.allow, &w.state, &w.owner, &w.imported, &w.openedAt, &w.closedAt); err != nil {
		t.Fatal(err)
	}
	return w
}

// Actual original selected contents and a pg_dump archive are captured before
// restore. SQL target/source clusters are independent; provider receipts are
// synthetic owned fixtures. This does not qualify paid provider permissions.
func cloneVerificationWorkerFixture(t *testing.T) *verificationWorkerFixture {
	t.Helper()
	f := cloneImportWorkerFixture(t)
	x := f.db.x
	v := &verificationWorkerFixture{f: f, store: &verificationWorkerFailureStore{importWorkerFailureStore: f.store},
		cfg: cloneContentsReadConfig(t)}
	x.f.srv.store = v.store
	x.f.srv.clonePostgresContentsReadPool = v.cfg.ReadPool
	if _, err := x.f.srv.projectEnvironmentClonePostgresContentsFromReader(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID,
		api.PostgresCopyCiphertextMaxBytes, contentsWorkerLimits(), v.cfg); err != nil {
		t.Fatal(err)
	}
	return v
}

func (v *verificationWorkerFixture) importData(t *testing.T, uncertain bool) {
	t.Helper()
	f := v.f
	if uncertain {
		f.childPostError = managedpostgres.ErrUnavailable
	}
	_, err := f.run(t)
	if uncertain && err == nil || !uncertain && err != nil || !f.restored || f.childCalls != 1 {
		t.Fatal("real original import", err)
	}
	f.childPostError = nil
	f.assertClosed(t, true)
	v.installVerificationReadConnector(t)
}

func (v *verificationWorkerFixture) installVerificationReadConnector(t *testing.T) {
	t.Helper()
	f, x := v.f, v.f.db.x
	bootstrapConnect := x.p.targetSQLConnect
	x.p.targetSQLConnect = func(ctx context.Context, selected string) (*pgx.Conn, error) {
		if selected == x.target.DatabaseName {
			return bootstrapConnect(ctx, selected)
		}
		if selected != f.target.DatabaseName {
			return nil, managedpostgres.ErrConflict
		}
		owner, err := v.store.ProjectEnvironmentClonePostgresVerificationForLease(ctx, x.f.lease, x.source.source.ID, f.sourceOID)
		if err != nil || owner.State != "verifying" {
			return nil, errors.New("verification child borrow preceded durable claim")
		}
		history, err := v.store.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx, x.f.lease, x.source.source.ID, f.sourceOID)
		if err != nil || len(history) > 0 && history[len(history)-1].State != "verifying" {
			return nil, errors.New("verification child borrow preceded current attempt claim")
		}
		budget, err := v.store.ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(ctx, x.f.lease, x.source.source.ID, f.sourceOID)
		if err != nil {
			return nil, err
		}
		expectedOwner, expectedAttempt := owner.VerificationID, int32(1)
		if len(history) > 0 {
			expectedOwner, expectedAttempt = history[len(history)-1].VerificationID, history[len(history)-1].Attempt
		}
		allocated := false
		for _, a := range budget.Allocations {
			if a.VerificationID == expectedOwner && a.Attempt == expectedAttempt {
				allocated = true
			}
		}
		if !allocated {
			return nil, errors.New("verification child borrow preceded durable read debit")
		}
		cfg := x.targetRoot.Config().Copy()
		cfg.Database, cfg.Password = selected, "verification-fixture-password"
		cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "off", "search_path": "pg_catalog"}
		cfg.Tracer = v.childTrace
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err == nil {
			v.dataReads++
			x.connections = append(x.connections, conn)
		}
		return conn, err
	}
}

func (v *verificationWorkerFixture) run(t *testing.T) (state.ProjectEnvironmentClonePostgresVerification, error) {
	t.Helper()
	f, x := v.f, v.f.db.x
	return x.f.srv.projectEnvironmentClonePostgresVerification(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, v.cfg)
}

func (v *verificationWorkerFixture) owner(t *testing.T) state.ProjectEnvironmentClonePostgresVerification {
	t.Helper()
	f, x := v.f, v.f.db.x
	o, err := v.store.ProjectEnvironmentClonePostgresVerificationForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (v *verificationWorkerFixture) assertClosed(t *testing.T, hasWindow bool) {
	t.Helper()
	f, x := v.f, v.f.db.x
	f.assertClosed(t, true)
	if hasWindow {
		cfg := x.targetRoot.Config().Copy()
		cfg.Database = x.target.DatabaseName
		conn, err := pgx.ConnectConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(context.WithoutCancel(t.Context()))
		var closed bool
		o := v.owner(t)
		if err := conn.QueryRow(t.Context(), `SELECT state='closed' AND owner_id=$2::uuid AND import_owner_id=$3::uuid AND closed_at IS NOT NULL
 FROM gregale_copy_database_verification.windows WHERE source_oid=$1::oid`, f.sourceOID, o.VerificationID, o.ImportID).Scan(&closed); err != nil || !closed {
			t.Fatal("original native verification window not closed", err)
		}
	}
	entries, err := os.ReadDir(v.cfg.SpoolDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".gregale-contents-read-pool" {
		t.Fatal("verification digest spool leaked", err)
	}
}

func (v *verificationWorkerFixture) handoff(t *testing.T) state.ProjectEnvironmentCloneLease {
	t.Helper()
	x := v.f.db.x
	previous := x.f.lease
	if err := v.store.ReleaseProjectEnvironmentCloneLease(t.Context(), previous, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	x.f.lease, err = v.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return previous
}

func TestPGClonePostgresVerificationRealComparisonAndUncertainImport(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "executed", true: "uncertain"}[uncertain], func(t *testing.T) {
			v := cloneVerificationWorkerFixture(t)
			v.importData(t, uncertain)
			imported := v.f.owner(t)
			gets, reads := v.f.backend.gets, v.f.db.x.p.readerSelectedSQLCalls
			verified, err := v.run(t)
			if err != nil || verified.State != "verified" || verified.NativeClosedAt.IsZero() || verified.VerifiedAt.IsZero() ||
				verified.ManifestFingerprint != verified.Sealed.ManifestFingerprint || v.dataReads != 1 {
				t.Fatal("independent actual comparison and native closure", err)
			}
			if got := v.f.owner(t); got != imported || v.f.backend.gets != gets || v.f.db.x.p.readerSelectedSQLCalls != reads || v.f.childCalls != 1 {
				t.Fatal("verification recaptured/restored or invented an import result")
			}
			if uncertain && imported.State != "importing" || !uncertain && imported.State != "executed" {
				t.Fatal("fixture did not exercise actual uncertain import")
			}
			v.assertClosed(t, true)
			for _, r := range v.f.db.x.f.lease.Operation.Resources {
				if r.Status != "captured" || r.TargetID != "" {
					t.Fatal("comparison supplied complete dataset/stage readiness")
				}
			}
		})
	}
}

func TestPGClonePostgresVerificationLostCommittedProofAndClosureRecoverWithoutDataRead(t *testing.T) {
	for _, phase := range []string{"match", "closure"} {
		t.Run(phase, func(t *testing.T) {
			v := cloneVerificationWorkerFixture(t)
			v.importData(t, false)
			if phase == "match" {
				v.store.loseMatch = true
			} else {
				v.store.loseClosure = true
			}
			if got, err := v.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) {
				t.Fatal("lost committed response escaped as verification", err)
			}
			first := v.owner(t)
			if phase == "match" && first.State != "compared" || phase == "closure" && first.State != "verified" {
				t.Fatal("committed original proof lost")
			}
			v.assertClosed(t, true)
			previous := v.handoff(t)
			oldKey := mfaIdentities()[0]
			rotated, _ := age.GenerateX25519Identity()
			mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{rotated, nil, oldKey} }
			setSecretRecipient = nil
			spoolDir := v.cfg.SpoolDir
			v.cfg = copycontents.Config{}
			x := v.f.db.x
			reads, gets := x.p.readerSelectedSQLCalls, v.f.backend.gets
			x.p.readerSelectedSQLConnect = nil
			got, err := v.run(t)
			if err != nil || got.State != "verified" || got.VerificationID != first.VerificationID || got.KeyID != first.KeyID ||
				got.Sealed.CiphertextSHA256 != first.Sealed.CiphertextSHA256 || !bytes.Equal(got.Sealed.Ciphertext, first.Sealed.Ciphertext) ||
				!got.Sealed.OpenedAt.Equal(first.Sealed.OpenedAt) || !got.ComparedAt.Equal(first.ComparedAt) ||
				v.dataReads != 1 || x.p.readerSelectedSQLCalls != reads || v.f.backend.gets != gets || v.f.childCalls != 1 {
				t.Fatal("handoff lost original proof or repeated data work", err)
			}
			if phase == "closure" && (!got.NativeClosedAt.Equal(first.NativeClosedAt) || !got.VerifiedAt.Equal(first.VerifiedAt)) {
				t.Fatal("closure recovery changed original timestamps")
			}
			if _, err := v.store.ProjectEnvironmentClonePostgresVerificationForLease(t.Context(), previous, x.source.source.ID, v.f.sourceOID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale worker obtained retained proof", err)
			}
			v.cfg.SpoolDir = spoolDir
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationLostReserveAndClaimBeforeNativeAccess(t *testing.T) {
	for _, phase := range []string{"reserve", "claim"} {
		t.Run(phase, func(t *testing.T) {
			v := cloneVerificationWorkerFixture(t)
			v.importData(t, false)
			if phase == "reserve" {
				v.store.loseVerificationReserve = true
			} else {
				v.store.loseVerificationClaim = true
			}
			x := v.f.db.x
			calls := x.p.targetSQLCalls
			if got, err := v.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) || v.dataReads != 0 || x.p.targetSQLCalls != calls {
				t.Fatal("unobserved reservation/claim reached native access", err)
			}
			first := v.owner(t)
			if phase == "reserve" && first.State != "reserved" || phase == "claim" && first.State != "verifying" {
				t.Fatal("first owner forgotten")
			}
			v.handoff(t)
			got, err := v.run(t)
			if err != nil || got.State != "verified" || got.VerificationID != first.VerificationID || v.dataReads != 1 {
				t.Fatal("never-opened original owner could not make first comparison", err)
			}
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationProviderPostchecksPrecedePublication(t *testing.T) {
	for _, phase := range []string{"child", "bootstrap"} {
		t.Run(phase, func(t *testing.T) {
			v := cloneVerificationWorkerFixture(t)
			v.importData(t, false)
			if phase == "child" {
				v.f.childPostError = managedpostgres.ErrUnavailable
			} else {
				v.f.bootstrapPostError = managedpostgres.ErrUnavailable
			}
			if got, err := v.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) {
				t.Fatal("failed provider postcheck became verification", err)
			}
			first := v.owner(t)
			if phase == "child" && (first.State != "verifying" || first.Sealed.Ciphertext != nil) || phase == "bootstrap" && first.State != "compared" {
				t.Fatal("comparison published before the required provider postchecks")
			}
			v.assertClosed(t, true)
			v.f.childPostError, v.f.bootstrapPostError = nil, nil
			got, err := v.run(t)
			if phase == "child" && (err == nil || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{})) || phase == "bootstrap" && (err != nil || got.State != "verified") || v.dataReads != 1 {
				t.Fatal("recovery repeated child data work or lost durable match", err)
			}
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationDataMismatchAndBudgetsNeverPublish(t *testing.T) {
	for _, mode := range []string{"changed row", "read budget", "sort budget"} {
		t.Run(mode, func(t *testing.T) {
			v := cloneVerificationWorkerFixture(t)
			if mode == "changed row" {
				v.f.afterChild = func(ctx context.Context, conn *pgx.Conn) error {
					_, err := conn.Exec(ctx, "UPDATE public.selected_events SET note='different restored data' WHERE id=7")
					return err
				}
			} else if mode == "sort budget" {
				v.f.afterChild = func(ctx context.Context, conn *pgx.Conn) error {
					_, err := conn.Exec(ctx, "INSERT INTO public.selected_events VALUES(8,'sort-budget-row')")
					return err
				}
			}
			v.importData(t, false)
			v.f.afterChild = nil
			if mode == "read budget" {
				v.cfg.MaxBytes = 1
			} else if mode == "sort budget" {
				v.cfg.SortMemoryBytes, v.cfg.SortDiskBytes = 32, 32
			}
			want := managedpostgres.ErrConflict
			if mode != "changed row" {
				want = managedpostgres.ErrQuotaExceeded
			}
			if got, err := v.run(t); !errors.Is(err, want) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) || v.dataReads != 1 {
				t.Fatal("mismatching/exhausted data became verification", err)
			}
			first := v.owner(t)
			if first.State != "verifying" || first.Sealed.Ciphertext != nil || !first.VerifiedAt.IsZero() {
				t.Fatal("failed data proof changed charged owner")
			}
			v.assertClosed(t, true)
			if _, err := v.run(t); err == nil || v.dataReads != 1 {
				t.Fatal("closed failed window redispatched comparison", err)
			}
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationDamagedOriginalProofCannotRecover(t *testing.T) {
	for _, mode := range []string{"missing old key", "ciphertext", "target pin", "manifest"} {
		t.Run(mode, func(t *testing.T) {
			v := cloneVerificationWorkerFixture(t)
			v.importData(t, false)
			v.store.loseMatch = true
			if _, err := v.run(t); err == nil {
				t.Fatal("fixture did not lose first committed match response")
			}
			first := v.owner(t)
			v.assertClosed(t, true)
			x := v.f.db.x
			calls := x.p.targetSQLCalls
			var err error
			switch mode {
			case "missing old key":
				rotated, _ := age.GenerateX25519Identity()
				mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{rotated} }
			case "ciphertext":
				data := bytes.Clone(first.Sealed.Ciphertext)
				data[len(data)-1] ^= 1
				h := sha256.Sum256(data)
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_verifications SET ciphertext=$2,ciphertext_sha256=$3 WHERE operation_id=$1", x.f.lease.Operation.ID, data, hex.EncodeToString(h[:]))
			case "target pin":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_verifications SET target_fingerprint=$2 WHERE operation_id=$1", x.f.lease.Operation.ID, strings.Repeat("a", 64))
			case "manifest":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_verifications SET manifest_fingerprint=$2 WHERE operation_id=$1", x.f.lease.Operation.ID, strings.Repeat("b", 64))
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := v.run(t); err == nil || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) || v.dataReads != 1 || x.p.targetSQLCalls != calls {
				t.Fatal("unreadable/substituted original proof reached native SQL", err)
			}
			x.assertPrivate(t)
		})
	}
}

type verificationWorkerReadTrace struct {
	onRead func()
	fired  bool
}

func (tr *verificationWorkerReadTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	// This native catalogue query occurs inside actual target data comparison.
	if !tr.fired && strings.Contains(d.SQL, "-- name: CopyContentsRelations") {
		tr.fired = true
		tr.onRead()
	}
	return ctx
}
func (*verificationWorkerReadTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestPGClonePostgresVerificationHandoffDuringActualReadCannotPublish(t *testing.T) {
	v := cloneVerificationWorkerFixture(t)
	v.importData(t, false)
	tr := &verificationWorkerReadTrace{onRead: func() { v.handoff(t) }}
	v.childTrace = tr
	if got, err := v.run(t); err == nil || !tr.fired || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) {
		t.Fatal("handoff during actual comparison became verification", err)
	}
	if first := v.owner(t); first.State != "verifying" || first.Sealed.Ciphertext != nil {
		t.Fatal("stale worker published actual data proof")
	}
	v.assertClosed(t, true)
	if _, err := v.run(t); err == nil || v.dataReads != 1 {
		t.Fatal("handoff repeated an already closed comparison", err)
	}
}

type verificationWorkerLostWindowReply struct {
	phase, armed string
	fired        bool
}

func (tr *verificationWorkerLostWindowReply) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "pg_temp.gregale_copy_database_verification_change(") && len(d.Args) > 7 {
		if action, ok := d.Args[7].(string); ok {
			tr.armed = action
		}
	}
	return context.WithValue(ctx, importWorkerCommitKey{}, strings.EqualFold(strings.TrimSpace(d.SQL), "commit"))
}

func (tr *verificationWorkerLostWindowReply) TraceQueryEnd(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryEndData) {
	if commit, _ := ctx.Value(importWorkerCommitKey{}).(bool); commit && d.Err == nil && tr.armed == tr.phase && !tr.fired {
		tr.fired = true
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = c.PgConn().Close(cleanup)
	}
}

func TestPGClonePostgresVerificationLostNativeWindowResponseRecoversCloseOnly(t *testing.T) {
	for _, phase := range []string{"open", "finish"} {
		t.Run(phase, func(t *testing.T) {
			v := cloneVerificationWorkerFixture(t)
			v.importData(t, false)
			tr := &verificationWorkerLostWindowReply{phase: phase}
			v.f.bootstrapTracer = tr
			if got, err := v.run(t); err == nil || !tr.fired || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) {
				t.Fatal("lost native reply became verification", err)
			}
			first, native := v.owner(t), v.window(t)
			if native.owner != first.VerificationID || native.imported != first.ImportID {
				t.Fatal("native reply loss forgot original owners")
			}
			if phase == "open" && (first.State != "verifying" || native.state != "open" || !native.allow || v.dataReads != 0) ||
				phase == "finish" && (first.State != "compared" || native.state != "closed" || native.allow || native.closedAt == nil || v.dataReads != 1) {
				t.Fatal("lost native reply does not represent its actual committed phase")
			}
			v.handoff(t)
			v.f.bootstrapTracer = nil
			got, err := v.run(t)
			if phase == "open" && (err == nil || v.dataReads != 0 || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{})) ||
				phase == "finish" && (err != nil || got.State != "verified" || v.dataReads != 1 || !got.NativeClosedAt.Equal(*native.closedAt) || !got.Sealed.OpenedAt.Equal(native.openedAt)) {
				t.Fatal("native recovery reopened data or lost original closure", err)
			}
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationCancellationDuringActualReadClosesAdmission(t *testing.T) {
	v := cloneVerificationWorkerFixture(t)
	v.importData(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tr := &verificationWorkerReadTrace{onRead: cancel}
	v.childTrace = tr
	f, x := v.f, v.f.db.x
	got, err := x.f.srv.projectEnvironmentClonePostgresVerification(ctx, x.f.lease, x.source, f.db.exports, f.sourceOID, v.cfg)
	if err == nil || !tr.fired || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerification{}) || v.dataReads != 1 {
		t.Fatal("cancelled actual read became verification", err)
	}
	if first := v.owner(t); first.State != "verifying" || first.Sealed.Ciphertext != nil {
		t.Fatal("cancelled worker published a match")
	}
	v.assertClosed(t, true)
}
