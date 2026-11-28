package state

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EffectiveStaticEgressIP is the app's pin when its account's plan allows
// static egress, nil otherwise. A downgrade keeps the recorded pin (an
// upgrade restores it) but the app leaves through shared egress meanwhile.
// schedd's wake spec and vmmd's intent read both use this one rule.
func EffectiveStaticEgressIP(app App, plan api.Plan) *netip.Addr {
	if app.StaticEgressIP == nil || !plan.StaticEgressIPAllowed() {
		return nil
	}
	ip := *app.StaticEgressIP
	return &ip
}

// StaticEgressAddress is one operator-provisioned additional public IPv4
// address (ADR-20261128090000). The provider routes it to NodeID; AccountID
// is the tenant it is assigned to, empty while unassigned. PinnedAppID names
// the live app currently pinning it, if any (read-only projection).
type StaticEgressAddress struct {
	IP          netip.Addr
	NodeID      string
	NodeName    string
	AccountID   string
	Note        string
	PinnedAppID string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ErrStaticEgressAddressPinned refuses a removal, or a reassignment to
// another account, while a live app still pins the address.
var ErrStaticEgressAddressPinned = errors.New("state: static egress address is pinned by a live app")

// StaticEgressAddressStore is the inventory surface. apid writes it on an
// operator's behalf; vmmd reads its node's assigned rows and schedd reads
// the node that holds a pinned address.
type StaticEgressAddressStore interface {
	ListStaticEgressAddresses(ctx context.Context) ([]StaticEgressAddress, error)
	StaticEgressAddressByIP(ctx context.Context, ip netip.Addr) (StaticEgressAddress, error)
	AddStaticEgressAddress(ctx context.Context, addr StaticEgressAddress) error
	UpdateStaticEgressAddress(ctx context.Context, addr StaticEgressAddress) error
	RemoveStaticEgressAddress(ctx context.Context, ip netip.Addr) error
	AssignedStaticEgressAddressesForNode(ctx context.Context, nodeID string) ([]StaticEgressIPGrant, error)
	StaticEgressAddressesForAccount(ctx context.Context, accountID string) ([]netip.Addr, error)
	StaticEgressAddressNodeIDs(ctx context.Context, accountID string, ip netip.Addr) ([]string, error)
	ReleaseDeletedAppStaticEgressPins(ctx context.Context, ip netip.Addr) (int64, error)
}

func validateStaticEgressAddress(addr StaticEgressAddress) error {
	if !addr.IP.Is4() {
		return fmt.Errorf("state: static egress address must be IPv4")
	}
	if strings.TrimSpace(addr.NodeID) == "" {
		return fmt.Errorf("state: static egress address needs a node")
	}
	if len(addr.Note) > 200 {
		return fmt.Errorf("state: static egress address note exceeds 200 bytes")
	}
	return nil
}

func optionalAccountUUID(id string) (pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	return parsePgUUID(id)
}

func staticEgressAddressFromRow(ip netip.Addr, node pgtype.UUID, nodeName string, account pgtype.UUID, note string, created, updated pgtype.Timestamptz, pinned string) StaticEgressAddress {
	return StaticEgressAddress{IP: ip, NodeID: pgUUIDString(node), NodeName: nodeName, AccountID: pgUUIDString(account),
		Note: note, PinnedAppID: pinned, CreatedAt: created.Time, UpdatedAt: updated.Time}
}

func (s *PgStore) ListStaticEgressAddresses(ctx context.Context) ([]StaticEgressAddress, error) {
	rows, err := sqlc.New().ListStaticEgressAddresses(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list static egress addresses: %w", err)
	}
	out := make([]StaticEgressAddress, 0, len(rows))
	for _, r := range rows {
		out = append(out, staticEgressAddressFromRow(r.Ip, r.NodeID, r.NodeName, r.AccountID, r.Note, r.CreatedAt, r.UpdatedAt, r.PinnedAppID))
	}
	return out, nil
}

func (s *PgStore) StaticEgressAddressByIP(ctx context.Context, ip netip.Addr) (StaticEgressAddress, error) {
	r, err := sqlc.New().GetStaticEgressAddress(ctx, s.pool, ip)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaticEgressAddress{}, ErrNotFound
	}
	if err != nil {
		return StaticEgressAddress{}, fmt.Errorf("state: read static egress address: %w", err)
	}
	return staticEgressAddressFromRow(r.Ip, r.NodeID, r.NodeName, r.AccountID, r.Note, r.CreatedAt, r.UpdatedAt, r.PinnedAppID), nil
}

func (s *PgStore) AddStaticEgressAddress(ctx context.Context, addr StaticEgressAddress) error {
	if err := validateStaticEgressAddress(addr); err != nil {
		return err
	}
	node, err := parsePgUUID(addr.NodeID)
	if err != nil {
		return err
	}
	account, err := optionalAccountUUID(addr.AccountID)
	if err != nil {
		return err
	}
	if err := sqlc.New().InsertStaticEgressAddress(ctx, s.pool, sqlc.InsertStaticEgressAddressParams{
		Ip: addr.IP, NodeID: node, AccountID: account, Note: addr.Note}); err != nil {
		return fmt.Errorf("state: add static egress address: %w", mapErr(err))
	}
	return nil
}

// UpdateStaticEgressAddress moves the address to another node or account.
// A live app's pin follows a node move; it blocks an account change.
func (s *PgStore) UpdateStaticEgressAddress(ctx context.Context, addr StaticEgressAddress) error {
	if err := validateStaticEgressAddress(addr); err != nil {
		return err
	}
	node, err := parsePgUUID(addr.NodeID)
	if err != nil {
		return err
	}
	account, err := optionalAccountUUID(addr.AccountID)
	if err != nil {
		return err
	}
	return s.withStaticEgressAddressGuard(ctx, addr.IP, func(tx pgx.Tx, pinnedAccount string) error {
		if pinnedAccount != "" && pinnedAccount != addr.AccountID {
			return ErrStaticEgressAddressPinned
		}
		n, err := sqlc.New().UpdateStaticEgressAddress(ctx, tx, sqlc.UpdateStaticEgressAddressParams{
			NodeID: node, AccountID: account, Note: addr.Note, Ip: addr.IP})
		if err != nil {
			return fmt.Errorf("state: update static egress address: %w", mapErr(err))
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *PgStore) RemoveStaticEgressAddress(ctx context.Context, ip netip.Addr) error {
	return s.withStaticEgressAddressGuard(ctx, ip, func(tx pgx.Tx, pinnedAccount string) error {
		if pinnedAccount != "" {
			return ErrStaticEgressAddressPinned
		}
		n, err := sqlc.New().DeleteStaticEgressAddress(ctx, tx, ip)
		if err != nil {
			return fmt.Errorf("state: remove static egress address: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// withStaticEgressAddressGuard frees deleted apps' pins on ip, then runs fn
// with the account of the live app still pinning it ("" when none), in one
// transaction holding the address row.
func (s *PgStore) withStaticEgressAddressGuard(ctx context.Context, ip netip.Addr, fn func(pgx.Tx, string) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM static_egress_addresses WHERE ip = $1 FOR UPDATE`, ip); err != nil {
		return fmt.Errorf("state: lock static egress address: %w", err)
	}
	q := sqlc.New()
	if _, err := q.ReleaseDeletedAppStaticEgressPins(ctx, tx, ip); err != nil {
		return fmt.Errorf("state: release deleted app pins: %w", err)
	}
	pinnedAccount := ""
	pin, err := q.LiveAppPinningStaticEgressIP(ctx, tx, ip)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("state: read static egress pin: %w", err)
	default:
		pinnedAccount = pin.AccountID
	}
	if err := fn(tx, pinnedAccount); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) AssignedStaticEgressAddressesForNode(ctx context.Context, nodeID string) ([]StaticEgressIPGrant, error) {
	node, err := parsePgUUID(nodeID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListAssignedStaticEgressAddressesForNode(ctx, s.pool, node)
	if err != nil {
		return nil, fmt.Errorf("state: list node static egress addresses: %w", err)
	}
	out := make([]StaticEgressIPGrant, 0, len(rows))
	for _, r := range rows {
		out = append(out, StaticEgressIPGrant{AccountID: r.AccountID, IP: r.Ip})
	}
	return out, nil
}

func (s *PgStore) StaticEgressAddressesForAccount(ctx context.Context, accountID string) ([]netip.Addr, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	ips, err := sqlc.New().ListStaticEgressAddressesForAccount(ctx, s.pool, account)
	if err != nil {
		return nil, fmt.Errorf("state: list account static egress addresses: %w", err)
	}
	return ips, nil
}

func (s *PgStore) StaticEgressAddressNodeIDs(ctx context.Context, accountID string, ip netip.Addr) ([]string, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	nodes, err := sqlc.New().StaticEgressAddressNodeIDs(ctx, s.pool, sqlc.StaticEgressAddressNodeIDsParams{Ip: ip, AccountID: account})
	if err != nil {
		return nil, fmt.Errorf("state: static egress address node: %w", err)
	}
	return nodes, nil
}

func (s *PgStore) ReleaseDeletedAppStaticEgressPins(ctx context.Context, ip netip.Addr) (int64, error) {
	n, err := sqlc.New().ReleaseDeletedAppStaticEgressPins(ctx, s.pool, ip)
	if err != nil {
		return 0, fmt.Errorf("state: release deleted app pins: %w", err)
	}
	return n, nil
}

// --- MemStore ---------------------------------------------------------

func (m *MemStore) staticEgressAddressLocked(a StaticEgressAddress) StaticEgressAddress {
	if n, ok := m.computeNodes[a.NodeID]; ok {
		a.NodeName = n.Name
	}
	a.PinnedAppID = ""
	if app, ok := m.livePinLocked(a.IP); ok {
		a.PinnedAppID = app.ID
	}
	return a
}

func (m *MemStore) livePinLocked(ip netip.Addr) (App, bool) {
	for _, app := range m.apps {
		if app.Status != AppDeleted && app.StaticEgressIP != nil && *app.StaticEgressIP == ip {
			return app, true
		}
	}
	return App{}, false
}

func (m *MemStore) releaseDeletedPinsLocked(ip netip.Addr) int64 {
	var n int64
	for id, app := range m.apps {
		if app.Status == AppDeleted && app.StaticEgressIP != nil && *app.StaticEgressIP == ip {
			app.StaticEgressIP, app.StaticEgressIPSetAt = nil, nil
			m.apps[id] = app
			n++
		}
	}
	return n
}

func (m *MemStore) ListStaticEgressAddresses(ctx context.Context) ([]StaticEgressAddress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]StaticEgressAddress, 0, len(m.staticEgressAddresses))
	for _, a := range m.staticEgressAddresses {
		out = append(out, m.staticEgressAddressLocked(a))
	}
	slices.SortFunc(out, func(a, b StaticEgressAddress) int { return a.IP.Compare(b.IP) })
	return out, nil
}

func (m *MemStore) StaticEgressAddressByIP(ctx context.Context, ip netip.Addr) (StaticEgressAddress, error) {
	if err := ctx.Err(); err != nil {
		return StaticEgressAddress{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.staticEgressAddresses[ip]
	if !ok {
		return StaticEgressAddress{}, ErrNotFound
	}
	return m.staticEgressAddressLocked(a), nil
}

func (m *MemStore) AddStaticEgressAddress(ctx context.Context, addr StaticEgressAddress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStaticEgressAddress(addr); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[addr.NodeID]; !ok {
		return ErrNotFound
	}
	if _, ok := m.staticEgressAddresses[addr.IP]; ok {
		return ErrConflict
	}
	if m.staticEgressAddresses == nil {
		m.staticEgressAddresses = make(map[netip.Addr]StaticEgressAddress)
	}
	now := time.Now().UTC()
	addr.CreatedAt, addr.UpdatedAt, addr.NodeName, addr.PinnedAppID = now, now, "", ""
	m.staticEgressAddresses[addr.IP] = addr
	return nil
}

func (m *MemStore) UpdateStaticEgressAddress(ctx context.Context, addr StaticEgressAddress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStaticEgressAddress(addr); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.staticEgressAddresses[addr.IP]
	if !ok {
		return ErrNotFound
	}
	if _, ok := m.computeNodes[addr.NodeID]; !ok {
		return ErrNotFound
	}
	m.releaseDeletedPinsLocked(addr.IP)
	if app, pinned := m.livePinLocked(addr.IP); pinned && app.AccountID != addr.AccountID {
		return ErrStaticEgressAddressPinned
	}
	cur.NodeID, cur.AccountID, cur.Note, cur.UpdatedAt = addr.NodeID, addr.AccountID, addr.Note, time.Now().UTC()
	m.staticEgressAddresses[addr.IP] = cur
	return nil
}

func (m *MemStore) RemoveStaticEgressAddress(ctx context.Context, ip netip.Addr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.staticEgressAddresses[ip]; !ok {
		return ErrNotFound
	}
	m.releaseDeletedPinsLocked(ip)
	if _, pinned := m.livePinLocked(ip); pinned {
		return ErrStaticEgressAddressPinned
	}
	delete(m.staticEgressAddresses, ip)
	return nil
}

func (m *MemStore) AssignedStaticEgressAddressesForNode(ctx context.Context, nodeID string) ([]StaticEgressIPGrant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StaticEgressIPGrant
	for _, a := range m.staticEgressAddresses {
		if a.NodeID == nodeID && a.AccountID != "" {
			out = append(out, StaticEgressIPGrant{AccountID: a.AccountID, IP: a.IP})
		}
	}
	slices.SortFunc(out, func(a, b StaticEgressIPGrant) int { return a.IP.Compare(b.IP) })
	return out, nil
}

func (m *MemStore) StaticEgressAddressesForAccount(ctx context.Context, accountID string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []netip.Addr
	for _, a := range m.staticEgressAddresses {
		if a.AccountID == accountID {
			out = append(out, a.IP)
		}
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return out, nil
}

func (m *MemStore) StaticEgressAddressNodeIDs(ctx context.Context, accountID string, ip netip.Addr) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.staticEgressAddresses[ip]; ok && a.AccountID == accountID && accountID != "" {
		return []string{a.NodeID}, nil
	}
	return nil, nil
}

func (m *MemStore) ReleaseDeletedAppStaticEgressPins(ctx context.Context, ip netip.Addr) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseDeletedPinsLocked(ip), nil
}

func (m *MemStore) staticEgressAddressGrantedLocked(accountID string, ip netip.Addr) bool {
	a, ok := m.staticEgressAddresses[ip]
	return ok && a.AccountID == accountID
}
