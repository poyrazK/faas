// Package agents stores Gregale Agents definitions, their immutable versions,
// and runs (ADR-20261027090010 §4). apid writes agents and versions, admits runs as
// queued and cancels queued runs; agentd (ADR-20261027090014) owns every later run
// transition. Every write of one account serializes on one advisory lock, so
// limit counts and dedupe checks cannot race.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

var (
	// ErrNotFound means the account has no such agent, version or run.
	ErrNotFound = errors.New("agents: not found")
	// ErrTenantNotFound means the account has no such platform tenant.
	ErrTenantNotFound = errors.New("agents: tenant not found")
	// ErrNameTaken means the account already has an active agent of that name.
	ErrNameTaken = errors.New("agents: name taken")
	// ErrBusy means the agent still has runs that have not finished.
	ErrBusy = errors.New("agents: agent has unfinished runs")
	// ErrTenantInactive means the run's tenant is suspended.
	ErrTenantInactive = errors.New("agents: tenant is not active")
	// ErrFinished means the run already reached a terminal state.
	ErrFinished = errors.New("agents: run already finished")
	// ErrCheckoutUnavailable means a run asks for a checkout, but the account
	// has no such GitHub installation.
	ErrCheckoutUnavailable = errors.New("agents: no GitHub installation for the checkout")
	// ErrGitHubUnavailable means a run names a GitHub delivery target, but
	// the account has no such GitHub installation.
	ErrGitHubUnavailable = errors.New("agents: no GitHub installation for the delivery target")
	// ErrDeliveryTargetMissing means the version declares a delivery the run
	// gives no destination for.
	ErrDeliveryTargetMissing = errors.New("agents: the run has no destination for a declared delivery")
	// ErrBucketNotFound means a version declares a bucket delivery to a
	// bucket the account does not have.
	ErrBucketNotFound = errors.New("agents: no such bucket for the delivery")
)

// GitHubTarget is the issue or pull request a run's GitHub comments go to
// (ADR-20261027090010 §8). InstallationID 0 asks for the account's most recent
// installation.
type GitHubTarget struct {
	Repository     string
	Number         int64
	InstallationID int64
}

// Delivery is one delivery a version declares: a destination and the
// result.json string it carries. A bucket delivery names the bucket and key
// prefix; App, the app that owns the bucket, is fixed when the version is
// written.
type Delivery struct {
	Type   string `json:"type"`
	Field  string `json:"field,omitempty"`
	Bucket string `json:"bucket,omitempty"`
	App    string `json:"app,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

// Checkout is a repository's tree at one commit (ADR-20261027090010 §3).
// InstallationID 0 asks for the account's most recent installation.
type Checkout struct {
	Repository     string
	Commit         string
	InstallationID int64
}

// IntegrationError means a version names a model integration that is not an
// enabled customer AI integration of the account for that provider.
type IntegrationError struct {
	Provider      string
	IntegrationID string
}

func (e *IntegrationError) Error() string {
	return fmt.Sprintf("agents: integration %s is not an enabled %s AI integration of the account", e.IntegrationID, e.Provider)
}

// LimitError reports a plan limit a write would exceed. Limit names the
// pkg/api/limits.go entry.
type LimitError struct {
	Limit    string
	Max      int
	Observed int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("agents: %s is %d, observed %d", e.Limit, e.Max, e.Observed)
}

// Run states (ADR-20261027090010 §4).
const (
	StateQueued      = "queued"
	StateStarting    = "starting"
	StateRunning     = "running"
	StateDelivering  = "delivering"
	StateSucceeded   = "succeeded"
	StateFailed      = "failed"
	StateTimedOut    = "timed_out"
	StateOutOfMemory = "out_of_memory"
	StateCancelled   = "cancelled"
)

// Harness describes what a version runs: a Gregale preset or a seller image.
type Harness struct {
	Preset string // "codex", "opencode" or "claude-code"; empty for an image
	Image  string // an OCI reference; empty for a preset
	Run    []string
	// Models maps a provider to the base-URL variable the harness reads.
	Models map[string]string
}

// VersionSpec is what a new version asks for.
type VersionSpec struct {
	Harness        Harness
	PromptTemplate string
	RAMMB          int // 0: sized automatically
	TimeoutSec     int
	// Integrations maps a model provider to the AI gateway integration the
	// harness reaches through the relay (ADR-20261027090010 §7).
	Integrations map[string]string
	// AIBudgetMillicents caps one run's model spend; 0 takes the plan's
	// default.
	AIBudgetMillicents int64
	// Deliveries are what a succeeded run delivers (ADR-20261027090010 §8).
	Deliveries []Delivery
}

// Agent is an agent definition.
type Agent struct {
	ID            string
	Name          string
	Description   string
	LatestVersion int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Version is one immutable agent version.
type Version struct {
	Version int
	VersionSpec
	CreatedAt time.Time
}

// Run is one agent run.
type Run struct {
	ID                string
	AgentID           string
	AgentName         string
	AgentVersion      int
	TenantID          string
	Trigger           string
	DedupeKey         string
	Prompt            *string
	Inputs            json.RawMessage
	Checkout          *Checkout
	GitHub            *GitHubTarget
	State             string
	RAMMB             int
	TimeoutSec        int
	Attempt           int
	CancelRequestedAt *time.Time
	OutcomeCode       string
	OutcomeMessage    string
	ExitCode          *int
	QueuedAt          time.Time
	StartedAt         *time.Time
	RunningAt         *time.Time
	FinishedAt        *time.Time
	UpdatedAt         time.Time
}

// Terminal reports whether the run reached a state it can never leave.
func (r Run) Terminal() bool {
	switch r.State {
	case StateSucceeded, StateFailed, StateTimedOut, StateOutOfMemory, StateCancelled:
		return true
	}
	return false
}

// AdmitLimits are the plan limits a run admission checks.
type AdmitLimits struct {
	RunsPerDay    int
	QueuedMax     int
	TimeoutMaxSec int
	RAMMBMax      int
}

// PlanLimits are plan's admission limits, from pkg/api/limits.go.
func PlanLimits(plan api.Plan) AdmitLimits {
	idx := plan.PlanIndex()
	return AdmitLimits{RunsPerDay: api.AgentRunsPerDay[idx], QueuedMax: api.AgentRunQueuedPerAccount[idx],
		TimeoutMaxSec: api.AgentRunTimeoutMaxSec[idx], RAMMBMax: api.AgentRunRAMMBMax[idx]}
}

// Admission is a run to admit.
type Admission struct {
	AccountID string
	Agent     string // the agent's name
	Version   int    // 0: the latest
	TenantID  string // empty: the seller's own run
	Trigger   string
	DedupeKey string
	Prompt    *string
	Inputs    json.RawMessage
	Checkout  *Checkout
	GitHub    *GitHubTarget
	// Workflow links a run an automation's agent step admitted to its step.
	Workflow *WorkflowLink
	Limits   AdmitLimits
}

// WorkflowLink is the workflow run, step and reserved event of an agent
// step's run (ADR-20261027090010 §9).
type WorkflowLink struct {
	RunID string
	Step  string
	Event string
}

// RunFilter narrows a run listing. Before pages backwards from a run.
type RunFilter struct {
	Agent    string
	State    string
	TenantID string
	Before   *Run
	Limit    int
}

// Store reads and writes agents, agent_versions and agent_runs.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// New returns a store over pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: sqlc.New()} }

// CreateAgent creates an agent with its first version.
func (s *Store) CreateAgent(ctx context.Context, accountID, name, description string, spec VersionSpec, maxAgents int) (Agent, Version, error) {
	var agent Agent
	var version Version
	err := s.locked(ctx, accountID, func(tx pgx.Tx) error {
		n, err := s.q.CountActiveAgents(ctx, tx, accountID)
		if err != nil {
			return fmt.Errorf("count agents: %w", err)
		}
		if int(n) >= maxAgents {
			return &LimitError{Limit: "AgentsPerAccount", Max: maxAgents, Observed: int(n)}
		}
		row, err := s.q.InsertAgent(ctx, tx, sqlc.InsertAgentParams{AccountID: accountID, Name: name, Description: description})
		if err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return fmt.Errorf("insert agent: %w", err)
		}
		agent = agentFrom(sqlc.GetActiveAgentRow(row))
		agent, version, err = s.addVersion(ctx, tx, accountID, agent, spec)
		return err
	})
	return agent, version, err
}

// AddVersion adds the next version of an agent.
func (s *Store) AddVersion(ctx context.Context, accountID, name string, spec VersionSpec, maxVersions int) (Agent, Version, error) {
	var agent Agent
	var version Version
	err := s.locked(ctx, accountID, func(tx pgx.Tx) error {
		var err error
		if agent, err = s.activeAgent(ctx, tx, accountID, name); err != nil {
			return err
		}
		if agent.LatestVersion >= maxVersions {
			return &LimitError{Limit: "AgentVersionsPerAgent", Max: maxVersions, Observed: agent.LatestVersion}
		}
		agent, version, err = s.addVersion(ctx, tx, accountID, agent, spec)
		return err
	})
	return agent, version, err
}

func (s *Store) addVersion(ctx context.Context, tx pgx.Tx, accountID string, agent Agent, spec VersionSpec) (Agent, Version, error) {
	next, err := s.q.BumpAgentVersion(ctx, tx, sqlc.BumpAgentVersionParams{AgentID: agent.ID, AccountID: accountID})
	if err != nil {
		return Agent{}, Version{}, fmt.Errorf("bump agent version: %w", err)
	}
	models, err := json.Marshal(nonNilModels(spec.Harness.Models))
	if err != nil {
		return Agent{}, Version{}, fmt.Errorf("encode harness models: %w", err)
	}
	if err := s.checkIntegrations(ctx, tx, accountID, spec.Integrations); err != nil {
		return Agent{}, Version{}, err
	}
	integrations, err := json.Marshal(nonNilModels(spec.Integrations))
	if err != nil {
		return Agent{}, Version{}, fmt.Errorf("encode model integrations: %w", err)
	}
	if err := s.resolveDeliveryBuckets(ctx, tx, accountID, spec.Deliveries); err != nil {
		return Agent{}, Version{}, err
	}
	deliveries, err := json.Marshal(nonNilDeliveries(spec.Deliveries))
	if err != nil {
		return Agent{}, Version{}, fmt.Errorf("encode deliveries: %w", err)
	}
	run := spec.Harness.Run
	if run == nil {
		run = []string{}
	}
	row, err := s.q.InsertAgentVersion(ctx, tx, sqlc.InsertAgentVersionParams{
		AgentID: agent.ID, Version: next, AccountID: accountID,
		HarnessPreset: optText(spec.Harness.Preset), HarnessImage: optText(spec.Harness.Image),
		HarnessRun: run, HarnessModels: models, PromptTemplate: spec.PromptTemplate,
		RamMb: optInt(spec.RAMMB), TimeoutSec: int32(spec.TimeoutSec),
		ModelIntegrations: integrations, AiBudgetMillicents: optInt8(spec.AIBudgetMillicents), Deliveries: deliveries,
	})
	if err != nil {
		return Agent{}, Version{}, fmt.Errorf("insert agent version: %w", err)
	}
	agent.LatestVersion = int(next)
	version, err := versionFrom(sqlc.GetAgentVersionRow(row))
	return agent, version, err
}

// resolveDeliveryBuckets requires each bucket delivery's bucket to be the
// account's, and records the app that owns it.
func (s *Store) resolveDeliveryBuckets(ctx context.Context, tx pgx.Tx, accountID string, deliveries []Delivery) error {
	for i, d := range deliveries {
		if d.Type != api.AgentDeliveryBucket {
			continue
		}
		if !isUUID(d.Bucket) {
			return ErrBucketNotFound
		}
		app, err := s.q.ResolveAgentDeliveryBucket(ctx, tx, sqlc.ResolveAgentDeliveryBucketParams{AccountID: accountID, BucketID: d.Bucket})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBucketNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve delivery bucket: %w", err)
		}
		deliveries[i].App = app
	}
	return nil
}

// checkIntegrations requires each named integration to be an enabled
// customer AI integration of the account for its provider. The check runs in
// the version's transaction; a later disable or delete makes the gateway
// refuse the run's calls instead.
func (s *Store) checkIntegrations(ctx context.Context, tx pgx.Tx, accountID string, integrations map[string]string) error {
	if len(integrations) == 0 {
		return nil
	}
	ids := make([]string, 0, len(integrations))
	for _, id := range integrations {
		ids = append(ids, id)
	}
	rows, err := s.q.ListAgentModelIntegrations(ctx, tx, sqlc.ListAgentModelIntegrationsParams{AccountID: accountID, Ids: ids})
	if err != nil {
		return fmt.Errorf("read model integrations: %w", err)
	}
	providers := make(map[string]string, len(rows))
	for _, row := range rows {
		providers[row.ID] = row.AiProvider
	}
	for _, provider := range sortedKeys(integrations) {
		if id := integrations[provider]; providers[id] != provider {
			return &IntegrationError{Provider: provider, IntegrationID: id}
		}
	}
	return nil
}

// Agent reads an active agent and its latest version.
func (s *Store) Agent(ctx context.Context, accountID, name string) (Agent, Version, error) {
	agent, err := s.activeAgent(ctx, s.pool, accountID, name)
	if err != nil {
		return Agent{}, Version{}, err
	}
	version, err := s.version(ctx, s.pool, accountID, agent.ID, agent.LatestVersion)
	return agent, version, err
}

// Version reads one version of an active agent.
func (s *Store) Version(ctx context.Context, accountID, name string, version int) (Version, error) {
	agent, err := s.activeAgent(ctx, s.pool, accountID, name)
	if err != nil {
		return Version{}, err
	}
	return s.version(ctx, s.pool, accountID, agent.ID, version)
}

// Agents lists active agents by name, after the given name.
func (s *Store) Agents(ctx context.Context, accountID, after string, limit int) ([]Agent, error) {
	rows, err := s.q.ListActiveAgents(ctx, s.pool, sqlc.ListActiveAgentsParams{AccountID: accountID, After: after, MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentFrom(sqlc.GetActiveAgentRow(row)))
	}
	return out, nil
}

// DeleteAgent deletes an agent that has no unfinished runs. Its versions and
// runs stay readable by run ID.
func (s *Store) DeleteAgent(ctx context.Context, accountID, name string) error {
	return s.locked(ctx, accountID, func(tx pgx.Tx) error {
		agent, err := s.activeAgent(ctx, tx, accountID, name)
		if err != nil {
			return err
		}
		live, err := s.q.CountLiveAgentRuns(ctx, tx, sqlc.CountLiveAgentRunsParams{AgentID: agent.ID, AccountID: accountID})
		if err != nil {
			return fmt.Errorf("count live runs: %w", err)
		}
		if live > 0 {
			return ErrBusy
		}
		if _, err := s.q.MarkAgentDeleted(ctx, tx, sqlc.MarkAgentDeletedParams{AgentID: agent.ID, AccountID: accountID}); err != nil {
			return fmt.Errorf("delete agent: %w", err)
		}
		return nil
	})
}

// AdmitRun inserts a queued run, or returns the run that already holds the
// dedupe key (existing is then true). Limits are checked in the same
// transaction as the insert.
func (s *Store) AdmitRun(ctx context.Context, a Admission) (run Run, existing bool, err error) {
	var id string
	err = s.locked(ctx, a.AccountID, func(tx pgx.Tx) error {
		agent, err := s.activeAgent(ctx, tx, a.AccountID, a.Agent)
		if err != nil {
			return err
		}
		if a.DedupeKey != "" {
			found, err := s.q.FindDedupedRun(ctx, tx, sqlc.FindDedupedRunParams{AgentID: agent.ID, TenantID: optText(a.TenantID), DedupeKey: a.DedupeKey})
			if err == nil {
				id, existing = found, true
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("find deduped run: %w", err)
			}
		}
		number := a.Version
		if number == 0 {
			number = agent.LatestVersion
		}
		version, err := s.version(ctx, tx, a.AccountID, agent.ID, number)
		if err != nil {
			return err
		}
		if err := s.checkAdmission(ctx, tx, a, version); err != nil {
			return err
		}
		if a.Checkout != nil {
			resolved, err := s.resolveCheckout(ctx, tx, a.AccountID, *a.Checkout)
			if err != nil {
				return err
			}
			a.Checkout = &resolved
		}
		if err := s.resolveDeliveryTargets(ctx, tx, &a, version); err != nil {
			return err
		}
		id, err = s.insertRun(ctx, tx, a, agent.ID, version)
		return err
	})
	if err != nil {
		return Run{}, false, err
	}
	run, err = s.Run(ctx, a.AccountID, id)
	return run, existing, err
}

// resolveCheckout fixes the installation that fetches a checkout at
// admission, so a later installation does not change what the run sees.
func (s *Store) resolveCheckout(ctx context.Context, tx pgx.Tx, accountID string, c Checkout) (Checkout, error) {
	installation, err := s.resolveInstallation(ctx, tx, accountID, c.InstallationID, ErrCheckoutUnavailable)
	c.InstallationID = installation
	return c, err
}

// resolveDeliveryTargets requires a destination for every delivery the
// version declares, and fixes the GitHub installation that writes the run's
// comments at admission, as for checkouts.
func (s *Store) resolveDeliveryTargets(ctx context.Context, tx pgx.Tx, a *Admission, v Version) error {
	for _, d := range v.Deliveries {
		if d.Type == api.AgentDeliveryGitHubComment && a.GitHub == nil {
			return ErrDeliveryTargetMissing
		}
	}
	if a.GitHub == nil {
		return nil
	}
	target := *a.GitHub
	installation, err := s.resolveInstallation(ctx, tx, a.AccountID, target.InstallationID, ErrGitHubUnavailable)
	if err != nil {
		return err
	}
	target.InstallationID = installation
	a.GitHub = &target
	return nil
}

// resolveInstallation returns the account's named installation, or its most
// recent one; missing is returned when there is none.
func (s *Store) resolveInstallation(ctx context.Context, tx pgx.Tx, accountID string, named int64, missing error) (int64, error) {
	installation, err := s.q.ResolveGitHubInstallation(ctx, tx, sqlc.ResolveGitHubInstallationParams{AccountID: accountID,
		InstallationID: pgtype.Int8{Int64: named, Valid: named != 0}})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, missing
	}
	if err != nil {
		return 0, fmt.Errorf("resolve GitHub installation: %w", err)
	}
	return installation, nil
}

func (s *Store) insertRun(ctx context.Context, tx pgx.Tx, a Admission, agentID string, version Version) (string, error) {
	inputs := a.Inputs
	if len(inputs) == 0 {
		inputs = json.RawMessage(`{}`)
	}
	var prompt pgtype.Text
	if a.Prompt != nil {
		prompt = pgtype.Text{String: *a.Prompt, Valid: true}
	}
	params := sqlc.InsertAgentRunParams{
		AccountID: a.AccountID, AgentID: agentID, AgentVersion: int32(version.Version), TenantID: optText(a.TenantID),
		Trigger: a.Trigger, DedupeKey: optText(a.DedupeKey), Prompt: prompt, Inputs: inputs,
		RamMb: optInt(version.RAMMB), TimeoutSec: int32(version.TimeoutSec),
	}
	if c := a.Checkout; c != nil {
		params.CheckoutRepository, params.CheckoutCommit = optText(c.Repository), optText(c.Commit)
		params.CheckoutInstallationID = optInt8(c.InstallationID)
	}
	if g := a.GitHub; g != nil {
		params.GithubRepository, params.GithubNumber = optText(g.Repository), optInt8(g.Number)
		params.GithubInstallationID = optInt8(g.InstallationID)
	}
	if w := a.Workflow; w != nil {
		params.WorkflowRunID, params.WorkflowStep, params.WorkflowEvent = optText(w.RunID), optText(w.Step), optText(w.Event)
	}
	id, err := s.q.InsertAgentRun(ctx, tx, params)
	if err != nil {
		return "", fmt.Errorf("insert agent run: %w", err)
	}
	return id, nil
}

// checkAdmission applies the tenant and plan checks of ADR-20261027090010 §4 and §5.
// The plan may have changed since the version was written, so its size and
// timeout are checked again here.
func (s *Store) checkAdmission(ctx context.Context, tx pgx.Tx, a Admission, v Version) error {
	if a.TenantID != "" {
		if !isUUID(a.TenantID) {
			return ErrTenantNotFound
		}
		status, err := s.q.GetTenantStatus(ctx, tx, sqlc.GetTenantStatusParams{TenantID: a.TenantID, AccountID: a.AccountID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTenantNotFound
		}
		if err != nil {
			return fmt.Errorf("read tenant: %w", err)
		}
		if status != "active" {
			return ErrTenantInactive
		}
	}
	if v.TimeoutSec > a.Limits.TimeoutMaxSec {
		return &LimitError{Limit: "AgentRunTimeoutMaxSec", Max: a.Limits.TimeoutMaxSec, Observed: v.TimeoutSec}
	}
	if v.RAMMB > a.Limits.RAMMBMax {
		return &LimitError{Limit: "AgentRunRAMMBMax", Max: a.Limits.RAMMBMax, Observed: v.RAMMB}
	}
	today, err := s.q.CountAgentRunsToday(ctx, tx, a.AccountID)
	if err != nil {
		return fmt.Errorf("count runs today: %w", err)
	}
	if int(today) >= a.Limits.RunsPerDay {
		return &LimitError{Limit: "AgentRunsPerDay", Max: a.Limits.RunsPerDay, Observed: int(today)}
	}
	queued, err := s.q.CountQueuedAgentRuns(ctx, tx, a.AccountID)
	if err != nil {
		return fmt.Errorf("count queued runs: %w", err)
	}
	if int(queued) >= a.Limits.QueuedMax {
		return &LimitError{Limit: "AgentRunQueuedPerAccount", Max: a.Limits.QueuedMax, Observed: int(queued)}
	}
	return nil
}

// Run reads one run.
func (s *Store) Run(ctx context.Context, accountID, id string) (Run, error) {
	if !isUUID(id) {
		return Run{}, ErrNotFound
	}
	row, err := s.q.GetAgentRun(ctx, s.pool, sqlc.GetAgentRunParams{RunID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("read agent run: %w", err)
	}
	return runFrom(row), nil
}

// Runs lists runs newest first.
func (s *Store) Runs(ctx context.Context, accountID string, f RunFilter) ([]Run, error) {
	params := sqlc.ListAgentRunsParams{AccountID: accountID, State: optText(f.State), TenantID: optText(f.TenantID), MaxRows: int32(f.Limit)}
	if f.Agent != "" {
		agent, err := s.activeAgent(ctx, s.pool, accountID, f.Agent)
		if errors.Is(err, ErrNotFound) {
			return []Run{}, nil
		}
		if err != nil {
			return nil, err
		}
		params.AgentID = optText(agent.ID)
	}
	if f.TenantID != "" && !isUUID(f.TenantID) {
		return []Run{}, nil
	}
	if f.Before != nil {
		params.BeforeQueuedAt = pgtype.Timestamptz{Time: f.Before.QueuedAt, Valid: true}
		params.BeforeID = optText(f.Before.ID)
	}
	rows, err := s.q.ListAgentRuns(ctx, s.pool, params)
	if err != nil {
		return nil, fmt.Errorf("list agent runs: %w", err)
	}
	out := make([]Run, 0, len(rows))
	for _, row := range rows {
		out = append(out, runFrom(sqlc.GetAgentRunRow(row)))
	}
	return out, nil
}

// CancelRun cancels a queued run at once, and asks agentd to stop a started
// one. Cancelling a run twice is not an error; cancelling a finished run is.
func (s *Store) CancelRun(ctx context.Context, accountID, id string) (Run, error) {
	if !isUUID(id) {
		return Run{}, ErrNotFound
	}
	err := s.locked(ctx, accountID, func(tx pgx.Tx) error {
		n, err := s.q.CancelQueuedAgentRun(ctx, tx, sqlc.CancelQueuedAgentRunParams{RunID: id, AccountID: accountID})
		if err != nil {
			return fmt.Errorf("cancel queued run: %w", err)
		}
		if n > 0 {
			// A queued run that is cancelled ends here, not in agentd.
			return s.notifyFinished(ctx, tx, accountID, id)
		}
		n, err = s.q.RequestAgentRunCancel(ctx, tx, sqlc.RequestAgentRunCancelParams{RunID: id, AccountID: accountID})
		if err != nil {
			return fmt.Errorf("request run cancel: %w", err)
		}
		if n > 0 {
			return nil
		}
		return errNoTransition
	})
	if err != nil && !errors.Is(err, errNoTransition) {
		return Run{}, err
	}
	run, readErr := s.Run(ctx, accountID, id)
	if readErr != nil {
		return Run{}, readErr
	}
	if errors.Is(err, errNoTransition) && run.State != StateCancelled {
		return run, ErrFinished
	}
	return run, nil
}

// errNoTransition ends a cancel transaction that changed nothing.
var errNoTransition = errors.New("agents: no transition")

func (s *Store) activeAgent(ctx context.Context, db sqlc.DBTX, accountID, name string) (Agent, error) {
	row, err := s.q.GetActiveAgent(ctx, db, sqlc.GetActiveAgentParams{AccountID: accountID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("read agent: %w", err)
	}
	return agentFrom(row), nil
}

func (s *Store) version(ctx context.Context, db sqlc.DBTX, accountID, agentID string, version int) (Version, error) {
	row, err := s.q.GetAgentVersion(ctx, db, sqlc.GetAgentVersionParams{AgentID: agentID, AccountID: accountID, Version: int32(version)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, fmt.Errorf("read agent version: %w", err)
	}
	return versionFrom(row)
}

func (s *Store) locked(ctx context.Context, accountID string, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.q.LockAccountAgents(ctx, tx, accountID); err != nil {
			return fmt.Errorf("lock account agents: %w", err)
		}
		return fn(tx)
	})
}

func agentFrom(row sqlc.GetActiveAgentRow) Agent {
	return Agent{ID: row.ID, Name: row.Name, Description: row.Description, LatestVersion: int(row.LatestVersion),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

func versionFrom(row sqlc.GetAgentVersionRow) (Version, error) {
	var models, integrations map[string]string
	if err := json.Unmarshal(row.HarnessModels, &models); err != nil {
		return Version{}, fmt.Errorf("decode harness models: %w", err)
	}
	if err := json.Unmarshal(row.ModelIntegrations, &integrations); err != nil {
		return Version{}, fmt.Errorf("decode model integrations: %w", err)
	}
	var deliveries []Delivery
	if err := json.Unmarshal(row.Deliveries, &deliveries); err != nil {
		return Version{}, fmt.Errorf("decode deliveries: %w", err)
	}
	if len(deliveries) == 0 {
		deliveries = nil
	}
	return Version{
		Version: int(row.Version),
		VersionSpec: VersionSpec{
			Harness:        Harness{Preset: row.HarnessPreset.String, Image: row.HarnessImage.String, Run: row.HarnessRun, Models: models},
			PromptTemplate: row.PromptTemplate, RAMMB: int(row.RamMb.Int32), TimeoutSec: int(row.TimeoutSec),
			Integrations: nilIfEmpty(integrations), AIBudgetMillicents: row.AiBudgetMillicents.Int64,
			Deliveries: deliveries,
		},
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func runFrom(row sqlc.GetAgentRunRow) Run {
	run := Run{
		ID: row.ID, AgentID: row.AgentID, AgentName: row.AgentName, AgentVersion: int(row.AgentVersion), TenantID: row.TenantID,
		Trigger: row.Trigger, DedupeKey: row.DedupeKey.String, Inputs: row.Inputs, State: row.State,
		RAMMB: int(row.RamMb.Int32), TimeoutSec: int(row.TimeoutSec), Attempt: int(row.Attempt),
		OutcomeCode: row.OutcomeCode.String, OutcomeMessage: row.OutcomeMessage.String,
		QueuedAt: row.QueuedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		CancelRequestedAt: optTime(row.CancelRequestedAt), StartedAt: optTime(row.StartedAt),
		RunningAt: optTime(row.RunningAt), FinishedAt: optTime(row.FinishedAt),
	}
	if row.Prompt.Valid {
		prompt := row.Prompt.String
		run.Prompt = &prompt
	}
	run.Checkout = checkoutFrom(row.CheckoutRepository, row.CheckoutCommit, row.CheckoutInstallationID)
	if row.GithubRepository.Valid {
		run.GitHub = &GitHubTarget{Repository: row.GithubRepository.String, Number: row.GithubNumber.Int64,
			InstallationID: row.GithubInstallationID.Int64}
	}
	if row.ExitCode.Valid {
		exit := int(row.ExitCode.Int32)
		run.ExitCode = &exit
	}
	return run
}

func checkoutFrom(repository, commit pgtype.Text, installation pgtype.Int8) *Checkout {
	if !repository.Valid {
		return nil
	}
	return &Checkout{Repository: repository.String, Commit: commit.String, InstallationID: installation.Int64}
}

func nonNilDeliveries(d []Delivery) []Delivery {
	if d == nil {
		return []Delivery{}
	}
	return d
}

func nonNilModels(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func optText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func optInt(n int) pgtype.Int4 { return pgtype.Int4{Int32: int32(n), Valid: n != 0} }

func optInt8(n int64) pgtype.Int8 { return pgtype.Int8{Int64: n, Valid: n != 0} }

func optTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
