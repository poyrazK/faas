package state

import (
	"context"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIPromptStore = (*MemStore)(nil)

// memOutboundAIPrompt finds an account's prompt by name. The caller holds
// m.mu.
func (m *MemStore) memOutboundAIPrompt(accountID, name string) (OutboundAIPrompt, bool) {
	for _, prompt := range m.outboundAIPrompts {
		if prompt.AccountID == accountID && prompt.Name == name {
			return prompt, true
		}
	}
	return OutboundAIPrompt{}, false
}

func (m *MemStore) memOutboundAIPromptHasVersion(promptID string, version int) bool {
	for _, stored := range m.outboundAIPromptVersions[promptID] {
		if stored.Version == version {
			return true
		}
	}
	return false
}

// memOutboundAIPromptTargetAllowed checks a version's target against the
// account's routes and integrations. The caller holds m.mu.
func (m *MemStore) memOutboundAIPromptTargetAllowed(accountID string, target api.OutboundAIPromptTarget) bool {
	if target.RouteID != "" {
		route, ok := m.outboundAIRoutes[target.RouteID]
		return ok && route.AccountID == accountID && route.Format == api.OutboundAIRouteFormatOpenAI
	}
	targets := []OutboundAIRouteTarget{{IntegrationID: target.IntegrationID, Model: target.Model}}
	return outboundAIRouteTargetsAllowed(api.OutboundAIRouteFormatOpenAI, targets, m.memOutboundAIProviders(accountID, targets))
}

func copyOutboundAIPromptVersion(in OutboundAIPromptVersion) OutboundAIPromptVersion {
	out := in
	out.Messages = slices.Clone(in.Messages)
	out.Defaults = maps.Clone(in.Defaults)
	out.Params.Stop = slices.Clone(in.Params.Stop)
	out.Params.ResponseFormat = slices.Clone(in.Params.ResponseFormat)
	return out
}

func (m *MemStore) ListOutboundAIPrompts(_ context.Context, accountID string) ([]OutboundAIPrompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundAIPrompt, 0)
	for _, prompt := range m.outboundAIPrompts {
		if prompt.AccountID == accountID {
			out = append(out, copyOutboundAIPrompt(prompt))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) GetOutboundAIPrompt(_ context.Context, accountID, name string) (OutboundAIPrompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return OutboundAIPrompt{}, ErrNotFound
	}
	return copyOutboundAIPrompt(prompt), nil
}

func (m *MemStore) PushOutboundAIPromptVersion(_ context.Context, push OutboundAIPromptPush) (OutboundAIPrompt, OutboundAIPromptVersion, bool, error) {
	if !validOutboundAIPromptVersion(push) {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIPrompts == nil {
		m.outboundAIPrompts = make(map[string]OutboundAIPrompt)
		m.outboundAIPromptVersions = make(map[string][]OutboundAIPromptVersion)
	}
	if !m.memOutboundAIPromptTargetAllowed(push.AccountID, push.Version.Target) {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, ErrInvalidArgument
	}
	now := time.Now().UTC()
	prompt, exists := m.memOutboundAIPrompt(push.AccountID, push.Name)
	if !exists {
		count := 0
		for _, other := range m.outboundAIPrompts {
			if other.AccountID == push.AccountID {
				count++
			}
		}
		if count >= push.MaxPrompts {
			return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, ErrOutboundAIPromptLimit
		}
		prompt = OutboundAIPrompt{ID: push.ID, AccountID: push.AccountID, Name: push.Name, CreatedAt: now}
	}
	versions := m.outboundAIPromptVersions[prompt.ID]
	if len(versions) > 0 {
		latest := versions[len(versions)-1]
		if latest.Version == prompt.LatestVersion &&
			api.SameOutboundAIPromptVersion(outboundAIPromptPushRequest(latest), outboundAIPromptPushRequest(push.Version)) {
			return copyOutboundAIPrompt(prompt), copyOutboundAIPromptVersion(latest), false, nil
		}
	}
	version := copyOutboundAIPromptVersion(push.Version)
	version.PromptID, version.Version, version.CreatedAt = prompt.ID, prompt.LatestVersion+1, now
	prompt.LatestVersion = version.Version
	if push.Description != nil {
		prompt.Description = *push.Description
	}
	if prompt.LiveVersion == nil || push.Live {
		prompt = memPromptGoLive(prompt, version.Version)
	}
	prompt.UpdatedAt = now
	versions = append(versions, version)
	numbers := make([]int, 0, len(versions))
	for _, stored := range versions {
		numbers = append(numbers, stored.Version)
	}
	if prune := outboundAIPromptPrunable(numbers, prompt); prune != 0 {
		versions = slices.DeleteFunc(versions, func(stored OutboundAIPromptVersion) bool { return stored.Version == prune })
	}
	m.outboundAIPrompts[prompt.ID] = prompt
	m.outboundAIPromptVersions[prompt.ID] = versions
	return copyOutboundAIPrompt(prompt), copyOutboundAIPromptVersion(version), true, nil
}

// memPromptGoLive makes a version live, keeps the old live version as the
// previous one and ends any canary.
func memPromptGoLive(prompt OutboundAIPrompt, version int) OutboundAIPrompt {
	if prompt.LiveVersion != nil && *prompt.LiveVersion != version {
		prompt.PreviousLiveVersion = promptVersionPtr(*prompt.LiveVersion)
	}
	prompt.LiveVersion = promptVersionPtr(version)
	prompt.Canary = nil
	return prompt
}

func (m *MemStore) ListOutboundAIPromptVersions(_ context.Context, accountID, name string) ([]OutboundAIPromptVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]OutboundAIPromptVersion, 0)
	for _, version := range slices.Backward(m.outboundAIPromptVersions[prompt.ID]) {
		out = append(out, OutboundAIPromptVersion{PromptID: version.PromptID, Version: version.Version, Target: version.Target,
			Note: version.Note, CreatedAt: version.CreatedAt})
	}
	return out, nil
}

func (m *MemStore) GetOutboundAIPromptVersion(_ context.Context, accountID, name string, version int) (OutboundAIPromptVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return OutboundAIPromptVersion{}, ErrNotFound
	}
	for _, stored := range m.outboundAIPromptVersions[prompt.ID] {
		if stored.Version == version {
			return copyOutboundAIPromptVersion(stored), nil
		}
	}
	return OutboundAIPromptVersion{}, ErrNotFound
}

func (m *MemStore) SetOutboundAIPromptLive(_ context.Context, accountID, name string, version int) (OutboundAIPrompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return OutboundAIPrompt{}, ErrNotFound
	}
	if !m.memOutboundAIPromptHasVersion(prompt.ID, version) {
		return OutboundAIPrompt{}, ErrInvalidArgument
	}
	prompt = memPromptGoLive(prompt, version)
	prompt.UpdatedAt = time.Now().UTC()
	m.outboundAIPrompts[prompt.ID] = prompt
	return copyOutboundAIPrompt(prompt), nil
}

func (m *MemStore) RollBackOutboundAIPromptLive(_ context.Context, accountID, name string) (OutboundAIPrompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return OutboundAIPrompt{}, ErrNotFound
	}
	if prompt.PreviousLiveVersion == nil || !m.memOutboundAIPromptHasVersion(prompt.ID, *prompt.PreviousLiveVersion) {
		return OutboundAIPrompt{}, ErrInvalidArgument
	}
	prompt = memPromptGoLive(prompt, *prompt.PreviousLiveVersion)
	prompt.UpdatedAt = time.Now().UTC()
	m.outboundAIPrompts[prompt.ID] = prompt
	return copyOutboundAIPrompt(prompt), nil
}

func (m *MemStore) DeleteOutboundAIPrompt(_ context.Context, accountID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return ErrNotFound
	}
	delete(m.outboundAIPrompts, prompt.ID)
	delete(m.outboundAIPromptVersions, prompt.ID)
	for id, evaluation := range m.outboundAIPromptEvaluations {
		if evaluation.PromptID == prompt.ID {
			delete(m.outboundAIPromptEvaluations, id)
		}
	}
	return nil
}

func (m *MemStore) SetOutboundAIPromptCanary(_ context.Context, accountID, name string, canary OutboundAIPromptCanary) (OutboundAIPromptCanary, error) {
	if !validOutboundAIPromptCanary(canary) {
		return OutboundAIPromptCanary{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return OutboundAIPromptCanary{}, ErrNotFound
	}
	if !m.memOutboundAIPromptHasVersion(prompt.ID, canary.Version) || prompt.LiveVersion == nil || *prompt.LiveVersion == canary.Version {
		return OutboundAIPromptCanary{}, ErrInvalidArgument
	}
	canary.State, canary.Reason, canary.StartedAt, canary.DecidedAt = api.OutboundAIRouteCanaryActive, "", time.Now().UTC(), nil
	prompt.Canary = &canary
	m.outboundAIPrompts[prompt.ID] = prompt
	return *copyOutboundAIPrompt(prompt).Canary, nil
}

func (m *MemStore) DeleteOutboundAIPromptCanary(_ context.Context, accountID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok || prompt.Canary == nil {
		return ErrNotFound
	}
	prompt.Canary = nil
	m.outboundAIPrompts[prompt.ID] = prompt
	return nil
}

func (m *MemStore) PromoteOutboundAIPromptCanary(_ context.Context, accountID, name string) (OutboundAIPrompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok || prompt.Canary == nil || prompt.Canary.State != api.OutboundAIRouteCanaryActive {
		return OutboundAIPrompt{}, ErrNotFound
	}
	if !m.memOutboundAIPromptHasVersion(prompt.ID, prompt.Canary.Version) {
		return OutboundAIPrompt{}, ErrInvalidArgument
	}
	prompt = memPromptGoLive(prompt, prompt.Canary.Version)
	prompt.UpdatedAt = time.Now().UTC()
	m.outboundAIPrompts[prompt.ID] = prompt
	return copyOutboundAIPrompt(prompt), nil
}

// OutboundAIPromptStats has no minute rows in memory: outboundd writes them
// to Postgres. It returns the prompt with an empty window.
func (m *MemStore) OutboundAIPromptStats(ctx context.Context, accountID, name string) (OutboundAIPromptStatsWindow, error) {
	prompt, err := m.GetOutboundAIPrompt(ctx, accountID, name)
	if err != nil {
		return OutboundAIPromptStatsWindow{}, err
	}
	return OutboundAIPromptStatsWindow{WindowStart: time.Now().UTC().Add(-api.OutboundAIPromptStatsWindowMinutes * time.Minute),
		Prompt: prompt, Versions: []OutboundAIPromptVersionStats{}}, nil
}

func (m *MemStore) CreateOutboundAIPromptEvaluation(_ context.Context, evaluation OutboundAIPromptEvaluation) (OutboundAIPromptEvaluation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.outboundAIPrompts[evaluation.PromptID]
	if !ok || prompt.AccountID != evaluation.AccountID {
		return OutboundAIPromptEvaluation{}, ErrNotFound
	}
	judge := []OutboundAIRouteTarget{{IntegrationID: evaluation.JudgeIntegrationID, Model: evaluation.JudgeModel}}
	if prompt.LiveVersion == nil || *prompt.LiveVersion == evaluation.CandidateVersion ||
		!m.memOutboundAIPromptHasVersion(prompt.ID, evaluation.CandidateVersion) ||
		len(m.memOutboundAIProviders(evaluation.AccountID, judge)) == 0 {
		return OutboundAIPromptEvaluation{}, ErrInvalidArgument
	}
	if m.outboundAIPromptEvaluations == nil {
		m.outboundAIPromptEvaluations = make(map[string]OutboundAIPromptEvaluation)
	}
	evaluation.PromptName, evaluation.BaselineVersion = prompt.Name, *prompt.LiveVersion
	evaluation.State, evaluation.CreatedAt = api.OutboundAIEvaluationPending, time.Now().UTC()
	m.outboundAIPromptEvaluations[evaluation.ID] = evaluation
	return evaluation, nil
}

func (m *MemStore) ListOutboundAIPromptEvaluations(_ context.Context, accountID, name string) ([]OutboundAIPromptEvaluation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prompt, ok := m.memOutboundAIPrompt(accountID, name)
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]OutboundAIPromptEvaluation, 0)
	for _, evaluation := range m.outboundAIPromptEvaluations {
		if evaluation.PromptID == prompt.ID {
			evaluation.Items = nil
			out = append(out, evaluation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) GetOutboundAIPromptEvaluation(_ context.Context, accountID, evaluationID string) (OutboundAIPromptEvaluation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evaluation, ok := m.outboundAIPromptEvaluations[evaluationID]
	if !ok || evaluation.AccountID != accountID {
		return OutboundAIPromptEvaluation{}, ErrNotFound
	}
	evaluation.Items = slices.Clone(evaluation.Items)
	return evaluation, nil
}
