package state

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type StaticEgressIPGrant struct {
	AccountID string
	IP        netip.Addr
}

type StaticEgressProvisioningStore interface {
	ReplaceStaticEgressProvisioning(context.Context, string, []StaticEgressIPGrant) error
}

func validateStaticEgressGrants(source string, grants []StaticEgressIPGrant) error {
	if strings.TrimSpace(source) == "" || len(source) > 128 || source == "legacy" {
		return fmt.Errorf("state: invalid static egress provisioning source")
	}
	for _, grant := range grants {
		if grant.AccountID == "" || !grant.IP.Is4() {
			return fmt.Errorf("state: invalid static egress provisioning grant")
		}
	}
	return nil
}

func (s *PgStore) ReplaceStaticEgressProvisioning(ctx context.Context, source string, grants []StaticEgressIPGrant) error {
	if err := validateStaticEgressGrants(source, grants); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := sqlc.New()
	// Legacy grants lack node provenance. Retire them on the first scoped
	// publication, rather than treating an unowned grant as permanent authority.
	if err := queries.DeleteStaticEgressProvisioningSource(ctx, tx, source); err != nil {
		return err
	}
	if err := queries.DeleteLegacyStaticEgressProvisioning(ctx, tx); err != nil {
		return err
	}
	for _, grant := range grants {
		var account pgtype.UUID
		if err := account.Scan(grant.AccountID); err != nil {
			return fmt.Errorf("static egress account: %w", err)
		}
		if err := queries.InsertStaticEgressProvisioningGrant(ctx, tx, sqlc.InsertStaticEgressProvisioningGrantParams{SourceID: source, AccountID: account, CustomerIp: grant.IP}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (m *MemStore) ReplaceStaticEgressProvisioning(ctx context.Context, source string, grants []StaticEgressIPGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStaticEgressGrants(source, grants); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.staticEgressProvisioningSources == nil {
		m.staticEgressProvisioningSources = make(map[string][]StaticEgressIPGrant)
	}
	m.staticEgressProvisioningSources[source] = append([]StaticEgressIPGrant(nil), grants...)
	// Matches retirement of the old unowned rows in PostgreSQL.
	m.provisionedStaticEgressIPs = make(map[string]map[string]netip.Addr)
	return nil
}
