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
	// MCPContractUnknown marks a deployment that went live without a check;
	// only the deployments trigger records it.
	MCPContractUnknown = "unknown"
)

// AppWebhookEventMCPContractChanged is queued when a candidate whose check
// changed the contract goes live.
const AppWebhookEventMCPContractChanged AppWebhookEvent = "mcp.contract.changed"

// ErrMCPContractOverrideRequired: the check needs an override and the app has
// no open one.
var ErrMCPContractOverrideRequired = errors.New("an MCP contract override is required")

// ErrMCPContractOverrideCap: the app already has the most open overrides.
var ErrMCPContractOverrideCap = errors.New("too many open MCP contract overrides")

// ErrMCPToolScanOverrideRequired: the candidate's tool scan has high findings
// the baseline had not reviewed and the app has no open tool_scan override
// (ADR-20261128135329).
var ErrMCPToolScanOverrideRequired = errors.New("an MCP tool scan override is required")

// MCP contract override kinds (ADR-20261128135329): an override admits
// either a contract refusal or a tool scan refusal.
const (
	MCPContractOverrideKindContract = "contract"
	MCPContractOverrideKindToolScan = "tool_scan"
)

// ValidMCPContractOverrideKind reports whether k names an override kind.
func ValidMCPContractOverrideKind(k string) bool {
	return k == MCPContractOverrideKindContract || k == MCPContractOverrideKindToolScan
}

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
	// ToolScan is the tool scan (mcptoolscan.Report JSON) of the same
	// source, nil when it was never scanned; ToolScanReviewed is set when its
	// high findings were admitted (ADR-20261128135329).
	ToolScan         json.RawMessage
	ToolScanAt       *time.Time
	ToolScanReviewed bool
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
	// AcceptedAt is when the candidate first went live; LiveAt the last
	// time, rollbacks included. The newest LiveAt is the baseline.
	AcceptedAt *time.Time
	LiveAt     *time.Time
	// ToolScan is the candidate's tool scan (ADR-20261128135329), nil when
	// its catalog could not be listed. ToolScanNewHigh counts the high
	// findings the baseline had not reviewed; they need the tool_scan
	// override ToolScanOverrideID. The store sets ToolScanReviewed when the
	// high findings were admitted.
	ToolScan           json.RawMessage
	ToolScanHigh       int
	ToolScanNewHigh    int
	ToolScanOverrideID string
	ToolScanReviewed   bool
}

// MCPContractOverride admits one breaking or unverified candidate (kind
// contract) or one candidate with high tool scan findings (kind tool_scan).
type MCPContractOverride struct {
	ID        string
	Kind      string
	AppID     string
	AccountID string
	Reason    string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
	// DeploymentID binds the override to one deployment; "" admits the
	// app's next breaking candidate.
	DeploymentID     string
	UsedByDeployment string
	UsedAt           *time.Time
}

// MCPContractStore keeps checks and overrides. Optional: a store without it
// gates nothing.
type MCPContractStore interface {
	// MCPContractBaseline returns ErrNotFound when the app has no listing
	// with a contract.
	MCPContractBaseline(ctx context.Context, appID string) (MCPContractBaseline, error)
	// RecordMCPContractCheck records the candidate's check. For each claim
	// other than MCPContractClaimNone it consumes an open override of that
	// kind in the same transaction (one bound to this deployment first; with
	// MCPContractClaimAny else the app's oldest unbound one), or reuses the
	// one an earlier attempt of the same deployment consumed. When a needed
	// override is missing it consumes none, still records the refused check
	// and returns it with ErrMCPContractOverrideRequired (contract) or
	// ErrMCPToolScanOverrideRequired (tool scan). A check already accepted
	// is not rewritten (ErrConflict).
	RecordMCPContractCheck(ctx context.Context, c MCPContractCheck, claims MCPContractClaims) (MCPContractCheck, error)
	// CreateMCPContractOverride refuses with ErrMCPContractOverrideCap when
	// the app already has openCap open overrides.
	CreateMCPContractOverride(ctx context.Context, o MCPContractOverride, openCap int) (MCPContractOverride, error)
	// ListOpenMCPContractOverrides lists usable overrides: unused,
	// unexpired, and unbound or bound to a deployment still a candidate.
	ListOpenMCPContractOverrides(ctx context.Context, accountID, appID string) ([]MCPContractOverride, error)
	ListMCPContractChecks(ctx context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error)
	// ListMCPContractChangelog lists accepted checks that changed the
	// contract since the listing was (re)published, newest first.
	ListMCPContractChangelog(ctx context.Context, accountID, appID string, limit int) ([]MCPContractCheck, error)
}

// MCPContractClaim says which overrides a check may consume.
type MCPContractClaim int

const (
	// MCPContractClaimNone: the check needs no override.
	MCPContractClaimNone MCPContractClaim = iota
	// MCPContractClaimBound: only an override bound to this deployment.
	MCPContractClaimBound
	// MCPContractClaimAny: a bound override, else the app's oldest unbound one.
	MCPContractClaimAny
)

// MCPContractClaims are the overrides one check may consume, by kind.
type MCPContractClaims struct {
	Contract MCPContractClaim
	ToolScan MCPContractClaim
}

// pgUUIDOrNull is NULL for "".
func pgUUIDOrNull(id string) pgtype.UUID {
	if id == "" {
		return pgtype.UUID{}
	}
	return uuidToPgtype(id)
}

// validMCPContractCheck checks what a recorded check may hold.
func validMCPContractCheck(c MCPContractCheck) bool {
	return ValidMCPContractVerdict(c.Verdict) && (c.Verdict == MCPContractUnverified) == (c.Candidate == nil) &&
		c.ToolScanHigh >= 0 && c.ToolScanNewHigh >= 0 && c.ToolScanNewHigh <= c.ToolScanHigh && (c.ToolScan != nil || c.ToolScanHigh == 0)
}

// mcpContractCheckRefused: a check a candidate's going live never accepts.
func mcpContractCheckRefused(c MCPContractCheck) bool {
	return (c.Verdict == MCPContractBreaking || c.Verdict == MCPContractUnverified) && c.OverrideID == "" ||
		c.ToolScanNewHigh > 0 && c.ToolScanOverrideID == ""
}

// missingClaimError names the override kind a claim found missing: the
// contract one first, then the tool scan one; nil when none is missing.
func missingClaimError(contractMissing, scanMissing bool) error {
	switch {
	case contractMissing:
		return ErrMCPContractOverrideRequired
	case scanMissing:
		return ErrMCPToolScanOverrideRequired
	}
	return nil
}

// recordedClaimError is the refusal a recorded check carries.
func recordedClaimError(c MCPContractCheck, claims MCPContractClaims) error {
	switch {
	case claims.Contract != MCPContractClaimNone && c.OverrideID == "":
		return ErrMCPContractOverrideRequired
	case claims.ToolScan != MCPContractClaimNone && c.ToolScanOverrideID == "":
		return ErrMCPToolScanOverrideRequired
	}
	return nil
}

var (
	_ MCPContractStore = (*MemStore)(nil)
	_ MCPContractStore = (*PgStore)(nil)
)

// ValidMCPContractVerdict reports whether v is a verdict a check may be
// recorded with (MCPContractUnknown is only the trigger's marker).
func ValidMCPContractVerdict(v string) bool {
	return slices.Contains([]string{MCPContractUnchanged, MCPContractCompatible, MCPContractReview, MCPContractBreaking, MCPContractUnverified}, v)
}

// mcpContractCandidateStatus: a bound override counts while its deployment
// can still reach the gate.
func mcpContractCandidateStatus(s DeploymentStatus) bool {
	return slices.Contains([]DeploymentStatus{"pending", "building", "imaging", "snapshotting"}, s)
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
	b.ToolScan, b.ToolScanAt, b.ToolScanReviewed = l.ToolScan, l.ToolScanAt, l.ToolScanReview != ""
	if newest := m.newestLiveMCPContractCheckLocked(l); newest != nil && newest.Candidate != nil {
		b.Contract, b.SHA256, b.FromDeployment = newest.Candidate, newest.CandidateSHA256, newest.DeploymentID
		checked := newest.CheckedAt
		b.ToolScan, b.ToolScanAt, b.ToolScanReviewed = newest.ToolScan, &checked, newest.ToolScanReviewed
	}
	b.Contract, b.ToolScan = slices.Clone(b.Contract), slices.Clone(b.ToolScan)
	return b, nil
}

// newestLiveMCPContractCheckLocked is the check of the deployment that most
// recently went live since the listing was published.
func (m *MemStore) newestLiveMCPContractCheckLocked(l MCPListing) *MCPContractCheck {
	var newest *MCPContractCheck
	for _, c := range m.mcpContractChecks {
		if c.AppID != l.AppID || c.AccountID != l.AccountID || c.LiveAt == nil || c.LiveAt.Before(l.PublishedAt) {
			continue
		}
		if newest == nil || c.LiveAt.After(*newest.LiveAt) {
			newest = &c
		}
	}
	return newest
}

func (m *MemStore) RecordMCPContractCheck(_ context.Context, c MCPContractCheck, claims MCPContractClaims) (MCPContractCheck, error) {
	if !validMCPContractCheck(c) {
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
	c.OverrideID, c.ToolScanOverrideID = "", ""
	if exists {
		c.OverrideID, c.ToolScanOverrideID = prev.OverrideID, prev.ToolScanOverrideID
	}
	// Claim both kinds or neither.
	var contract, scan *MCPContractOverride
	if claims.Contract != MCPContractClaimNone && c.OverrideID == "" {
		contract = m.findMCPContractOverrideLocked(c, MCPContractOverrideKindContract, claims.Contract == MCPContractClaimAny)
	}
	if claims.ToolScan != MCPContractClaimNone && c.ToolScanOverrideID == "" {
		scan = m.findMCPContractOverrideLocked(c, MCPContractOverrideKindToolScan, claims.ToolScan == MCPContractClaimAny)
	}
	missing := missingClaimError(claims.Contract != MCPContractClaimNone && c.OverrideID == "" && contract == nil,
		claims.ToolScan != MCPContractClaimNone && c.ToolScanOverrideID == "" && scan == nil)
	if missing == nil {
		now := time.Now().UTC()
		for _, o := range []*MCPContractOverride{contract, scan} {
			if o != nil {
				o.UsedByDeployment, o.UsedAt = c.DeploymentID, &now
				m.mcpContractOverrides[o.ID] = *o
			}
		}
		if contract != nil {
			c.OverrideID = contract.ID
		}
		if scan != nil {
			c.ToolScanOverrideID = scan.ID
		}
	}
	if c.Changes == nil {
		c.Changes = json.RawMessage("[]")
	}
	c.ToolScanReviewed = c.ToolScanHigh > 0 && (c.ToolScanNewHigh == 0 || c.ToolScanOverrideID != "")
	c.CheckedAt, c.AcceptedAt, c.LiveAt = time.Now().UTC(), nil, nil
	c.Candidate, c.Changes, c.ToolScan = slices.Clone(c.Candidate), slices.Clone(c.Changes), slices.Clone(c.ToolScan)
	m.mcpContractChecks[c.DeploymentID] = c
	if missing != nil {
		return c, missing
	}
	return c, recordedClaimError(c, claims)
}

// findMCPContractOverrideLocked is the open override of kind a check would
// consume: one bound to its deployment first, then the oldest unbound one.
func (m *MemStore) findMCPContractOverrideLocked(c MCPContractCheck, kind string, allowUnbound bool) *MCPContractOverride {
	now := time.Now().UTC()
	var oldest *MCPContractOverride
	for _, o := range m.mcpContractOverrides {
		if o.AppID != c.AppID || o.AccountID != c.AccountID || o.Kind != kind || o.UsedAt != nil || !o.ExpiresAt.After(now) ||
			o.DeploymentID != c.DeploymentID && (o.DeploymentID != "" || !allowUnbound) {
			continue
		}
		bound, oldestBound := o.DeploymentID != "", oldest != nil && oldest.DeploymentID != ""
		if oldest == nil || bound && !oldestBound || bound == oldestBound && o.CreatedAt.Before(oldest.CreatedAt) {
			oldest = &o
		}
	}
	return oldest
}

// openMCPContractOverrideLocked mirrors the open filter of the Pg queries.
func (m *MemStore) openMCPContractOverrideLocked(o MCPContractOverride, now time.Time) bool {
	if o.UsedAt != nil || !o.ExpiresAt.After(now) {
		return false
	}
	return o.DeploymentID == "" || mcpContractCandidateStatus(m.deployments[o.DeploymentID].Status)
}

func (m *MemStore) CreateMCPContractOverride(_ context.Context, o MCPContractOverride, openCap int) (MCPContractOverride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	open := 0
	for _, existing := range m.mcpContractOverrides {
		if existing.AppID == o.AppID && m.openMCPContractOverrideLocked(existing, now) {
			open++
		}
	}
	if open >= openCap {
		return MCPContractOverride{}, ErrMCPContractOverrideCap
	}
	if m.mcpContractOverrides == nil {
		m.mcpContractOverrides = map[string]MCPContractOverride{}
	}
	if o.Kind == "" {
		o.Kind = MCPContractOverrideKindContract
	}
	if !ValidMCPContractOverrideKind(o.Kind) {
		return MCPContractOverride{}, ErrInvalidArgument
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
		if o.AccountID == accountID && o.AppID == appID && m.openMCPContractOverrideLocked(o, now) {
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
		return c.AccountID == accountID && c.AppID == appID && c.AcceptedAt != nil && !c.AcceptedAt.Before(l.PublishedAt) &&
			c.Verdict != MCPContractUnchanged && c.Verdict != MCPContractUnknown
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
// accept_mcp_contract_check: a production candidate going live accepts its
// check (unless refused, or made against a baseline that has moved since)
// and, when the check changed the contract, queues mcp.contract.changed; a
// deployment without a check records an unknown marker.
func (m *MemStore) acceptMCPContractCheckLocked(dep Deployment) {
	l, listed := m.mcpListings[dep.AppID]
	if !IsProductionScope(dep.Scope) || !listed || l.Contract == nil {
		return
	}
	now := time.Now().UTC()
	c, ok := m.mcpContractChecks[dep.ID]
	if !ok {
		if m.mcpContractChecks == nil {
			m.mcpContractChecks = map[string]MCPContractCheck{}
		}
		m.mcpContractChecks[dep.ID] = MCPContractCheck{DeploymentID: dep.ID, AppID: dep.AppID, AccountID: l.AccountID,
			BaselineSHA256: l.ContractSHA256, Verdict: MCPContractUnknown, Changes: json.RawMessage("[]"), CheckedAt: now,
			AcceptedAt: &now, LiveAt: &now}
		return
	}
	first := c.AcceptedAt == nil
	if first {
		current := l.ContractSHA256
		if newest := m.newestLiveMCPContractCheckLocked(l); newest != nil && newest.Candidate != nil {
			current = newest.CandidateSHA256
		}
		if mcpContractCheckRefused(c) || c.BaselineSHA256 != current {
			return
		}
		c.AcceptedAt = &now
	}
	c.LiveAt = &now
	m.mcpContractChecks[dep.ID] = c
	app, ok := m.apps[dep.AppID]
	if !ok || !first || c.Verdict == MCPContractUnchanged {
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
		Changes: json.RawMessage(row.Changes), OverrideID: pgUUIDStringOrEmpty(row.OverrideID), CheckedAt: row.CheckedAt.Time.UTC(),
		ToolScanHigh: int(row.ToolScanHigh), ToolScanNewHigh: int(row.ToolScanNewHigh),
		ToolScanOverrideID: pgUUIDStringOrEmpty(row.ToolScanOverrideID), ToolScanReviewed: row.ToolScanReviewed}
	if row.Candidate != nil {
		c.Candidate = json.RawMessage(row.Candidate)
	}
	if row.ToolScan != nil {
		c.ToolScan = json.RawMessage(row.ToolScan)
	}
	if row.AcceptedAt.Valid {
		t := row.AcceptedAt.Time.UTC()
		c.AcceptedAt = &t
	}
	if row.LiveAt.Valid {
		t := row.LiveAt.Time.UTC()
		c.LiveAt = &t
	}
	return c
}

func fromMCPContractOverride(row sqlc.McpContractOverride) MCPContractOverride {
	o := MCPContractOverride{ID: pgUUIDString(row.ID), Kind: row.Kind, AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID),
		Reason: row.Reason, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
		DeploymentID: pgUUIDStringOrEmpty(row.DeploymentID), UsedByDeployment: pgUUIDStringOrEmpty(row.UsedByDeployment)}
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
	b := MCPContractBaseline{AccountID: pgUUIDString(row.AccountID), AppID: appID, ListingVersion: int(row.ListingVersion),
		Endpoint: row.Endpoint, ProbeSecret: row.ProbeSecret, PublishedAt: row.PublishedAt.Time.UTC(),
		Contract: json.RawMessage(row.Contract), SHA256: row.ContractSha256, FromDeployment: row.FromDeployment,
		ToolScanReviewed: row.ToolScanReviewed}
	if row.ToolScan != nil {
		b.ToolScan = json.RawMessage(row.ToolScan)
	}
	if row.ToolScanAt.Valid {
		t := row.ToolScanAt.Time.UTC()
		b.ToolScanAt = &t
	}
	return b, nil
}

func (s *PgStore) RecordMCPContractCheck(ctx context.Context, c MCPContractCheck, claims MCPContractClaims) (MCPContractCheck, error) {
	if !validMCPContractCheck(c) {
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
	c.OverrideID, c.ToolScanOverrideID = "", ""
	prev, err := q.GetMCPContractCheck(ctx, tx, uuidToPgtype(c.DeploymentID))
	switch {
	case err == nil:
		if prev.AcceptedAt.Valid || pgUUIDString(prev.AppID) != c.AppID {
			return MCPContractCheck{}, ErrConflict
		}
		c.OverrideID, c.ToolScanOverrideID = pgUUIDStringOrEmpty(prev.OverrideID), pgUUIDStringOrEmpty(prev.ToolScanOverrideID)
	case !errors.Is(err, pgx.ErrNoRows):
		return MCPContractCheck{}, fmt.Errorf("read MCP contract check: %w", err)
	}
	missing, err := s.claimMCPContractOverrides(ctx, tx, &c, claims)
	if err != nil {
		return MCPContractCheck{}, err
	}
	c.ToolScanReviewed = c.ToolScanHigh > 0 && (c.ToolScanNewHigh == 0 || c.ToolScanOverrideID != "")
	row, err := q.UpsertMCPContractCheck(ctx, tx, sqlc.UpsertMCPContractCheckParams{DeploymentID: uuidToPgtype(c.DeploymentID),
		AppID: uuidToPgtype(c.AppID), AccountID: uuidToPgtype(c.AccountID), BaselineSha256: c.BaselineSHA256,
		CandidateSha256: pgText(c.CandidateSHA256), Candidate: []byte(c.Candidate), Verdict: c.Verdict, Changes: []byte(c.Changes),
		OverrideID: pgUUIDOrNull(c.OverrideID), ToolScan: []byte(c.ToolScan), ToolScanHigh: int32(c.ToolScanHigh),
		ToolScanNewHigh: int32(c.ToolScanNewHigh), ToolScanOverrideID: pgUUIDOrNull(c.ToolScanOverrideID), ToolScanReviewed: c.ToolScanReviewed})
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPContractCheck{}, ErrConflict
	}
	if err != nil {
		return MCPContractCheck{}, fmt.Errorf("record MCP contract check: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MCPContractCheck{}, fmt.Errorf("record MCP contract check commit: %w", err)
	}
	// A refused check is recorded so the seller sees what was refused; it is
	// never accepted.
	recorded := fromMCPContractCheck(row)
	if missing != nil {
		return recorded, missing
	}
	return recorded, recordedClaimError(recorded, claims)
}

// claimMCPContractOverrides consumes the overrides a check needs, both kinds
// or neither: the claims run under a savepoint that is rolled back when one
// is missing, and missing names it.
func (s *PgStore) claimMCPContractOverrides(ctx context.Context, tx pgx.Tx, c *MCPContractCheck, claims MCPContractClaims) (missing, err error) {
	needContract := claims.Contract != MCPContractClaimNone && c.OverrideID == ""
	needScan := claims.ToolScan != MCPContractClaimNone && c.ToolScanOverrideID == ""
	if !needContract && !needScan {
		return nil, nil
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("claim MCP contract overrides savepoint: %w", err)
	}
	defer func() { _ = sp.Rollback(ctx) }()
	claim := func(kind string, mode MCPContractClaim) (string, error) {
		row, err := sqlc.New().ClaimMCPContractOverride(ctx, sp, sqlc.ClaimMCPContractOverrideParams{
			DeploymentID: uuidToPgtype(c.DeploymentID), AppID: uuidToPgtype(c.AppID), AccountID: uuidToPgtype(c.AccountID),
			Kind: kind, AllowUnbound: mode == MCPContractClaimAny})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("claim MCP %s override: %w", kind, err)
		}
		return pgUUIDString(row.ID), nil
	}
	contract, scan := "", ""
	if needContract {
		if contract, err = claim(MCPContractOverrideKindContract, claims.Contract); err != nil {
			return nil, err
		}
	}
	if needScan {
		if scan, err = claim(MCPContractOverrideKindToolScan, claims.ToolScan); err != nil {
			return nil, err
		}
	}
	if missing := missingClaimError(needContract && contract == "", needScan && scan == ""); missing != nil {
		return missing, nil // the deferred rollback releases whichever was claimed
	}
	if err := sp.Commit(ctx); err != nil {
		return nil, fmt.Errorf("claim MCP contract overrides: %w", err)
	}
	if contract != "" {
		c.OverrideID = contract
	}
	if scan != "" {
		c.ToolScanOverrideID = scan
	}
	return nil, nil
}

func (s *PgStore) CreateMCPContractOverride(ctx context.Context, o MCPContractOverride, openCap int) (MCPContractOverride, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return MCPContractOverride{}, fmt.Errorf("create MCP contract override begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockMCPContractOverrides(ctx, tx, uuidToPgtype(o.AppID)); err != nil {
		return MCPContractOverride{}, fmt.Errorf("lock MCP contract overrides: %w", err)
	}
	var deployment pgtype.UUID
	if o.DeploymentID != "" {
		deployment = uuidToPgtype(o.DeploymentID)
	}
	if o.Kind == "" {
		o.Kind = MCPContractOverrideKindContract
	}
	if !ValidMCPContractOverrideKind(o.Kind) {
		return MCPContractOverride{}, ErrInvalidArgument
	}
	row, err := q.CreateMCPContractOverride(ctx, tx, sqlc.CreateMCPContractOverrideParams{AppID: uuidToPgtype(o.AppID),
		AccountID: uuidToPgtype(o.AccountID), Reason: o.Reason, CreatedBy: o.CreatedBy, ExpiresAt: pgTimestamptzUTC(o.ExpiresAt),
		DeploymentID: deployment, OpenCap: int64(openCap), Kind: o.Kind})
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPContractOverride{}, ErrMCPContractOverrideCap
	}
	if err != nil {
		return MCPContractOverride{}, fmt.Errorf("create MCP contract override: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MCPContractOverride{}, fmt.Errorf("create MCP contract override commit: %w", err)
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
