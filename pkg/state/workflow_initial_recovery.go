package state

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowInitialRecoveryStore interface {
	ListWorkflowInitialFailures(context.Context, string, string, api.WorkflowInitialFailureOptions) (api.WorkflowInitialFailures, error)
	PreviewWorkflowInitialRecovery(context.Context, string, string, string) (api.WorkflowInitialRecoveryPreview, error)
}
type initialFailureCursor struct {
	Scope      string
	ObservedAt time.Time
	At         time.Time
	ID         string
}

func initialFailureWindow(account, app string, o api.WorkflowInitialFailureOptions, now time.Time) (initialFailureCursor, int, error) {
	var c initialFailureCursor
	if err := eventRecoveryIDs(account, app); err != nil {
		return c, 0, err
	}
	if err := o.Validate(); err != nil {
		return c, 0, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	scope := fmt.Sprintf("%x", sha256.Sum256([]byte(canonicalMemUUID(account)+":"+canonicalMemUUID(app)+":"+o.Workflow)))
	c = initialFailureCursor{Scope: scope, ObservedAt: now}
	if o.After != "" {
		raw, err := base64.RawURLEncoding.DecodeString(o.After)
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Scope != scope || c.ObservedAt.IsZero() || c.ObservedAt.After(now) || !c.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureRetention)) || c.At.IsZero() || c.At.After(c.ObservedAt) || !c.At.After(now.Add(-api.WorkflowRecoveryFailureRetention)) {
			return c, 0, ErrEventRecoveryQuery
		}
		id, err := uuid.Parse(c.ID)
		if err != nil || id.String() != c.ID {
			return c, 0, ErrEventRecoveryQuery
		}
	}
	limit := o.Limit
	if limit == 0 {
		limit = api.WorkflowRecoveryHistoryDefaultPage
	}
	return c, limit, nil
}
func initialFailureAPI(e workflowFailureEvidence) api.WorkflowInitialFailure {
	return api.WorkflowInitialFailure{ID: e.ID, AppID: e.AppID, RunID: e.RunID, Workflow: e.Workflow, FailureStatus: e.FailureStatus, ObservedAt: e.ObservedAt, CompletedAt: cloneEventReceiptTime(e.CompletedAt)}
}
func initialFailurePage(c initialFailureCursor, limit int, items []api.WorkflowInitialFailure) api.WorkflowInitialFailures {
	out := api.WorkflowInitialFailures{ObservedAt: c.ObservedAt, Coverage: "partial", Items: items}
	if len(items) > limit {
		last := items[limit-1]
		c.At, c.ID = last.ObservedAt, last.ID
		raw, _ := json.Marshal(c)
		out.NextAfter = base64.RawURLEncoding.EncodeToString(raw)
		out.Items = items[:limit]
	}
	return out
}
func initialRecoveryPreview(e workflowFailureEvidence, observation *api.WorkflowRecoveryFailureEligibility) api.WorkflowInitialRecoveryPreview {
	out := api.WorkflowInitialRecoveryPreview{Failure: initialFailureAPI(e), ObservedAt: observation.ObservedAt, Reason: observation.Reason, Blockers: observation.Blockers}
	if observation.Reason == "eligible" {
		out.Target = &api.WorkflowRunRecoveryTarget{RunID: e.RunID, ExpectedResumeCount: 0, FailureEvidenceID: e.ID}
	}
	return out
}
func (m *MemStore) initialFailureMatchesLocked(app, workflow string, t api.WorkflowRunRecoveryTarget, status string, now time.Time) bool {
	if t.ExpectedResumeCount != 0 {
		return false
	}
	for _, e := range m.workflowFailureEvidence[canonicalMemUUID(app)] {
		if e.ID == t.FailureEvidenceID && e.ResumeCount == 0 && sameMemUUID(e.RunID, t.RunID) && e.Workflow == workflow && e.FailureStatus == status && !e.ObservedAt.After(now) && e.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureRetention)) {
			return true
		}
	}
	return false
}
func (m *MemStore) ListWorkflowInitialFailures(ctx context.Context, account, app string, o api.WorkflowInitialFailureOptions) (api.WorkflowInitialFailures, error) {
	now := time.Now().UTC()
	c, limit, err := initialFailureWindow(account, app, o, now)
	if err != nil {
		return api.WorkflowInitialFailures{}, err
	}
	if err = ctx.Err(); err != nil {
		return api.WorkflowInitialFailures{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventRecoveryAppLocked(account, app) {
		return api.WorkflowInitialFailures{}, ErrNotFound
	}
	items := []api.WorkflowInitialFailure{}
	for _, e := range m.workflowFailureEvidence[canonicalMemUUID(app)] {
		if e.ResumeCount != 0 || !e.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureRetention)) || e.ObservedAt.After(c.ObservedAt) || o.Workflow != "" && e.Workflow != o.Workflow || c.ID != "" && (e.ObservedAt.After(c.At) || e.ObservedAt.Equal(c.At) && e.ID >= c.ID) {
			continue
		}
		items = append(items, initialFailureAPI(e))
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].ObservedAt.Equal(items[j].ObservedAt) {
			return items[i].ObservedAt.After(items[j].ObservedAt)
		}
		return items[i].ID > items[j].ID
	})
	if len(items) > limit+1 {
		items = items[:limit+1]
	}
	return initialFailurePage(c, limit, items), nil
}
func (m *MemStore) PreviewWorkflowInitialRecovery(ctx context.Context, account, app, id string) (api.WorkflowInitialRecoveryPreview, error) {
	var out api.WorkflowInitialRecoveryPreview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	return m.previewWorkflowInitialRecoveryLocked(ctx, account, app, id, now)
}
func (m *MemStore) previewWorkflowInitialRecoveryLocked(ctx context.Context, account, app, id string, now time.Time) (api.WorkflowInitialRecoveryPreview, error) {
	var out api.WorkflowInitialRecoveryPreview
	for _, e := range m.workflowFailureEvidence[canonicalMemUUID(app)] {
		if !sameMemUUID(e.ID, id) || e.ResumeCount != 0 || !e.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureRetention)) || e.ObservedAt.After(now) {
			continue
		}
		d, err := m.workflowRunDiagnosticsLocked(ctx, WorkflowDiagnosticsOptions{RunID: e.RunID, AppID: app, AccountID: account})
		_, owned := m.operationForWorkflowLocked(e.RunID)
		observation, err := failureEligibilityDiagnostic(e, d, err, owned, now)
		if err != nil {
			return out, err
		}
		return initialRecoveryPreview(e, observation), nil
	}
	return out, ErrNotFound
}
