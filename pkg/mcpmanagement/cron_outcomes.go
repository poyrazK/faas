package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const cronOutcomeNamespace = "cron-mutation-outcome-lookups/"

type cronOutcomeLookup struct {
	RequestID, ReceiptProof, Binding string
	ExpiresAt                        time.Time
}
type cronOutcomeSnapshot struct {
	Version int
	Items   map[string]cronOutcomeLookup
}

func guardedCronAction(action string) bool {
	return action == "operation_cron_update_guarded" || action == "operation_cron_delete_guarded"
}
func validCronLookup(ref cronOutcomeLookup) bool {
	id, err := uuid.Parse(ref.RequestID)
	return err == nil && id != uuid.Nil && id.String() == ref.RequestID && validOutcomeHash(ref.ReceiptProof) && validOutcomeHash(ref.Binding) && !ref.ExpiresAt.IsZero()
}
func (m *management) restoreCronOutcomeLookups() error {
	var saved cronOutcomeSnapshot
	if err := m.opts.planStore.read(cronOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid cron recovery snapshot")
	}
	m.cronOutcomes = map[string]cronOutcomeLookup{}
	for id, ref := range saved.Items {
		if !validActionID(id) || !validCronLookup(ref) {
			return errors.New("invalid cron recovery reference")
		}
		if entry, ok := m.actions[id]; ok && (!guardedCronAction(entry.Action) || ref.Binding != actionOutcomeBinding(entry) || !ref.ExpiresAt.Equal(entry.PlanExpiresAt)) {
			return errors.New("cron recovery binding mismatch")
		}
		m.cronOutcomes[id] = ref
	}
	return nil
}
func (m *management) syncCronOutcomeLookups(now time.Time) error {
	if m.cronOutcomes == nil {
		m.cronOutcomes = map[string]cronOutcomeLookup{}
	}
	for id, p := range m.plans {
		entry, ok := m.actions[id]
		if !ok || !guardedCronOperation(p) || !p.attempted || !now.Before(entry.RetainedUntil) {
			continue
		}
		ref := cronOutcomeLookup{RequestID: cronMutationRequestID(p), ReceiptProof: p.Operation.CronReceiptProof, Binding: actionOutcomeBinding(entry), ExpiresAt: p.ExpiresAt.UTC()}
		if !validCronLookup(ref) {
			return invalid("invalid_plan", "invalid cron recovery reference")
		}
		if old, ok := m.cronOutcomes[id]; ok && old != ref {
			return invalid("invalid_plan", "cron recovery reference changed")
		}
		m.cronOutcomes[id] = ref
	}
	for id, ref := range m.cronOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.cronOutcomes, id)
			continue
		}
		if !guardedCronAction(entry.Action) || ref.Binding != actionOutcomeBinding(entry) || !ref.ExpiresAt.Equal(entry.PlanExpiresAt) {
			return invalid("invalid_plan", "cron recovery binding changed")
		}
	}
	return nil
}
func (m *management) readCronOutcome(ctx context.Context, entry actionRecord, ref cronOutcomeLookup, out *actionOutcome) {
	receipt, err := m.client.GetCronMutation(ctx, entry.App, ref.RequestID, ref.ReceiptProof)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	if entry.Cron == nil {
		out.Reason = "receipt_unverified"
		return
	}
	kind := "cron_update_guarded"
	if entry.Action == "operation_cron_delete_guarded" {
		kind = "cron_delete_guarded"
	}
	target := &releasePlan{ID: entry.PlanID, AppID: entry.AppID, ExpiresAt: ref.ExpiresAt, Operation: &operationPlan{Kind: kind, ResourceID: entry.Cron.ID}}
	if !validCronReceipt(target, receipt) || receipt.RequestID != ref.RequestID {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome, out.CronMutation = "verified_receipt", &receipt
	out.NativeCompletion = receipt.Status
	out.Instruction = "This receipt reports the original cron mutation and retained cleanup progress. It never resumes work, renews write authority or proves already running work has finished. Partial or conflicting deletion requires inspection before a new review."
}
