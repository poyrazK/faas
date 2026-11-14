package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var ErrToolPublicationLimit = errors.New("private publication journal limit reached")

// ToolPublicationPage is the storage projection before search removes catalog bodies.
type ToolPublicationPage struct {
	Publications []api.ToolPublicationRecord
	NextCursor   string
}

// ToolPublicationStore is an apid-owned evidence journal. A duplicate begin
// returns created=false, including abandoned attempts; it never authorizes work.
type ToolPublicationStore interface {
	BeginToolPublication(context.Context, string, string, api.ToolPublicationIntent) (api.BeginToolPublicationResponse, error)
	GetToolPublication(context.Context, string, string) (api.ToolPublicationRecord, error)
	FinishToolPublication(context.Context, string, string, api.ToolPublicationOutcome, string) (api.ToolPublicationRecord, error)
	SearchToolRegistry(context.Context, string, string, string, int) (ToolPublicationPage, error)
}

func newToolPublication(account, id string, in api.ToolPublicationIntent) (api.ToolPublicationRecord, error) {
	if account == "" || !toolpublication.ValidID(id) || toolpublication.ValidateIntent(in) != nil {
		return api.ToolPublicationRecord{}, ErrInvalidArgument
	}
	now := time.Now().UTC()
	return api.ToolPublicationRecord{ID: id, AccountID: account, Intent: in, Status: "running", CreatedAt: now, Deadline: now.Add(api.ToolPublicationDeadline), EvidenceOrigin: "publisher_attested"}, nil
}

func completeToolPublication(record api.ToolPublicationRecord, out api.ToolPublicationOutcome, source string) (api.ToolPublicationRecord, error) {
	if toolpublication.ValidateOutcome(record.Intent, out) != nil || (out.Status == "completed" && !toolpublication.ValidID(source)) {
		return record, ErrInvalidArgument
	}
	if record.Status != "running" {
		if record.Outcome != nil && reflect.DeepEqual(*record.Outcome, out) && record.DeploymentSourceSHA256 == source {
			return record, nil
		}
		return record, ErrConflict
	}
	now := time.Now().UTC()
	if !now.Before(record.Deadline) {
		return record, ErrConflict
	}
	record.Status, record.Outcome, record.DeploymentSourceSHA256, record.FinishedAt = out.Status, &out, source, &now
	return record, nil
}

func cloneToolPublication(in api.ToolPublicationRecord) api.ToolPublicationRecord {
	data, _ := json.Marshal(in)
	var out api.ToolPublicationRecord
	_ = json.Unmarshal(data, &out)
	return out
}

func (m *MemStore) BeginToolPublication(_ context.Context, account, id string, in api.ToolPublicationIntent) (api.BeginToolPublicationResponse, error) {
	record, err := newToolPublication(account, id, in)
	if err != nil {
		return api.BeginToolPublicationResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[account]; !ok {
		return api.BeginToolPublicationResponse{}, ErrNotFound
	}
	if m.toolPublications == nil {
		m.toolPublications = map[string]api.ToolPublicationRecord{}
	}
	key := account + ":" + id
	if old, ok := m.toolPublications[key]; ok {
		if !reflect.DeepEqual(old.Intent, in) {
			return api.BeginToolPublicationResponse{}, ErrConflict
		}
		return api.BeginToolPublicationResponse{Record: toolpublication.View(cloneToolPublication(old), time.Now())}, nil
	}
	count := 0
	for _, old := range m.toolPublications {
		if old.AccountID == account {
			count++
		}
	}
	if count >= api.ToolPublicationsPerAccount {
		return api.BeginToolPublicationResponse{}, ErrToolPublicationLimit
	}
	m.toolPublications[key] = cloneToolPublication(record)
	return api.BeginToolPublicationResponse{Created: true, Record: record}, nil
}

func (m *MemStore) GetToolPublication(_ context.Context, account, id string) (api.ToolPublicationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.toolPublications[account+":"+id]
	if !ok {
		return record, ErrNotFound
	}
	return toolpublication.View(cloneToolPublication(record), time.Now()), nil
}

func (m *MemStore) FinishToolPublication(_ context.Context, account, id string, out api.ToolPublicationOutcome, source string) (api.ToolPublicationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + ":" + id
	record, ok := m.toolPublications[key]
	if !ok {
		return record, ErrNotFound
	}
	record, err := completeToolPublication(record, out, source)
	if err != nil {
		return record, err
	}
	m.toolPublications[key] = cloneToolPublication(record)
	return record, nil
}

func (m *MemStore) SearchToolRegistry(_ context.Context, account, query, cursor string, limit int) (ToolPublicationPage, error) {
	if !validToolRegistryQuery(query, cursor, limit) {
		return ToolPublicationPage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := ToolPublicationPage{Publications: []api.ToolPublicationRecord{}}
	for _, record := range m.toolPublications {
		if record.AccountID == account && record.Status == "completed" && record.ID > cursor && toolpublication.Matches(record, query) {
			out.Publications = append(out.Publications, cloneToolPublication(record))
		}
	}
	sort.Slice(out.Publications, func(i, j int) bool { return out.Publications[i].ID < out.Publications[j].ID })
	if len(out.Publications) > limit {
		out.Publications = out.Publications[:limit]
		out.NextCursor = out.Publications[limit-1].ID
	}
	return out, nil
}

func validToolRegistryQuery(query, cursor string, limit int) bool {
	return len(query) <= api.ToolRegistryQueryMaxBytes && (cursor == "" || toolpublication.ValidID(cursor)) && limit > 0 && limit <= api.ToolRegistryPageMax
}
