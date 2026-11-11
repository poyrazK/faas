package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// SessionCredentialEvidence is the qualification proof for the direct
// session credential mode (ADR-20261110120000). A session binding exists for
// clients that need one server session per client connection, so the probe
// shows exactly that: the material names a direct endpoint, which delivery
// and the probe use; a session-scoped advisory lock outlives the statement
// that took it; and a LISTEN on the connection receives a NOTIFY. Re-issue returns the same
// password and revocation ends the login.
type SessionCredentialEvidence struct {
	DirectEndpoint        bool `json:"direct_endpoint"`
	SessionStateHeld      bool `json:"session_state_held"`
	NotificationDelivered bool `json:"notification_delivered"`
	PasswordRecovered     bool `json:"password_recovered"`
	Revoked               bool `json:"revoked"`
}

func (e SessionCredentialEvidence) Validate() error {
	if !e.DirectEndpoint || !e.SessionStateHeld || !e.NotificationDelivered || !e.PasswordRecovered || !e.Revoked {
		return ErrUnavailable
	}
	return nil
}

// SessionCredentialProber is implemented by providers that advertise the
// session credential mode.
type SessionCredentialProber interface {
	ProbeSessionCredentials(context.Context, string) (SessionCredentialEvidence, error)
}

// sessionProbeLockKey is an arbitrary advisory lock key reserved for the
// qualification probe; qualification runs on an isolated database.
const sessionProbeLockKey int64 = 0x67726567616c65

// SessionMaterialHasDirect reports whether session material names a direct
// endpoint. Delivery writes only that endpoint for a session binding
// (CredentialAccess.DirectOnly), so no transaction pooler sits between the
// client and its server session.
func SessionMaterialHasDirect(material CredentialMaterial) bool {
	for _, endpoint := range material.Endpoints {
		if endpoint.Role == EndpointDirect && endpoint.Host != "" && endpoint.Port != 0 {
			return true
		}
	}
	return false
}

// VerifySessionCredentialProbe proves session semantics on a connection made
// with session material: an advisory lock taken by one statement is still
// held by the next, and the connection receives a notification it listens
// for. It releases what it takes.
func VerifySessionCredentialProbe(ctx context.Context, conn *pgx.Conn, channel string) (held, delivered bool, err error) {
	if conn == nil || !validReadOnlyProbeName(channel) {
		return false, false, ErrInvalid
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", sessionProbeLockKey); err != nil {
		return false, false, ErrUnavailable
	}
	var locks int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid() AND granted").Scan(&locks); err != nil {
		return false, false, ErrUnavailable
	}
	var unlocked bool
	if err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", sessionProbeLockKey).Scan(&unlocked); err != nil || !unlocked || locks < 1 {
		return false, false, ErrUnavailable
	}
	identifier := pgx.Identifier{channel}.Sanitize()
	if _, err := conn.Exec(ctx, "LISTEN "+identifier); err != nil {
		return true, false, ErrUnavailable
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN "+identifier) }()
	if _, err := conn.Exec(ctx, "SELECT pg_notify($1, 'qualification')", channel); err != nil {
		return true, false, ErrUnavailable
	}
	waitContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	notification, err := conn.WaitForNotification(waitContext)
	if err != nil || notification == nil || notification.Channel != channel {
		return true, false, ErrUnavailable
	}
	return true, true, nil
}
