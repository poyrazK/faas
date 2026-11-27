package sales

import (
	"context"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/referrals"
)

// roster maps a lower-case email to its strongest sales role.
type roster map[string]string

func (r roster) role(email string) string { return r[strings.ToLower(strings.TrimSpace(email))] }

func (r roster) emails() []string {
	out := make([]string, 0, len(r))
	for email := range r {
		out = append(out, email)
	}
	sort.Strings(out)
	return out
}

func (r roster) repEmails() []string {
	out := []string{}
	for email, role := range r {
		if role == referrals.RoleRep {
			out = append(out, email)
		}
	}
	sort.Strings(out)
	return out
}

// lastRoster is the most recent roster read, for callers without a request
// context (session MFA stamping, dashboard links). Access checks never use it.
type LastRoster struct{ v atomic.Pointer[roster] }

func (s *Service) configRoster() roster {
	out := roster{}
	for email := range s.Config.Members {
		out[email] = s.Config.ConfigRole(email)
	}
	for email := range s.Config.Admins {
		out[email] = referrals.RoleAdmin
	}
	return out
}

// roster merges configuration grants with active portal members; the
// strongest grant wins. It is read fresh for every access decision, so a
// removal takes effect on the next request.
func (s *Service) roster(ctx context.Context) (roster, error) {
	out := s.configRoster()
	if store, ok := s.Store.(referrals.TeamStore); ok {
		stored, err := store.TeamRoster(ctx)
		if err != nil {
			return nil, err
		}
		for email, role := range stored {
			if role == referrals.RoleAdmin {
				continue // owner admins come only from configuration
			}
			if referrals.RoleRank(role) > referrals.RoleRank(out[email]) {
				out[email] = role
			}
		}
	}
	if s.Last != nil {
		s.Last.v.Store(&out)
	}
	return out, nil
}

// CachedMember reports configured membership, or membership seen in the last
// roster read. It only decides whether login confines a member without an
// authenticator to enrollment and whether to show a dashboard link.
func (s *Service) CachedMember(email string) bool {
	if !s.Enabled() {
		return false
	}
	if s.Config.ConfigRole(email) != "" {
		return true
	}
	if s.Last == nil {
		return false
	}
	if r := s.Last.v.Load(); r != nil {
		return r.role(email) != ""
	}
	return false
}
