package state

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

func scanPrivateNetwork(row interface{ Scan(...any) error }) (PrivateNetwork, error) {
	var (
		network                 PrivateNetwork
		accountID, cidr, status string
		statusDetail            string
	)
	err := row.Scan(&network.ID, &accountID, &network.Name, &network.Region, &cidr, &status, &statusDetail, &network.CreatedAt, &network.UpdatedAt)
	if err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return PrivateNetwork{}, err
	}
	network.AccountID, network.CIDR, network.Status, network.StatusDetail = accountID, prefix, status, statusDetail
	return network, nil
}

func scanPrivateNetworkAddress(row interface{ Scan(...any) error }) (PrivateNetworkAddress, error) {
	var (
		address                           PrivateNetworkAddress
		accountID, networkID, addressText string
	)
	err := row.Scan(&address.ID, &accountID, &networkID, &address.OwnerType, &address.OwnerID, &addressText, &address.CreatedAt)
	if err != nil {
		return PrivateNetworkAddress{}, mapErr(err)
	}
	parsed, err := parsePrivateNetworkAddress(addressText)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	address.AccountID, address.NetworkID, address.Address = accountID, networkID, parsed
	return address, nil
}

func scanPrivateNetworkPeering(row interface{ Scan(...any) error }) (PrivateNetworkPeering, error) {
	var peering PrivateNetworkPeering
	var accountID string
	if err := row.Scan(&peering.ID, &accountID, &peering.LeftNetworkID, &peering.RightNetworkID, &peering.Region, &peering.Status, &peering.StatusDetail, &peering.CreatedAt, &peering.UpdatedAt); err != nil {
		return PrivateNetworkPeering{}, mapErr(err)
	}
	peering.AccountID = accountID
	return peering, nil
}

func parsePrivateNetworkAddress(value string) (netip.Addr, error) {
	parsed, err := netip.ParseAddr(value)
	if err == nil {
		return parsed, nil
	}
	// PostgreSQL's inet text representation includes the host prefix
	// length (for example, "10.60.0.2/32"). Accept that canonical
	// representation as well as the bare address used by MemStore and
	// drivers that omit the prefix.
	prefix, prefixErr := netip.ParsePrefix(value)
	if prefixErr != nil {
		return netip.Addr{}, err
	}
	return prefix.Addr(), nil
}

func privateNetworkArgs(network PrivateNetwork) (string, pgtype.UUID, string, string, string, string, string) {
	return network.ID, mustPgUUID(network.AccountID), network.Name, network.Region, network.CIDR.String(), network.Status, network.StatusDetail
}

func privateNetworkPolicyArgs(network PrivateNetwork) []string {
	policy := make([]string, 0, len(network.AllowedCIDRs))
	for _, prefix := range network.AllowedCIDRs {
		policy = append(policy, prefix.String())
	}
	return policy
}

func privateNetworkFirewallRulesJSON(rules []api.PrivateNetworkFirewallRule) ([]byte, error) {
	if len(rules) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(rules)
}

func loadPrivateNetworkPolicy(ctx context.Context, row pgx.Row, network *PrivateNetwork) error {
	var raw []string
	var rulesJSON []byte
	if err := row.Scan(&raw, &rulesJSON); err != nil {
		return mapErr(err)
	}
	policy := make([]netip.Prefix, 0, len(raw))
	for _, value := range raw {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return err
		}
		policy = append(policy, prefix.Masked())
	}
	network.AllowedCIDRs = policy
	if len(rulesJSON) > 0 {
		var rules []api.PrivateNetworkFirewallRule
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return err
		}
		validated, err := api.ValidatePrivateNetworkFirewallRules(rules, network.CIDR)
		if err != nil {
			return err
		}
		network.FirewallRules = validated
	}
	return nil
}

func (s *PgStore) CreatePrivateNetwork(ctx context.Context, network PrivateNetwork) (PrivateNetwork, error) {
	var err error
	network, err = validatePrivateNetwork(network)
	if err != nil {
		return PrivateNetwork{}, err
	}
	if network.ID == "" {
		network.ID = "net-" + uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivateNetwork{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	accountID := mustPgUUID(network.AccountID)
	var lockedAccountID string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1 for update`, accountID).Scan(&lockedAccountID); err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from private_networks where account_id = $1 and name = $2)`, accountID, network.Name).Scan(&exists); err != nil {
		return PrivateNetwork{}, err
	}
	if exists {
		return PrivateNetwork{}, ErrConflict
	}
	if err := tx.QueryRow(ctx, `select exists(select 1 from private_networks where account_id = $1 and region = $2 and cidr && $3::cidr)`, accountID, network.Region, network.CIDR.String()).Scan(&exists); err != nil {
		return PrivateNetwork{}, err
	}
	if exists {
		return PrivateNetwork{}, ErrConflict
	}
	id, _, name, region, cidr, status, detail := privateNetworkArgs(network)
	policy := privateNetworkPolicyArgs(network)
	rulesJSON, err := privateNetworkFirewallRulesJSON(network.FirewallRules)
	if err != nil {
		return PrivateNetwork{}, err
	}
	created, err := scanPrivateNetwork(tx.QueryRow(ctx, `
		insert into private_networks (id, account_id, name, region, cidr, allowed_cidrs, firewall_rules, status, status_detail)
		values ($1, $2, $3, $4, $5::cidr, $6::cidr[], $7::jsonb, $8, $9)
		returning id, account_id, name, region, cidr::text, status, status_detail, created_at, updated_at`, id, accountID, name, region, cidr, policy, rulesJSON, status, detail))
	if err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivateNetwork{}, err
	}
	created.AllowedCIDRs = append([]netip.Prefix(nil), network.AllowedCIDRs...)
	created.FirewallRules = clonePrivateNetworkFirewallRules(network.FirewallRules)
	return created, nil
}

func (s *PgStore) GetPrivateNetwork(ctx context.Context, accountID, id string) (PrivateNetwork, error) {
	network, err := scanPrivateNetwork(s.pool.QueryRow(ctx, `
		select id, account_id, name, region, cidr::text, status, status_detail, created_at, updated_at
		  from private_networks where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id)))
	if err != nil {
		return PrivateNetwork{}, err
	}
	if err := loadPrivateNetworkPolicy(ctx, s.pool.QueryRow(ctx, `select allowed_cidrs::text[], firewall_rules from private_networks where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id)), &network); err != nil {
		return PrivateNetwork{}, err
	}
	return network, nil
}

func (s *PgStore) ListPrivateNetworks(ctx context.Context, accountID string) ([]PrivateNetwork, error) {
	rows, err := s.pool.Query(ctx, `
		select id, account_id, name, region, cidr::text, status, status_detail, created_at, updated_at
		  from private_networks where account_id = $1 order by name asc, id asc`, mustPgUUID(accountID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]PrivateNetwork, 0)
	for rows.Next() {
		network, scanErr := scanPrivateNetwork(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if err := loadPrivateNetworkPolicy(ctx, s.pool.QueryRow(ctx, `select allowed_cidrs::text[], firewall_rules from private_networks where account_id = $1 and id = $2`, mustPgUUID(accountID), network.ID), &network); err != nil {
			return nil, err
		}
		out = append(out, network)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) UpdatePrivateNetworkPolicy(ctx context.Context, accountID, id string, allowedCIDRs []netip.Prefix) (PrivateNetwork, error) {
	network, err := s.GetPrivateNetwork(ctx, accountID, id)
	if err != nil {
		return PrivateNetwork{}, err
	}
	raw := make([]string, 0, len(allowedCIDRs))
	for _, prefix := range allowedCIDRs {
		raw = append(raw, prefix.String())
	}
	policy, err := api.ValidatePrivateNetworkPolicyCIDRs(raw, []netip.Prefix{network.CIDR})
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	result, err := s.pool.Exec(ctx, `update private_networks set allowed_cidrs = $3::cidr[] where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id), raw)
	if err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	if result.RowsAffected() == 0 {
		return PrivateNetwork{}, ErrNotFound
	}
	network.AllowedCIDRs = policy
	network.UpdatedAt = time.Now().UTC()
	return network, nil
}

func (s *PgStore) UpdatePrivateNetworkFirewallPolicy(ctx context.Context, accountID, id string, allowedCIDRs []netip.Prefix, rules []api.PrivateNetworkFirewallRule) (PrivateNetwork, error) {
	network, err := s.GetPrivateNetwork(ctx, accountID, id)
	if err != nil {
		return PrivateNetwork{}, err
	}
	raw := make([]string, 0, len(allowedCIDRs))
	for _, prefix := range allowedCIDRs {
		raw = append(raw, prefix.String())
	}
	policy, err := api.ValidatePrivateNetworkPolicyCIDRs(raw, []netip.Prefix{network.CIDR})
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	validatedRules, err := api.ValidatePrivateNetworkFirewallRules(rules, network.CIDR)
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	rulesJSON, err := privateNetworkFirewallRulesJSON(validatedRules)
	if err != nil {
		return PrivateNetwork{}, err
	}
	result, err := s.pool.Exec(ctx, `update private_networks set allowed_cidrs = $3::cidr[], firewall_rules = $4::jsonb where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id), raw, rulesJSON)
	if err != nil {
		return PrivateNetwork{}, mapErr(err)
	}
	if result.RowsAffected() == 0 {
		return PrivateNetwork{}, ErrNotFound
	}
	network.AllowedCIDRs = policy
	network.FirewallRules = clonePrivateNetworkFirewallRules(validatedRules)
	network.UpdatedAt = time.Now().UTC()
	return network, nil
}

func (s *PgStore) DeletePrivateNetwork(ctx context.Context, accountID, id string) error {
	result, err := s.pool.Exec(ctx, `delete from private_networks where account_id = $1 and id = $2 and not exists (select 1 from app_private_network_attachments a where a.account_id = $1 and a.network_id = $2) and not exists (select 1 from private_network_peerings p where p.account_id = $1 and (p.left_network_id = $2 or p.right_network_id = $2))`, mustPgUUID(accountID), strings.TrimSpace(id))
	if err != nil {
		return mapErr(err)
	}
	if result.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from private_networks where account_id = $1 and id = $2)`, mustPgUUID(accountID), strings.TrimSpace(id)).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (s *PgStore) CreatePrivateNetworkPeering(ctx context.Context, peering PrivateNetworkPeering) (PrivateNetworkPeering, error) {
	var err error
	peering, err = validatePrivateNetworkPeering(peering)
	if err != nil {
		return PrivateNetworkPeering{}, err
	}
	if peering.ID == "" {
		peering.ID = "peer-" + uuid.NewString()
	}
	if peering.StatusDetail == "" {
		peering.StatusDetail = "network route convergence is pending"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivateNetworkPeering{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var leftAccount, leftRegion, leftCIDR, rightAccount, rightRegion, rightCIDR string
	if err := tx.QueryRow(ctx, `
		select l.account_id::text, l.region, l.cidr::text,
		       r.account_id::text, r.region, r.cidr::text
		  from private_networks l
		  join private_networks r on r.id = $3
		 where l.id = $2 and l.account_id = $1 and r.account_id = $1
		 for update`, mustPgUUID(peering.AccountID), peering.LeftNetworkID, peering.RightNetworkID).
		Scan(&leftAccount, &leftRegion, &leftCIDR, &rightAccount, &rightRegion, &rightCIDR); err != nil {
		return PrivateNetworkPeering{}, mapErr(err)
	}
	if leftAccount != peering.AccountID || rightAccount != peering.AccountID {
		return PrivateNetworkPeering{}, ErrNotFound
	}
	if leftRegion != peering.Region || rightRegion != peering.Region {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	leftPrefix, leftErr := netip.ParsePrefix(leftCIDR)
	rightPrefix, rightErr := netip.ParsePrefix(rightCIDR)
	if leftErr != nil || rightErr != nil {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	if leftPrefix.Overlaps(rightPrefix) {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	created, err := scanPrivateNetworkPeering(tx.QueryRow(ctx, `
		insert into private_network_peerings
		  (id, account_id, left_network_id, right_network_id, region, status, status_detail)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning id, account_id, left_network_id, right_network_id, region, status, status_detail, created_at, updated_at`,
		peering.ID, mustPgUUID(peering.AccountID), peering.LeftNetworkID, peering.RightNetworkID, peering.Region, peering.Status, peering.StatusDetail))
	if err != nil {
		return PrivateNetworkPeering{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivateNetworkPeering{}, err
	}
	return created, nil
}

func (s *PgStore) ListPrivateNetworkPeerings(ctx context.Context, accountID, networkID string) ([]PrivateNetworkPeering, error) {
	rows, err := s.pool.Query(ctx, `
		select id, account_id, left_network_id, right_network_id, region, status, status_detail, created_at, updated_at
		  from private_network_peerings
		 where account_id = $1 and ($2 = '' or left_network_id = $2 or right_network_id = $2)
		 order by left_network_id asc, right_network_id asc, id asc`, mustPgUUID(accountID), strings.TrimSpace(networkID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]PrivateNetworkPeering, 0)
	for rows.Next() {
		peering, scanErr := scanPrivateNetworkPeering(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, peering)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) ListPrivateNetworkPeeringsForReconcile(ctx context.Context, statuses []string, limit int) ([]PrivateNetworkPeering, error) {
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalidArgument
	}
	for _, status := range statuses {
		if status != api.PrivateNetworkPeeringStatusPending && status != api.PrivateNetworkPeeringStatusReady && status != api.PrivateNetworkPeeringStatusError {
			return nil, ErrInvalidArgument
		}
	}
	rows, err := s.pool.Query(ctx, `
		select id, account_id, left_network_id, right_network_id, region, status, status_detail, created_at, updated_at
		  from private_network_peerings
		 where ($1::text[] is null or status = any($1::text[]))
		 order by updated_at asc, id asc
		 limit $2`, nullableStrings(statuses), limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]PrivateNetworkPeering, 0)
	for rows.Next() {
		peering, scanErr := scanPrivateNetworkPeering(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, peering)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) GetPrivateNetworkPeering(ctx context.Context, accountID, id string) (PrivateNetworkPeering, error) {
	return scanPrivateNetworkPeering(s.pool.QueryRow(ctx, `
		select id, account_id, left_network_id, right_network_id, region, status, status_detail, created_at, updated_at
		  from private_network_peerings where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id)))
}

func (s *PgStore) UpdatePrivateNetworkPeeringStatus(ctx context.Context, accountID, id, status, detail string) (PrivateNetworkPeering, error) {
	if status != api.PrivateNetworkPeeringStatusPending && status != api.PrivateNetworkPeeringStatusReady && status != api.PrivateNetworkPeeringStatusError {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	return scanPrivateNetworkPeering(s.pool.QueryRow(ctx, `
		update private_network_peerings
		   set status = $3, status_detail = $4, updated_at = now()
		 where account_id = $1 and id = $2
		returning id, account_id, left_network_id, right_network_id, region, status, status_detail, created_at, updated_at`, mustPgUUID(accountID), strings.TrimSpace(id), status, detail))
}

func (s *PgStore) DeletePrivateNetworkPeering(ctx context.Context, accountID, id string) error {
	result, err := s.pool.Exec(ctx, `delete from private_network_peerings where account_id = $1 and id = $2`, mustPgUUID(accountID), strings.TrimSpace(id))
	if err != nil {
		return mapErr(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) AllocatePrivateNetworkAddress(ctx context.Context, accountID, networkID, ownerType, ownerID string) (PrivateNetworkAddress, error) {
	ownerType, ownerID = strings.TrimSpace(ownerType), strings.TrimSpace(ownerID)
	if !validPrivateNetworkOwner(ownerType, ownerID) {
		return PrivateNetworkAddress{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var prefixText string
	if err := tx.QueryRow(ctx, `select cidr::text from private_networks where account_id = $1 and id = $2 for update`, mustPgUUID(accountID), networkID).Scan(&prefixText); err != nil {
		return PrivateNetworkAddress{}, mapErr(err)
	}
	prefix, err := netip.ParsePrefix(prefixText)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	var existingID, existingAccountID, existingNetworkID, existingOwnerType, existingOwnerID, existingAddress string
	var existingCreatedAt time.Time
	existingErr := tx.QueryRow(ctx, `select id, account_id, network_id, owner_type, owner_id, address::text, created_at from private_network_addresses where network_id = $1 and owner_type = $2 and owner_id = $3`, networkID, ownerType, ownerID).Scan(&existingID, &existingAccountID, &existingNetworkID, &existingOwnerType, &existingOwnerID, &existingAddress, &existingCreatedAt)
	if existingErr == nil {
		address, parseErr := parsePrivateNetworkAddress(existingAddress)
		if parseErr != nil {
			return PrivateNetworkAddress{}, parseErr
		}
		return PrivateNetworkAddress{ID: existingID, AccountID: existingAccountID, NetworkID: existingNetworkID, OwnerType: existingOwnerType, OwnerID: existingOwnerID, Address: address, CreatedAt: existingCreatedAt}, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return PrivateNetworkAddress{}, mapErr(existingErr)
	}
	usedRows, err := tx.Query(ctx, `select address::text from private_network_addresses where network_id = $1`, networkID)
	if err != nil {
		return PrivateNetworkAddress{}, err
	}
	used := map[netip.Addr]struct{}{}
	for usedRows.Next() {
		var value string
		if err := usedRows.Scan(&value); err != nil {
			usedRows.Close()
			return PrivateNetworkAddress{}, err
		}
		address, parseErr := parsePrivateNetworkAddress(value)
		if parseErr != nil {
			usedRows.Close()
			return PrivateNetworkAddress{}, parseErr
		}
		used[address] = struct{}{}
	}
	if err := usedRows.Err(); err != nil {
		usedRows.Close()
		return PrivateNetworkAddress{}, err
	}
	usedRows.Close()
	address, ok := allocatePrivateNetworkAddress(prefix, used)
	if !ok {
		return PrivateNetworkAddress{}, ErrConflict
	}
	id := uuid.NewString()
	row, err := scanPrivateNetworkAddress(tx.QueryRow(ctx, `
		insert into private_network_addresses (id, account_id, network_id, owner_type, owner_id, address)
		values ($1, $2, $3, $4, $5, $6::inet)
		returning id, account_id, network_id, owner_type, owner_id, address::text, created_at`, id, mustPgUUID(accountID), networkID, ownerType, ownerID, address.String()))
	if err != nil {
		return PrivateNetworkAddress{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivateNetworkAddress{}, err
	}
	return row, nil
}

func (s *PgStore) ReleasePrivateNetworkAddress(ctx context.Context, accountID, networkID, ownerType, ownerID string) error {
	result, err := s.pool.Exec(ctx, `delete from private_network_addresses where account_id = $1 and network_id = $2 and owner_type = $3 and owner_id = $4`, mustPgUUID(accountID), networkID, strings.TrimSpace(ownerType), strings.TrimSpace(ownerID))
	if err != nil {
		return mapErr(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
