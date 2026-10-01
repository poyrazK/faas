package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrInvalidUDPListener identifies a listener that cannot be exposed by the
// raw UDP edge. The migration repeats these checks at the database boundary.
var ErrInvalidUDPListener = errors.New("state: invalid UDP listener")

var ErrUDPListenerLimit = errors.New("state: UDP listener reservation limit")

type UDPListenerLimitError struct{ Limit, Observed int }

func (e *UDPListenerLimitError) Error() string {
	return fmt.Sprintf("%s: observed %d, maximum %d", ErrUDPListenerLimit, e.Observed, e.Limit)
}
func (e *UDPListenerLimitError) Unwrap() error { return ErrUDPListenerLimit }

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

func udpListenerFromSQL(row sqlc.AppUdpListener) UDPListener {
	return UDPListener{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), ListenerName: row.ListenerName, GuestPort: int(row.GuestPort), PublicPort: int(row.PublicPort), Protocol: row.Protocol, Enabled: row.Enabled, CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt)}
}

func udpListenersFromSQL(rows []sqlc.AppUdpListener) []UDPListener {
	out := make([]UDPListener, 0, len(rows))
	for _, row := range rows {
		out = append(out, udpListenerFromSQL(row))
	}
	return out
}

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
	q := sqlc.New()
	accountID, err := q.LockUDPListenerAppOwner(ctx, tx, in.AppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: lock app for UDP listener: %w", err)
	}
	if accountID != in.AccountID {
		return UDPListener{}, ErrNotFound
	}
	// The app row remains locked through insertion, serializing count and
	// create even across concurrent apid processes. Disabled rows count too.
	if _, err := q.UDPListenerByAppAndName(ctx, tx, sqlc.UDPListenerByAppAndNameParams{AppID: in.AppID, ListenerName: in.ListenerName}); err == nil {
		return UDPListener{}, ErrConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, fmt.Errorf("state: check UDP listener name: %w", err)
	}
	count, err := q.CountUDPListenersForApp(ctx, tx, in.AppID)
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: count UDP listener reservations: %w", err)
	}
	if count >= int64(api.UDPListenerReservationsPerAppMax) {
		return UDPListener{}, &UDPListenerLimitError{Limit: api.UDPListenerReservationsPerAppMax, Observed: int(count) + 1}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	row, err := q.CreateUDPListener(ctx, tx, sqlc.CreateUDPListenerParams{ID: in.ID, AppID: in.AppID, AccountID: in.AccountID, ListenerName: in.ListenerName, GuestPort: int32(in.GuestPort), PublicPort: int32(in.PublicPort), Protocol: in.Protocol, Enabled: in.Enabled})
	if isUniqueViolation(err) {
		return UDPListener{}, ErrConflict
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: insert UDP listener: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UDPListener{}, fmt.Errorf("state: commit UDP listener: %w", err)
	}
	return udpListenerFromSQL(row), nil
}

func (s *PgStore) UDPListenerByID(ctx context.Context, id string) (UDPListener, error) {

	row, err := sqlc.New().UDPListenerByID(ctx, s.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: read UDP listener: %w", err)
	}
	return udpListenerFromSQL(row), nil
}

func (s *PgStore) UDPListenerByAppAndName(ctx context.Context, appID, listenerName string) (UDPListener, error) {

	row, err := sqlc.New().UDPListenerByAppAndName(ctx, s.pool, sqlc.UDPListenerByAppAndNameParams{AppID: appID, ListenerName: strings.ToLower(strings.TrimSpace(listenerName))})
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: read UDP listener by app/name: %w", err)
	}
	return udpListenerFromSQL(row), nil
}

func (s *PgStore) UDPListenerByPublicPort(ctx context.Context, publicPort int) (UDPListener, error) {
	if publicPort < UDPListenerPublicPortMin || publicPort > UDPListenerPublicPortMax {
		return UDPListener{}, ErrNotFound
	}
	row, err := sqlc.New().UDPListenerByPublicPort(ctx, s.pool, int32(publicPort))
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: read UDP listener by public port: %w", err)
	}
	return udpListenerFromSQL(row), nil
}

func (s *PgStore) SetUDPListenerEnabled(ctx context.Context, id string, enabled bool) (UDPListener, error) {

	row, err := sqlc.New().SetUDPListenerEnabled(ctx, s.pool, sqlc.SetUDPListenerEnabledParams{ID: id, Enabled: enabled})
	if errors.Is(err, pgx.ErrNoRows) {
		return UDPListener{}, ErrNotFound
	}
	if err != nil {
		return UDPListener{}, fmt.Errorf("state: update UDP listener: %w", err)
	}
	return udpListenerFromSQL(row), nil
}

func (s *PgStore) ListUDPListenersForApp(ctx context.Context, appID string) ([]UDPListener, error) {
	rows, err := sqlc.New().ListUDPListenersForApp(ctx, s.pool, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list UDP listeners: %w", err)
	}
	return udpListenersFromSQL(rows), nil
}

// ListEnabledUDPListeners supplies current edge bindings without widening
// the optional customer-intent store interface. Deleted or foreign-owned apps
// cannot contribute a public binding.
func (s *PgStore) ListEnabledUDPListeners(ctx context.Context) ([]UDPListener, error) {
	rows, err := sqlc.New().ListEnabledUDPListeners(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list enabled UDP listeners: %w", err)
	}
	return udpListenersFromSQL(rows), nil
}

func (s *PgStore) DeleteUDPListener(ctx context.Context, id string) error {
	count, err := sqlc.New().DeleteUDPListener(ctx, s.pool, id)
	if err != nil {
		return fmt.Errorf("state: delete UDP listener: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
