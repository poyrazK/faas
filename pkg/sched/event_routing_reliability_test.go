// adr: 20261111140000 — event delivery reliability repairs.
package sched_test

import (
	"bytes"
	"encoding/json"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/triggerconfig"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestEventReliabilityPermanentRoutingFailureStopsImmediately(t *testing.T) {
	t.Run("memory", func(t *testing.T) { auditPermanentRoutingFailure(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { auditPermanentRoutingFailure(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func auditPermanentRoutingFailure(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "routing-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "routing-" + uuid.NewString()[:8], RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.(state.EventSubscriptionStore).UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1}
	if _, err = store.(state.AppWorkPolicyStore).UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	if _, err = store.(state.EventWorkBindingStore).SetEventWorkBinding(ctx, app.ID, sub.ID, policy.Name, "data.order_id"); err != nil {
		t.Fatal(err)
	}
	envelope, err := (events.Envelope{ID: "missing-required-work-key", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"unrelated":true}`)}).Normalize(account.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
		t.Fatal(err)
	}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	loop.XRunEventFanoutSweep(ctx)
	receipt, err := store.(state.EventReceiptStore).EventReceipt(ctx, account.ID, envelope.Source, envelope.ID, state.EventReceiptCursor{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Recipients) != 1 {
		t.Fatalf("recipients=%+v", receipt.Recipients)
	}
	progress := receipt.Recipients[0].Routing
	if progress.FailureCode != "invalid_subscription" || progress.Retryable {
		t.Fatalf("expected classified permanent error, got %+v", progress)
	}
	if progress.State != "failed" || progress.RetryStopReason != "non_retryable" {
		t.Fatalf("permanent missing work key was scheduled for retry: state=%s retryable=%t attempts=%d stop=%q next=%v error=%q", progress.State, progress.Retryable, progress.Attempts, progress.RetryStopReason, progress.NextAttemptAt, progress.LastError)
	}
}

func TestEventReliabilityCircuitProbeTracksRenewedClaim(t *testing.T) {
	t.Run("memory", func(t *testing.T) { auditCircuitProbeRenewal(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { auditCircuitProbeRenewal(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func auditCircuitProbeRenewal(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "probe-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "probe-" + uuid.NewString()[:8], RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.(state.EventSubscriptionStore).UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	breaker := store.(state.EventCircuitBreakerStore)
	policy := api.DefaultEventCircuitBreakerPolicy()
	policy.MinSamples = 1
	policy.CooldownSeconds = 1
	if _, err = breaker.SetEventCircuitBreaker(ctx, account.ID, app.ID, sub.ID, &policy); err != nil {
		t.Fatal(err)
	}
	workStore := store.(state.PublishedEventWorkStore)
	recipients := store.(state.PublishedEventRecipientWorkStore)
	base := time.Now().UTC()
	for _, id := range []string{"failure", "probe", "contender"} {
		envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(account.ID, base)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
			t.Fatal(err)
		}
		receipt, err := workStore.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err = recipients.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	base = time.Now().UTC()
	failed, err := recipients.ClaimDuePublishedEventRecipient(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if reason, _, err := breaker.AcquireEventCircuitPermit(ctx, failed, base); err != nil || reason != "" {
		t.Fatalf("closed permit=%q error=%v", reason, err)
	}
	failure := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: failed.TotalAttempts, LastError: "synthetic route failure", FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true, UpdatedAt: base}
	if err = recipients.FinishPublishedEventRecipient(ctx, failed, failure, base); err != nil {
		t.Fatal(err)
	}
	candidate, err := recipients.ClaimDuePublishedEventRecipient(ctx, base.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	reason, next, err := breaker.AcquireEventCircuitPermit(ctx, candidate, base.Add(2*time.Second))
	if err != nil || reason != "circuit_open" {
		t.Fatalf("expected open circuit after failure, reason=%q next=%v error=%v", reason, next, err)
	}
	wait := state.EventCircuitWaitProgress(candidate.PreviousProgress, candidate.TotalAttempts, reason, next)
	if err = recipients.FinishPublishedEventRecipient(ctx, candidate, wait, next); err != nil {
		t.Fatal(err)
	}
	probe, err := recipients.ClaimDuePublishedEventRecipient(ctx, base.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	reason, _, err = breaker.AcquireEventCircuitPermit(ctx, probe, base.Add(4*time.Second))
	if err != nil || reason != "" {
		t.Fatalf("half-open probe not admitted: reason=%q error=%v", reason, err)
	}
	if err = store.(state.PublishedEventClaimRenewalStore).RenewPublishedEventRecipientClaims(ctx, []*state.PublishedEventRecipientWork{probe}, base.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	at := base.Add(70 * time.Second)
	// The original probe lease was base+64s; its actual claim is now live until base+100s.
	// Claim order can select the contender as the probe; read its actual envelope identity.
	var probeEnvelope events.Envelope
	if err = json.Unmarshal(probe.Payload, &probeEnvelope); err != nil {
		t.Fatal(err)
	}
	claimReceipt, err := store.(state.EventReceiptStore).EventReceipt(ctx, account.ID, "orders", probeEnvelope.ID, state.EventReceiptCursor{}, 100)
	if err != nil || len(claimReceipt.Recipients) != 1 {
		t.Fatalf("probe receipt=%+v error=%v", claimReceipt, err)
	}
	live := claimReceipt.Recipients[0].Routing
	if live.LeaseUntil == nil || !live.LeaseUntil.After(at) {
		t.Fatalf("renewal did not preserve actual probe claim: %+v", live)
	}
	contender, err := recipients.ClaimDuePublishedEventRecipient(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if contender.OutboxID == probe.OutboxID {
		t.Fatal("renewed probe was incorrectly reclaimed")
	}
	reason, _, err = breaker.AcquireEventCircuitPermit(ctx, contender, at)
	if err != nil {
		t.Fatal(err)
	}
	status, err := breaker.GetEventCircuitBreaker(ctx, account.ID, app.ID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "half_open" || reason != "circuit_probe_wait" {
		t.Fatalf("renewed probe still owns its claim until %v, but breaker state=%s reason=%s contender=%s", live.LeaseUntil, status.State, status.Reason, reason)
	}
}

func TestEventReliabilityNATSTokenNeverEntersConsumerHealth(t *testing.T) {
	t.Run("memory", func(t *testing.T) { auditNATSTokenHealth(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { auditNATSTokenHealth(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func auditNATSTokenHealth(t *testing.T, store state.Store) {
	for _, mode := range []string{"password_control", "token"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, "secret-"+uuid.NewString()+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{ID: strings.ReplaceAll(uuid.NewString(), "-", ""), AccountID: account.ID, Slug: "secret-" + uuid.NewString()[:8], RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			// Synthetic credential; the egress policy refuses this loopback destination.
			const marker = "audit-only-nats-secret-4631"
			userinfo := marker
			if mode == "password_control" {
				userinfo = "svc:" + marker
			}
			config, _ := json.Marshal(map[string]string{"url": "nats://" + userinfo + "@127.0.0.1:1", "stream": "ORDERS", "subject": "orders.*", "durable": "audit"})
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := triggerconfig.Seal(api.TriggerKindNATS, config, []*age.X25519Recipient{identity.Recipient()})
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(sealed, []byte(marker)) {
				t.Fatal("credential was not sealed in fixture")
			}
			trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "nats", "orders", true, sealed, "", 10, 1000, 5, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
			if err != nil {
				t.Fatal(err)
			}
			var log bytes.Buffer
			engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(&log, nil))).WithTriggerSecretIdentities([]*age.X25519Identity{identity})
			dispatchErr := loop.DispatchOneTrigger(ctx, trigger, store, func(string) api.Plan { return api.PlanPro })
			if dispatchErr == nil {
				t.Fatal("connection to denied loopback unexpectedly succeeded")
			}
			health, err := store.(state.TriggerConsumerHealthStore).TriggerConsumerHealth(ctx, trigger.ID.String())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(health.LastError, marker) || strings.Contains(dispatchErr.Error(), marker) {
				t.Fatalf("sealed NATS %s credential was exposed by connection failure: returned_error_leaked=%t retained_health_leaked=%t", mode, strings.Contains(dispatchErr.Error(), marker), strings.Contains(health.LastError, marker))
			}
		})
	}
}
