package state

import (
	"context"
	"sort"
	"time"
)

func (m *MemStore) OperatorAlertWorkload(ctx context.Context, f OperatorAlertWorkloadFilter) (OperatorAlertWorkloadSnapshot, error) {
	out := OperatorAlertWorkloadSnapshot{Buckets: []OperatorAlertWorkloadBucket{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if f.Now.IsZero() {
		return out, ErrInvalidArgument
	}
	f.Now = f.Now.UTC().Truncate(time.Microsecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	out.Collection = m.operatorAlertCollection
	out.Retention = m.operatorAlertRetentionStatusLocked(f.Now)
	if out.Collection.LastSuccessAt.IsZero() {
		return out, nil
	}
	type key struct {
		status, severity    string
		ack, owned, snoozed bool
	}
	groups := map[key]OperatorAlertWorkloadBucket{}
	for _, e := range m.operatorAlertEpisodes {
		status := e.Status(out.Collection, f.Now)
		if !f.CollectionEnabled && status != "resolved" {
			status = "unknown"
		}
		k := key{status, e.Severity, e.Triage.Acknowledged, e.Triage.OwnerID != "", e.Triage.SnoozedUntil != nil && e.Triage.SnoozedUntil.After(f.Now)}
		b := groups[k]
		b.Status, b.Severity, b.Acknowledged, b.Owned, b.Snoozed = k.status, k.severity, k.ack, k.owned, k.snoozed
		b.Count++
		if b.OldestID == "" || e.StartedAt.Before(b.OldestStartedAt) || e.StartedAt.Equal(b.OldestStartedAt) && e.ID < b.OldestID {
			b.OldestID, b.OldestStartedAt = e.ID, e.StartedAt
		}
		groups[k] = b
	}
	for _, b := range groups {
		out.Buckets = append(out.Buckets, b)
	}
	sort.Slice(out.Buckets, func(i, j int) bool {
		a, b := out.Buckets[i], out.Buckets[j]
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Acknowledged != b.Acknowledged {
			return !a.Acknowledged
		}
		if a.Owned != b.Owned {
			return !a.Owned
		}
		return !a.Snoozed && b.Snoozed
	})
	return out, ctx.Err()
}
