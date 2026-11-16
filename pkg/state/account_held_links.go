package state

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	factsql "github.com/onebox-faas/faas/pkg/state/signupfacts/sqlc"
)

var (
	_ AccountHeldLinkStore = (*PgStore)(nil)
	_ AccountHeldLinkStore = (*MemStore)(nil)
)

// heldLinkMax bounds the links one sign-up records, as the SQL LIMIT does.
const heldLinkMax = 50

// LinkAccountToHeld implements AccountHeldLinkStore.
func (s *PgStore) LinkAccountToHeld(ctx context.Context, accountID string, domainLinkable bool, since, at time.Time) ([]AccountHeldLink, error) {
	account := mustPgUUID(accountID)
	if !account.Valid {
		return nil, ErrInvalidArgument
	}
	rows, err := factsql.New().LinkHeldAccounts(ctx, s.pool, factsql.LinkHeldAccountsParams{
		AccountID:      account,
		DetectedAt:     pgtype.Timestamptz{Time: at.UTC(), Valid: true},
		Since:          pgtype.Timestamptz{Time: since.UTC(), Valid: true},
		DomainLinkable: domainLinkable,
	})
	if err != nil {
		return nil, fmt.Errorf("state: link %s to held accounts: %w", accountID, mapErr(err))
	}
	out := make([]AccountHeldLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountHeldLink{
			AccountID: accountID, HeldAccountID: pgUUIDString(r.HeldAccountID), HeldOrgID: pgUUIDString(r.HeldOrgID),
			HeldReason: r.HeldReason, Signal: r.Signal, DetectedAt: at.UTC(),
		})
	}
	sortHeldLinks(out)
	return out, nil
}

// AccountHeldLinks implements AccountHeldLinkStore.
func (s *PgStore) AccountHeldLinks(ctx context.Context, accountID string) ([]AccountHeldLink, error) {
	account := mustPgUUID(accountID)
	if !account.Valid {
		return nil, ErrInvalidArgument
	}
	rows, err := factsql.New().HeldLinksForAccount(ctx, s.pool, account)
	if err != nil {
		return nil, fmt.Errorf("state: held links for %s: %w", accountID, mapErr(err))
	}
	out := make([]AccountHeldLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountHeldLink{
			AccountID: accountID, HeldAccountID: pgUUIDString(r.HeldAccountID), HeldOrgID: pgUUIDString(r.HeldOrgID),
			HeldReason: r.HeldReason, Signal: r.Signal, DetectedAt: r.DetectedAt.Time.UTC(),
		})
	}
	return out, nil
}

// signupNetwork is the /24 (IPv4) or /48 (IPv6) an address belongs to.
func signupNetwork(ip string) (netip.Prefix, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	p, err := addr.Prefix(bits)
	return p, err == nil
}

func sortHeldLinks(links []AccountHeldLink) {
	sort.Slice(links, func(i, j int) bool {
		if links[i].HeldAccountID != links[j].HeldAccountID {
			return links[i].HeldAccountID < links[j].HeldAccountID
		}
		return links[i].Signal < links[j].Signal
	})
}

// LinkAccountToHeld implements AccountHeldLinkStore.
func (m *MemStore) LinkAccountToHeld(_ context.Context, accountID string, domainLinkable bool, since, at time.Time) ([]AccountHeldLink, error) {
	if accountID == "" {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	me, ok := m.signupFacts[accountID]
	if !ok {
		return nil, nil
	}
	type heldSubject struct{ account, org, reason string }
	var held []heldSubject
	for id, a := range m.accounts {
		if id != accountID && a.AbuseHoldAt != nil && a.AbuseHoldReason != "" {
			held = append(held, heldSubject{account: id, reason: a.AbuseHoldReason})
		}
	}
	for _, org := range m.orgs {
		if org.Personal || org.AbuseHoldAt == nil || org.AbuseHoldReason == "" {
			continue
		}
		for _, mem := range m.memberships {
			if mem.OrgID == org.ID && mem.Role == OrgRoleOwner && mem.RemovedAt == nil && mem.AccountID != accountID {
				held = append(held, heldSubject{account: mem.AccountID, org: org.ID, reason: org.AbuseHoldReason})
			}
		}
	}
	network, hasNetwork := signupNetwork(me.SignupIP)
	inNetwork := func(ip string) bool {
		addr, err := netip.ParseAddr(ip)
		return err == nil && hasNetwork && network.Contains(addr.Unmap())
	}
	if m.heldLinks == nil {
		m.heldLinks = make(map[string][]AccountHeldLink)
	}
	seen := make(map[string]bool)
	for _, l := range m.heldLinks[accountID] {
		seen[l.HeldAccountID+"|"+l.Signal] = true
	}
	var out []AccountHeldLink
	add := func(h heldSubject, signal string) {
		key := h.account + "|" + signal
		if seen[key] || len(out) >= heldLinkMax {
			return
		}
		seen[key] = true
		out = append(out, AccountHeldLink{AccountID: accountID, HeldAccountID: h.account, HeldOrgID: h.org,
			HeldReason: h.reason, Signal: signal, DetectedAt: at.UTC()})
	}
	sort.Slice(held, func(i, j int) bool { return held[i].account < held[j].account })
	for _, h := range held {
		if hasNetwork {
			match := false
			if f, ok := m.signupFacts[h.account]; ok && inNetwork(f.SignupIP) {
				match = true
			}
			for _, sess := range m.sessions {
				if !match && sess.AccountID == h.account && !sess.IssuedAt.Before(since) && inNetwork(sess.IssuedIP) {
					match = true
				}
			}
			if match {
				add(h, HeldLinkSignalNetwork)
			}
		}
		if domainLinkable && me.EmailDomain != "" {
			if a, ok := m.accounts[h.account]; ok {
				if i := strings.LastIndexByte(a.Email, '@'); i >= 0 && strings.ToLower(a.Email[i+1:]) == me.EmailDomain {
					add(h, HeldLinkSignalEmailDomain)
				}
			}
		}
	}
	sortHeldLinks(out)
	m.heldLinks[accountID] = append(m.heldLinks[accountID], out...)
	return out, nil
}

// AccountHeldLinks implements AccountHeldLinkStore.
func (m *MemStore) AccountHeldLinks(_ context.Context, accountID string) ([]AccountHeldLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AccountHeldLink(nil), m.heldLinks[accountID]...), nil
}
