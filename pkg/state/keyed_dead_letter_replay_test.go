package state_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type keyedDLQWork struct {
	id, item string
	broker   bool
}

type keyedDLQFixture struct {
	store            *state.PgStore
	pool             *pgxpool.Pool
	ctx              context.Context
	accountID, appID string
	triggerID        string
	policy           state.AppWorkPolicy
	queueBinding     state.QueueBinding
	queueTriggerID   string
}

func newKeyedDLQFixture(t *testing.T) *keyedDLQFixture {
	t.Helper()
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	policy, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, workpolicy.Policy{
		Name: "orders", MaxRunningPerKey: 1, ExpiresAfter: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "orders", true,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	return &keyedDLQFixture{store: store, pool: pool, ctx: ctx, accountID: accountID,
		appID: appID, triggerID: trigger.ID.String(), policy: policy}
}

func (f *keyedDLQFixture) admit(t *testing.T, item string, broker bool, key string) keyedDLQWork {
	t.Helper()
	work := keyedDLQWork{item: item, broker: broker}
	var err error
	if broker {
		work.id, err = f.store.InsertKeyedTriggerRecord(f.ctx, f.triggerID, item,
			[]byte(`{"order":1}`), nil, nil, f.policy, key)
	} else {
		var inv state.Invocation
		inv, err = f.store.EnqueueKeyedInvocation(f.ctx, state.Invocation{AccountID: f.accountID,
			AppID: f.appID, Source: state.InvocationQueue, DeploymentScope: "staging",
			QueueName: f.queueBinding.QueueName, QueueBindingID: f.queueBinding.ID,
			WorkPolicyRevision: f.policy.Revision, Payload: []byte(`{"order":1}`)}, f.policy.Policy, key)
		work.id = inv.ID
	}
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func (f *keyedDLQFixture) claim(work keyedDLQWork) (int64, error) {
	if work.broker {
		rows, err := f.store.ClaimTriggerRecordsByItems(f.ctx, f.triggerID, []string{work.item})
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return 0, state.ErrConflict
		}
		return rows[0].ClaimGeneration, nil
	}
	var row state.Invocation
	var err error
	if f.queueBinding.ID != "" {
		row, err = f.store.ClaimQueueTriggerInvocation(f.ctx, work.id, f.queueTriggerID, f.appID, f.queueBinding.QueueName, 60)
	} else {
		row, err = f.store.ClaimInvocationWithCap(f.ctx, work.id, "", 60, 10)
	}
	return int64(row.Attempts), err
}

func (f *keyedDLQFixture) complete(work keyedDLQWork, generation int64) error {
	if work.broker {
		return f.store.CompleteClaimedTriggerRecord(f.ctx, work.id, generation)
	}
	return f.store.CompleteKeyedInvocation(f.ctx, work.id, int(generation), nil)
}

func (f *keyedDLQFixture) deadLetter(t *testing.T, work keyedDLQWork) (string, int64) {
	t.Helper()
	generation, err := f.claim(work)
	if err != nil {
		t.Fatal(err)
	}
	source := "invocation"
	if work.broker {
		source = "trigger_record"
		err = f.store.RouteClaimedTriggerDeadLetter(f.ctx, work.id, generation, f.triggerID, "poison_record", []byte(`{}`))
	} else {
		err = f.store.FailInvocation(f.ctx, work.id, "poison_record", time.Nanosecond, 1, state.WithClaimAttempt(int(generation)))
	}
	if err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := f.pool.QueryRow(f.ctx, "select id::text from dead_letter_events where source=$1 and source_id=$2", source, work.id).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	return eventID, generation
}

func (f *keyedDLQFixture) replay(work keyedDLQWork, eventID, surface string) error {
	switch surface {
	case "direct":
		if work.broker {
			return f.store.RetryTriggerRecordByOperator(f.ctx, work.id)
		}
		_, err := f.store.RetryQueueDeadLetter(f.ctx, f.accountID, work.id)
		return err
	case "account":
		_, err := f.store.ReplayDeadLetterEventForAccount(f.ctx, f.accountID, eventID)
		return err
	case "app-batch":
		n, err := f.store.ReplayDeadLetterEvents(f.ctx, f.accountID, f.appID, 100)
		if err == nil && n != 1 {
			return fmt.Errorf("replayed %d events, want 1", n)
		}
		return err
	case "account-batch":
		n, err := f.store.ReplayDeadLetterEventsForAccount(f.ctx, f.accountID, 100)
		if err == nil && n != 1 {
			return fmt.Errorf("replayed %d events, want 1", n)
		}
		return err
	default:
		_, err := f.store.ReplayDeadLetterEvent(f.ctx, f.accountID, f.appID, eventID)
		return err
	}
}

func (f *keyedDLQFixture) identity(t *testing.T, work keyedDLQWork) string {
	t.Helper()
	query := `select jsonb_build_array(work_policy_name,work_key_digest,work_sequence,work_policy_revision,
		work_expires_at,work_fairness_digest,work_fairness_limit,payload,headers)::text from invocations where id=$1`
	if work.broker {
		query = `select jsonb_build_array(work_policy_name,work_key_digest,work_sequence,work_policy_revision,
			work_expires_at,work_fairness_digest,work_fairness_limit,payload,headers)::text from trigger_records where id=$1`
	}
	var identity string
	if err := f.pool.QueryRow(f.ctx, query, work.id).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	return identity
}

func claimKeyedDLQConcurrently(t *testing.T, f *keyedDLQFixture, work keyedDLQWork) int64 {
	t.Helper()
	type result struct {
		generation int64
		err        error
	}
	start, results := make(chan struct{}), make(chan result, 8)
	for range 8 {
		go func() {
			<-start
			generation, err := f.claim(work)
			results <- result{generation, err}
		}()
	}
	close(start)
	var winner int64
	for range 8 {
		got := <-results
		if got.err == nil {
			if winner != 0 {
				t.Fatal("concurrent schedulers admitted the replay twice")
			}
			winner = got.generation
		} else if !errors.Is(got.err, state.ErrConflict) {
			t.Fatal(got.err)
		}
	}
	if winner == 0 {
		t.Fatal("no scheduler could claim the replay")
	}
	return winner
}

// adr: 610
func TestPgKeyedDeadLetterReplayWaitsForNewerClaim(t *testing.T) {
	for _, oldBroker := range []bool{false, true} {
		for _, newBroker := range []bool{false, true} {
			for _, surface := range []string{"app", "account", "app-batch", "account-batch", "direct"} {
				t.Run(fmt.Sprintf("old-broker=%t/new-broker=%t/%s", oldBroker, newBroker, surface), func(t *testing.T) {
					f := newKeyedDLQFixture(t)
					old := f.admit(t, "old", oldBroker, "s:order-one")
					eventID, oldGeneration := f.deadLetter(t, old)
					identity := f.identity(t, old)
					newer := f.admit(t, "new", newBroker, "s:order-one")
					newGeneration, err := f.claim(newer)
					if err != nil {
						t.Fatal(err)
					}
					if err := f.replay(old, eventID, surface); err != nil {
						t.Fatal(err)
					}
					if got := f.identity(t, old); got != identity {
						t.Fatal("replay changed captured lane, policy, payload or pending expiry")
					}
					if _, err := f.claim(old); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("replayed older row overlapped newer same-key claim: %v", err)
					}
					if !oldBroker {
						for _, paged := range []bool{false, true} {
							var rows []state.Invocation
							if paged {
								rows, err = f.store.ListDueInvocationsAfter(f.ctx, time.Now(), state.InvocationDueCursor{}, 64)
							} else {
								rows, err = f.store.ListDueInvocations(f.ctx, time.Now(), 64)
							}
							if err != nil || len(rows) != 0 {
								t.Fatalf("busy replay entered due page: %+v %v", rows, err)
							}
						}
					}
					other := f.admit(t, "other", !newBroker, "s:order-two")
					otherGeneration, err := f.claim(other)
					if err != nil {
						t.Fatalf("unrelated key was blocked: %v", err)
					}
					if err := f.complete(other, otherGeneration); err != nil {
						t.Fatal(err)
					}
					if err := f.complete(newer, newGeneration); err != nil {
						t.Fatal(err)
					}
					generation := claimKeyedDLQConcurrently(t, f, old)
					if oldBroker {
						if generation <= oldGeneration {
							t.Fatal("replay reused a stale broker generation")
						}
						if err := f.complete(old, oldGeneration); !errors.Is(err, state.ErrNotFound) {
							t.Fatalf("stale broker completion accepted: %v", err)
						}
					} else {
						row, err := f.store.InvocationByID(f.ctx, old.id)
						if err != nil || row.ReplayGeneration != 1 || row.DeploymentScope != "staging" {
							t.Fatalf("replay identity: %+v %v", row, err)
						}
						carrier := state.Invocation{ID: old.id, Source: "esm", Attempts: 1}
						if _, err := state.AdmitPlatformTenantInvocation(f.ctx, f.store, f.appID, carrier); !errors.Is(err, state.ErrConflict) {
							t.Fatalf("stale delivery admitted: %v", err)
						}
					}
					if err := f.complete(old, generation); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestPgKeyedDeadLetterReplayAllowsLeaseRecovery(t *testing.T) {
	for _, broker := range []bool{false, true} {
		t.Run(fmt.Sprintf("broker=%t", broker), func(t *testing.T) {
			f := newKeyedDLQFixture(t)
			old := f.admit(t, "old", !broker, "s:order-one")
			eventID, _ := f.deadLetter(t, old)
			newer := f.admit(t, "new", broker, "s:order-one")
			oldClaim, err := f.claim(newer)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.replay(old, eventID, "app"); err != nil {
				t.Fatal(err)
			}
			if broker {
				if _, err := f.pool.Exec(f.ctx, "update trigger_records set claim_expires_at=clock_timestamp()-interval '1 second' where id=$1", newer.id); err != nil {
					t.Fatal(err)
				}
				if _, err := f.claim(old); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("replay bypassed expired, unrecovered owner: %v", err)
				}
				newClaim, err := f.claim(newer)
				if err != nil || newClaim <= oldClaim {
					t.Fatalf("broker owner could not recover lease ahead of replay: %d %v", newClaim, err)
				}
				if err := f.complete(newer, oldClaim); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("expired owner completed newer generation: %v", err)
				}
				if err := f.complete(newer, newClaim); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.pool.Exec(f.ctx, "update invocations set lease_expires_at=clock_timestamp()-interval '1 second' where id=$1", newer.id); err != nil {
					t.Fatal(err)
				}
				if _, err := f.claim(old); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("replay bypassed unreaped invocation: %v", err)
				}
				if n, err := f.store.RequeueExpiredInvocations(f.ctx, time.Now(), 64); err != nil || n != 1 {
					t.Fatalf("reaper: %d %v", n, err)
				}
				if err := f.complete(newer, oldClaim); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("reaped owner completed: %v", err)
				}
			}
			generation, err := f.claim(old)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.complete(old, generation); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func holdKeyedDLQLane(t *testing.T, f *keyedDLQFixture, invID string) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(f.ctx, `select l.app_id from invocation_work_lanes l join invocations i
		on i.app_id=l.app_id and i.work_policy_name=l.policy_name and i.work_key_digest=l.key_digest
		where i.id=$1 for update of l`, invID); err != nil {
		t.Fatal(err)
	}
	return tx
}

func waitKeyedDLQReplayLock(t *testing.T, f *keyedDLQFixture, query string, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := f.pool.QueryRow(f.ctx, `select count(*) from pg_stat_activity
			where datname=current_database() and wait_event_type='Lock' and state='active' and query like $1`, "%-- name: "+query+"%").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%d replay operations did not wait on the work lane", count)
}

func TestPgKeyedDeadLetterReplaySerializesWithClaimLane(t *testing.T) {
	for _, broker := range []bool{false, true} {
		for _, surface := range []string{"direct", "app", "app-batch", "account-batch", "receipt"} {
			t.Run(fmt.Sprintf("broker=%t/%s", broker, surface), func(t *testing.T) {
				f := newKeyedDLQFixture(t)
				old := f.admit(t, "old", broker, "s:order-one")
				eventID, _ := f.deadLetter(t, old)
				holdID, receiptID := old.id, old.id
				if broker {
					holdID = f.admit(t, "anchor", false, "s:order-one").id
				} else if surface == "receipt" {
					trigger, err := f.store.CreateTriggerIfUnderQuota(f.ctx, f.appID, "queue", "jobs", true, []byte(`{}`), "queue", 10, 1000, 3, 1024, "commit", api.MustLimitsFor(api.PlanPro))
					if err != nil {
						t.Fatal(err)
					}
					receiptID, err = f.store.InsertTriggerRecord(f.ctx, trigger.ID.String(), old.id, []byte(`{}`), nil, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				tx := holdKeyedDLQLane(t, f, holdID)
				result := make(chan error, 1)
				go func() {
					if surface == "receipt" {
						result <- f.store.RetryTriggerRecordByOperator(f.ctx, receiptID)
					} else {
						result <- f.replay(old, eventID, surface)
					}
				}()
				query := "LockInvocationReplayLane"
				if broker {
					query = "LockTriggerReplayLane"
				}
				if surface == "app" || surface == "app-batch" || surface == "account-batch" {
					query = "LockDeadLetterReplayLanes"
				}
				waitKeyedDLQReplayLock(t, f, query, 1)
				var status string
				if broker {
					if err := f.pool.QueryRow(f.ctx, "select state from trigger_records where id=$1", old.id).Scan(&status); err != nil {
						t.Fatal(err)
					}
				} else {
					row, err := f.store.InvocationByID(f.ctx, old.id)
					if err != nil {
						t.Fatal(err)
					}
					status = string(row.State)
				}
				if status != "dead_letter" {
					t.Fatalf("replay changed row while lane was owned: %s", status)
				}
				if err := tx.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-result:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("replay did not resume")
				}
			})
		}
	}
}

func TestPgKeyedDeadLetterBulkReplayLocksLanesInOrder(t *testing.T) {
	f := newKeyedDLQFixture(t)
	var works []keyedDLQWork
	for i, key := range []string{"s:one", "s:two", "s:two", "s:one"} {
		work := f.admit(t, fmt.Sprintf("old-%d", i), false, key)
		eventID, _ := f.deadLetter(t, work)
		if _, err := f.pool.Exec(f.ctx, "update dead_letter_events set last_failed_at=$2 where id=$1", eventID, time.Now().Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		works = append(works, work)
	}
	first, err := f.store.InvocationByID(f.ctx, works[0].id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.store.InvocationByID(f.ctx, works[1].id)
	if err != nil {
		t.Fatal(err)
	}
	lowest := works[0].id
	if bytes.Compare(second.WorkKeyDigest, first.WorkKeyDigest) < 0 {
		lowest = works[1].id
	}
	tx := holdKeyedDLQLane(t, f, lowest)
	result := make(chan error, 2)
	go func() {
		n, err := f.store.ReplayDeadLetterEvents(f.ctx, f.accountID, f.appID, 2)
		if err == nil && n != 2 {
			err = fmt.Errorf("first batch replayed %d", n)
		}
		result <- err
	}()
	waitKeyedDLQReplayLock(t, f, "LockDeadLetterReplayLanes", 1)
	go func() {
		n, err := f.store.ReplayDeadLetterEventsForAccount(f.ctx, f.accountID, 4)
		if err == nil && n != 2 {
			err = fmt.Errorf("second batch replayed %d", n)
		}
		result <- err
	}()
	waitKeyedDLQReplayLock(t, f, "LockDeadLetterReplayLanes", 2)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("bulk replay deadlocked")
		}
	}
}

func TestPgKeyedDeadLetterNamedQueueWaitsForRunningBroker(t *testing.T) {
	f := newKeyedDLQFixture(t)
	if _, err := f.store.SetAppWorkloadClass(f.ctx, f.appID, state.WorkloadClassWorker, "scan_hint"); err != nil {
		t.Fatal(err)
	}
	created, err := f.store.CreateQueueBindingWithConsumer(f.ctx, state.QueueBinding{AccountID: f.accountID, AppID: f.appID,
		Name: "jobs", QueueName: "jobs", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	f.queueBinding, f.queueTriggerID = created.Binding, created.Changes[0].TriggerID
	old := f.admit(t, "old", false, "s:order-one")
	eventID, _ := f.deadLetter(t, old)
	newer := f.admit(t, "new", true, "s:order-one")
	generation, err := f.claim(newer)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.replay(old, eventID, "direct"); err != nil {
		t.Fatal(err)
	}
	params := sqlc.QueuePollCandidatesParams{TriggerID: keyedDLQUUID(t, f.queueTriggerID), AppID: keyedDLQUUID(t, f.appID), QueueName: "jobs", CandidateLimit: 64}
	if rows, err := sqlc.New().QueuePollCandidates(f.ctx, f.pool, params); err != nil || len(rows) != 0 {
		t.Fatalf("busy replay entered queue poll: %v %v", rows, err)
	}
	if _, err := f.claim(old); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("named queue replay bypassed running broker: %v", err)
	}
	if err := f.complete(newer, generation); err != nil {
		t.Fatal(err)
	}
	if rows, err := sqlc.New().QueuePollCandidates(f.ctx, f.pool, params); err != nil || len(rows) != 1 || rows[0] != old.id {
		t.Fatalf("replay did not enter queue poll after completion: %v %v", rows, err)
	}
	if _, err := f.claim(old); err != nil {
		t.Fatal(err)
	}
}

func TestPgKeyedDeadLetterReplayDoesNotRenewExpiry(t *testing.T) {
	for _, broker := range []bool{false, true} {
		t.Run(fmt.Sprintf("broker=%t", broker), func(t *testing.T) {
			f := newKeyedDLQFixture(t)
			old := f.admit(t, "old", broker, "s:order-one")
			eventID, _ := f.deadLetter(t, old)
			query := "update invocations set work_expires_at=clock_timestamp()-interval '1 second' where id=$1"
			if broker {
				query = "update trigger_records set work_expires_at=clock_timestamp()-interval '1 second' where id=$1"
			}
			if _, err := f.pool.Exec(f.ctx, query, old.id); err != nil {
				t.Fatal(err)
			}
			identity := f.identity(t, old)
			if err := f.replay(old, eventID, "app"); err != nil {
				t.Fatal(err)
			}
			if f.identity(t, old) != identity {
				t.Fatal("dead-letter replay renewed expired work")
			}
			if n, err := f.store.ExpirePendingKeyedInvocations(f.ctx, time.Now(), 64); err != nil || n != 1 {
				t.Fatalf("expiry sweep: %d %v", n, err)
			}
			if _, err := f.claim(old); err == nil {
				t.Fatal("expired replay was dispatched")
			}
		})
	}
}

func keyedDLQUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func TestPgKeyedDeadLetterReplayLocksSourceBeforeFailureLedger(t *testing.T) {
	for _, broker := range []bool{false, true} {
		for _, surface := range []string{"app", "account", "app-batch", "account-batch"} {
			t.Run(fmt.Sprintf("broker=%t/%s", broker, surface), func(t *testing.T) {
				f := newKeyedDLQFixture(t)
				old := f.admit(t, "old", broker, "s:order-one")
				eventID, _ := f.deadLetter(t, old)
				if err := f.replay(old, eventID, "direct"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.claim(old); err != nil {
					t.Fatal(err)
				}
				// A direct retry can leave a projection for an earlier failure.
				// Model a failure writer paused after locking its execution but
				// before updating the ledger; replay must not take that ledger first.
				if _, err := f.pool.Exec(f.ctx, "update dead_letter_events set replayed_at=null where id=$1", eventID); err != nil {
					t.Fatal(err)
				}
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
				lock, query := "select id from invocations where id=$1 for update", "LockKeyedDeadLetterInvocationRows"
				if broker {
					lock, query = "select id from trigger_records where id=$1 for update", "LockKeyedDeadLetterTriggerRows"
				}
				if _, err := tx.Exec(f.ctx, lock, old.id); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- f.replay(old, eventID, surface) }()
				waitKeyedDLQReplayLock(t, f, query, 1)
				writerCtx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
				defer cancel()
				update := "update invocations set state='dead_letter',last_error='second failure',quota_reserved=false,lease_expires_at=null where id=$1"
				if broker {
					update = "update trigger_records set state='dead_letter',last_error='second failure',claim_expires_at=null where id=$1"
				}
				if _, err := tx.Exec(writerCtx, update, old.id); err != nil {
					t.Fatalf("failure writer blocked on replay's ledger: %v", err)
				}
				if _, err := tx.Exec(writerCtx, "update dead_letter_events set error_detail=$2::jsonb where id=$1", eventID, []byte(`{"message":"second failure"}`)); err != nil {
					t.Fatalf("failure writer could not update its ledger: %v", err)
				}
				if err := tx.Commit(writerCtx); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-result:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("replay did not resume after failure writer")
				}
			})
		}
	}
}

func TestPgKeyedDeadLetterReplayRetainsPendingSequenceOrder(t *testing.T) {
	f := newKeyedDLQFixture(t)
	old := f.admit(t, "old", false, "s:order-one")
	eventID, _ := f.deadLetter(t, old)
	newer := f.admit(t, "new", true, "s:order-one")
	newGeneration, err := f.claim(newer)
	if err != nil {
		t.Fatal(err)
	}
	later := f.admit(t, "later", false, "s:order-one")
	if err := f.replay(old, eventID, "app"); err != nil {
		t.Fatal(err)
	}
	if err := f.complete(newer, newGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := f.claim(later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("later pending work passed original replay sequence: %v", err)
	}
	oldGeneration, err := f.claim(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.complete(old, oldGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := f.claim(later); err != nil {
		t.Fatalf("later pending work stayed blocked: %v", err)
	}
}
