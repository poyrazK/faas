package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type RealtimeSubscriptionTicket struct {
	// ID identifies the ticket for revocation; the bearer token is never
	// stored and cannot be derived from it.
	ID        string    `json:"id,omitempty"`
	Principal string    `json:"principal"`
	Channels  []string  `json:"channels"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
type RealtimeSubscriptionTicketStore interface {
	IssueRealtimeSubscriptionTicket(context.Context, string, string, []string, int) (string, RealtimeSubscriptionTicket, error)
	ReadRealtimeSubscriptionTicket(context.Context, string, string) (RealtimeSubscriptionTicket, error)
}

// RealtimeSubscriptionTicketRevoker ends tickets before they expire. Live
// sockets and SSE streams re-read their ticket every second, so a revoked
// ticket's connections close within about a second.
type RealtimeSubscriptionTicketRevoker interface {
	// RevokeRealtimeSubscriptionTicket returns ErrNotFound when the endpoint
	// has no live ticket with that ID.
	RevokeRealtimeSubscriptionTicket(ctx context.Context, ep, id string) error
	// RevokeRealtimeSubscriptionTicketsForPrincipal returns how many live
	// tickets it revoked.
	RevokeRealtimeSubscriptionTicketsForPrincipal(ctx context.Context, ep, principal string) (int, error)
}

func validTicketID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func ticketHash(token string) ([]byte, error) {
	if !strings.HasPrefix(token, "grt_") {
		return nil, ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "grt_"))
	if err != nil || len(raw) != 32 {
		return nil, ErrInvalidArgument
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:], nil
}
func validateTicket(principal string, channels []string, ttl int) error {
	if api.ValidateRealtimePrincipal(principal) != nil || ttl < 1 || ttl > api.RealtimeTicketMaxTTLSeconds || len(channels) < 1 || len(channels) > api.RealtimeTicketMaxChannels {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, ch := range channels {
		if validateManagedRealtimeHistoryRequest("endpoint", ch) != nil || strings.ContainsRune(ch, '\x00') || seen[ch] {
			return ErrInvalidArgument
		}
		seen[ch] = true
	}
	return nil
}
func filterTicket(ticket RealtimeSubscriptionTicket, access ManagedRealtimeAccess) (RealtimeSubscriptionTicket, error) {
	if !time.Now().Before(ticket.ExpiresAt) || (access.SessionsRevokedBefore != nil && !ticket.IssuedAt.After(*access.SessionsRevokedBefore)) {
		return ticket, ErrNotFound
	}
	allowed := []string{}
	for _, ch := range ticket.Channels {
		parent, _, err := api.ParseRealtimeActivityScope(ch)
		if err != nil {
			continue
		}
		denied := false
		for _, d := range access.DeniedChannels {
			if d == ch || (parent != "" && d == parent) {
				denied = true
				break
			}
		}
		if !denied {
			allowed = append(allowed, ch)
		}
	}
	ticket.Channels = allowed
	return ticket, nil
}
func (s *PgStore) IssueRealtimeSubscriptionTicket(ctx context.Context, ep, principal string, channels []string, ttl int) (string, RealtimeSubscriptionTicket, error) {
	var ticket RealtimeSubscriptionTicket
	if err := validateTicket(principal, channels, ttl); err != nil {
		return "", ticket, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ticket, err
	}
	token := "grt_" + base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := ticketHash(token)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", ticket, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	if err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 and enabled for update`, ep).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ticket, ErrNotFound
		}
		return "", ticket, err
	}
	if _, err = tx.Exec(ctx, `delete from managed_realtime_subscription_tickets where endpoint_id=$1 and expires_at<=clock_timestamp()`, ep); err != nil {
		return "", ticket, err
	}
	var count int
	if err = tx.QueryRow(ctx, `select count(*) from managed_realtime_subscription_tickets where endpoint_id=$1`, ep).Scan(&count); err != nil {
		return "", ticket, err
	}
	if count >= api.RealtimeTicketMaxPerEndpoint {
		return "", ticket, ErrManagedRealtimeHistoryLimit
	}
	encoded, _ := json.Marshal(channels)
	err = tx.QueryRow(ctx, `insert into managed_realtime_subscription_tickets(token_hash,endpoint_id,principal,channels,issued_at,expires_at)
 select $1,$2,$3,$4::jsonb,t,t+make_interval(secs=>$5::integer) from (select clock_timestamp() as t) stamp returning id::text,issued_at,expires_at`, hash, ep, principal, encoded, ttl).Scan(&ticket.ID, &ticket.IssuedAt, &ticket.ExpiresAt)
	if err != nil {
		return "", ticket, err
	}
	ticket.Principal = principal
	ticket.Channels = append([]string(nil), channels...)
	return token, ticket, tx.Commit(ctx)
}
func (s *PgStore) ReadRealtimeSubscriptionTicket(ctx context.Context, ep, token string) (RealtimeSubscriptionTicket, error) {
	var ticket RealtimeSubscriptionTicket
	hash, err := ticketHash(token)
	if err != nil {
		return ticket, err
	}
	var encoded []byte
	err = s.pool.QueryRow(ctx, `select t.id::text,t.principal,t.channels,t.issued_at,t.expires_at from managed_realtime_subscription_tickets t join managed_realtime_endpoints e on e.id=t.endpoint_id where t.token_hash=$1 and t.endpoint_id=$2 and e.enabled and t.expires_at>clock_timestamp()`, hash, ep).Scan(&ticket.ID, &ticket.Principal, &encoded, &ticket.IssuedAt, &ticket.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ticket, ErrNotFound
	}
	if err != nil {
		return ticket, err
	}
	if err = json.Unmarshal(encoded, &ticket.Channels); err != nil {
		return ticket, err
	}
	access, err := s.ReadManagedRealtimeAccess(ctx, ep, ticket.Principal)
	if err != nil {
		return ticket, err
	}
	return filterTicket(ticket, access)
}

func (s *PgStore) RevokeRealtimeSubscriptionTicket(ctx context.Context, ep, id string) error {
	if !validTicketID(id) {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `delete from managed_realtime_subscription_tickets where endpoint_id=$1 and id=$2 and expires_at>clock_timestamp()`, ep, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PgStore) RevokeRealtimeSubscriptionTicketsForPrincipal(ctx context.Context, ep, principal string) (int, error) {
	if api.ValidateRealtimePrincipal(principal) != nil {
		return 0, ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `delete from managed_realtime_subscription_tickets where endpoint_id=$1 and principal=$2 and expires_at>clock_timestamp()`, ep, principal)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

type storedRealtimeTicket struct {
	EndpointID string
	Ticket     RealtimeSubscriptionTicket
}

func (m *MemStore) IssueRealtimeSubscriptionTicket(_ context.Context, ep, principal string, channels []string, ttl int) (string, RealtimeSubscriptionTicket, error) {
	var ticket RealtimeSubscriptionTicket
	if err := validateTicket(principal, channels, ttl); err != nil {
		return "", ticket, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ticket, err
	}
	token := "grt_" + base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := ticketHash(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, ok := m.managedRealtimeEndpoints[ep]
	if !ok || !endpoint.Enabled {
		return "", ticket, ErrNotFound
	}
	now := time.Now().UTC()
	count := 0
	for key, row := range m.realtimeTickets {
		if row.EndpointID == ep {
			if !now.Before(row.Ticket.ExpiresAt) {
				delete(m.realtimeTickets, key)
			} else {
				count++
			}
		}
	}
	if count >= api.RealtimeTicketMaxPerEndpoint {
		return "", ticket, ErrManagedRealtimeHistoryLimit
	}
	ticket = RealtimeSubscriptionTicket{ID: uuid.NewString(), Principal: principal, Channels: append([]string(nil), channels...), IssuedAt: now, ExpiresAt: now.Add(time.Duration(ttl) * time.Second)}
	if m.realtimeTickets == nil {
		m.realtimeTickets = map[string]storedRealtimeTicket{}
	}
	m.realtimeTickets[string(hash)] = storedRealtimeTicket{ep, ticket}
	return token, ticket, nil
}
func (m *MemStore) ReadRealtimeSubscriptionTicket(_ context.Context, ep, token string) (RealtimeSubscriptionTicket, error) {
	hash, err := ticketHash(token)
	if err != nil {
		return RealtimeSubscriptionTicket{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.realtimeTickets[string(hash)]
	endpoint, enabled := m.managedRealtimeEndpoints[ep]
	if !ok || row.EndpointID != ep || !enabled || !endpoint.Enabled {
		return RealtimeSubscriptionTicket{}, ErrNotFound
	}
	return filterTicket(row.Ticket, m.managedRealtimeAccess[ep][row.Ticket.Principal])
}
func (m *MemStore) RevokeRealtimeSubscriptionTicket(_ context.Context, ep, id string) error {
	if !validTicketID(id) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for key, row := range m.realtimeTickets {
		if row.EndpointID == ep && row.Ticket.ID == id && now.Before(row.Ticket.ExpiresAt) {
			delete(m.realtimeTickets, key)
			return nil
		}
	}
	return ErrNotFound
}
func (m *MemStore) RevokeRealtimeSubscriptionTicketsForPrincipal(_ context.Context, ep, principal string) (int, error) {
	if api.ValidateRealtimePrincipal(principal) != nil {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now, revoked := time.Now(), 0
	for key, row := range m.realtimeTickets {
		if row.EndpointID == ep && row.Ticket.Principal == principal && now.Before(row.Ticket.ExpiresAt) {
			delete(m.realtimeTickets, key)
			revoked++
		}
	}
	return revoked, nil
}
