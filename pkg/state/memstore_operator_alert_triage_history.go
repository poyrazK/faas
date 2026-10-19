package state

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorAlertTriageHistory(ctx context.Context, f OperatorAlertTriageHistoryFilter) (OperatorAlertTriageHistorySnapshot, error) {
	out := OperatorAlertTriageHistorySnapshot{Items: []api.ObsAlertTriageHistoryEntry{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validateOperatorAlertTriageHistoryFilter(f); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	out.Episode, out.Collection, err = m.operatorAlertEpisodeLocked(f.ID)
	if err != nil {
		return out, err
	}
	out.SnapshotRevision, err = triageHistoryRevision(out.Episode, f)
	if err != nil {
		return out, err
	}
	type candidate struct {
		audit    AuditLog
		revision int64
	}
	candidates := []candidate{}
	seen := map[int64]bool{}
	var oldest, newest *int64
	for _, audit := range m.auditLog {
		if audit.Kind != "operator.action.alert_triage" {
			continue
		}
		var projected struct {
			EpisodeID string `json:"episode_id"`
			Triage    struct {
				Revision json.RawMessage `json:"revision"`
			} `json:"triage"`
		}
		if err := json.Unmarshal(audit.Data, &projected); err != nil {
			if auditLogDataHasKey(audit.Data, "episode_id", out.Episode.ID) {
				return out, errAlertTriageReceipt
			}
			continue
		}
		if projected.EpisodeID != out.Episode.ID {
			continue
		}
		revision, err := strconv.ParseInt(string(projected.Triage.Revision), 10, 64)
		// SQL's safe index expression accepts canonical positive decimal numbers only.
		if err != nil || len(projected.Triage.Revision) > 16 || revision < 1 || strconv.FormatInt(revision, 10) != string(projected.Triage.Revision) {
			return out, errAlertTriageReceipt
		}
		if revision > out.SnapshotRevision {
			continue
		}
		if seen[revision] {
			return out, errAlertTriageReceipt
		}
		seen[revision] = true
		if oldest == nil || revision < *oldest {
			value := revision
			oldest = &value
		}
		if newest == nil || revision > *newest {
			value := revision
			newest = &value
		}
		if f.BeforeRevision != 0 && revision >= f.BeforeRevision {
			continue
		}
		candidates = append(candidates, candidate{audit, revision})
	}
	out.Coverage, err = triageHistoryCoverage(out.SnapshotRevision, int64(len(seen)), int64(len(seen)), oldest, newest)
	if err != nil {
		return out, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].revision > candidates[j].revision })
	if len(candidates) > f.Limit {
		candidates = candidates[:f.Limit]
		out.Truncated = true
	}
	for _, candidate := range candidates {
		entry, err := decodeAlertTriageReceipt(candidate.audit.ID.String(), out.Episode.ID, candidate.audit.ReceivedAt, candidate.audit.Data)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, entry)
	}
	return out, ctx.Err()
}
