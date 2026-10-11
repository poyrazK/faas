package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func seedOwnedWorkflowRouting(t *testing.T, store Store, amount int) (App, *PublishedEventWork, PublishedEventRoutingClaim) {
	t.Helper()
	app, _ := seedEventWorkflow(t, store)
	ctx := t.Context()
	payload, err := json.Marshal(map[string]any{"specversion": "1.0", "id": uuid.NewString(), "source": "billing.stripe", "type": "invoice.paid",
		"accountid": canonicalMemUUID(app.AccountID), "datacontenttype": "application/json", "time": time.Now().UTC(), "data": map[string]any{"amount": amount}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &app.AccountID, payload); err != nil {
		t.Fatal(err)
	}
	work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.(PublishedEventRecipientWorkStore).InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.(PublishedEventRecipientWorkStore).ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return app, work, PublishedEventRoutingClaim{OutboxID: work.ID, SubscriptionID: claimed.Recipient.ID,
		ClaimToken: claimed.ClaimToken, Generation: claimed.Generation}
}

// adr: 648 — the run, admission receipt and routing checkpoint are one commit.
func TestEventWorkflowRecipientAdmissionConcurrentRecoveryAndPruning(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, work, claim := seedOwnedWorkflowRouting(t, store, 150)
		ctx := t.Context()
		replacement, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
			ImageDigest: "sha256:def", Status: DeployPending, Workflows: json.RawMessage(`[]`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, replacement.ID); err != nil {
			t.Fatal(err)
		}
		admission := store.(EventWorkflowRecipientAdmissionStore)
		results := make(chan EventWorkflowRoutingResult, 8)
		var group sync.WaitGroup
		for range cap(results) {
			group.Add(1)
			go func() {
				defer group.Done()
				result, err := admission.AdmitEventWorkflowRecipient(ctx, claim)
				if err != nil {
					t.Error(err)
				}
				results <- result
			}()
		}
		group.Wait()
		close(results)
		var admitted EventWorkflowRoutingResult
		created := 0
		for result := range results {
			if admitted.RunID != "" && result.RunID != admitted.RunID {
				t.Fatalf("different run identities: %s and %s", result.RunID, admitted.RunID)
			}
			if !result.Matched || !result.ReceiptSettled || result.Progress.State != PublishedEventRecipientEnqueued || result.Progress.Attempts != 1 {
				t.Fatalf("result=%+v", result)
			}
			if result.RunCreated {
				created++
			}
			admitted = result
		}
		if created != 1 || admitted.RunID == "" {
			t.Fatalf("created=%d result=%+v", created, admitted)
		}
		run, err := store.GetWorkflowRun(ctx, admitted.RunID)
		if err != nil || !equalWorkflowJSON(run.Input, work.Payload) || !equalWorkflowJSON(run.DefinitionSnapshot, work.RecipientSnapshot[0].Workflow) {
			t.Fatalf("captured run=%+v err=%v", run, err)
		}
		assertWorkflowRoutingEvidence(t, store, app, claim, PublishedEventRecipientEnqueued, 1)
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
			t.Fatalf("pruned=%d err=%v", n, err)
		}
		again, err := admission.AdmitEventWorkflowRecipient(ctx, claim)
		if err != nil || again.RunID != "" || again.RunCreated || again.Progress != admitted.Progress {
			t.Fatalf("pruned recovery=%+v err=%v", again, err)
		}
		if _, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
			t.Fatalf("pruned run repeated: total=%d err=%v", total, err)
		}
		assertWorkflowRoutingEvidence(t, store, app, claim, PublishedEventRecipientEnqueued, 1)
	})
}

func assertWorkflowRoutingEvidence(t *testing.T, store Store, app App, claim PublishedEventRoutingClaim, want string, historyCount int) {
	t.Helper()
	ctx := t.Context()
	var state string
	var history int
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		state = s.routingReceiptLocked(claim.OutboxID).RecipientProgress[claim.SubscriptionID].State
		for _, attempt := range s.eventFanoutAttempts {
			if attempt.OutboxID == claim.OutboxID && attempt.SubscriptionID == claim.SubscriptionID {
				history++
			}
		}
		s.mu.Unlock()
	case *PgStore:
		if err := s.pool.QueryRow(ctx, `SELECT coalesce(recipient_progress->$2->>'state',''),
			(SELECT count(*) FROM event_fanout_attempt_history WHERE outbox_id=$1 AND subscription_id=$2)
			FROM event_fanout_outbox WHERE id=$1`, claim.OutboxID, claim.SubscriptionID).Scan(&state, &history); err != nil {
			t.Fatal(err)
		}
	}
	if state != want || history != historyCount {
		t.Fatalf("routing state=%q history=%d want=%q/%d", state, history, want, historyCount)
	}
	if invocations, err := store.ListInvocationsForApp(ctx, app.ID); err != nil || len(invocations) != 0 {
		t.Fatalf("workflow routed as application invocation: %+v err=%v", invocations, err)
	}
}

func TestEventWorkflowRecipientAdmissionGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, Store, App, *PublishedEventWork, *PublishedEventRoutingClaim)
		want   error
	}{
		{"wrong_token", func(_ *testing.T, _ Store, _ App, _ *PublishedEventWork, c *PublishedEventRoutingClaim) {
			c.ClaimToken = uuid.NewString()
		}, ErrConflict},
		{"wrong_generation", func(_ *testing.T, _ Store, _ App, _ *PublishedEventWork, c *PublishedEventRoutingClaim) {
			c.Generation++
		}, ErrConflict},
		{"parent_lease", func(_ *testing.T, _ Store, _ App, w *PublishedEventWork, c *PublishedEventRoutingClaim) {
			c.ClaimToken, c.Generation = w.ClaimToken, 0
		}, ErrInvalidArgument},
		{"maintenance", func(t *testing.T, s Store, app App, _ *PublishedEventWork, _ *PublishedEventRoutingClaim) {
			value := true
			if _, err := s.UpdateApp(t.Context(), app.ID, UpdateAppParams{MaintenanceMode: &value, SetMaintenanceMode: true}); err != nil {
				t.Fatal(err)
			}
		}, ErrWorkflowEventTargetUnavailable},
		{"deleted", func(t *testing.T, s Store, app App, _ *PublishedEventWork, _ *PublishedEventRoutingClaim) {
			if _, err := s.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
		}, ErrNotFound},
		{"expired_lease", func(t *testing.T, s Store, _ App, _ *PublishedEventWork, c *PublishedEventRoutingClaim) {
			setWorkflowRecipientLease(t, s, *c, time.Now().UTC().Add(-time.Second))
		}, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				app, work, claim := seedOwnedWorkflowRouting(t, store, 150)
				original := claim
				tc.change(t, store, app, work, &claim)
				if _, err := store.(EventWorkflowRecipientAdmissionStore).AdmitEventWorkflowRecipient(t.Context(), claim); !errors.Is(err, tc.want) {
					t.Fatalf("guard error=%v want=%v", err, tc.want)
				}
				if _, total, err := store.ListWorkflowRuns(t.Context(), app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
					t.Fatalf("guard admitted runs=%d err=%v", total, err)
				}
				assertWorkflowRoutingEvidence(t, store, app, original, "", 0)
			})
		})
	}
}

func setWorkflowRecipientLease(t *testing.T, store Store, claim PublishedEventRoutingClaim, until time.Time) {
	t.Helper()
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		s.routingReceiptLocked(claim.OutboxID).routingRecipients[claim.SubscriptionID].LeaseUntil = until
		s.mu.Unlock()
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), `UPDATE event_fanout_recipients SET lease_until=$3 WHERE outbox_id=$1 AND subscription_id=$2`, claim.OutboxID, claim.SubscriptionID, until); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEventWorkflowRecipientAdmissionFilter(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _, claim := seedOwnedWorkflowRouting(t, store, 50)
		result, err := store.(EventWorkflowRecipientAdmissionStore).AdmitEventWorkflowRecipient(t.Context(), claim)
		if err != nil || result.Matched || result.RunCreated || result.RunID != "" || !result.ReceiptSettled || result.Progress.State != PublishedEventRecipientFiltered {
			t.Fatalf("filtered=%+v err=%v", result, err)
		}
		assertWorkflowRoutingEvidence(t, store, app, claim, PublishedEventRecipientFiltered, 1)
	})
}

func TestEventWorkflowRecipientAdmissionReclaimedLeaseFencesStaleWorker(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _, claim := seedOwnedWorkflowRouting(t, store, 150)
		ctx := t.Context()
		setWorkflowRecipientLease(t, store, claim, time.Now().UTC().Add(-time.Second))
		fresh, err := store.(PublishedEventRecipientWorkStore).ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil || fresh.ClaimToken == claim.ClaimToken || fresh.TotalAttempts != 2 {
			t.Fatalf("reclaim=%+v err=%v", fresh, err)
		}
		admission := store.(EventWorkflowRecipientAdmissionStore)
		if _, err := admission.AdmitEventWorkflowRecipient(ctx, claim); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale worker admitted after reclaim: %v", err)
		}
		assertWorkflowRoutingEvidence(t, store, app, claim, "", 0)
		freshClaim := PublishedEventRoutingClaim{OutboxID: fresh.OutboxID, SubscriptionID: fresh.Recipient.ID, ClaimToken: fresh.ClaimToken, Generation: fresh.Generation}
		result, err := admission.AdmitEventWorkflowRecipient(ctx, freshClaim)
		if err != nil || !result.RunCreated || result.Progress.Attempts != 2 {
			t.Fatalf("fresh admission=%+v err=%v", result, err)
		}
		bad := freshClaim
		bad.Generation++
		if _, err := admission.AdmitEventWorkflowRecipient(ctx, bad); !errors.Is(err, ErrConflict) {
			t.Fatalf("settled generation fencing=%v", err)
		}
		assertWorkflowRoutingEvidence(t, store, app, claim, PublishedEventRecipientEnqueued, 1)
	})
}

func TestEventWorkflowRecipientAdmissionInvalidDefinitionIsTerminal(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _, claim := seedOwnedWorkflowRouting(t, store, 150)
		// Corruption fixture: accepted definitions are immutable in production.
		switch s := store.(type) {
		case *MemStore:
			s.mu.Lock()
			s.routingReceiptLocked(claim.OutboxID).RecipientSnapshot[0].Workflow = json.RawMessage(`{"name":"paid","steps":[]}`)
			s.mu.Unlock()
		case *PgStore:
			if _, err := s.pool.Exec(t.Context(), `UPDATE event_fanout_outbox SET recipient_snapshot=jsonb_set(recipient_snapshot,'{0,workflow}','{"name":"paid","steps":[]}'::jsonb) WHERE id=$1`, claim.OutboxID); err != nil {
				t.Fatal(err)
			}
		}
		result, err := store.(EventWorkflowRecipientAdmissionStore).AdmitEventWorkflowRecipient(t.Context(), claim)
		var classified *EventRecipientAdmissionError
		if !errors.Is(err, ErrWorkflowEventDefinitionInvalid) || !errors.As(err, &classified) || classified.Retryable || classified.FailureCode != EventFanoutFailureCodeInvalidSubscription || result.Matched {
			t.Fatalf("invalid definition=%+v err=%v", result, err)
		}
		assertWorkflowRoutingEvidence(t, store, app, claim, "", 0)
	})
}

func TestEventWorkflowRecipientAdmissionWebhook(t *testing.T) {
	for _, provider := range []InboundWebhookProvider{InboundWebhookProviderStripe, InboundWebhookProviderGeneric} {
		t.Run(string(provider), func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				ctx := t.Context()
				app, _ := seedEventWorkflow(t, store)
				endpoint, err := store.(InboundWebhookStore).CreateInboundWebhookEndpointIfUnderQuota(ctx, InboundWebhookEndpoint{
					AppID: app.ID, AccountID: app.AccountID, Name: "workflow-routing", Provider: provider, DeliveryPath: "/",
					TokenHash: bytes.Repeat([]byte{7}, 32), SigningSecretSealed: []byte("sealed"), Enabled: true,
				}, api.MustLimitsFor(api.PlanHobby))
				if err != nil {
					t.Fatal(err)
				}
				automation := store.(WebhookAutomationStore)
				if _, err := automation.SaveWebhookAutomationBinding(ctx, WebhookAutomationBindingOptions{
					EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, WorkflowName: "paid", EventType: "invoice.*",
				}); err != nil {
					t.Fatal(err)
				}
				body := json.RawMessage(`{"amount":150}`)
				accepted, handled, err := automation.AcceptVerifiedWebhookAutomation(ctx, endpoint, "webhook-isolation", "invoice.paid", body, true)
				if err != nil || !handled {
					t.Fatalf("webhook acceptance=%+v handled=%t err=%v", accepted, handled, err)
				}
				work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				routing := store.(PublishedEventRecipientWorkStore)
				if err := routing.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				claimed, err := routing.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				result, err := store.(EventWorkflowRecipientAdmissionStore).AdmitEventWorkflowRecipient(ctx,
					PublishedEventRoutingClaim{OutboxID: work.ID, SubscriptionID: claimed.Recipient.ID, ClaimToken: claimed.ClaimToken, Generation: claimed.Generation})
				if err != nil || !result.RunCreated || !result.ReceiptSettled {
					t.Fatalf("webhook admission=%+v err=%v", result, err)
				}
				receipt, err := automation.GetWebhookAutomationReceipt(ctx, endpoint.ID, "webhook-isolation")
				if err != nil || receipt.RunID != result.RunID || receipt.RoutingStatus != "enqueued" {
					t.Fatalf("webhook receipt=%+v err=%v", receipt, err)
				}
				duplicate, handled, err := automation.AcceptVerifiedWebhookAutomation(ctx, endpoint, "webhook-isolation", "invoice.paid", body, true)
				if err != nil || !handled || !duplicate.Duplicate {
					t.Fatalf("duplicate=%+v err=%v", duplicate, err)
				}
				if next, err := routing.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC()); !errors.Is(err, ErrNotFound) {
					t.Fatalf("webhook admission repeated=%+v err=%v", next, err)
				}
			})
		})
	}
}

func TestEventWorkflowRecipientAdmissionReconcilesLegacyHandoff(t *testing.T) {
	for _, pruned := range []bool{false, true} {
		name := "retained_run"
		if pruned {
			name = "pruned_run"
		}
		t.Run(name, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				app, _ := seedEventWorkflow(t, store)
				work := publishWorkflowEvent(t, store, app, uuid.NewString())
				ctx := t.Context()
				runID, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				if pruned {
					if err := store.MarkWorkflowRunStatus(ctx, runID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
						t.Fatal(err)
					}
					if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
						t.Fatalf("prune=%d err=%v", n, err)
					}
					runID = ""
				}
				routing := store.(PublishedEventRecipientWorkStore)
				if err := routing.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				claimed, err := routing.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				claim := PublishedEventRoutingClaim{OutboxID: work.ID, SubscriptionID: claimed.Recipient.ID, ClaimToken: claimed.ClaimToken, Generation: claimed.Generation}
				result, err := store.(EventWorkflowRecipientAdmissionStore).AdmitEventWorkflowRecipient(ctx, claim)
				if err != nil || result.RunCreated || result.RunID != runID || !result.ReceiptSettled {
					t.Fatalf("legacy reconciliation=%+v err=%v", result, err)
				}
				assertWorkflowRoutingEvidence(t, store, app, claim, PublishedEventRecipientEnqueued, 1)
			})
		})
	}
}

func TestPgEventWorkflowRecipientAdmissionRollback(t *testing.T) {
	for _, fault := range []string{"receipt", "history", "lease_during_history"} {
		t.Run(fault, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				pg, ok := store.(*PgStore)
				if !ok {
					t.Skip("requires PostgreSQL fault injection")
				}
				app, _, claim := seedOwnedWorkflowRouting(t, store, 150)
				ctx := t.Context()
				var inject, undo string
				switch fault {
				case "receipt":
					inject = `ALTER TABLE workflow_event_receipts ADD CONSTRAINT reject_workflow_receipt CHECK (false) NOT VALID`
					undo = `ALTER TABLE workflow_event_receipts DROP CONSTRAINT reject_workflow_receipt`
				case "history":
					inject = `CREATE FUNCTION reject_workflow_routing_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'history failure'; END $$;
					CREATE TRIGGER reject_workflow_routing_history BEFORE INSERT ON event_fanout_attempt_history FOR EACH ROW EXECUTE FUNCTION reject_workflow_routing_history()`
					undo = `DROP TRIGGER reject_workflow_routing_history ON event_fanout_attempt_history; DROP FUNCTION reject_workflow_routing_history()`
				case "lease_during_history":
					inject = `CREATE FUNCTION expire_workflow_routing_lease() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
					UPDATE event_fanout_recipients SET lease_until=clock_timestamp()-interval '1 second' WHERE outbox_id=NEW.outbox_id AND subscription_id=NEW.subscription_id; RETURN NEW; END $$;
					CREATE TRIGGER expire_workflow_routing_lease BEFORE INSERT ON event_fanout_attempt_history FOR EACH ROW EXECUTE FUNCTION expire_workflow_routing_lease()`
					undo = `DROP TRIGGER expire_workflow_routing_lease ON event_fanout_attempt_history; DROP FUNCTION expire_workflow_routing_lease()`
				}
				if _, err := pg.pool.Exec(ctx, inject); err != nil {
					t.Fatal(err)
				}
				result, err := pg.AdmitEventWorkflowRecipient(ctx, claim)
				if err == nil || !result.Matched {
					t.Fatalf("fault admission=%+v err=%v", result, err)
				}
				if fault == "lease_during_history" && !errors.Is(err, ErrConflict) {
					t.Fatalf("expired lease=%v", err)
				}
				if _, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
					t.Fatalf("partial runs=%d err=%v", total, err)
				}
				var receipts int
				if err := pg.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_event_receipts WHERE outbox_id=$1`, claim.OutboxID).Scan(&receipts); err != nil || receipts != 0 {
					t.Fatalf("partial admission receipts=%d err=%v", receipts, err)
				}
				assertWorkflowRoutingEvidence(t, store, app, claim, "", 0)
				if _, err := pg.pool.Exec(ctx, undo); err != nil {
					t.Fatal(err)
				}
				result, err = pg.AdmitEventWorkflowRecipient(ctx, claim)
				if err != nil || !result.RunCreated || !result.ReceiptSettled {
					t.Fatalf("recovery=%+v err=%v", result, err)
				}
				assertWorkflowRoutingEvidence(t, store, app, claim, PublishedEventRecipientEnqueued, 1)
			})
		})
	}
}

func TestPgEventWorkflowRecipientAdmissionLeaseExpiresWhileWaiting(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		pg, ok := store.(*PgStore)
		if !ok {
			t.Skip("requires PostgreSQL lock contention")
		}
		app, _, claim := seedOwnedWorkflowRouting(t, store, 150)
		ctx := t.Context()
		blocker, err := pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Rollback(ctx) }()
		if _, err := blocker.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
			t.Fatal(err)
		}
		setWorkflowRecipientLease(t, store, claim, time.Now().UTC().Add(200*time.Millisecond))
		done := make(chan error, 1)
		go func() { _, err := pg.AdmitEventWorkflowRecipient(ctx, claim); done <- err }()
		time.Sleep(300 * time.Millisecond)
		if err := blocker.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("expired waiting claim=%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("admission did not unblock")
		}
		if _, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
			t.Fatalf("expired admission created runs=%d err=%v", total, err)
		}
		assertWorkflowRoutingEvidence(t, store, app, claim, "", 0)
	})
}
