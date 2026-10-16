package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// MemoryStore mirrors the durable control-plane transitions for portable tests.
type MemoryStore struct {
	mu             sync.Mutex
	revisions      map[string]Revision
	runs           map[string]Run
	keys           map[string]string
	events         map[string][]Event
	executions     map[string]executionRecord
	executionKeys  map[string]string
	executionClock func() time.Time // nil uses the real clock; overridden only by qualification tests.
	githubBindings map[string]GitHubBinding
	githubTriggers map[string]gitHubWorkRecord[GitHubTrigger]
	githubKeys     map[string]string
	githubChecks   map[string]gitHubWorkRecord[GitHubCheck]
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{revisions: make(map[string]Revision), runs: make(map[string]Run), keys: make(map[string]string), events: make(map[string][]Event), executions: make(map[string]executionRecord), executionKeys: make(map[string]string), githubBindings: make(map[string]GitHubBinding), githubTriggers: make(map[string]gitHubWorkRecord[GitHubTrigger]), githubKeys: make(map[string]string), githubChecks: make(map[string]gitHubWorkRecord[GitHubCheck])}
}

// RemoveAccount mirrors the database's account-owned cascading deletion.
func (s *MemoryStore) RemoveAccount(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, row := range s.revisions {
		if row.AccountID == accountID {
			delete(s.revisions, id)
		}
	}
	for id, row := range s.runs {
		if row.AccountID == accountID {
			delete(s.runs, id)
			delete(s.events, id)
		}
	}
	for key := range s.keys {
		if strings.HasPrefix(key, accountID+"/") {
			delete(s.keys, key)
		}
	}
	for id, row := range s.executions {
		if row.Execution.AccountID == accountID {
			delete(s.executions, id)
		}
	}
	for key := range s.executionKeys {
		if strings.HasPrefix(key, accountID+"/") {
			delete(s.executionKeys, key)
		}
	}
	s.removeGitHubAccount(accountID)
}

func clone[T any](value T) T {
	data, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}

func (s *MemoryStore) PutRevision(ctx context.Context, accountID, name string, def Definition) (Revision, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	if !ValidName(name) {
		return Revision{}, fmt.Errorf("invalid CI pipeline name")
	}
	plan, canonical, err := Compile(def)
	if err != nil {
		return Revision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, revision := range s.revisions {
		if revision.AccountID != accountID {
			continue
		}
		if revision.Name == name && revision.Digest == plan.DefinitionDigest {
			return clone(revision), nil
		}
		count++
	}
	if count >= api.CIMaxRevisionsPerAccount {
		return Revision{}, &LimitError{Resource: "revisions", Limit: api.CIMaxRevisionsPerAccount, Observed: count + 1}
	}
	var normalized Definition
	if err := json.Unmarshal(canonical, &normalized); err != nil {
		return Revision{}, fmt.Errorf("decode normalized CI definition: %w", err)
	}
	revision := Revision{ID: uuid.NewString(), AccountID: accountID, Name: name, Digest: plan.DefinitionDigest, Definition: normalized, CreatedAt: time.Now().UTC()}
	s.revisions[revision.ID] = revision
	return clone(revision), nil
}

func (s *MemoryStore) ListRevisions(ctx context.Context, accountID, name string) ([]Revision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]Revision, 0)
	for _, revision := range s.revisions {
		if revision.AccountID == accountID && (name == "" || revision.Name == name) {
			rows = append(rows, clone(revision))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (s *MemoryStore) Revision(ctx context.Context, accountID, id string) (Revision, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.revisions[id]
	if !ok || row.AccountID != accountID {
		return Revision{}, ErrNotFound
	}
	return clone(row), nil
}

func validateSubmission(key string, req SubmitRequest) error {
	if !ValidID(req.RevisionID) || !ValidCommitSHA(req.CommitSHA) {
		return fmt.Errorf("CI submission requires a revision UUID and a full lowercase commit SHA")
	}
	if !ValidIdempotencyKey(key) {
		return fmt.Errorf("CI requires an Idempotency-Key of 1-%d printable ASCII bytes", api.CIIdempotencyKeyMaxBytes)
	}
	return nil
}

func ValidIdempotencyKey(key string) bool {
	return key != "" && len(key) <= api.CIIdempotencyKeyMaxBytes && strings.IndexFunc(key, func(r rune) bool { return r > unicode.MaxASCII || r < '!' || r > '~' }) < 0
}

func (s *MemoryStore) Submit(ctx context.Context, accountID, key string, req SubmitRequest) (Run, bool, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, false, err
	}
	if err := validateSubmission(key, req); err != nil {
		return Run{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.keys[accountID+"/"+key]; ok {
		row := s.runs[id]
		if row.RevisionID != req.RevisionID || row.CommitSHA != req.CommitSHA {
			return Run{}, false, ErrConflict
		}
		return clone(row), true, nil
	}
	revision, ok := s.revisions[req.RevisionID]
	if !ok || revision.AccountID != accountID {
		return Run{}, false, ErrNotFound
	}
	count := 0
	for _, row := range s.runs {
		if row.AccountID == accountID {
			count++
		}
	}
	if count >= api.CIMaxRunsPerAccount {
		return Run{}, false, &LimitError{Resource: "runs", Limit: api.CIMaxRunsPerAccount, Observed: count + 1}
	}
	plan, _, err := Compile(revision.Definition)
	if err != nil {
		return Run{}, false, err
	}
	row := Run{ID: uuid.NewString(), AccountID: accountID, Pipeline: revision.Name, RevisionID: revision.ID, CommitSHA: req.CommitSHA, Status: StatusBlocked, BlockedReason: ComputeUnavailable, Plan: plan, CreatedAt: time.Now().UTC()}
	s.runs[row.ID] = row
	s.keys[accountID+"/"+key] = row.ID
	s.events[row.ID] = []Event{{Sequence: 1, Type: StatusBlocked, At: row.CreatedAt}}
	return clone(row), false, nil
}

func (s *MemoryStore) ListRuns(ctx context.Context, accountID string) ([]Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]Run, 0)
	for _, row := range s.runs {
		if row.AccountID == accountID {
			rows = append(rows, clone(row))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (s *MemoryStore) Run(ctx context.Context, accountID, id string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.runs[id]
	if !ok || row.AccountID != accountID {
		return Run{}, ErrNotFound
	}
	return clone(row), nil
}

func (s *MemoryStore) Cancel(ctx context.Context, accountID, id string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.runs[id]
	if !ok || row.AccountID != accountID {
		return Run{}, ErrNotFound
	}
	if row.Status == StatusCancelled {
		return clone(row), nil
	}
	now := time.Now().UTC()
	row.Status, row.BlockedReason, row.FinishedAt = StatusCancelled, "", &now
	s.runs[id] = row
	s.events[id] = append(s.events[id], Event{Sequence: 2, Type: StatusCancelled, At: now})
	return clone(row), nil
}

func (s *MemoryStore) Events(ctx context.Context, accountID, id string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.runs[id]; !ok || row.AccountID != accountID {
		return nil, ErrNotFound
	}
	return clone(s.events[id]), nil
}
