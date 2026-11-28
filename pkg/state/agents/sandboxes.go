package agents

// Sandbox sessions (ADR-20261027090013): the intents apid writes. A session
// is admitted under the account's agent lock, so its queued count and its
// runs' agree; agentd claims it and owns every transition after queued
// (ADR-20261027090014).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// Sandbox session states (ADR-20261027090013 §1).
const (
	SandboxQueued   = "queued"
	SandboxStarting = "starting"
	SandboxReady    = "ready"
	SandboxFrozen   = "frozen"
	// SandboxPausing, SandboxPaused and SandboxResuming are a persistent
	// session's (ADR-20261122132055 §1): a paused session has no VM and
	// keeps a memory capture and its workspace volume.
	SandboxPausing  = "pausing"
	SandboxPaused   = "paused"
	SandboxResuming = "resuming"
	SandboxClosed   = "closed"
	SandboxExpired  = "expired"
	SandboxFailed   = "failed"
)

// Session kinds (ADR-20261027090014 §5).
const (
	SandboxEphemeral  = "ephemeral"
	SandboxPersistent = "persistent"
)

// SandboxTerminal reports a state no session leaves.
func SandboxTerminal(state string) bool {
	return state == SandboxClosed || state == SandboxExpired || state == SandboxFailed
}

// Sandbox is one session.
type Sandbox struct {
	ID               string
	AccountID        string
	TenantID         string
	Kind             string
	Preset           string
	RAMMB            int
	NetworkMode      string
	State            string
	IdleTimeoutSec   int
	ExpiresAt        time.Time
	Labels           map[string]string
	OutcomeCode      string
	OutcomeMessage   string
	CloseRequestedAt *time.Time
	// NodeID and InstanceID place a session once agentd has booted it.
	NodeID         string
	InstanceID     string
	LastActivityAt time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClosedAt       *time.Time
	// WorkspaceSnapshotID is the persisted workspace the session started
	// from, if any.
	WorkspaceSnapshotID string
	// ImageRef is the seller image the session boots, normalized; empty for
	// a preset.
	ImageRef string
	// Browser is a session created with the sandbox browser running
	// (ADR-20261116182615 §1).
	Browser bool
	// EgressAllow and EgressDeny are the web-mode domain policy the session
	// was admitted under (ADR-20261117091026 §2); empty is none.
	EgressAllow []string
	EgressDeny  []string
	// Persistent sessions (ADR-20261122132055): the shape and volume asked
	// for (0: the defaults), the pending pause and resume requests, when the
	// session paused, where its capture is (node or remote), whether its
	// last resume cold-booted, and the session it was forked from.
	VCPU              int
	VolumeGB          int
	PauseRequestedAt  *time.Time
	ResumeRequestedAt *time.Time
	PausedAt          *time.Time
	Durability        string
	ResumedCold       bool
	ForkedFrom        string
}

// SandboxAdmission is a session to admit. QueuedMax is the plan's
// AgentRunQueuedPerAccount, which queued sessions share with queued runs.
type SandboxAdmission struct {
	AccountID      string
	TenantID       string
	Kind           string
	Preset         string
	RAMMB          int
	NetworkMode    string
	IdleTimeoutSec int
	ExpiresAt      time.Time
	Labels         map[string]string
	QueuedMax      int
	// WorkspaceSnapshotID starts the session from a persisted workspace,
	// which must be the account's and ready.
	WorkspaceSnapshotID string
	// ImageRef and ImageJobID boot the session from a seller image: the
	// normalized reference and the image-run runner job caching it
	// (ADR-1161). agentd claims the session once the image is ready.
	ImageRef   string
	ImageJobID string
	// TemplateID boots the session from a sandbox template's newest
	// finished build; ImageRef is then template:<name>.
	TemplateID string
	// Browser boots the session with the sandbox browser running.
	Browser bool
	// Task makes the session a sandbox task (ADR-20261116182615 §3).
	Task *SandboxTask
	// Ports are exposed with the session (ExposeSandboxPort's rows).
	Ports []SandboxPort
	// EgressAllow and EgressDeny are the session's effective web-mode domain
	// policy (ADR-20261117091026 §2), fixed at admission.
	EgressAllow []string
	EgressDeny  []string
	// VCPU and VolumeGB size a persistent session (0: the defaults), and
	// PersistentMax is the plan's SandboxPersistentMax, checked under the
	// account's agent lock.
	VCPU          int
	VolumeGB      int
	PersistentMax int
}

// SandboxFilter narrows a session listing. Before pages backwards from a
// session.
type SandboxFilter struct {
	State  string
	Before *Sandbox
	Limit  int
}

// CreateSandbox inserts a queued session.
func (s *Store) CreateSandbox(ctx context.Context, a SandboxAdmission) (Sandbox, error) {
	labels, err := json.Marshal(nonNilLabels(a.Labels))
	if err != nil {
		return Sandbox{}, fmt.Errorf("encode labels: %w", err)
	}
	var id string
	err = s.locked(ctx, a.AccountID, func(tx pgx.Tx) error {
		id, err = s.insertSandbox(ctx, tx, a, labels)
		return err
	})
	if err != nil {
		return Sandbox{}, err
	}
	return s.Sandbox(ctx, a.AccountID, id)
}

// insertSandbox admits a session within the account's lock.
func (s *Store) insertSandbox(ctx context.Context, tx pgx.Tx, a SandboxAdmission, labels []byte) (string, error) {
	if err := s.checkTenant(ctx, tx, a.AccountID, a.TenantID); err != nil {
		return "", err
	}
	if a.WorkspaceSnapshotID != "" {
		if err := s.usableSnapshot(ctx, tx, a.AccountID, a.WorkspaceSnapshotID); err != nil {
			return "", err
		}
	}
	runs, err := s.q.CountQueuedAgentRuns(ctx, tx, a.AccountID)
	if err != nil {
		return "", fmt.Errorf("count queued runs: %w", err)
	}
	sessions, err := s.q.CountQueuedSandboxSessions(ctx, tx, a.AccountID)
	if err != nil {
		return "", fmt.Errorf("count queued sessions: %w", err)
	}
	if queued := int(runs + sessions); queued >= a.QueuedMax {
		return "", &LimitError{Limit: "AgentRunQueuedPerAccount", Max: a.QueuedMax, Observed: queued}
	}
	if a.Kind == SandboxPersistent {
		persistent, err := s.q.CountPersistentSandboxSessions(ctx, tx, a.AccountID)
		if err != nil {
			return "", fmt.Errorf("count persistent sessions: %w", err)
		}
		if int(persistent) >= a.PersistentMax {
			return "", &LimitError{Limit: "SandboxPersistentMax", Max: a.PersistentMax, Observed: int(persistent)}
		}
	}
	params := sqlc.InsertSandboxSessionParams{AccountID: a.AccountID, TenantID: optText(a.TenantID),
		Kind: a.Kind, Preset: a.Preset, RamMb: int32(a.RAMMB), NetworkMode: a.NetworkMode, IdleTimeoutSec: int32(a.IdleTimeoutSec),
		ExpiresAt: pgtype.Timestamptz{Time: a.ExpiresAt, Valid: true}, Labels: labels, WorkspaceSnapshotID: optText(a.WorkspaceSnapshotID),
		ImageRef: optText(a.ImageRef), ImageJobID: optText(a.ImageJobID), TemplateID: optText(a.TemplateID), Browser: a.Browser,
		EgressAllow: optRules(a.EgressAllow), EgressDeny: optRules(a.EgressDeny), Vcpu: optInt16(a.VCPU), VolumeGb: optInt(a.VolumeGB)}
	if a.Task != nil {
		params.TaskKind, params.TaskUrl, params.TaskMaxChars = optText(a.Task.Kind), optText(a.Task.URL), optInt(a.Task.MaxChars)
	}
	id, err := s.q.InsertSandboxSession(ctx, tx, params)
	if err != nil {
		return "", fmt.Errorf("insert sandbox session: %w", err)
	}
	for _, p := range a.Ports {
		if p.Port < 1 || p.Port > 65535 || (p.Visibility != SandboxPortPrivate && p.Visibility != SandboxPortPublic) {
			return "", fmt.Errorf("insert sandbox session: invalid port %d or visibility %q", p.Port, p.Visibility)
		}
		if _, err := s.q.UpsertSandboxPort(ctx, tx, sqlc.UpsertSandboxPortParams{SandboxID: id, AccountID: a.AccountID,
			Port: int32(p.Port), Visibility: p.Visibility}); err != nil {
			return "", fmt.Errorf("insert sandbox port: %w", err)
		}
	}
	return id, nil
}

// checkTenant requires an active platform tenant of the account; no tenant
// is the seller's own.
func (s *Store) checkTenant(ctx context.Context, tx pgx.Tx, accountID, tenantID string) error {
	if tenantID == "" {
		return nil
	}
	if !isUUID(tenantID) {
		return ErrTenantNotFound
	}
	status, err := s.q.GetTenantStatus(ctx, tx, sqlc.GetTenantStatusParams{TenantID: tenantID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTenantNotFound
	}
	if err != nil {
		return fmt.Errorf("read tenant: %w", err)
	}
	if status != "active" {
		return ErrTenantInactive
	}
	return nil
}

// Sandbox reads one session of the account.
func (s *Store) Sandbox(ctx context.Context, accountID, id string) (Sandbox, error) {
	if !isUUID(id) {
		return Sandbox{}, ErrNotFound
	}
	row, err := s.q.GetSandboxSession(ctx, s.pool, sqlc.GetSandboxSessionParams{SessionID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Sandbox{}, ErrNotFound
	}
	if err != nil {
		return Sandbox{}, fmt.Errorf("read sandbox session: %w", err)
	}
	return sandboxFrom(sqlc.ListSandboxSessionsRow(row))
}

// Sandboxes lists the account's sessions, newest first.
func (s *Store) Sandboxes(ctx context.Context, accountID string, f SandboxFilter) ([]Sandbox, error) {
	params := sqlc.ListSandboxSessionsParams{AccountID: accountID, State: optText(f.State), MaxRows: int32(f.Limit)}
	if b := f.Before; b != nil {
		params.BeforeCreatedAt = pgtype.Timestamptz{Time: b.CreatedAt, Valid: true}
		params.BeforeID = optText(b.ID)
	}
	rows, err := s.q.ListSandboxSessions(ctx, s.pool, params)
	if err != nil {
		return nil, fmt.Errorf("list sandbox sessions: %w", err)
	}
	out := make([]Sandbox, 0, len(rows))
	for _, row := range rows {
		sb, err := sandboxFrom(row)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, nil
}

// UpdateSandboxTimeouts changes a live session's idle timeout and end.
// ErrFinished: the session ended.
func (s *Store) UpdateSandboxTimeouts(ctx context.Context, accountID, id string, idleTimeoutSec int, expiresAt time.Time) (Sandbox, error) {
	if !isUUID(id) {
		return Sandbox{}, ErrNotFound
	}
	n, err := s.q.UpdateSandboxSessionTimeouts(ctx, s.pool, sqlc.UpdateSandboxSessionTimeoutsParams{IdleTimeoutSec: int32(idleTimeoutSec),
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true}, SessionID: id, AccountID: accountID})
	if err != nil {
		return Sandbox{}, fmt.Errorf("update sandbox session: %w", err)
	}
	sb, err := s.Sandbox(ctx, accountID, id)
	if err == nil && n == 0 {
		return sb, ErrFinished
	}
	return sb, err
}

// CloseSandbox closes a queued session, or asks agentd to close a claimed
// one. Closing an ended session changes nothing.
func (s *Store) CloseSandbox(ctx context.Context, accountID, id string) (Sandbox, error) {
	if !isUUID(id) {
		return Sandbox{}, ErrNotFound
	}
	params := sqlc.CloseQueuedSandboxSessionParams{SessionID: id, AccountID: accountID}
	if _, err := s.q.CloseQueuedSandboxSession(ctx, s.pool, params); err != nil {
		return Sandbox{}, fmt.Errorf("close sandbox session: %w", err)
	}
	n, err := s.q.RequestSandboxSessionClose(ctx, s.pool, sqlc.RequestSandboxSessionCloseParams(params))
	if err != nil {
		return Sandbox{}, fmt.Errorf("request sandbox session close: %w", err)
	}
	if n > 0 {
		s.nudgeSandboxSession(ctx, id)
	}
	return s.Sandbox(ctx, accountID, id)
}

// ErrSandboxNotPausable is a pause of a session that is not a running
// persistent one, or a resume of one that is not paused.
var ErrSandboxNotPausable = errors.New("sandbox session cannot be paused or resumed in its state")

// PauseSandbox asks agentd to pause a running persistent session
// (ADR-20261122132055 §1). A session already pausing or paused is answered
// as it is.
func (s *Store) PauseSandbox(ctx context.Context, accountID, id string) (Sandbox, error) {
	return s.requestPauseOrResume(ctx, accountID, id, true)
}

// ResumeSandbox asks agentd to resume a paused persistent session. A
// session already resuming or running is answered as it is.
func (s *Store) ResumeSandbox(ctx context.Context, accountID, id string) (Sandbox, error) {
	return s.requestPauseOrResume(ctx, accountID, id, false)
}

func (s *Store) requestPauseOrResume(ctx context.Context, accountID, id string, pause bool) (Sandbox, error) {
	if !isUUID(id) {
		return Sandbox{}, ErrNotFound
	}
	sb, err := s.Sandbox(ctx, accountID, id)
	if err != nil {
		return Sandbox{}, err
	}
	if sb.Kind != SandboxPersistent || SandboxTerminal(sb.State) || sb.CloseRequestedAt != nil {
		return sb, ErrSandboxNotPausable
	}
	var n int64
	if pause {
		switch sb.State {
		case SandboxPausing, SandboxPaused:
			return sb, nil
		}
		n, err = s.q.RequestSandboxSessionPause(ctx, s.pool, sqlc.RequestSandboxSessionPauseParams{SessionID: id, AccountID: accountID})
	} else {
		switch sb.State {
		case SandboxResuming, SandboxReady, SandboxFrozen, SandboxQueued, SandboxStarting:
			return sb, nil
		}
		n, err = s.q.RequestSandboxSessionResume(ctx, s.pool, sqlc.RequestSandboxSessionResumeParams{SessionID: id, AccountID: accountID})
	}
	if err != nil {
		return Sandbox{}, fmt.Errorf("request sandbox pause or resume: %w", err)
	}
	if n == 0 {
		return sb, ErrSandboxNotPausable
	}
	if pause {
		s.nudgeSandboxSession(ctx, id)
	}
	return s.Sandbox(ctx, accountID, id)
}

// nudgeSandboxSession wakes the agentd replica holding session id to renew
// its lease now (db.NotifySandboxSessionRequest). Best effort: the request
// is recorded, and the next renewal sees it if the nudge is lost.
func (s *Store) nudgeSandboxSession(ctx context.Context, id string) {
	_ = s.q.NotifySandboxSessionRequest(ctx, s.pool, id)
}

// SandboxSessionRequests streams the ids of sessions apid recorded a pause,
// close or fork request on, for agentd to renew their leases at once.
func (s *Store) SandboxSessionRequests(ctx context.Context, log *slog.Logger) (<-chan string, error) {
	notes, err := db.SubscribeWithReconnect(ctx, s.pool, []string{db.NotifySandboxSessionRequest}, log)
	if err != nil {
		return nil, fmt.Errorf("listen for sandbox session requests: %w", err)
	}
	ids := make(chan string, 64)
	go func() {
		defer close(ids)
		for n := range notes {
			if n.Channel != db.NotifySandboxSessionRequest || !isUUID(n.Payload) {
				continue
			}
			select {
			case ids <- n.Payload:
			default: // a busy consumer catches up at the next renewal
			}
		}
	}()
	return ids, nil
}

func sandboxFrom(row sqlc.ListSandboxSessionsRow) (Sandbox, error) {
	sb := Sandbox{ID: row.ID, AccountID: row.AccountID, TenantID: row.TenantID, Kind: row.Kind, Preset: row.Preset,
		RAMMB: int(row.RamMb), NetworkMode: row.NetworkMode, State: row.State, IdleTimeoutSec: int(row.IdleTimeoutSec),
		ExpiresAt: row.ExpiresAt.Time.UTC(), OutcomeCode: row.OutcomeCode.String, OutcomeMessage: row.OutcomeMessage.String,
		CloseRequestedAt: optTime(row.CloseRequestedAt), LastActivityAt: row.LastActivityAt.Time.UTC(),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), ClosedAt: optTime(row.ClosedAt),
		NodeID: row.NodeID, InstanceID: row.InstanceID, WorkspaceSnapshotID: row.WorkspaceSnapshotID, ImageRef: row.ImageRef, Browser: row.Browser,
		EgressAllow: row.EgressAllow, EgressDeny: row.EgressDeny, VCPU: int(row.Vcpu), VolumeGB: int(row.VolumeGb),
		PauseRequestedAt: optTime(row.PauseRequestedAt), ResumeRequestedAt: optTime(row.ResumeRequestedAt), PausedAt: optTime(row.PausedAt),
		Durability: row.Durability, ResumedCold: row.ResumedCold, ForkedFrom: row.ForkedFrom}
	if err := json.Unmarshal(row.Labels, &sb.Labels); err != nil {
		return Sandbox{}, fmt.Errorf("decode sandbox labels: %w", err)
	}
	return sb, nil
}

func nonNilLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return map[string]string{}
	}
	return labels
}

// optRules is a policy list as stored: NULL when empty, so a session
// without a policy reads as one.
func optRules(rules []string) []string {
	if len(rules) == 0 {
		return nil
	}
	return rules
}

// SandboxEgressPolicy is an account's default web-mode domain policy
// (ADR-20261117091026 §2).
type SandboxEgressPolicy struct {
	Allow     []string
	Deny      []string
	UpdatedAt time.Time
}

// SandboxEgressPolicy reads the account's default policy; an account without
// one has the empty policy.
func (s *Store) SandboxEgressPolicy(ctx context.Context, accountID string) (SandboxEgressPolicy, error) {
	row, err := s.q.GetSandboxEgressPolicy(ctx, s.pool, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxEgressPolicy{Allow: []string{}, Deny: []string{}}, nil
	}
	if err != nil {
		return SandboxEgressPolicy{}, fmt.Errorf("read sandbox egress policy: %w", err)
	}
	return SandboxEgressPolicy{Allow: row.AllowRules, Deny: row.DenyRules, UpdatedAt: row.UpdatedAt.Time.UTC()}, nil
}

// PutSandboxEgressPolicy replaces the account's default policy. Sessions
// already admitted keep theirs.
func (s *Store) PutSandboxEgressPolicy(ctx context.Context, accountID string, allow, deny []string) (SandboxEgressPolicy, error) {
	if allow == nil {
		allow = []string{}
	}
	if deny == nil {
		deny = []string{}
	}
	row, err := s.q.PutSandboxEgressPolicy(ctx, s.pool, sqlc.PutSandboxEgressPolicyParams{AccountID: accountID, AllowRules: allow, DenyRules: deny})
	if err != nil {
		return SandboxEgressPolicy{}, fmt.Errorf("put sandbox egress policy: %w", err)
	}
	return SandboxEgressPolicy{Allow: row.AllowRules, Deny: row.DenyRules, UpdatedAt: row.UpdatedAt.Time.UTC()}, nil
}
