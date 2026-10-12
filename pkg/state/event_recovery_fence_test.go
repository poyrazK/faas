package state

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEventRecoveryFence(t *testing.T) {
	for _, modern := range []bool{false, true} {
		name := "legacy"
		if modern {
			name = "recipient"
		}
		t.Run(name, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				ctx := context.Background()
				app, _ := seedEventWorkflow(t, store)
				author := store.(AutomationStore)
				draft := mutateForTest(t, author, app.ID, "paid", "save", 0, automationDraft("paid", "/paid", `{"type":"event","source":"billing.*","event_type":"invoice.paid"}`), false)
				published := mutateForTest(t, author, app.ID, "paid", "publish", draft.Version, nil, true)
				work := publishWorkflowEvent(t, store, app, "fenced-recovery")
				if len(work.RecipientSnapshot) != 1 {
					t.Fatalf("snapshot: %+v", work.RecipientSnapshot)
				}
				recipient := work.RecipientSnapshot[0]
				failure := PublishedEventRecipientProgress{State: PublishedEventRecipientFailed, Attempts: 1, UpdatedAt: time.Now().UTC()}
				expectation := api.EventRoutingRecoveryExpectation{WorkflowName: "paid", PublishedVersion: published.PublishedVersion}
				zero := int64(0)
				if modern {
					routing := store.(PublishedEventRecipientWorkStore)
					if err := routing.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					claimed, err := routing.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
					if err != nil {
						t.Fatal(err)
					}
					generation := claimed.Generation
					expectation.Generation = &generation
					if err := routing.FinishPublishedEventRecipient(ctx, claimed, failure, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := store.(PublishedEventRecipientProgressStore).RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, recipient.ID, failure); err != nil {
						t.Fatal(err)
					}
					if err := store.(PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
						t.Fatal(err)
					}
				}

				replay := store.(EventRoutingRecoveryStore)
				invoke := func(e api.EventRoutingRecoveryExpectation) error {
					return replay.ReplayFailedWorkflowEventRecipient(ctx, app.AccountID, app.ID, "billing.stripe", "fenced-recovery", recipient.ID, e)
				}
				wrong := expectation
				wrong.PublishedVersion++
				if err := invoke(wrong); !errors.Is(err, ErrEventRecoveryChanged) {
					t.Fatalf("stale revision=%v", err)
				}
				wrong = expectation
				wrong.ReplayCount = 1
				if err := invoke(wrong); !errors.Is(err, ErrEventRecoveryChanged) {
					t.Fatalf("stale count=%v", err)
				}
				wrong = expectation
				if modern {
					one := *expectation.Generation + 1
					wrong.Generation = &one
				} else {
					wrong.Generation = &zero
				}
				if err := invoke(wrong); !errors.Is(err, ErrEventRecoveryChanged) {
					t.Fatalf("stale generation=%v", err)
				}
				// Publishing between review and mutation invalidates the original plan.
				newer := mutateForTest(t, author, app.ID, "paid", "publish", published.Version, nil, false)
				if err := invoke(expectation); !errors.Is(err, ErrEventRecoveryChanged) {
					t.Fatalf("publication race=%v", err)
				}
				expectation.PublishedVersion = newer.PublishedVersion
				var successes atomic.Int32
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						err := invoke(expectation)
						if err == nil {
							successes.Add(1)
						} else if !errors.Is(err, ErrEventRecoveryChanged) {
							t.Errorf("concurrent replay: %v", err)
						}
					}()
				}
				wg.Wait()
				if successes.Load() != 1 {
					t.Fatalf("accepted=%d", successes.Load())
				}
				receipt, err := store.(EventReceiptStore).EventReceipt(ctx, app.AccountID, "billing.stripe", "fenced-recovery", EventReceiptCursor{}, 100)
				if err != nil {
					t.Fatal(err)
				}
				if len(receipt.Recipients) != 1 || receipt.Recipients[0].Routing.ReplayCount != 1 || receipt.Recipients[0].Routing.State != "pending" {
					t.Fatalf("unexpected replay result: %+v", receipt.Recipients)
				}
			})
		})
	}
}
