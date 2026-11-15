package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func guardedCronOperation(p *releasePlan) bool {
	return p != nil && p.Operation != nil && (p.Operation.Kind == "cron_update_guarded" || p.Operation.Kind == "cron_delete_guarded")
}

func newCronReceiptProof() (string, error) {
	var proof [32]byte
	if _, err := rand.Read(proof[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(proof[:]), nil
}

func cronMutationRequestID(p *releasePlan) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-mcp-cron-mutation:"+p.ID)).String()
}

func validCronReceipt(p *releasePlan, r api.CronMutationReceipt) bool {
	if !guardedCronOperation(p) || r.SchemaVersion != 1 || r.RequestID != cronMutationRequestID(p) || r.AppID != p.AppID || r.CronID != p.Operation.ResourceID || r.Delete != (p.Operation.Kind == "cron_delete_guarded") || !r.ExpiresAt.Equal(p.ExpiresAt) || r.AcceptedAt.IsZero() || r.UpdatedAt.Before(r.AcceptedAt) || !r.RetainedUntil.After(r.AcceptedAt) || r.Cancelled < 0 || r.DetachedInvocations < 0 || r.DetachedTasks < 0 {
		return false
	}
	switch r.Status {
	case "completed":
		return r.Phase == "complete"
	case "accepted", "partial", "conflict":
		return r.Phase == "cancel_pending" || r.Phase == "detach_invocations" || r.Phase == "detach_tasks" || r.Phase == "delete"
	}
	return false
}

func (m *management) applyReviewedCron(ctx context.Context, p *releasePlan) (any, error) {
	if !api.ValidEnvBatchReceiptProof(p.Operation.CronReceiptProof) {
		return nil, invalid("invalid_plan", "original cron recovery proof is unavailable")
	}
	var changes api.UpdateCronRequest
	if err := json.Unmarshal(p.Operation.Request, &changes); err != nil {
		return nil, err
	}
	request := api.CronMutationRequest{RequestID: cronMutationRequestID(p), AppID: p.AppID, CronID: p.Operation.ResourceID, ExpectedState: p.Operation.ResourceHash,
		ReceiptProof: p.Operation.CronReceiptProof, ExpiresAt: p.ExpiresAt, Delete: p.Operation.Kind == "cron_delete_guarded", Schedule: changes.Schedule, Path: changes.Path, Timezone: changes.Timezone, Enabled: changes.Enabled}
	// Native attempt receipts, rather than an HTTP response cache, fence each
	// independently committed deletion batch. A retry advances the same intent.
	ctx = api.ContextWithIdempotencyKey(ctx, "")
	out, err := m.client.ApplyCronMutation(ctx, p.App, request)
	if err != nil {
		return nil, redactApprovalError(err, p.Operation.CronReceiptProof)
	}
	if !validCronReceipt(p, out) {
		return nil, invalid("invalid_response", "cron mutation receipt differs from the reviewed attempt")
	}
	return out, nil
}

func cronReceiptRecovery(m *management, p *releasePlan) Recovery {
	if p.failure != nil && recoveryForError(p.failure.Problem).Action == "request_access" {
		return Recovery{Action: "request_access"}
	}
	if p.Operation.CronReceipt != nil && p.Operation.CronReceipt.Status == "conflict" {
		return Recovery{Action: "inspect"}
	}
	r := m.recoveryTool(Recovery{Action: "retry_same_plan"}, "gregale_apply_operation", map[string]any{"plan_id": p.ID})
	seconds := int64(1)
	r.AfterSeconds = &seconds
	return r
}
