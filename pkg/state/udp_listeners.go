package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// ErrInvalidUDPListener identifies a listener that cannot be exposed by the
// raw UDP edge. The migration repeats these checks at the database boundary.
var ErrInvalidUDPListener = errors.New("state: invalid UDP listener")

const (
	UDPListenerPublicPortMin = api.UDPListenerPublicPortMin
	UDPListenerPublicPortMax = api.UDPListenerPublicPortMax
	udpListenerPublicPortMin = UDPListenerPublicPortMin
	udpListenerPublicPortMax = UDPListenerPublicPortMax
)

func normalizeUDPListener(in UDPListener) (UDPListener, error) {
	in.ListenerName = strings.ToLower(strings.TrimSpace(in.ListenerName))
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	if in.Protocol == "" {
		in.Protocol = "udp"
	}
	if !validUDPListenerName(in.ListenerName) {
		return UDPListener{}, fmt.Errorf("%w: listener name %q is not a DNS-safe token", ErrInvalidUDPListener, in.ListenerName)
	}
	if in.GuestPort < 1 || in.GuestPort > 65535 {
		return UDPListener{}, fmt.Errorf("%w: guest port %d is outside 1..65535", ErrInvalidUDPListener, in.GuestPort)
	}
	if in.PublicPort < udpListenerPublicPortMin || in.PublicPort > udpListenerPublicPortMax {
		return UDPListener{}, fmt.Errorf("%w: public port %d is outside %d..%d", ErrInvalidUDPListener, in.PublicPort, udpListenerPublicPortMin, udpListenerPublicPortMax)
	}
	if in.Protocol != "udp" {
		return UDPListener{}, fmt.Errorf("%w: protocol %q is not udp", ErrInvalidUDPListener, in.Protocol)
	}
	return in, nil
}

func validUDPListenerName(name string) bool {
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

type udpListenerScanner interface {
	Scan(dest ...any) error
}

func scanUDPListener(row udpListenerScanner) (UDPListener, error) {
	var listener UDPListener
	if err := row.Scan(
		&listener.ID, &listener.AppID, &listener.AccountID,
		&listener.ListenerName, &listener.GuestPort, &listener.PublicPort,
		&listener.Protocol, &listener.Enabled, &listener.CreatedAt, &listener.UpdatedAt,
	); err != nil {
		return UDPListener{}, err
	}
	return listener, nil
}

const udpListenerColumns = `
    id, app_id, account_id, listener_name, guest_port, public_port,
    protocol, enabled, created_at, updated_at`

func (s *PgStore) CreateUDPListener(ctx context.Context, in UDPListener) (UDPListener, error) {
	in, err := normalizeUDPListener(in)
	if err != nil {
		return UDPListener{}, err
	}
	if in.AppID == "" || in.AccountID == "" {
		return UDPListener{}, fmt.Errorf("%w: app and account IDs are required", ErrInvalidUDPListener)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: begin UDP listener tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var accountID string
	if err := tx.QueryRow(ctx, `
		select account_id from apps where id = $1 and status <> 'deleted' for update
	`, in.AppID).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UDPListener{}, ErrNotFound
		}
		return UDPListener{}, fmt.Errorf("state: lock app for UDP listener: %w", err)
	}
	if accountID != in.AccountID {
		return UDPListener{}, ErrNotFound
	}
	if in.ID == "" {
		in.ID = newID()
	}
	row := tx.QueryRow(ctx, `
		insert into app_udp_listeners
			(id, app_id, account_id, listener_name, guest_port, public_port, protocol, enabled)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		returning `+udpListenerColumns,
		in.ID, in.AppID, in.AccountID, in.ListenerName, in.GuestPort,
		in.PublicPort, in.Protocol, in.Enabled)
	listener, err := scanUDPListener(row)
	if err != nil {
		if isUniqueViolation(err) {
			return UDPListener{}, ErrConflict
		}
		return UDPListener{}, fmt.Errorf("state: insert UDP listener: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UDPListener{}, fmt.Errorf("state: commit UDP listener: %w", err)
	}
	return listener, nil
}

func (s *PgStore) UDPListenerByID(ctx context.Context, id string) (UDPListener, error) {
	row := s.pool.QueryRow(ctx, `
		select `+udpListenerColumns+` from app_udp_listeners where id = $1
	`, id)
	listener, err := scanUDPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: read UDP listener: %w", err)
	}
	return listener, nil
}

func (s *PgStore) UDPListenerByAppAndName(ctx context.Context, appID, listenerName string) (UDPListener, error) {
	listenerName = strings.ToLower(strings.TrimSpace(listenerName))
	row := s.pool.QueryRow(ctx, `
		select `+udpListenerColumns+` from app_udp_listeners
		 where app_id = $1 and listener_name = $2
	`, appID, listenerName)
	listener, err := scanUDPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: read UDP listener by app/name: %w", err)
	}
	return listener, nil
}

func (s *PgStore) UDPListenerByPublicPort(ctx context.Context, publicPort int) (UDPListener, error) {
	row := s.pool.QueryRow(ctx, `
		select `+udpListenerColumns+` from app_udp_listeners
		 where public_port = $1 and enabled and exists (select 1 from apps a where a.id = app_udp_listeners.app_id and a.account_id = app_udp_listeners.account_id and a.status <> 'deleted')
	`, publicPort)
	listener, err := scanUDPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: read UDP listener by public port: %w", err)
	}
	return listener, nil
}

func (s *PgStore) ListUDPListenersForApp(ctx context.Context, appID string) ([]UDPListener, error) {
	rows, err := s.pool.Query(ctx, `
		select `+udpListenerColumns+` from app_udp_listeners
		 where app_id = $1 order by created_at desc, id desc
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list UDP listeners: %w", err)
	}
	defer rows.Close()
	listeners := make([]UDPListener, 0)
	for rows.Next() {
		listener, err := scanUDPListener(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan UDP listener: %w", err)
		}
		listeners = append(listeners, listener)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate UDP listeners: %w", err)
	}
	return listeners, nil
}

// ListEnabledUDPListeners returns the listener identities that the raw UDP
// edge should currently bind. It is intentionally separate from
// UDPListenerStore so existing narrow store adapters do not need to grow a
// fleet-wide listing method just to adopt udpd.
func (s *PgStore) ListEnabledUDPListeners(ctx context.Context) ([]UDPListener, error) {
	rows, err := s.pool.Query(ctx, `
		select `+udpListenerColumns+` from app_udp_listeners
		 where enabled and exists (select 1 from apps a where a.id = app_udp_listeners.app_id and a.account_id = app_udp_listeners.account_id and a.status <> 'deleted') order by public_port asc
	`)
	if err != nil {
		return nil, fmt.Errorf("state: list enabled UDP listeners: %w", err)
	}
	defer rows.Close()
	listeners := make([]UDPListener, 0)
	for rows.Next() {
		listener, err := scanUDPListener(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan enabled UDP listener: %w", err)
		}
		listeners = append(listeners, listener)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate enabled UDP listeners: %w", err)
	}
	return listeners, nil
}

func (s *PgStore) SetUDPListenerEnabled(ctx context.Context, id string, enabled bool) (UDPListener, error) {
	row := s.pool.QueryRow(ctx, `
		update app_udp_listeners set enabled = $2, updated_at = now()
		 where id = $1
		 returning `+udpListenerColumns,
		id, enabled)
	listener, err := scanUDPListener(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: update UDP listener: %w", err)
	}
	return listener, nil
}

func (s *PgStore) DeleteUDPListener(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from app_udp_listeners where id = $1`, id)
	if err != nil {
		return fmt.Errorf("state: delete UDP listener: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
