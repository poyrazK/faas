package livequeries

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Gate decides who sees live queries. Rollout, read from
// FAAS_LIVE_QUERIES_ACCOUNTS, is the boot-time gate; apid substitutes the
// live_queries platform flag (ADR-1700) once an operator has written it.
type Gate interface {
	// Allows reports whether an account sees live queries.
	Allows(accountID string) bool
	// Off reports whether no account sees live queries.
	Off() bool
	// Hidden reports whether a request must answer as an unregistered route
	// before its handler runs.
	Hidden(req *http.Request) bool
}

// Rollout is who may see live queries (FAAS_LIVE_QUERIES_ACCOUNTS): nobody
// by default, the listed accounts while the preview is tested on
// production, and everyone ("*") at launch. Everyone else meets exactly what
// an unregistered API route answers, and their realtime endpoints get no
// live routes, so neither the API nor the protocol is discoverable.
type Rollout struct {
	all      bool
	accounts map[string]bool
}

// ParseRollout reads FAAS_LIVE_QUERIES_ACCOUNTS: comma-separated account
// UUIDs, or "*" for every account. Empty turns live queries off.
func ParseRollout(value string) (Rollout, error) {
	var r Rollout
	for _, entry := range strings.Split(value, ",") {
		switch entry = strings.TrimSpace(entry); entry {
		case "":
			continue
		case "*":
			return Rollout{all: true}, nil
		}
		id, err := uuid.Parse(entry)
		if err != nil {
			return Rollout{}, fmt.Errorf("FAAS_LIVE_QUERIES_ACCOUNTS: %q is not an account UUID", entry)
		}
		if r.accounts == nil {
			r.accounts = map[string]bool{}
		}
		r.accounts[id.String()] = true
	}
	return r, nil
}

// Allows reports whether an account sees live queries.
func (r Rollout) Allows(accountID string) bool {
	if r.all {
		return true
	}
	id, err := uuid.Parse(accountID)
	return err == nil && r.accounts[id.String()]
}

// Off reports whether no account sees live queries.
func (r Rollout) Off() bool { return !r.all && len(r.accounts) == 0 }

// Accounts reports the rollout's shape: every account, or the listed ones in
// sorted order.
func (r Rollout) Accounts() (all bool, accounts []string) {
	if r.all {
		return true, nil
	}
	accounts = make([]string, 0, len(r.accounts))
	for id := range r.accounts {
		accounts = append(accounts, id)
	}
	slices.Sort(accounts)
	return false, accounts
}

// Hidden reports whether a request to a live-query path must answer as an
// unregistered route before its handler runs: always while the rollout is
// not everyone, so a method mismatch's 405 and Allow header do not reveal
// the route either.
func (r Rollout) Hidden(req *http.Request) bool { return !r.all && IsLivePath(req.URL.Path) }

// IsLivePath matches /v1/postgres/databases/{id}/live and /live/rotate.
func IsLivePath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/v1/postgres/databases/")
	if !ok {
		return false
	}
	id, tail, ok := strings.Cut(rest, "/")
	return ok && id != "" && (tail == "live" || tail == "live/rotate")
}

// Route answers as an unregistered route, before authentication, while no
// account sees live queries.
func Route(gate func() Gate, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gate().Off() {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r)
	}
}

// Account answers as an unregistered route, right after authentication, to
// an account outside the rollout.
func Account(gate func() Gate, next httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !gate().Allows(acct.ID) {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r, acct)
	}
}
