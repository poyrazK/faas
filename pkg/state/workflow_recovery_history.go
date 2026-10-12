package state

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowRecoveryHistoryStore interface {
	GetWorkflowRecoveryHistory(context.Context, string, string, api.WorkflowRecoveryHistoryOptions) (api.WorkflowRecoveryHistory, error)
}
type recoveryHistoryCursor struct {
	HistorySequence int64
	HistoryFence    bool
	Scope           string
	ObservedAt      time.Time
	Start           time.Time
	End             time.Time
	At              time.Time
	Key             string
}

func recoveryHistoryWindow(account, app string, o api.WorkflowRecoveryHistoryOptions, now time.Time) (recoveryHistoryCursor, int, error) {
	var c recoveryHistoryCursor
	if err := eventRecoveryIDs(account, app); err != nil {
		return c, 0, err
	}
	if err := o.Validate(); err != nil {
		return c, 0, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	limit := o.Limit
	if limit == 0 {
		limit = api.WorkflowRecoveryHistoryDefaultPage
	}
	filter := o
	filter.After = ""
	filter.Limit = 0
	filter.CreatedAfter = filter.CreatedAfter.UTC()
	filter.CreatedBefore = filter.CreatedBefore.UTC()
	if filter.RunID != "" {
		filter.RunID = canonicalMemUUID(filter.RunID)
	}
	raw, _ := json.Marshal(filter)
	scope := fmt.Sprintf("%x", sha256.Sum256(append([]byte(canonicalMemUUID(account)+":"+canonicalMemUUID(app)+":"), raw...)))
	c = recoveryHistoryCursor{Scope: scope, ObservedAt: now.UTC(), End: now.UTC()}
	if !o.CreatedBefore.IsZero() {
		c.End = o.CreatedBefore.UTC()
	}
	c.Start = c.End.Add(-api.WorkflowAutomationHealthDefaultRange)
	if !o.CreatedAfter.IsZero() {
		c.Start = o.CreatedAfter.UTC()
	}
	if o.After != "" {
		raw, err := base64.RawURLEncoding.DecodeString(o.After)
		if err != nil {
			return c, 0, ErrEventRecoveryQuery
		}
		if json.Unmarshal(raw, &c) != nil || c.Scope != scope || c.HistorySequence < 0 || c.Key == "" || c.At.IsZero() || c.ObservedAt.IsZero() || c.ObservedAt.After(now) || c.At.Before(c.Start) || c.At.After(c.End) {
			return c, 0, ErrEventRecoveryQuery
		}
	}
	if c.Start.After(c.End) || c.End.After(c.ObservedAt) || c.End.Sub(c.Start) > api.WorkflowAutomationHealthMaxRange {
		return c, 0, ErrEventRecoveryQuery
	}
	return c, limit, nil
}
func recoveryHistoryPage(c recoveryHistoryCursor, limit int, entries []api.WorkflowRecoveryHistoryEntry) api.WorkflowRecoveryHistory {
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].OccurredAt.Equal(entries[j].OccurredAt) {
			return entries[i].OccurredAt.After(entries[j].OccurredAt)
		}
		return entries[i].ID > entries[j].ID
	})
	out := api.WorkflowRecoveryHistory{ObservedAt: c.ObservedAt, CreatedAfter: c.Start, CreatedBefore: c.End, Entries: []api.WorkflowRecoveryHistoryEntry{}, RetainedObservations: true}
	if len(entries) > limit {
		last := entries[limit-1]
		c.At = last.OccurredAt
		c.Key = last.ID
		raw, _ := json.Marshal(c)
		out.NextAfter = base64.RawURLEncoding.EncodeToString(raw)
		entries = entries[:limit]
	}
	out.Entries = entries
	return out
}
func recoveryHistoryMatches(e api.WorkflowRecoveryHistoryEntry, o api.WorkflowRecoveryHistoryOptions, c recoveryHistoryCursor) bool {
	return !e.OccurredAt.IsZero() && !e.OccurredAt.Before(c.Start) && !e.OccurredAt.After(c.End) && (c.Key == "" || e.OccurredAt.Before(c.At) || e.OccurredAt.Equal(c.At) && e.ID < c.Key) && (o.Workflow == "" || e.Workflow == o.Workflow) && (o.RunID == "" || sameMemUUID(o.RunID, e.RunID)) && (o.PolicyVersion == 0 || e.PolicyVersion == o.PolicyVersion)
}
