// adr: 1524
package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type recoverySubmissionStore interface {
	state.Store
	state.OperatorRecoverySubmissionStore
	state.AuditEventOutboxStore
}

type recoverySubmissionFixture struct {
	store    recoverySubmissionStore
	actor    state.Account
	owner    state.Account
	app      state.App
	dep      state.Deployment
	instance state.Instance
}

func seedRecoverySubmission(t *testing.T, store recoverySubmissionStore) recoverySubmissionFixture {
	t.Helper()
	actor, err := store.CreateAccount(t.Context(), uuid.NewString()+"@operator.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAccount(t.Context(), uuid.NewString()+"@tenant.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "recovery-" + uuid.NewString()[:8], RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:submission"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return recoverySubmissionFixture{store, actor, owner, app, dep, instance}
}

func (f recoverySubmissionFixture) request(kind state.OperatorIntentKind) state.OperatorRecoverySubmission {
	target := f.instance.ID
	if kind == state.OperatorIntentKindForceColdBoot {
		target = f.app.Slug
	}
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	return state.OperatorRecoverySubmission{ActorID: f.actor.ID, RequestKey: uuid.NewString(), Kind: kind, Target: target, Reason: "incident_42", TraceID: &trace}
}

func assertOnlyRecoverySubmission(t *testing.T, f recoverySubmissionFixture, receipt state.OperatorRecoveryReceipt) {
	t.Helper()
	intent, err := f.store.ClaimPendingOperatorIntent(t.Context())
	if err != nil || intent.ID != receipt.IntentID || intent.AccountID == nil || uuid.MustParse(*intent.AccountID) != uuid.MustParse(f.owner.ID) || uuid.MustParse(intent.ActorID) != uuid.MustParse(f.actor.ID) {
		t.Fatalf("missing attributed intent: %+v, %v", intent, err)
	}
	if _, err := f.store.ClaimPendingOperatorIntent(t.Context()); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Fatalf("duplicate recovery intent: %v", err)
	}
	audit, err := f.store.ClaimAuditEvent(t.Context(), "submission-test", time.Minute)
	if err != nil || audit.ID != receipt.AuditOutboxID || audit.Subject == nil || uuid.MustParse(*audit.Subject) != uuid.MustParse(f.owner.ID) || audit.Kind != state.OperatorRecoveryAuditKind(receipt.Kind) || audit.Actor != "apid" {
		t.Fatalf("missing attributed request audit: %+v, %v", audit, err)
	}
	var data map[string]any
	if err := json.Unmarshal(audit.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["intent_id"] != receipt.IntentID || data["reason"] != "incident_42" || data["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || data["result"] != "enqueued" {
		t.Fatalf("incorrect audit: %s", audit.Data)
	}
	if _, err := f.store.ClaimAuditEvent(t.Context(), "submission-test", time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate request audit: %v", err)
	}
}

func runRecoverySubmissionTests(t *testing.T, factory func(*testing.T) recoverySubmissionStore) {
	for _, kind := range []state.OperatorIntentKind{state.OperatorIntentKindForcePark, state.OperatorIntentKindForceRestart, state.OperatorIntentKindForceColdBoot} {
		t.Run(string(kind), func(t *testing.T) {
			f := seedRecoverySubmission(t, factory(t))
			in := f.request(kind)
			first, err := f.store.SubmitOperatorRecovery(t.Context(), in)
			if err != nil || first.Replayed {
				t.Fatalf("accept: %+v %v", first, err)
			}
			if kind == state.OperatorIntentKindForceColdBoot {
				if _, err := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new"}); err != nil {
					t.Fatal(err)
				}
				if err := f.store.DeleteApp(t.Context(), f.app.ID); err != nil {
					t.Fatal(err)
				}
			} else if err := f.store.UpdateInstanceState(t.Context(), f.instance.ID, string(state.StateStopped)); err != nil {
				t.Fatal(err)
			}
			// A retry after a lost response does not re-read mutable target state.
			trace := "11111111111111111111111111111111"
			in.TraceID = &trace
			replay, err := f.store.SubmitOperatorRecovery(t.Context(), in)
			if err != nil || !replay.Replayed || replay.IntentID != first.IntentID || replay.TargetID != first.TargetID || !bytes.Equal(first.ResponseBody, replay.ResponseBody) {
				t.Fatalf("replay lost original acceptance: %+v %v", replay, err)
			}
			for _, mutate := range []func(*state.OperatorRecoverySubmission){
				func(r *state.OperatorRecoverySubmission) { r.Reason = "different_reason" },
				func(r *state.OperatorRecoverySubmission) {
					r.Kind, r.Target = state.OperatorIntentKindForceColdBoot, "other-app"
				},
				func(r *state.OperatorRecoverySubmission) {
					if r.Kind == state.OperatorIntentKindForceColdBoot {
						r.Target = "missing-app"
					} else {
						r.Target = uuid.NewString()
					}
				},
			} {
				changed := in
				mutate(&changed)
				if _, err := f.store.SubmitOperatorRecovery(t.Context(), changed); !errors.Is(err, state.ErrOperatorRecoveryKeyConflict) {
					t.Fatalf("changed parameters accepted: %v", err)
				}
			}
			assertOnlyRecoverySubmission(t, f, first)
			if _, err := f.store.DeliverAuditEvent(t.Context(), first.AuditOutboxID); err != nil {
				t.Fatal(err)
			}
			if n, err := f.store.PruneAuditEventOutbox(t.Context(), time.Now().Add(time.Hour)); err != nil || n != 1 {
				t.Fatalf("prune audit: %d %v", n, err)
			}
			replay, err = f.store.SubmitOperatorRecovery(t.Context(), in)
			if err != nil || !bytes.Equal(replay.ResponseBody, first.ResponseBody) || replay.AuditOutboxID != first.AuditOutboxID {
				t.Fatalf("pruning erased acceptance: %+v %v", replay, err)
			}
			if _, err := f.store.ClaimAuditEvent(t.Context(), "after-prune", time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("replay recreated pruned audit: %v", err)
			}
		})
	}
	t.Run("concurrent_acceptance", func(t *testing.T) {
		f := seedRecoverySubmission(t, factory(t))
		in := f.request(state.OperatorIntentKindForceRestart)
		var wg sync.WaitGroup
		var winners atomic.Int32
		results := make(chan state.OperatorRecoveryReceipt, 16)
		for range 16 {
			wg.Go(func() {
				result, err := f.store.SubmitOperatorRecovery(t.Context(), in)
				if err != nil {
					t.Error(err)
					return
				}
				if !result.Replayed {
					winners.Add(1)
				}
				results <- result
			})
		}
		wg.Wait()
		close(results)
		if winners.Load() != 1 || len(results) != 16 {
			t.Fatalf("winners=%d results=%d", winners.Load(), len(results))
		}
		first := <-results
		for receipt := range results {
			if receipt.IntentID != first.IntentID || !bytes.Equal(receipt.ResponseBody, first.ResponseBody) {
				t.Fatal("concurrent requests diverged")
			}
		}
		assertOnlyRecoverySubmission(t, f, first)
	})
	t.Run("actor_isolation", func(t *testing.T) {
		f := seedRecoverySubmission(t, factory(t))
		in := f.request(state.OperatorIntentKindForcePark)
		first, err := f.store.SubmitOperatorRecovery(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		in.ActorID = f.owner.ID
		second, err := f.store.SubmitOperatorRecovery(t.Context(), in)
		if err != nil || second.IntentID == first.IntentID || second.Replayed {
			t.Fatalf("actor scope leaked: %+v %v", second, err)
		}
	})
	t.Run("cancelled_before_acceptance", func(t *testing.T) {
		f := seedRecoverySubmission(t, factory(t))
		in := f.request(state.OperatorIntentKindForcePark)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.store.SubmitOperatorRecovery(ctx, in); err == nil {
			t.Fatal("cancelled submission accepted")
		}
		first, err := f.store.SubmitOperatorRecovery(t.Context(), in)
		if err != nil || first.Replayed {
			t.Fatalf("failed submission reserved key: %+v %v", first, err)
		}
		assertOnlyRecoverySubmission(t, f, first)
	})
	t.Run("legacy_key_fails_closed", func(t *testing.T) {
		f := seedRecoverySubmission(t, factory(t))
		in := f.request(state.OperatorIntentKindForceRestart)
		if err := f.store.PutIdempotent(t.Context(), f.actor.ID, "POST /v1/admin/instances/"+f.instance.ID+"/force-park\n"+in.RequestKey, 202, []byte(`{"intent_id":"legacy"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.SubmitOperatorRecovery(t.Context(), in); !errors.Is(err, state.ErrOperatorRecoveryLegacyKey) {
			t.Fatalf("legacy key re-executed: %v", err)
		}
		if _, err := f.store.ClaimPendingOperatorIntent(t.Context()); !errors.Is(err, state.ErrOperatorIntentNotFound) {
			t.Fatalf("legacy key created intent: %v", err)
		}
	})
	t.Run("canonical_uuid_and_default_reason", func(t *testing.T) {
		f := seedRecoverySubmission(t, factory(t))
		in := f.request(state.OperatorIntentKindForceRestart)
		in.Reason = ""
		first, err := f.store.SubmitOperatorRecovery(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		in.Target = strings.ToUpper(uuid.MustParse(in.Target).String())
		in.ActorID = strings.ReplaceAll(uuid.MustParse(in.ActorID).String(), "-", "")
		in.Reason = "operator_force_restart"
		second, err := f.store.SubmitOperatorRecovery(t.Context(), in)
		if err != nil || !second.Replayed || !bytes.Equal(first.ResponseBody, second.ResponseBody) {
			t.Fatalf("equivalent request diverged: %+v %v", second, err)
		}
		second.ResponseBody[0] = '!'
		third, err := f.store.SubmitOperatorRecovery(t.Context(), in)
		if err != nil || !bytes.Equal(first.ResponseBody, third.ResponseBody) {
			t.Fatal("caller mutated durable receipt")
		}
	})
}

func TestOperatorRecoverySubmissionMemory(t *testing.T) {
	runRecoverySubmissionTests(t, func(*testing.T) recoverySubmissionStore { return state.NewMemStore() })
}
