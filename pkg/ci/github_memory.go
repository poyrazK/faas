package ci

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *MemoryStore) removeGitHubAccount(account string) {
	for id, b := range s.githubBindings {
		if sameAccount(b.AccountID, account) {
			delete(s.githubBindings, id)
		}
	}
	for id, t := range s.githubTriggers {
		if sameAccount(t.Value.AccountID, account) {
			delete(s.githubTriggers, id)
		}
	}
	for key, id := range s.githubKeys {
		if _, ok := s.githubTriggers[id]; !ok {
			delete(s.githubKeys, key)
		}
	}
	for id, c := range s.githubChecks {
		if sameAccount(c.Value.AccountID, account) {
			delete(s.githubChecks, id)
		}
	}
}

func (s *MemoryStore) PutGitHubBinding(ctx context.Context, account, name string, req GitHubBindingRequest) (GitHubBinding, error) {
	if err := ctx.Err(); err != nil {
		return GitHubBinding{}, err
	}
	req, err := NormalizeGitHubBinding(req)
	if err != nil || !ValidName(name) {
		return GitHubBinding{}, fmt.Errorf("invalid CI GitHub binding")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.revisions[req.RevisionID]
	if !ok || !sameAccount(r.AccountID, account) {
		return GitHubBinding{}, ErrNotFound
	}
	var row GitHubBinding
	count := 0
	for _, b := range s.githubBindings {
		if sameAccount(b.AccountID, account) {
			count++
			if b.Name == name {
				row = b
			}
		}
	}
	if row.ID == "" {
		if count >= api.CIGitHubMaxBindingsPerAccount {
			return row, &LimitError{Resource: "github_bindings", Limit: api.CIGitHubMaxBindingsPerAccount, Observed: count + 1}
		}
		row = GitHubBinding{ID: uuid.NewString(), AccountID: account, Name: name, CreatedAt: s.executionNow()}
	}
	if row.Enabled && sameGitHubBindingRequest(row.GitHubBindingRequest, req) {
		return clone(row), nil
	}
	row.GitHubBindingRequest, row.Repository, row.Enabled = req, r.Definition.Repository, true
	row.Generation++
	s.githubBindings[row.ID] = clone(row)
	return clone(row), nil
}

func (s *MemoryStore) GitHubBindings(ctx context.Context, account string) ([]GitHubBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]GitHubBinding, 0)
	for _, b := range s.githubBindings {
		if sameAccount(b.AccountID, account) {
			rows = append(rows, clone(b))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func (s *MemoryStore) DisableGitHubBinding(ctx context.Context, account, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for id, b := range s.githubBindings {
		if !sameAccount(b.AccountID, account) || b.Name != name {
			continue
		}
		found = true
		b.Enabled = false
		b.Generation++
		s.githubBindings[id] = b
		for tid, record := range s.githubTriggers {
			if record.Value.Binding.ID == id && (record.Status == "pending" || record.Status == "processing") {
				record.Status, record.Token = "ignored", ""
				s.githubTriggers[tid] = record
			}
		}
		for rid, record := range s.githubChecks {
			if record.Value.Source.BindingID == id && (record.Status == "pending" || record.Status == "sending") {
				record.Status, record.Token = "ignored", ""
				s.githubChecks[rid] = record
			}
		}
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func (s *MemoryStore) RecordGitHubDelivery(ctx context.Context, account, deliveryID string, event GitHubEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidID(deliveryID) || !event.valid() {
		return fmt.Errorf("invalid authenticated CI GitHub event")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, r := range s.githubTriggers {
		if sameAccount(r.Value.AccountID, account) {
			count++
		}
	}
	rows := make([]GitHubTrigger, 0)
	for _, b := range s.githubBindings {
		key := b.ID + "/" + event.Key()
		if !sameAccount(b.AccountID, account) || !bindingMatches(b, event) || s.githubKeys[key] != "" {
			continue
		}
		rows = append(rows, GitHubTrigger{ID: uuid.NewString(), AccountID: b.AccountID, Binding: clone(b), Source: GitHubSource{GitHubEvent: event, BindingID: b.ID, Generation: b.Generation, DeliveryID: deliveryID}})
	}
	if count+len(rows) > api.CIGitHubMaxTriggersPerAccount {
		return &LimitError{Resource: "github_triggers", Limit: api.CIGitHubMaxTriggersPerAccount, Observed: count + len(rows)}
	}
	for _, row := range rows {
		s.githubKeys[row.Binding.ID+"/"+event.Key()] = row.ID
		s.githubTriggers[row.ID] = gitHubWorkRecord[GitHubTrigger]{Value: row, Status: "pending"}
	}
	return nil
}

func (s *MemoryStore) ClaimGitHubTrigger(ctx context.Context, account string) (GitHubTrigger, error) {
	if err := ctx.Err(); err != nil {
		return GitHubTrigger{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, record := range s.githubTriggers {
		if !sameAccount(record.Value.AccountID, account) || !githubWorkDue(record.Status, "processing", record.Until, record.Next, s.executionNow()) {
			continue
		}
		if record.Attempts >= api.CIGitHubMaxAttempts {
			record.Status = "dead"
			s.githubTriggers[id] = record
			continue
		}
		record.Status, record.Token = "processing", uuid.NewString()
		record.Attempts++
		record.Until = s.executionNow().Add(api.CIGitHubLeaseSeconds * time.Second)
		s.githubTriggers[id] = record
		row := clone(record.Value)
		row.LeaseToken, row.Status, row.Attempts = record.Token, record.Status, record.Attempts
		return row, nil
	}
	return GitHubTrigger{}, ErrNoGitHubWork
}

func (s *MemoryStore) AdmitGitHubTrigger(ctx context.Context, claim GitHubTrigger) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.githubTriggers[claim.ID]
	if !ok || !sameAccount(record.Value.AccountID, claim.AccountID) {
		return Run{}, ErrNotFound
	}
	if record.Status == "admitted" {
		return clone(s.runs[record.Value.RunID]), nil
	}
	if record.Status != "processing" || record.Token != claim.LeaseToken || !s.executionNow().Before(record.Until) {
		return Run{}, ErrLeaseLost
	}
	t := record.Value
	if !githubBindingStillAuthorized(s.githubBindings[t.Binding.ID], t.Binding) {
		return Run{}, ErrGitHubIgnored
	}
	r, ok := s.revisions[t.Binding.RevisionID]
	if !ok || !sameAccount(r.AccountID, t.AccountID) {
		return Run{}, ErrNotFound
	}
	count := 0
	for _, run := range s.runs {
		if sameAccount(run.AccountID, t.AccountID) {
			count++
		}
	}
	if count >= api.CIMaxRunsPerAccount {
		return Run{}, &LimitError{Resource: "runs", Limit: api.CIMaxRunsPerAccount, Observed: count + 1}
	}
	plan, _, err := Compile(r.Definition)
	if err != nil {
		return Run{}, err
	}
	run := Run{ID: uuid.NewString(), AccountID: t.AccountID, Pipeline: r.Name, RevisionID: r.ID, CommitSHA: t.Source.CommitSHA, Status: StatusBlocked, BlockedReason: ComputeUnavailable, Plan: plan, CreatedAt: s.executionNow(), GitHub: clone(&t.Source)}
	s.runs[run.ID] = run
	s.events[run.ID] = []Event{{Sequence: 1, Type: StatusBlocked, At: run.CreatedAt}}
	record.Value.RunID, record.Status, record.Token = run.ID, "admitted", ""
	s.githubTriggers[claim.ID] = record
	s.githubChecks[run.ID] = gitHubWorkRecord[GitHubCheck]{Status: "pending", Value: GitHubCheck{AccountID: run.AccountID, RunID: run.ID, Pipeline: run.Pipeline, Source: t.Source}}
	return clone(run), nil
}

func (s *MemoryStore) FailGitHubTrigger(ctx context.Context, claim GitHubTrigger, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.githubTriggers[claim.ID]
	if !ok || !sameAccount(record.Value.AccountID, claim.AccountID) {
		return ErrNotFound
	}
	if record.Token != claim.LeaseToken || record.Status != "processing" || !s.executionNow().Before(record.Until) {
		return ErrLeaseLost
	}
	record.Status, record.Token = githubFailureState(record.Attempts, reason), ""
	record.Next = githubRetry(record.Attempts, s.executionNow())
	s.githubTriggers[claim.ID] = record
	return nil
}

func (s *MemoryStore) ClaimGitHubCheck(ctx context.Context, account string) (GitHubCheck, error) {
	if err := ctx.Err(); err != nil {
		return GitHubCheck{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, record := range s.githubChecks {
		if !sameAccount(record.Value.AccountID, account) || !githubWorkDue(record.Status, "sending", record.Until, record.Next, s.executionNow()) {
			continue
		}
		if record.Attempts >= api.CIGitHubMaxAttempts {
			record.Status = "dead"
			s.githubChecks[id] = record
			continue
		}
		b := s.githubBindings[record.Value.Source.BindingID]
		if !b.Enabled || b.InstallationID != record.Value.Source.InstallationID || b.RepositoryID != record.Value.Source.RepositoryID || !strings.EqualFold(b.Repository, record.Value.Source.Repository) {
			record.Status = "ignored"
			s.githubChecks[id] = record
			continue
		}
		record.Status, record.Token = "sending", uuid.NewString()
		record.Attempts++
		record.Until = s.executionNow().Add(api.CIGitHubLeaseSeconds * time.Second)
		s.githubChecks[id] = record
		row := clone(record.Value)
		row.LeaseToken, row.Attempts = record.Token, record.Attempts
		return row, nil
	}
	return GitHubCheck{}, ErrNoGitHubWork
}

func (s *MemoryStore) FinishGitHubCheck(ctx context.Context, claim GitHubCheck, checkID int64, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.githubChecks[claim.RunID]
	if !ok || !sameAccount(record.Value.AccountID, claim.AccountID) {
		return ErrNotFound
	}
	if record.Token != claim.LeaseToken || record.Status != "sending" || !s.executionNow().Before(record.Until) {
		return ErrLeaseLost
	}
	if reason == "" && checkID <= 0 {
		return fmt.Errorf("CI check publication requires a remote identity")
	}
	record.Status, record.Token = "sent", ""
	if reason != "" {
		record.Status = githubFailureState(record.Attempts, reason)
	} else {
		record.Value.CheckID = checkID
	}
	record.Next = githubRetry(record.Attempts, s.executionNow())
	s.githubChecks[claim.RunID] = record
	return nil
}

var _ GitHubStore = (*MemoryStore)(nil)
