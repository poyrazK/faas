package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type environmentGitOpsMemory struct {
	source         EnvironmentGitSource
	revisions      map[string]EnvironmentDesiredRevision
	runs           map[string]EnvironmentGitOpsRun
	runTokens      map[string]string
	next           time.Time
	lease          *EnvironmentGitOpsLease
	attempts       int
	resources      map[string]string
	queues         map[string]string
	owners         map[string]environmentsync.Ownership
	overrides      map[string]environmentsync.Override
	effects        map[string]EnvironmentGitOpsEffect
	runtime        map[string]EnvironmentGitOpsRuntimeEffect
	graphs         map[string]EnvironmentWorkloadGraph
	qualifications map[string]EnvironmentWorkloadQualificationRequest
	poll           environmentGitSourcePoll
	approvals      map[string]EnvironmentGitRevisionApproval
}

var _ EnvironmentGitOpsStore = (*MemStore)(nil)

func cloneEnvironmentRevision(r EnvironmentDesiredRevision) EnvironmentDesiredRevision {
	r.Definition = append(json.RawMessage(nil), r.Definition...)
	return r
}

func cloneEnvironmentRun(r EnvironmentGitOpsRun) EnvironmentGitOpsRun {
	r.Plan = append(json.RawMessage(nil), r.Plan...)
	r.Steps = append(json.RawMessage(nil), r.Steps...)
	if r.CompletedAt != nil {
		completed := *r.CompletedAt
		r.CompletedAt = &completed
	}
	return r
}

func (m *MemStore) CreateEnvironmentGitSource(_ context.Context, accountID, projectID, environment string, spec EnvironmentGitSourceSpec) (EnvironmentGitSource, error) {
	if spec.Validate() != nil {
		return EnvironmentGitSource{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID || project.RepoFullName != spec.Repository || project.InstallID != spec.InstallationID {
		return EnvironmentGitSource{}, ErrNotFound
	}
	env, err := m.projectEnvironmentBySlugLocked(projectID, environment)
	if err != nil {
		return EnvironmentGitSource{}, err
	}
	for _, memory := range m.environmentGitOps {
		if memory.source.EnvironmentID == env.ID {
			return EnvironmentGitSource{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	source := EnvironmentGitSource{ID: newID(), AccountID: accountID, ProjectID: projectID, EnvironmentID: env.ID, EnvironmentSlug: env.Slug, Spec: spec, CreatedAt: now, UpdatedAt: now}
	if m.environmentGitOps == nil {
		m.environmentGitOps = make(map[string]*environmentGitOpsMemory)
	}
	m.environmentGitOps[source.ID] = &environmentGitOpsMemory{source: source, revisions: map[string]EnvironmentDesiredRevision{}, runs: map[string]EnvironmentGitOpsRun{}, runTokens: map[string]string{}}
	return source, nil
}

func (m *MemStore) EnvironmentGitSource(_ context.Context, accountID, projectID, environment string) (EnvironmentGitSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return EnvironmentGitSource{}, ErrNotFound
	}
	for _, memory := range m.environmentGitOps {
		if memory.source.AccountID == accountID && memory.source.ProjectID == projectID && memory.source.EnvironmentSlug == environment {
			if _, err := m.projectEnvironmentBySlugLocked(projectID, environment); err != nil {
				return EnvironmentGitSource{}, err
			}
			return cloneEnvironmentGitSource(memory.source), nil
		}
	}
	return EnvironmentGitSource{}, ErrNotFound
}

func (m *MemStore) ApproveEnvironmentDesiredRevision(_ context.Context, input ApproveEnvironmentRevision) (EnvironmentGitSource, EnvironmentDesiredRevision, error) {
	desired, err := validateEnvironmentApproval(input)
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, ok := m.environmentGitOps[input.SourceID]
	if !ok || memory.source.AccountID != input.AccountID {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrNotFound
	}
	source := memory.source
	if source.Generation != input.ExpectedGeneration || source.Suspended {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrConflict
	}
	if source.Spec.ApprovalPolicy != "manual" {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrInvalidArgument
	}
	project, exists := m.projects[source.ProjectID]
	if !exists {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrNotFound
	}
	if _, err := m.projectEnvironmentBySlugLocked(source.ProjectID, source.EnvironmentSlug); err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, err
	}
	if desired.Definition.Project != project.Slug || desired.Definition.Environment != source.EnvironmentSlug {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrInvalidArgument
	}
	var revision EnvironmentDesiredRevision
	for _, existing := range memory.revisions {
		if existing.CommitSHA == input.CommitSHA && existing.Digest == desired.Digest {
			revision = existing
			break
		}
	}
	if revision.ID == "" {
		definition, _ := json.Marshal(desired.Definition)
		revision = EnvironmentDesiredRevision{ID: newID(), SourceID: source.ID, CommitSHA: input.CommitSHA, Digest: desired.Digest, Definition: definition, ApprovedBy: input.ApprovedBy, ApprovedAt: time.Now().UTC()}
		memory.revisions[revision.ID] = revision
	}
	if source.ApprovedRevisionID != revision.ID {
		source.Generation++
		source.ApprovedRevisionID = revision.ID
		source.UpdatedAt = time.Now().UTC()
		memory.source = source
		memory.next = source.UpdatedAt
	}
	return source, cloneEnvironmentRevision(revision), nil
}

func (m *MemStore) ClaimEnvironmentGitOps(_ context.Context, token string, now time.Time, duration time.Duration) (EnvironmentGitOpsLease, error) {
	if token == "" || duration <= 0 || now.IsZero() {
		return EnvironmentGitOpsLease{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var candidates []*environmentGitOpsMemory
	for _, memory := range m.environmentGitOps {
		source := memory.source
		if source.Suspended || source.ApprovedRevisionID == "" || memory.next.After(now) || memory.lease != nil && memory.lease.Source.Generation == source.Generation && memory.lease.LeaseUntil.After(now) {
			continue
		}
		if source.Spec.ApprovalPolicy == "protected_branch" {
			if _, ok := memory.approvalForRevision(source.ApprovedRevisionID); !ok {
				continue
			}
		}
		if _, err := m.projectEnvironmentBySlugLocked(source.ProjectID, source.EnvironmentSlug); err != nil {
			continue
		}
		candidates = append(candidates, memory)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].next.Equal(candidates[j].next) {
			return candidates[i].next.Before(candidates[j].next)
		}
		return candidates[i].source.ID < candidates[j].source.ID
	})
	if len(candidates) == 0 {
		return EnvironmentGitOpsLease{}, ErrNotFound
	}
	memory := candidates[0]
	for _, oldToken := range memory.runTokens {
		if oldToken == token {
			return EnvironmentGitOpsLease{}, ErrConflict
		}
	}
	for id, run := range memory.runs {
		if run.CompletedAt == nil {
			completed := now.UTC()
			run.Status, run.CompletedAt = "superseded", &completed
			memory.runs[id] = run
		}
	}
	memory.attempts++
	revision := memory.revisions[memory.source.ApprovedRevisionID]
	lease := EnvironmentGitOpsLease{RunID: newID(), Source: memory.source, Revision: cloneEnvironmentRevision(revision), LeaseToken: token, LeaseUntil: now.UTC().Add(duration), AttemptCount: memory.attempts}
	memory.lease = &lease
	memory.runs[lease.RunID] = EnvironmentGitOpsRun{ID: lease.RunID, SourceID: lease.Source.ID, RevisionID: revision.ID, Generation: lease.Source.Generation, Status: "planning", Plan: json.RawMessage(`{}`), Steps: json.RawMessage(`[]`), StartedAt: now.UTC()}
	memory.runTokens[lease.RunID] = token
	return lease, nil
}

func (m *MemStore) gitOpsLeaseLocked(lease EnvironmentGitOpsLease, now time.Time) (*environmentGitOpsMemory, error) {
	memory, ok := m.environmentGitOps[lease.Source.ID]
	if !ok || memory.source.AccountID != lease.Source.AccountID {
		return nil, ErrNotFound
	}
	environment, err := m.projectEnvironmentBySlugLocked(memory.source.ProjectID, memory.source.EnvironmentSlug)
	if err != nil || environment.ID != memory.source.EnvironmentID {
		return nil, ErrNotFound
	}
	if memory.source.Suspended || memory.source.Generation != lease.Source.Generation || memory.source.ApprovedRevisionID != lease.Revision.ID || memory.lease == nil || memory.lease.LeaseToken != lease.LeaseToken || !memory.lease.LeaseUntil.After(now) {
		return nil, ErrConflict
	}
	return memory, nil
}

func (m *MemStore) RenewEnvironmentGitOps(_ context.Context, lease EnvironmentGitOpsLease, now time.Time, duration time.Duration) error {
	if duration <= 0 || now.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, now)
	if err != nil {
		return err
	}
	memory.lease.LeaseUntil = now.UTC().Add(duration)
	return nil
}

func (m *MemStore) FinishEnvironmentGitOps(_ context.Context, lease EnvironmentGitOpsLease, status string, plan, steps json.RawMessage, errorCode string, now, next time.Time) error {
	if err := validateGitOpsRunFinish(lease, status, plan, steps, now, next); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, now)
	if err != nil {
		return err
	}
	run, exists := memory.runs[lease.RunID]
	if !exists || run.CompletedAt != nil || memory.runTokens[lease.RunID] != lease.LeaseToken {
		return ErrConflict
	}
	if status == "converged" {
		var verified environmentsync.Plan
		_ = json.Unmarshal(plan, &verified)
		if memory.source.IntentVersion != verified.ObservedVersion {
			return ErrConflict
		}
		for _, effect := range memory.effects {
			if effect.CompletedAt == nil {
				return ErrConflict
			}
		}
		for _, effect := range memory.runtime {
			if effect.CompletedAt == nil {
				return ErrConflict
			}
		}
		if !gitOpsRuntimeReady(m.gitOpsRuntimeTargetsLocked(memory)) {
			return ErrConflict
		}
	}
	completed := now.UTC()
	var incomingSteps []EnvironmentGitOpsStep
	_ = json.Unmarshal(steps, &incomingSteps)
	if len(incomingSteps) == 0 && len(run.Steps) > 2 {
		// A lost transaction reply may hide steps already committed with
		// intent. Finishing partial must retain that durable progress.
		steps = run.Steps
	}
	run.Status, run.Plan, run.Steps, run.ErrorCode, run.CompletedAt = status, append(json.RawMessage(nil), plan...), append(json.RawMessage(nil), steps...), errorCode, &completed
	memory.runs[run.ID] = run
	memory.lease, memory.next = nil, next.UTC()
	if status == "converged" {
		memory.source.AppliedRevisionID = memory.source.ApprovedRevisionID
		memory.source.UpdatedAt = now.UTC()
	}
	return nil
}

func (m *MemStore) ListEnvironmentGitOpsRuns(_ context.Context, accountID, sourceID string, limit int) ([]EnvironmentGitOpsRun, error) {
	if limit < 1 || int64(limit) > int64(^uint32(0)>>1) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []EnvironmentGitOpsRun{}
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return out, nil
	}
	for _, run := range memory.runs {
		out = append(out, cloneEnvironmentRun(run))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
