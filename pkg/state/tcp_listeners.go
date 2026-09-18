package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrInvalidTCPListener identifies a listener that cannot be exposed by the
// raw TCP edge. The migration repeats these checks at the database boundary.
var ErrInvalidTCPListener = errors.New("state: invalid TCP listener")

const (
	tcpListenerPublicPortMin = 40000
	tcpListenerPublicPortMax = 49999
)

func normalizeTCPListener(in TCPListener) (TCPListener, error) {
	in.ListenerName = strings.ToLower(strings.TrimSpace(in.ListenerName))
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	if in.Protocol == "" {
		in.Protocol = "tcp"
	}
	if !validTCPListenerName(in.ListenerName) {
		return TCPListener{}, fmt.Errorf("%w: listener name %q is not a DNS-safe token", ErrInvalidTCPListener, in.ListenerName)
	}
	if in.GuestPort < 1 || in.GuestPort > 65535 {
		return TCPListener{}, fmt.Errorf("%w: guest port %d is outside 1..65535", ErrInvalidTCPListener, in.GuestPort)
	}
	if in.PublicPort < tcpListenerPublicPortMin || in.PublicPort > tcpListenerPublicPortMax {
		return TCPListener{}, fmt.Errorf("%w: public port %d is outside %d..%d", ErrInvalidTCPListener, in.PublicPort, tcpListenerPublicPortMin, tcpListenerPublicPortMax)
	}
	if in.Protocol != "tcp" {
		return TCPListener{}, fmt.Errorf("%w: protocol %q is not tcp", ErrInvalidTCPListener, in.Protocol)
	}
	return in, nil
}

func validTCPListenerName(name string) bool {
	if len(name) == 0 || len(name) > 31 {
		return false
	}
	for i, char := range []byte(name) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (i > 0 && char == '-') {
			continue
		}
		return false
	}
	return true
}

type tcpListenerScanner interface {
	Scan(dest ...any) error
}

func scanTCPListener(row tcpListenerScanner) (TCPListener, error) {
	var listener TCPListener
	if err := row.Scan(
		&listener.ID, &listener.AppID, &listener.AccountID,
		&listener.ListenerName, &listener.GuestPort, &listener.PublicPort,
		&listener.Protocol, &listener.Enabled, &listener.CreatedAt, &listener.UpdatedAt,
	); err != nil {
		return TCPListener{}, err
	}
	return listener, nil
}

const tcpListenerColumns = `
    id, app_id, account_id, listener_name, guest_port, public_port,
    protocol, enabled, created_at, updated_at`

func (s *PgStore) CreateTCPListener(ctx context.Context, in TCPListener) (TCPListener, error) {
	in, err := normalizeTCPListener(in)
	if err != nil {
		return TCPListener{}, err
	}
	if in.AppID == "" || in.AccountID == "" {
		return TCPListener{}, fmt.Errorf("%w: app and account IDs are required", ErrInvalidTCPListener)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TCPListener{}, fmt.Errorf("state: begin TCP listener tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var accountID string
	if err := tx.QueryRow(ctx, `
		select account_id from apps where id = $1 and status <> 'deleted' for update
	`, in.AppID).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TCPListener{}, ErrNotFound
		}
		return TCPListener{}, fmt.Errorf("state: lock app for TCP listener: %w", err)
	}
	if accountID != in.AccountID {
		return TCPListener{}, ErrNotFound
	}
	if in.ID == "" {
		in.ID = newID()
	}
	row := tx.QueryRow(ctx, `
		insert into app_tcp_listeners
			(id, app_id, account_id, listener_name, guest_port, public_port, protocol, enabled)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		returning `+tcpListenerColumns,
		in.ID, in.AppID, in.AccountID, in.ListenerName, in.GuestPort,
		in.PublicPort, in.Protocol, in.Enabled)
	listener, err := scanTCPListener(row)
	if err != nil {
		if isUniqueViolation(err) {
			return TCPListener{}, ErrConflict
		}
		return TCPListener{}, fmt.Errorf("state: insert TCP listener: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TCPListener{}, fmt.Errorf("state: commit TCP listener: %w", err)
	}
	return listener, nil
}

func (s *PgStore) TCPListenerByID(ctx context.Context, id string) (TCPListener, error) {
	row := s.pool.QueryRow(ctx, `
		select `+tcpListenerColumns+` from app_tcp_listeners where id = $1
	`, id)
	listener, err := scanTCPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TCPListener{}, ErrNotFound
	}
	if err != nil {
		return TCPListener{}, fmt.Errorf("state: read TCP listener: %w", err)
	}
	return listener, nil
}

func (s *PgStore) TCPListenerByAppAndName(ctx context.Context, appID, listenerName string) (TCPListener, error) {
	listenerName = strings.ToLower(strings.TrimSpace(listenerName))
	row := s.pool.QueryRow(ctx, `
		select `+tcpListenerColumns+` from app_tcp_listeners
		 where app_id = $1 and listener_name = $2
	`, appID, listenerName)
	listener, err := scanTCPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TCPListener{}, ErrNotFound
	}
	if err != nil {
		return TCPListener{}, fmt.Errorf("state: read TCP listener by app/name: %w", err)
	}
	return listener, nil
}

func (s *PgStore) TCPListenerByPublicPort(ctx context.Context, publicPort int) (TCPListener, error) {
	row := s.pool.QueryRow(ctx, `
		select `+tcpListenerColumns+` from app_tcp_listeners
		 where public_port = $1 and enabled
	`, publicPort)
	listener, err := scanTCPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TCPListener{}, ErrNotFound
	}
	if err != nil {
		return TCPListener{}, fmt.Errorf("state: read TCP listener by public port: %w", err)
	}
	return listener, nil
}

func (s *PgStore) ListTCPListenersForApp(ctx context.Context, appID string) ([]TCPListener, error) {
	rows, err := s.pool.Query(ctx, `
		select `+tcpListenerColumns+` from app_tcp_listeners
		 where app_id = $1 order by created_at desc, id desc
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list TCP listeners: %w", err)
	}
	defer rows.Close()
	listeners := make([]TCPListener, 0)
	for rows.Next() {
		listener, err := scanTCPListener(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan TCP listener: %w", err)
		}
		listeners = append(listeners, listener)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate TCP listeners: %w", err)
	}
	return listeners, nil
}

func (s *PgStore) SetTCPListenerEnabled(ctx context.Context, id string, enabled bool) (TCPListener, error) {
	row := s.pool.QueryRow(ctx, `
		update app_tcp_listeners set enabled = $2, updated_at = now()
		 where id = $1
		 returning `+tcpListenerColumns,
		id, enabled)
	listener, err := scanTCPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TCPListener{}, ErrNotFound
	}
	if err != nil {
		return TCPListener{}, fmt.Errorf("state: update TCP listener: %w", err)
	}
	return listener, nil
}

func (s *PgStore) DeleteTCPListener(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from app_tcp_listeners where id = $1`, id)
	if err != nil {
		return fmt.Errorf("state: delete TCP listener: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
