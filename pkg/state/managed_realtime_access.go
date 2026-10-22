package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type ManagedRealtimeAccess struct {
	DeniedChannels        []string   `json:"denied_channels"`
	SessionsRevokedBefore *time.Time `json:"sessions_revoked_before,omitempty"`
	Revision              int64      `json:"revision"`
	UpdatedAt             time.Time  `json:"updated_at"`
}
type ManagedRealtimeAccessStore interface {
	ReadManagedRealtimeAccess(context.Context, string, string) (ManagedRealtimeAccess, error)
	ChangeManagedRealtimeAccess(context.Context, string, string, string, string) (ManagedRealtimeAccess, error)
}

type ManagedRealtimeAccessLimitError struct{ Limit, Observed int }

func (e *ManagedRealtimeAccessLimitError) Error() string {
	return "state: realtime access preview capacity reached"
}
func (e *ManagedRealtimeAccessLimitError) Unwrap() error { return ErrManagedRealtimeHistoryLimit }

func validateRealtimeAccess(principal, action, channel string) error {
	if api.ValidateRealtimePrincipal(principal) != nil {
		return ErrInvalidArgument
	}
	switch action {
	case "revoke_sessions":
		if channel != "" {
			return ErrInvalidArgument
		}
	case "revoke_channel", "restore_channel":
		if strings.ContainsRune(channel, '\x00') || validateManagedRealtimeHistoryRequest("endpoint", channel) != nil {
			return ErrInvalidArgument
		}
	default:
		return ErrInvalidArgument
	}
	return nil
}

func changeRealtimeAccess(row ManagedRealtimeAccess, action, channel string, now time.Time) (ManagedRealtimeAccess, error) {
	channels := map[string]bool{}
	for _, c := range row.DeniedChannels {
		channels[c] = true
	}
	switch action {
	case "revoke_sessions":
		if row.SessionsRevokedBefore == nil || now.After(*row.SessionsRevokedBefore) {
			row.SessionsRevokedBefore = &now
		}
	case "revoke_channel":
		channels[channel] = true
	case "restore_channel":
		delete(channels, channel)
	}
	if len(channels) > api.RealtimeAccessMaxDeniedChannels {
		return row, &ManagedRealtimeAccessLimitError{Limit: api.RealtimeAccessMaxDeniedChannels, Observed: len(channels)}
	}
	row.DeniedChannels = []string{}
	for c := range channels {
		row.DeniedChannels = append(row.DeniedChannels, c)
	}
	sort.Strings(row.DeniedChannels)
	row.Revision++
	row.UpdatedAt = now
	return row, nil
}

func readRealtimeAccessPG(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, ep, principal string) (ManagedRealtimeAccess, error) {
	row := ManagedRealtimeAccess{DeniedChannels: []string{}}
	var raw []byte
	err := db.QueryRow(ctx, `SELECT denied_channels,sessions_revoked_before,revision,updated_at FROM managed_realtime_principal_access WHERE endpoint_id=$1 AND principal=$2`, ep, principal).Scan(&raw, &row.SessionsRevokedBefore, &row.Revision, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, nil
	}
	if err == nil {
		err = json.Unmarshal(raw, &row.DeniedChannels)
	}
	return row, err
}

func (s *PgStore) ReadManagedRealtimeAccess(ctx context.Context, ep, principal string) (ManagedRealtimeAccess, error) {
	if api.ValidateRealtimePrincipal(principal) != nil {
		return ManagedRealtimeAccess{}, ErrInvalidArgument
	}
	return readRealtimeAccessPG(ctx, s.pool, ep, principal)
}

func (s *PgStore) ChangeManagedRealtimeAccess(ctx context.Context, ep, principal, action, channel string) (ManagedRealtimeAccess, error) {
	if err := validateRealtimeAccess(principal, action, channel); err != nil {
		return ManagedRealtimeAccess{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedRealtimeAccess{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The handler decides which auth modes may use access controls: OIDC
	// endpoints, plus any endpoint once ticket preview is enabled (ADR-1117).
	var endpoint string
	if err = tx.QueryRow(ctx, `SELECT id FROM managed_realtime_endpoints WHERE id=$1 FOR UPDATE`, ep).Scan(&endpoint); err != nil {
		return ManagedRealtimeAccess{}, mapErr(err)
	}
	row, err := readRealtimeAccessPG(ctx, tx, ep, principal)
	if err != nil {
		return row, err
	}
	if row.Revision == 0 {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM managed_realtime_principal_access WHERE endpoint_id=$1`, ep).Scan(&count); err != nil {
			return row, err
		}
		if count >= api.RealtimeAccessMaxPrincipalsPerEndpoint {
			return row, &ManagedRealtimeAccessLimitError{Limit: api.RealtimeAccessMaxPrincipalsPerEndpoint, Observed: count + 1}
		}
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return row, err
	}
	row, err = changeRealtimeAccess(row, action, channel, now)
	if err != nil {
		return row, err
	}
	raw, _ := json.Marshal(row.DeniedChannels)
	_, err = tx.Exec(ctx, `INSERT INTO managed_realtime_principal_access(endpoint_id,principal,denied_channels,sessions_revoked_before,revision,updated_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(endpoint_id,principal) DO UPDATE SET denied_channels=excluded.denied_channels,sessions_revoked_before=excluded.sessions_revoked_before,revision=excluded.revision,updated_at=excluded.updated_at`, ep, principal, raw, row.SessionsRevokedBefore, row.Revision, row.UpdatedAt)
	if err != nil {
		return row, err
	}
	return row, tx.Commit(ctx)
}
