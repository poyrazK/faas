package agents

// Exposed sandbox ports (ADR-20261027090013 §11). apid adds and removes a
// live session's ports; gatewayd-internal reads a session's route, its
// placement and ports, into a short-lived cache when it serves a preview
// host.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// Port visibilities.
const (
	SandboxPortPrivate = "private"
	SandboxPortPublic  = "public"
)

// ErrSandboxNotLive is a session that has ended or is closing.
var ErrSandboxNotLive = errors.New("agents: sandbox session is not live")

// SandboxPort is one exposed port of a session.
type SandboxPort struct {
	Port       int
	Visibility string
	CreatedAt  time.Time
}

// ExposeSandboxPort exposes port on a live session of the account, or
// changes its visibility. max is the plan's SandboxExposedPortsMax: a
// session at it is a *LimitError. ErrNotFound: no such session;
// ErrSandboxNotLive: it has ended or is closing.
func (s *Store) ExposeSandboxPort(ctx context.Context, accountID, sandboxID string, port int, visibility string, max int) (SandboxPort, error) {
	if !isUUID(sandboxID) {
		return SandboxPort{}, ErrNotFound
	}
	if port < 1 || port > 65535 || (visibility != SandboxPortPrivate && visibility != SandboxPortPublic) {
		return SandboxPort{}, fmt.Errorf("expose sandbox port: invalid port %d or visibility %q", port, visibility)
	}
	var out SandboxPort
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lockLiveSandbox(ctx, tx, accountID, sandboxID); err != nil {
			return err
		}
		others, err := s.q.CountSandboxPorts(ctx, tx, sqlc.CountSandboxPortsParams{SandboxID: sandboxID, Port: int32(port)})
		if err != nil {
			return fmt.Errorf("count sandbox ports: %w", err)
		}
		if int(others) >= max {
			return &LimitError{Limit: "SandboxExposedPortsMax", Max: max, Observed: int(others)}
		}
		row, err := s.q.UpsertSandboxPort(ctx, tx, sqlc.UpsertSandboxPortParams{SandboxID: sandboxID, AccountID: accountID,
			Port: int32(port), Visibility: visibility})
		if err != nil {
			return fmt.Errorf("upsert sandbox port: %w", err)
		}
		out = SandboxPort{Port: int(row.Port), Visibility: row.Visibility, CreatedAt: row.CreatedAt.Time.UTC()}
		return nil
	})
	return out, err
}

// lockLiveSandbox holds a live session's row for the transaction.
func (s *Store) lockLiveSandbox(ctx context.Context, tx pgx.Tx, accountID, sandboxID string) error {
	_, err := s.q.LockLiveSandboxSession(ctx, tx, sqlc.LockLiveSandboxSessionParams{SessionID: sandboxID, AccountID: accountID})
	if !errors.Is(err, pgx.ErrNoRows) {
		if err != nil {
			return fmt.Errorf("lock sandbox session: %w", err)
		}
		return nil
	}
	if _, err := s.Sandbox(ctx, accountID, sandboxID); err != nil {
		return err
	}
	return ErrSandboxNotLive
}

// SandboxPorts lists a session's exposed ports. A session the account does
// not have lists none.
func (s *Store) SandboxPorts(ctx context.Context, accountID, sandboxID string) ([]SandboxPort, error) {
	if !isUUID(sandboxID) {
		return nil, nil
	}
	rows, err := s.q.ListSandboxPorts(ctx, s.pool, sqlc.ListSandboxPortsParams{SandboxID: sandboxID, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("list sandbox ports: %w", err)
	}
	out := make([]SandboxPort, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxPort{Port: int(r.Port), Visibility: r.Visibility, CreatedAt: r.CreatedAt.Time.UTC()})
	}
	return out, nil
}

// UnexposeSandboxPort removes an exposed port. ErrNotFound: it is not exposed.
func (s *Store) UnexposeSandboxPort(ctx context.Context, accountID, sandboxID string, port int) error {
	if !isUUID(sandboxID) {
		return ErrNotFound
	}
	n, err := s.q.DeleteSandboxPort(ctx, s.pool, sqlc.DeleteSandboxPortParams{SandboxID: sandboxID, AccountID: accountID, Port: int32(port)})
	if err != nil {
		return fmt.Errorf("delete sandbox port: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SandboxPreviewRoute is what a gateway needs to serve a session's preview
// hosts.
type SandboxPreviewRoute struct {
	AccountID  string
	Plan       string
	State      string
	NodeID     string
	InstanceID string
	// Ports maps each exposed port to its visibility.
	Ports map[int]string
}

// PreviewRoute reads a session's route. ErrNotFound: no such session.
func (s *Store) PreviewRoute(ctx context.Context, sandboxID string) (SandboxPreviewRoute, error) {
	if !isUUID(sandboxID) {
		return SandboxPreviewRoute{}, ErrNotFound
	}
	row, err := s.q.GetSandboxPreviewRoute(ctx, s.pool, sandboxID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxPreviewRoute{}, ErrNotFound
	}
	if err != nil {
		return SandboxPreviewRoute{}, fmt.Errorf("read sandbox preview route: %w", err)
	}
	r := SandboxPreviewRoute{AccountID: row.AccountID, Plan: row.Plan, State: row.State, NodeID: row.NodeID,
		InstanceID: row.InstanceID, Ports: map[int]string{}}
	for _, pair := range strings.Split(row.Ports, ",") {
		p, vis, ok := strings.Cut(pair, ":")
		if n, err := strconv.Atoi(p); ok && err == nil {
			r.Ports[n] = vis
		}
	}
	return r, nil
}
