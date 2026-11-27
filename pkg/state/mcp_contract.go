package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-20261127191645: the tool contract gate for sold MCP servers.

// MCP contract check verdicts.
const (
	MCPContractUnchanged  = "unchanged"
	MCPContractCompatible = "compatible"
	MCPContractReview     = "review"
	MCPContractBreaking   = "breaking"
	MCPContractUnverified = "unverified"
)

// AppWebhookEventMCPContractChanged is queued when a candidate whose check
// changed the contract goes live.
const AppWebhookEventMCPContractChanged AppWebhookEvent = "mcp.contract.changed"

// ErrMCPContractOverrideRequired: the check needs an override and the app has
// no open one.
var ErrMCPContractOverrideRequired = errors.New("an MCP contract override is required")

// ErrMCPContractOverrideCap: the app already has the most open overrides.
var ErrMCPContractOverrideCap = errors.New("too many open MCP contract overrides")

// MCPContractBaseline is what a candidate of a listed app is compared with.
type MCPContractBaseline struct {
	AccountID      string
	AppID          string
	ListingVersion int
	Endpoint       string
	ProbeSecret    string
	PublishedAt    time.Time
	Contract       json.RawMessage
	SHA256         string
	// FromDeployment is the accepted candidate the baseline came from; ""
	// when it is the contract the listing was published with.
	FromDeployment string
}

// MCPContractCheck is one candidate's comparison with the baseline.
type MCPContractCheck struct {
	DeploymentID    string
	AppID           string
	AccountID       string
	BaselineSHA256  string
	CandidateSHA256 string
	Candidate       json.RawMessage // nil when unverified
	Verdict         string
	Changes         json.RawMessage // []mcphosting.ContractChange
	OverrideID      string
	CheckedAt       time.Time
	AcceptedAt      *time.Time
}

// MCPContractOverride admits one breaking or unverified candidate.
type MCPContractOverride struct {
	ID               string
	AppID            string
	AccountID        string
	Reason           string
	CreatedBy        string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	UsedByDeployment string
	UsedAt           *time.Time
}

// MCPContractStore keeps checks and overrides. Optional: a store without it
// gates nothing.
type MCPContractStore interface {
	// MCPContractBaseline returns ErrNotFound when the app has no listing
	// with a contract.
	MCPContractBaseline(ctx context.Context, appID string) (MCPContractBaseline, error)
	// RecordMCPContractCheck records the candidate's check. With
	// needOverride it consumes the app's oldest open override in the same
	// transaction, or reuses the one an earlier attempt of the same
	// deployment consumed; ErrMCPContractOverrideRequired when there is
	// none. A check already accepted is not rewritten (ErrConflict).
	RecordMCPContractCheck(ctx context.Context, c MCPContractCheck, needOverride bool) (MCPContractCheck, error)
	// CreateMCPContractOverride refuses with ErrMCPContractOverrideCap when the app
	// already has openCap open overrides.
	CreateMCPContractOverride(ctx context.Context, o MCPContractOverride, openCap int) (MCPContractOverride, error)
	ListOpenMCPContractOverrides(ctx context.Context, accountID, appID string) ([]MCPContractOverride, error)
	ListMCPContractChecks(ctx context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error)
	// ListMCPContractChangelog lists accepted checks that changed the
	// contract since the listing was (re)published, newest first.
	ListMCPContractChangelog(ctx context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error)
}

var (
	_ MCPContractStore = (*MemStore)(nil)
	_ MCPContractStore = (*PgStore)(nil)
)

// ValidMCPContractVerdict reports whether v is a stored verdict.
func ValidMCPContractVerdict(v string) bool {
	return slices.Contains([]string{MCPContractUnchanged, MCPContractCompatible, MCPContractReview, MCPContractBreaking, MCPContractUnverified}, v)
}

func (m *MemStore) MCPContractBaseline(_ context.Context, appID string) (MCPContractBaseline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mcpListings[appID]
	if !ok || l.Contract == nil {
		return MCPContractBaseline{}, ErrNotFound
	}
	b := MCPContractBaseline{AccountID: l.AccountID, AppID: appID, ListingVersion: l.Version, Endpoint: l.Endpoint,
		ProbeSecret: l.ProbeSecret, PublishedAt: l.PublishedAt, Contract: l.Contract, SHA256: l.ContractSHA256}
	var newest *MCPContractCheck
	for _, c := range m.mcpContractChecks {
		if c.AppID != appID || c.AccountID != l.AccountID || c.AcceptedAt == nil || c.AcceptedAt.Before(l.PublishedAt) || c.Candidate == nil {
			continue
		}
		if newest == nil || c.AcceptedAt.After(*newest.AcceptedAt) {
			newest = &c
		}
	}
	if newest != nil {
		b.Contract, b.SHA256, b.FromDeployment = newest.Candidate, newest.CandidateSHA256, newest.DeploymentID
	}
	b.Contract = slices.Clone(b.Contract)
	return b, nil
}

func (m *MemStore) RecordMCPContractCheck(_ context.Context, c MCPContractCheck, needOverride bool) (MCPContractCheck, error) {
	if !ValidMCPContractVerdict(c.Verdict) || (c.Verdict == MCPContractUnverified) != (c.Candidate == nil) {
		return MCPContractCheck{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcpContractChecks == nil {
		m.mcpContractChecks = map[string]MCPContractCheck{}
	}
	prev, exists := m.mcpContractChecks[c.DeploymentID]
	if exists && (prev.AcceptedAt != nil || prev.AppID != c.AppID) {
		return MCPContractCheck{}, ErrConflict
	}
	c.OverrideID = ""
	if exists {
		c.OverrideID = prev.OverrideID
	}
	if needOverride && c.OverrideID == "" {
		id := m.claimMCPContractOverrideLocked(c)
		if id == "" {
			return MCPContractCheck{}, ErrMCPContractOverrideRequired
		}
		c.OverrideID = id
	}
	if c.Changes == nil {
		c.Changes = json.RawMessage("[]")
	}
	c.CheckedAt, c.AcceptedAt = time.Now().UTC(), nil
	c.Candidate, c.Changes = slices.Clone(c.Candidate), slices.Clone(c.Changes)
	m.mcpContractChecks[c.DeploymentID] = c
	return c, nil
}

func (m *MemStore) claimMCPContractOverrideLocked(c MCPContractCheck) string {
	now := time.Now().UTC()
	var oldest *MCPContractOverride
	for _, o := range m.mcpContractOverrides {
		if o.AppID != c.AppID || o.AccountID != c.AccountID || o.UsedAt != nil || !o.ExpiresAt.After(now) {
			continue
		}
		if oldest == nil || o.CreatedAt.Before(oldest.CreatedAt) {
			oldest = &o
		}
	}
	if oldest == nil {
		return ""
	}
	oldest.UsedByDeployment, oldest.UsedAt = c.DeploymentID, &now
	m.mcpContractOverrides[oldest.ID] = *oldest
	return oldest.ID
}

func (m *MemStore) CreateMCPContractOverride(_ context.Context, o MCPContractOverride, openCap int) (MCPContractOverride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	open := 0
	for _, existing := range m.mcpContractOverrides {
		if existing.AppID == o.AppID && existing.UsedAt == nil && existing.ExpiresAt.After(now) {
			open++
		}
	}
	if open >= openCap {
		return MCPContractOverride{}, ErrMCPContractOverrideCap
	}
	if m.mcpContractOverrides == nil {
		m.mcpContractOverrides = map[string]MCPContractOverride{}
	}
	o.ID, o.CreatedAt, o.UsedByDeployment, o.UsedAt = newID(), now, "", nil
	m.mcpContractOverrides[o.ID] = o
	return o, nil
}

func (m *MemStore) ListOpenMCPContractOverrides(_ context.Context, accountID, appID string) ([]MCPContractOverride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	out := []MCPContractOverride{}
	for _, o := range m.mcpContractOverrides {
		if o.AccountID == accountID && o.AppID == appID && o.UsedAt == nil && o.ExpiresAt.After(now) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListMCPContractChecks(_ context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mcpContractChecksLocked(limit, func(c MCPContractCheck) bool { return c.AccountID == accountID && c.AppID == appID },
		func(a, b MCPContractCheck) bool { return a.CheckedAt.After(b.CheckedAt) }), nil
}

func (m *MemStore) ListMCPContractChangelog(_ context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mcpListings[appID]
	if !ok || l.AccountID != accountID {
		return []MCPContractCheck{}, nil
	}
	return m.mcpContractChecksLocked(limit, func(c MCPContractCheck) bool {
		return c.AccountID == accountID && c.AppID == appID && c.AcceptedAt != nil && !c.AcceptedAt.Before(l.PublishedAt) && c.Verdict != MCPContractUnchanged
	}, func(a, b MCPContractCheck) bool { return a.AcceptedAt.After(*b.AcceptedAt) }), nil
}

func (m *MemStore) mcpContractChecksLocked(limit int, keep func(MCPContractCheck) bool, newer func(a, b MCPContractCheck) bool) []MCPContractCheck {
	out := []MCPContractCheck{}
	for _, c := range m.mcpContractChecks {
		if keep(c) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[i], out[j]) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// acceptMCPContractCheckLocked mirrors the deployments trigger
// accept_mcp_contract_check: a candidate going live accepts its check and,
// when the check changed the contract, queues mcp.contract.changed.
func (m *MemStore) acceptMCPContractCheckLocked(dep Deployment) {
	c, ok := m.mcpContractChecks[dep.ID]
	if !ok || c.AcceptedAt != nil {
		return
	}
	now := time.Now().UTC()
	c.AcceptedAt = &now
	m.mcpContractChecks[dep.ID] = c
	app, ok := m.apps[dep.AppID]
	if !ok || c.Verdict == MCPContractUnchanged {
		return
	}
	body, _ := json.Marshal(MCPContractChangedPayload(c)) // JSON-safe fields only
	for _, hook := range m.appWebhooks {
		if hook.Scope != AppWebhookScopeApp || hook.AppID != dep.AppID || hook.AccountID != app.AccountID || !hook.Enabled ||
			len(hook.EventFilter) > 0 && !slices.Contains(hook.EventFilter, string(AppWebhookEventMCPContractChanged)) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{
			ID: id, WebhookID: hook.ID, AppID: dep.AppID, AccountID: app.AccountID,
			Event: AppWebhookEventMCPContractChanged, Payload: json.RawMessage(body), Status: AppWebhookDeliveryPending,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
	}
}

// MCPContractChangedPayload is the mcp.contract.changed body the trigger
// builds: changed tool names, never schemas.
func MCPContractChangedPayload(c MCPContractCheck) map[string]any {
	var changes []struct {
		Tool string `json:"tool"`
	}
	_ = json.Unmarshal(c.Changes, &changes)
	tools := []string{}
	for _, ch := range changes {
		if ch.Tool != "" && !slices.Contains(tools, ch.Tool) {
			tools = append(tools, ch.Tool)
		}
	}
	sort.Strings(tools)
	var contractSHA any
	if c.CandidateSHA256 != "" {
		contractSHA = c.CandidateSHA256
	}
	return map[string]any{"app_id": c.AppID, "deployment_id": c.DeploymentID, "verdict": c.Verdict,
		"baseline_sha256": c.BaselineSHA256, "contract_sha256": contractSHA, "overridden": c.OverrideID != "",
		"tools": tools, "changes": len(changes)}
}

func fromMCPContractCheck(row sqlc.McpContractCheck) MCPContractCheck {
	c := MCPContractCheck{DeploymentID: pgUUIDString(row.DeploymentID), AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID),
		BaselineSHA256: row.BaselineSha256, CandidateSHA256: row.CandidateSha256.String, Verdict: row.Verdict,
		Changes: json.RawMessage(row.Changes), OverrideID: pgUUIDStringOrEmpty(row.OverrideID), CheckedAt: row.CheckedAt.Time.UTC()}
	if row.Candidate != nil {
		c.Candidate = json.RawMessage(row.Candidate)
	}
	if row.AcceptedAt.Valid {
		t := row.AcceptedAt.Time.UTC()
		c.AcceptedAt = &t
	}
	return c
}

func fromMCPContractOverride(row sqlc.McpContractOverride) MCPContractOverride {
	o := MCPContractOverride{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID),
		Reason: row.Reason, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
		UsedByDeployment: pgUUIDStringOrEmpty(row.UsedByDeployment)}
	if row.UsedAt.Valid {
		t := row.UsedAt.Time.UTC()
		o.UsedAt = &t
	}
	return o
}

func (s *PgStore) MCPContractBaseline(ctx context.Context, appID string) (MCPContractBaseline, error) {
	row, err := sqlc.New().GetMCPContractBaseline(ctx, s.pool, uuidToPgtype(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPContractBaseline{}, ErrNotFound
	}
	if err != nil {
		return MCPContractBaseline{}, fmt.Errorf("read MCP contract baseline: %w", err)
	}
	return MCPContractBaseline{AccountID: pgUUIDString(row.AccountID), AppID: appID, ListingVersion: int(row.ListingVersion),
		Endpoint: row.Endpoint, ProbeSecret: row.ProbeSecret, PublishedAt: row.PublishedAt.Time.UTC(),
		Contract: json.RawMessage(row.Contract), SHA256: row.ContractSha256, FromDeployment: row.FromDeployment}, nil
}

func (s *PgStore) RecordMCPContractCheck(ctx context.Context, c MCPContractCheck, needOverride bool) (MCPContractCheck, error) {
	if !ValidMCPContractVerdict(c.Verdict) || (c.Verdict == MCPContractUnverified) != (c.Candidate == nil) {
		return MCPContractCheck{}, ErrInvalidArgument
	}
	if c.Changes == nil {
		c.Changes = json.RawMessage("[]")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return MCPContractCheck{}, fmt.Errorf("record MCP contract check begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	overrideID := ""
	prev, err := q.GetMCPContractCheck(ctx, tx, uuidToPgtype(c.DeploymentID))
	switch {
	case err == nil:
		if prev.AcceptedAt.Valid || pgUUIDString(prev.AppID) != c.AppID {
			return MCPContractCheck{}, ErrConflict
		}
		overrideID = pgUUIDStringOrEmpty(prev.OverrideID)
	case !errors.Is(err, pgx.ErrNoRows):
		return MCPContractCheck{}, fmt.Errorf("read MCP contract check: %w", err)
	}
	if needOverride && overrideID == "" {
		claimed, err := q.ClaimMCPContractOverride(ctx, tx, sqlc.ClaimMCPContractOverrideParams{
			DeploymentID: uuidToPgtype(c.DeploymentID), AppID: uuidToPgtype(c.AppID), AccountID: uuidToPgtype(c.AccountID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return MCPContractCheck{}, ErrMCPContractOverrideRequired
		}
		if err != nil {
			return MCPContractCheck{}, fmt.Errorf("claim MCP contract override: %w", err)
		}
		overrideID = pgUUIDString(claimed.ID)
	}
	var override pgtype.UUID
	if overrideID != "" {
		override = uuidToPgtype(overrideID)
	}
	row, err := q.UpsertMCPContractCheck(ctx, tx, sqlc.UpsertMCPContractCheckParams{DeploymentID: uuidToPgtype(c.DeploymentID),
		AppID: uuidToPgtype(c.AppID), AccountID: uuidToPgtype(c.AccountID), BaselineSha256: c.BaselineSHA256,
		CandidateSha256: pgText(c.CandidateSHA256), Candidate: []byte(c.Candidate), Verdict: c.Verdict, Changes: []byte(c.Changes),
		OverrideID: override})
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPContractCheck{}, ErrConflict
	}
	if err != nil {
		return MCPContractCheck{}, fmt.Errorf("record MCP contract check: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MCPContractCheck{}, fmt.Errorf("record MCP contract check commit: %w", err)
	}
	return fromMCPContractCheck(row), nil
}

func (s *PgStore) CreateMCPContractOverride(ctx context.Context, o MCPContractOverride, openCap int) (MCPContractOverride, error) {
	row, err := sqlc.New().CreateMCPContractOverride(ctx, s.pool, sqlc.CreateMCPContractOverrideParams{AppID: uuidToPgtype(o.AppID),
		AccountID: uuidToPgtype(o.AccountID), Reason: o.Reason, CreatedBy: o.CreatedBy, ExpiresAt: pgTimestamptzUTC(o.ExpiresAt),
		OpenCap: int64(openCap)})
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPContractOverride{}, ErrMCPContractOverrideCap
	}
	if err != nil {
		return MCPContractOverride{}, fmt.Errorf("create MCP contract override: %w", err)
	}
	return fromMCPContractOverride(row), nil
}

func (s *PgStore) ListOpenMCPContractOverrides(ctx context.Context, accountID, appID string) ([]MCPContractOverride, error) {
	rows, err := sqlc.New().ListOpenMCPContractOverrides(ctx, s.pool, sqlc.ListOpenMCPContractOverridesParams{
		AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID)})
	if err != nil {
		return nil, fmt.Errorf("list MCP contract overrides: %w", err)
	}
	out := make([]MCPContractOverride, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromMCPContractOverride(r))
	}
	return out, nil
}

func (s *PgStore) ListMCPContractChecks(ctx context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error) {
	rows, err := sqlc.New().ListMCPContractChecks(ctx, s.pool, sqlc.ListMCPContractChecksParams{
		AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID), Limit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list MCP contract checks: %w", err)
	}
	return fromMCPContractChecks(rows), nil
}

func (s *PgStore) ListMCPContractChangelog(ctx context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error) {
	rows, err := sqlc.New().ListMCPContractChangelog(ctx, s.pool, sqlc.ListMCPContractChangelogParams{
		AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID), Limit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list MCP contract changelog: %w", err)
	}
	return fromMCPContractChecks(rows), nil
}

func fromMCPContractChecks(rows []sqlc.McpContractCheck) []MCPContractCheck {
	out := make([]MCPContractCheck, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromMCPContractCheck(r))
	}
	return out
}
