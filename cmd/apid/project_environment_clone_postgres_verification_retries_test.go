//go:build !no_pg

// adr: 590
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

type verificationRetryFailureStore struct {
	*verificationWorkerFailureStore
	loseRetryReserve, loseRetryClaim, loseRetryMatch, loseRetryClosure, loseFailure bool
}

func (s *verificationRetryFailureStore) ReserveProjectEnvironmentClonePostgresVerificationRetry(ctx context.Context, l state.ProjectEnvironmentCloneLease, r state.ProjectEnvironmentClonePostgresVerificationRetryRequest) (state.ProjectEnvironmentClonePostgresVerificationAttempt, bool, error) {
	a, first, err := s.PgStore.ReserveProjectEnvironmentClonePostgresVerificationRetry(ctx, l, r)
	if err == nil && s.loseRetryReserve {
		s.loseRetryReserve = false
		return state.ProjectEnvironmentClonePostgresVerificationAttempt{}, false, managedpostgres.ErrUnavailable
	}
	return a, first, err
}
func (s *verificationRetryFailureStore) ClaimProjectEnvironmentClonePostgresVerificationAttempt(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, owner string) (state.ProjectEnvironmentClonePostgresVerificationAttempt, bool, error) {
	a, first, err := s.PgStore.ClaimProjectEnvironmentClonePostgresVerificationAttempt(ctx, l, id, oid, owner)
	if err == nil && s.loseRetryClaim {
		s.loseRetryClaim = false
		return state.ProjectEnvironmentClonePostgresVerificationAttempt{}, false, managedpostgres.ErrUnavailable
	}
	return a, first, err
}
func (s *verificationRetryFailureStore) RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, owner string, sealed copycontents.SealedMatch) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	a, err := s.PgStore.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(ctx, l, id, oid, owner, sealed)
	if err == nil && s.loseRetryMatch {
		s.loseRetryMatch = false
		return state.ProjectEnvironmentClonePostgresVerificationAttempt{}, managedpostgres.ErrUnavailable
	}
	return a, err
}
func (s *verificationRetryFailureStore) RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, owner string, c state.ProjectEnvironmentClonePostgresVerificationCompletion) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	a, err := s.PgStore.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(ctx, l, id, oid, owner, c)
	if err == nil && s.loseRetryClosure {
		s.loseRetryClosure = false
		return a, managedpostgres.ErrUnavailable
	}
	return a, err
}
func (s *verificationRetryFailureStore) RecordProjectEnvironmentClonePostgresVerificationFailure(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, owner string, c state.ProjectEnvironmentClonePostgresVerificationFailure) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	a, err := s.PgStore.RecordProjectEnvironmentClonePostgresVerificationFailure(ctx, l, id, oid, owner, c)
	if err == nil && s.loseFailure {
		s.loseFailure = false
		return state.ProjectEnvironmentClonePostgresVerificationAttempt{}, managedpostgres.ErrUnavailable
	}
	return a, err
}
func retryVerificationWorkerFixture(t *testing.T) (*verificationWorkerFixture, *verificationRetryFailureStore) {
	t.Helper()
	v := cloneVerificationWorkerFixture(t)
	store := &verificationRetryFailureStore{verificationWorkerFailureStore: v.store}
	v.f.db.x.f.srv.store = store
	return v, store
}
func runVerificationRetryWorker(t *testing.T, v *verificationWorkerFixture) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	t.Helper()
	f, x := v.f, v.f.db.x
	return x.f.srv.projectEnvironmentClonePostgresVerificationWithRetries(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, v.cfg)
}
func verificationRetryHistory(t *testing.T, v *verificationWorkerFixture) []state.ProjectEnvironmentClonePostgresVerificationAttempt {
	t.Helper()
	f, x := v.f, v.f.db.x
	history, err := v.store.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
	if err != nil {
		t.Fatal(err)
	}
	return history
}
func failFirstVerificationWorker(t *testing.T, v *verificationWorkerFixture) {
	t.Helper()
	v.f.childPostError = managedpostgres.ErrUnavailable
	if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) {
		t.Fatal("first failed provider postcheck became proof", err)
	}
	v.f.childPostError = nil
	history := verificationRetryHistory(t, v)
	if len(history) != 1 || history[0].State != "failed" || v.dataReads != 1 {
		t.Fatal("first failure not retained")
	}
	v.assertClosed(t, true)
}
func verificationRetryNativeWindow(t *testing.T, v *verificationWorkerFixture, attempt int32) (string, string, time.Time, *time.Time) {
	t.Helper()
	f, x := v.f, v.f.db.x
	cfg := x.targetRoot.Config().Copy()
	cfg.Database = x.target.DatabaseName
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.WithoutCancel(t.Context()))
	var phase, owner string
	var opened time.Time
	var closed *time.Time
	if err = conn.QueryRow(t.Context(), "SELECT state,owner_id::text,opened_at,closed_at FROM gregale_copy_database_verification_retries.windows WHERE source_oid=$1::oid AND attempt=$2", f.sourceOID, attempt).Scan(&phase, &owner, &opened, &closed); err != nil {
		t.Fatal(err)
	}
	return phase, owner, opened, closed
}

func TestPGClonePostgresVerificationRetryWorkerRealDataAndUncertainImport(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "executed", true: "uncertain"}[uncertain], func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			v.importData(t, uncertain)
			imported := v.f.owner(t)
			gets, reads := v.f.backend.gets, v.f.db.x.p.readerSelectedSQLCalls
			failFirstVerificationWorker(t, v)
			first := verificationRetryHistory(t, v)[0]
			v.handoff(t)
			verified, err := runVerificationRetryWorker(t, v)
			if err != nil || verified.State != "verified" || verified.Attempt != 2 || v.dataReads != 2 {
				t.Fatal("real retry comparison", err)
			}
			replay, err := runVerificationRetryWorker(t, v)
			if err != nil || !reflect.DeepEqual(replay, verified) || v.dataReads != 2 {
				t.Fatal("verified retry re-read data", err)
			}
			history := verificationRetryHistory(t, v)
			if len(history) != 2 || !reflect.DeepEqual(history[0], first) || v.owner(t).State != "verifying" {
				t.Fatal("original history replaced")
			}
			if v.f.owner(t) != imported || v.f.backend.gets != gets || v.f.db.x.p.readerSelectedSQLCalls != reads || v.f.childCalls != 1 {
				t.Fatal("retry recaptured or redispatched import")
			}
			v.assertClosed(t, true)
		})
	}
}
func TestPGClonePostgresVerificationRetryWorkerLostCommittedReplies(t *testing.T) {
	for _, phase := range []string{"reserve", "claim", "match", "closure", "failure"} {
		t.Run(phase, func(t *testing.T) {
			v, store := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			if phase == "failure" {
				store.loseFailure = true
				failFirstVerificationWorker(t, v)
			} else {
				failFirstVerificationWorker(t, v)
			}
			first := verificationRetryHistory(t, v)[0]
			if phase == "failure" {
				if got, err := runVerificationRetryWorker(t, v); err != nil || got.State != "verified" || got.Attempt != 2 || v.dataReads != 2 {
					t.Fatal("lost failure response replaced intent", err)
				}
				return
			}
			store.loseRetryReserve = phase == "reserve"
			store.loseRetryClaim = phase == "claim"
			store.loseRetryMatch = phase == "match"
			store.loseRetryClosure = phase == "closure"
			if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) {
				t.Fatal("lost committed reply became proof", err)
			}
			history := verificationRetryHistory(t, v)
			held := history[1]
			reads := v.dataReads
			want := map[string]string{"reserve": "reserved", "claim": "verifying", "match": "compared", "closure": "verified"}[phase]
			if len(history) != 2 || held.State != want || !reflect.DeepEqual(history[0], first) {
				t.Fatal("lost response did not retain its actual phase")
			}
			v.handoff(t)
			got, err := runVerificationRetryWorker(t, v)
			if err != nil || got.State != "verified" || got.VerificationID != held.VerificationID || got.Attempt != 2 || v.dataReads != 2 {
				t.Fatal("lost response recovery changed owner or comparison", err)
			}
			if phase == "match" || phase == "closure" {
				if reads != v.dataReads || !bytes.Equal(got.Sealed.Ciphertext, held.Sealed.Ciphertext) || !got.ComparedAt.Equal(held.ComparedAt) {
					t.Fatal("committed proof read again or replaced")
				}
			}
			if phase == "closure" && !reflect.DeepEqual(got, held) {
				t.Fatal("first closure times replaced")
			}
			v.assertClosed(t, true)
		})
	}
}

type verificationRetryLostNativeReply struct {
	verificationWorkerLostWindowReply
}

func (tr *verificationRetryLostNativeReply) TraceQueryStart(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	d.SQL = strings.ReplaceAll(d.SQL, "pg_temp.gregale_copy_database_verification_retries_change(", "pg_temp.gregale_copy_database_verification_change(")
	return tr.verificationWorkerLostWindowReply.TraceQueryStart(ctx, c, d)
}
func TestPGClonePostgresVerificationRetryWorkerLostNativeReplies(t *testing.T) {
	for _, phase := range []string{"open", "finish"} {
		t.Run(phase, func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			failFirstVerificationWorker(t, v)
			tr := &verificationRetryLostNativeReply{verificationWorkerLostWindowReply: verificationWorkerLostWindowReply{phase: phase}}
			v.f.bootstrapTracer = tr
			if got, err := runVerificationRetryWorker(t, v); err == nil || !tr.fired || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) {
				t.Fatal("lost native reply became proof", err)
			}
			phaseNative, owner, opened, closed := verificationRetryNativeWindow(t, v, 2)
			history := verificationRetryHistory(t, v)
			if phaseNative != "closed" || closed == nil || owner != history[1].VerificationID || !opened.Equal(history[1].WindowOpenedAt) {
				t.Fatal("native response loss not recovered exactly")
			}
			reads := v.dataReads
			v.f.bootstrapTracer = nil
			v.handoff(t)
			got, err := runVerificationRetryWorker(t, v)
			wantAttempt := int32(2)
			if phase == "open" {
				wantAttempt = 3
			}
			if err != nil || got.State != "verified" || got.Attempt != wantAttempt || v.dataReads != 2 {
				t.Fatal("lost native response recovery", err)
			}
			if phase == "finish" && (reads != v.dataReads || !got.NativeClosedAt.Equal(*closed) || !got.Sealed.OpenedAt.Equal(opened)) {
				t.Fatal("native closed match re-read or replaced")
			}
			if phase == "open" && (history[1].State != "failed" || !history[1].NativeClosedAt.Equal(*closed) || reads != 1) {
				t.Fatal("uncertain opening did not retain failed owner")
			}
			v.assertClosed(t, true)
		})
	}
}
func TestPGClonePostgresVerificationRetryWorkerMismatchesAndBudgetsStayBounded(t *testing.T) {
	for _, mode := range []string{"data", "read budget"} {
		t.Run(mode, func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			if mode == "data" {
				v.f.afterChild = func(ctx context.Context, conn *pgx.Conn) error {
					_, err := conn.Exec(ctx, "UPDATE public.selected_events SET note='actual different contents' WHERE id=7")
					return err
				}
			}
			v.importData(t, false)
			v.f.afterChild = nil
			want := managedpostgres.ErrConflict
			if mode == "read budget" {
				v.cfg.MaxBytes = 1
				want = managedpostgres.ErrQuotaExceeded
			}
			for attempt := 1; attempt <= 3; attempt++ {
				if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, want) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != attempt {
					t.Fatal("mismatch/budget published or re-read", attempt, err)
				}
				history := verificationRetryHistory(t, v)
				if len(history) != attempt || history[attempt-1].State != "failed" {
					t.Fatal("closed failed attempt missing")
				}
				v.assertClosed(t, true)
			}
			if _, err := runVerificationRetryWorker(t, v); !errors.Is(err, state.ErrQuotaExceeded) || v.dataReads != 3 || len(verificationRetryHistory(t, v)) != 3 || v.f.childCalls != 1 {
				t.Fatal("total ceiling escaped", err)
			}
		})
	}
}
func TestPGClonePostgresVerificationRetryWorkerCancellationAndHandoffDuringRead(t *testing.T) {
	for _, mode := range []string{"cancel", "handoff"} {
		t.Run(mode, func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			failFirstVerificationWorker(t, v)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tr := &verificationWorkerReadTrace{onRead: func() {
				if mode == "cancel" {
					cancel()
				} else {
					v.handoff(t)
				}
			}}
			v.childTrace = tr
			f, x := v.f, v.f.db.x
			if got, err := x.f.srv.projectEnvironmentClonePostgresVerificationWithRetries(ctx, x.f.lease, x.source, f.db.exports, f.sourceOID, v.cfg); err == nil || !tr.fired || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != 2 {
				t.Fatal("lost authority published retry", err)
			}
			history := verificationRetryHistory(t, v)
			held := history[1]
			if held.State != "verifying" || held.Sealed.Ciphertext != nil {
				t.Fatal("lost lease persisted evidence")
			}
			v.childTrace = nil
			if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrConflict) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != 2 {
				t.Fatal("recovery re-read closed owner", err)
			}
			history = verificationRetryHistory(t, v)
			if history[1].State != "failed" || history[1].VerificationID != held.VerificationID || len(history) != 2 {
				t.Fatal("interrupted owner replaced")
			}
			verified, err := runVerificationRetryWorker(t, v)
			if err != nil || verified.State != "verified" || verified.Attempt != 3 || v.dataReads != 3 {
				t.Fatal("new owned attempt after authority recovery", err)
			}
			v.assertClosed(t, true)
		})
	}
}
func TestPGClonePostgresVerificationRetryWorkerOldKeyHandoffAndDamagedParents(t *testing.T) {
	for _, mode := range []string{"rotation", "missing old key", "ciphertext", "manifest"} {
		t.Run(mode, func(t *testing.T) {
			v, store := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			failFirstVerificationWorker(t, v)
			store.loseRetryMatch = true
			if _, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrUnavailable) {
				t.Fatal(err)
			}
			held := verificationRetryHistory(t, v)[1]
			reads := v.dataReads
			x := v.f.db.x
			originalIdentities, originalRecipient := mfaIdentities, setSecretRecipient
			defer func() { mfaIdentities, setSecretRecipient = originalIdentities, originalRecipient }()
			next, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "rotation":
				ids := append(originalIdentities(), next)
				mfaIdentities = func() []*age.X25519Identity { return ids }
				setSecretRecipient = func() *age.X25519Recipient { return next.Recipient() }
			case "missing old key":
				mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{next} }
				setSecretRecipient = func() *age.X25519Recipient { return next.Recipient() }
			case "ciphertext":
				changed := bytes.Clone(held.Sealed.Ciphertext)
				changed[len(changed)/2] ^= 1
				h := sha256.Sum256(changed)
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_verification_attempts SET ciphertext=$2,ciphertext_sha256=$3 WHERE verification_id=$1", held.VerificationID, changed, hex.EncodeToString(h[:]))
				if err != nil {
					t.Fatal(err)
				}
			case "manifest":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_contents SET fingerprint=$2 WHERE operation_id=$1", held.Scope.OperationID, strings.Repeat("c", 64))
				if err != nil {
					t.Fatal(err)
				}
			}
			v.handoff(t)
			got, err := runVerificationRetryWorker(t, v)
			if mode == "rotation" {
				if err != nil || got.State != "verified" || got.KeyID != held.KeyID || !bytes.Equal(got.Sealed.Ciphertext, held.Sealed.Ciphertext) {
					t.Fatal("rotated recipient replaced original retry proof", err)
				}
			} else if err == nil || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) {
				t.Fatal("damaged key/parent/proof recovered", err)
			}
			if reads != v.dataReads || v.f.childCalls != 1 {
				t.Fatal("damage/rotation caused recapture or data read")
			}
		})
	}
}

func TestPGClonePostgresVerificationRetryWorkerProviderPostchecksAndAbsentDispatch(t *testing.T) {
	for _, phase := range []string{"missing native row", "child", "bootstrap after match"} {
		t.Run(phase, func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			failFirstVerificationWorker(t, v)
			f, x := v.f, v.f.db.x
			check := x.p.targetSQLCheck
			switch phase {
			case "missing native row":
				f.bootstrapPostError = managedpostgres.ErrUnavailable
			case "child":
				f.childPostError = managedpostgres.ErrUnavailable
			case "bootstrap after match":
				x.p.targetSQLCheck = func(ctx context.Context, conn *pgx.Conn, selected string) error {
					if err := check(ctx, conn, selected); err != nil {
						return err
					}
					if selected == x.target.DatabaseName {
						history, err := v.store.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx, x.f.lease, x.source.source.ID, f.sourceOID)
						if err != nil {
							return err
						}
						if len(history) == 2 && history[1].State == "compared" {
							return managedpostgres.ErrUnavailable
						}
					}
					return nil
				}
			}
			if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) {
				t.Fatal("provider failure became proof", err)
			}
			history := verificationRetryHistory(t, v)
			held := history[1]
			reads := v.dataReads
			want := map[string]string{"missing native row": "verifying", "child": "failed", "bootstrap after match": "compared"}[phase]
			if len(history) != 2 || held.State != want {
				t.Fatal("provider failure did not retain actual phase")
			}
			if phase == "missing native row" {
				if v.dataReads != 1 {
					t.Fatal("absence plus failed provider postcheck opened access")
				}
				// No retry journal may be installed until fully authenticated dispatch.
				cfg := x.targetRoot.Config().Copy()
				cfg.Database = x.target.DatabaseName
				conn, err := pgx.ConnectConfig(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				var absent bool
				err = conn.QueryRow(t.Context(), "SELECT NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='gregale_copy_database_verification_retries')").Scan(&absent)
				_ = conn.Close(context.Background())
				if err != nil || !absent {
					t.Fatal("failed provider postcheck committed native dispatch", err)
				}
			}
			f.childPostError, f.bootstrapPostError = nil, nil
			x.p.targetSQLCheck = check
			got, err := runVerificationRetryWorker(t, v)
			if err != nil || got.State != "verified" {
				t.Fatal("provider recovery did not verify", err)
			}
			if phase == "bootstrap after match" {
				if got.VerificationID != held.VerificationID || !bytes.Equal(got.Sealed.Ciphertext, held.Sealed.Ciphertext) || v.dataReads != reads {
					t.Fatal("provider recovery replaced matched evidence")
				}
			}
			if phase == "missing native row" && (got.VerificationID != held.VerificationID || v.dataReads != 2) {
				t.Fatal("never opened intent changed owner or data count")
			}
			if phase == "child" && (got.Attempt != 3 || v.dataReads != 3) {
				t.Fatal("child failure did not use new owned window")
			}
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationRetryWorkerFirstResponseRecovery(t *testing.T) {
	for _, phase := range []string{"reserve", "claim", "match", "closure", "native open"} {
		t.Run(phase, func(t *testing.T) {
			v, _ := retryVerificationWorkerFixture(t)
			v.importData(t, false)
			v.store.loseVerificationReserve = phase == "reserve"
			v.store.loseVerificationClaim = phase == "claim"
			v.store.loseMatch = phase == "match"
			v.store.loseClosure = phase == "closure"
			if phase == "native open" {
				v.f.bootstrapTracer = &verificationWorkerLostWindowReply{phase: "open"}
			}
			if got, err := runVerificationRetryWorker(t, v); err == nil || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) {
				t.Fatal("lost first response became proof", err)
			}
			first := v.owner(t)
			history := verificationRetryHistory(t, v)
			if phase == "native open" {
				if len(history) != 1 || history[0].State != "failed" || v.dataReads != 0 {
					t.Fatal("uncertain first opening not closed and retained")
				}
			} else if len(history) != 0 {
				t.Fatal("undispatched or matched first attempt became failed")
			}
			v.f.bootstrapTracer = nil
			v.handoff(t)
			got, err := runVerificationRetryWorker(t, v)
			want := int32(1)
			if phase == "native open" {
				want = 2
			}
			if err != nil || got.State != "verified" || got.Attempt != want || v.dataReads != 1 {
				t.Fatal("first response recovery re-read or replaced intent", err)
			}
			if phase != "native open" && got.VerificationID != first.VerificationID {
				t.Fatal("first reservation replaced")
			}
			if phase == "match" || phase == "closure" {
				if !bytes.Equal(got.Sealed.Ciphertext, first.Sealed.Ciphertext) || !got.ComparedAt.Equal(first.ComparedAt) {
					t.Fatal("first committed proof replaced")
				}
			}
			if phase == "closure" && (!got.NativeClosedAt.Equal(first.NativeClosedAt) || !got.VerifiedAt.Equal(first.VerifiedAt)) {
				t.Fatal("first closed timestamps replaced")
			}
			v.assertClosed(t, true)
		})
	}
}
