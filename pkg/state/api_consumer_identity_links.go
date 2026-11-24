package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Linking an OAuth identity to an existing consumer (ADR-20261124094745):
// a person who already holds a consumer key for an app and then signs in to
// its MCP server proves the key, and their sign-ins bill to that consumer.
// The consumer their first sign-in enrolled is revoked, and its external ref
// becomes linkedConsumerRef so the ref is free: after the owner unlinks the
// identity, its next sign-in enrolls a new consumer. (A consumer the owner
// revoked keeps its ref, so its sign-ins stay refused.) An identity linked
// to a key's consumer is moved again only after the owner unlinks it.

// linkedConsumerRef is the external ref of a consumer a link closed.
func linkedConsumerRef(consumerID string) string { return "linked:" + consumerID }

// ErrAPIConsumerKeyRejected reports a consumer key that is not a live key
// of one of the app's active consumers. Callers answer every such key the
// same way.
var ErrAPIConsumerKeyRejected = errors.New("consumer key rejected")

// APIConsumerIdentityLink points an issuer and subject at ConsumerID.
type APIConsumerIdentityLink struct {
	AccountID, AppID, Issuer, Subject, Email, ConsumerID string
}

// APIConsumerIdentityLinkStore links, lists and unlinks OAuth identities.
// Optional, like the other pricing stores.
type APIConsumerIdentityLinkStore interface {
	// LinkAPIConsumerIdentity points the identity at in.ConsumerID, creating
	// it when needed, and returns it with the ID of the consumer it was
	// enrolled as when that consumer was revoked ("" otherwise).
	// ErrNotFound when the consumer is not an active consumer of the app;
	// ErrConflict when the identity is linked to another key's consumer.
	LinkAPIConsumerIdentity(ctx context.Context, in APIConsumerIdentityLink) (APIConsumerIdentity, string, error)
	// ListAPIConsumerIdentities returns the app's identities, newest first.
	ListAPIConsumerIdentities(ctx context.Context, accountID, appID string) ([]APIConsumerIdentity, error)
	// DeleteAPIConsumerIdentity unlinks an identity; the person's next
	// sign-in enrolls again or links again. ErrNotFound when it is not the
	// app's.
	DeleteAPIConsumerIdentity(ctx context.Context, accountID, appID, identityID string) error
}

var (
	_ APIConsumerIdentityLinkStore = (*MemStore)(nil)
	_ APIConsumerIdentityLinkStore = (*PgStore)(nil)
)

func validateAPIConsumerIdentityLink(in APIConsumerIdentityLink) error {
	// A link creates no consumer, so it has no effective minute of its own.
	if err := validateAPIConsumerEnrollment(APIConsumerEnrollment{AccountID: in.AccountID, AppID: in.AppID, Issuer: in.Issuer,
		Subject: in.Subject, EffectiveFrom: time.Now().UTC().Truncate(time.Minute)}); err != nil {
		return err
	}
	if _, err := uuid.Parse(in.ConsumerID); err != nil {
		return fmt.Errorf("consumer identity link: consumer_id must be a UUID: %w", err)
	}
	return nil
}

// LinkAPIConsumerIdentity implements APIConsumerIdentityLinkStore.
func (m *MemStore) LinkAPIConsumerIdentity(_ context.Context, in APIConsumerIdentityLink) (APIConsumerIdentity, string, error) {
	if err := validateAPIConsumerIdentityLink(in); err != nil {
		return APIConsumerIdentity{}, "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.apiConsumers[in.ConsumerID]
	if !ok || target.AccountID != in.AccountID || target.AppID != in.AppID || !target.Active() {
		return APIConsumerIdentity{}, "", ErrNotFound
	}
	now := time.Now().UTC()
	if m.apiConsumerIdentities == nil {
		m.apiConsumerIdentities = map[string]APIConsumerIdentity{}
	}
	for key, id := range m.apiConsumerIdentities {
		if id.AppID != in.AppID || id.Issuer != in.Issuer || id.Subject != in.Subject {
			continue
		}
		if id.ConsumerID == target.ID {
			return id, "", nil
		}
		previous, ok := m.apiConsumers[id.ConsumerID]
		if ok && previous.ExternalRef != apiConsumerIdentityRef(in.Issuer, in.Subject) {
			return APIConsumerIdentity{}, "", ErrConflict
		}
		revoked := ""
		if ok && previous.Active() {
			previous.Status, previous.RevokedAt, previous.UpdatedAt = APIConsumerStatusRevoked, &now, now
			revoked = previous.ID
		}
		if ok {
			previous.ExternalRef = linkedConsumerRef(previous.ID)
			m.apiConsumers[previous.ID] = previous
		}
		id.ConsumerID, id.UpdatedAt = target.ID, now
		m.apiConsumerIdentities[key] = id
		return id, revoked, nil
	}
	id := APIConsumerIdentity{ID: uuid.NewString(), AccountID: in.AccountID, AppID: in.AppID, ConsumerID: target.ID, Issuer: in.Issuer,
		Subject: in.Subject, Email: apiConsumerIdentityEmail(in.Email), CreatedAt: now, UpdatedAt: now}
	m.apiConsumerIdentities[id.ID] = id
	return id, "", nil
}

// ListAPIConsumerIdentities implements APIConsumerIdentityLinkStore.
func (m *MemStore) ListAPIConsumerIdentities(_ context.Context, accountID, appID string) ([]APIConsumerIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []APIConsumerIdentity{}
	for _, id := range m.apiConsumerIdentities {
		if id.AccountID == accountID && id.AppID == appID {
			out = append(out, id)
		}
	}
	sortAPIConsumerIdentities(out)
	return out, nil
}

func sortAPIConsumerIdentities(ids []APIConsumerIdentity) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && (ids[j].CreatedAt.After(ids[j-1].CreatedAt) ||
			(ids[j].CreatedAt.Equal(ids[j-1].CreatedAt) && ids[j].ID > ids[j-1].ID)); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// DeleteAPIConsumerIdentity implements APIConsumerIdentityLinkStore.
func (m *MemStore) DeleteAPIConsumerIdentity(_ context.Context, accountID, appID, identityID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.apiConsumerIdentities[identityID]
	if !ok || id.AccountID != accountID || id.AppID != appID {
		return ErrNotFound
	}
	delete(m.apiConsumerIdentities, identityID)
	return nil
}

// LinkAPIConsumerIdentity implements APIConsumerIdentityLinkStore.
func (s *PgStore) LinkAPIConsumerIdentity(ctx context.Context, in APIConsumerIdentityLink) (APIConsumerIdentity, string, error) {
	if err := validateAPIConsumerIdentityLink(in); err != nil {
		return APIConsumerIdentity{}, "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerIdentity{}, "", fmt.Errorf("link consumer identity: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid for update`, in.ConsumerID, in.AccountID, in.AppID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !target.Active()) {
		return APIConsumerIdentity{}, "", ErrNotFound
	}
	if err != nil {
		return APIConsumerIdentity{}, "", fmt.Errorf("link consumer identity: consumer: %w", err)
	}
	id, err := scanAPIConsumerIdentity(tx.QueryRow(ctx, `select `+apiConsumerIdentityCols+` from api_consumer_identities
		where app_id = $1::uuid and issuer = $2 and subject = $3 for update`, in.AppID, in.Issuer, in.Subject))
	switch {
	case errors.Is(err, ErrNotFound):
		id, err = scanAPIConsumerIdentity(tx.QueryRow(ctx, `insert into api_consumer_identities
			(account_id, app_id, consumer_id, issuer, subject, email) values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6)
			returning `+apiConsumerIdentityCols, in.AccountID, in.AppID, target.ID, in.Issuer, in.Subject, apiConsumerIdentityEmail(in.Email)))
		if err != nil {
			return APIConsumerIdentity{}, "", fmt.Errorf("link consumer identity: insert: %w", err)
		}
		return id, "", tx.Commit(ctx)
	case err != nil:
		return APIConsumerIdentity{}, "", fmt.Errorf("link consumer identity: identity: %w", err)
	case id.ConsumerID == target.ID:
		return id, "", nil
	}
	previous, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers where id = $1::uuid for update`,
		id.ConsumerID))
	if err != nil {
		return APIConsumerIdentity{}, "", fmt.Errorf("link consumer identity: previous consumer: %w", err)
	}
	if previous.ExternalRef != apiConsumerIdentityRef(in.Issuer, in.Subject) {
		return APIConsumerIdentity{}, "", ErrConflict
	}
	revoked := ""
	if previous.Active() {
		revoked = previous.ID
	}
	if _, err := tx.Exec(ctx, `update api_consumers set status = 'revoked', revoked_at = coalesce(revoked_at, now()), updated_at = now(),
		external_ref = $2 where id = $1::uuid`, previous.ID, linkedConsumerRef(previous.ID)); err != nil {
		return APIConsumerIdentity{}, "", fmt.Errorf("link consumer identity: revoke: %w", err)
	}
	id, err = scanAPIConsumerIdentity(tx.QueryRow(ctx, `update api_consumer_identities set consumer_id = $2::uuid, updated_at = now()
		where id = $1::uuid returning `+apiConsumerIdentityCols, id.ID, target.ID))
	if err != nil {
		return APIConsumerIdentity{}, "", fmt.Errorf("link consumer identity: update: %w", err)
	}
	return id, revoked, tx.Commit(ctx)
}

// ListAPIConsumerIdentities implements APIConsumerIdentityLinkStore.
func (s *PgStore) ListAPIConsumerIdentities(ctx context.Context, accountID, appID string) ([]APIConsumerIdentity, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := uuid.Parse(appID); err != nil {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `select `+apiConsumerIdentityCols+` from api_consumer_identities
		where account_id = $1::uuid and app_id = $2::uuid order by created_at desc, id desc`, accountID, appID)
	if err != nil {
		return nil, fmt.Errorf("list consumer identities: %w", err)
	}
	defer rows.Close()
	out := []APIConsumerIdentity{}
	for rows.Next() {
		id, err := scanAPIConsumerIdentity(rows)
		if err != nil {
			return nil, fmt.Errorf("list consumer identities: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteAPIConsumerIdentity implements APIConsumerIdentityLinkStore.
func (s *PgStore) DeleteAPIConsumerIdentity(ctx context.Context, accountID, appID, identityID string) error {
	if _, err := uuid.Parse(identityID); err != nil {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `delete from api_consumer_identities where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid`,
		identityID, accountID, appID)
	if err != nil {
		return fmt.Errorf("delete consumer identity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
