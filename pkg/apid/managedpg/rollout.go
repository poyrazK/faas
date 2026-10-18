package managedpg

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Rollout is who may see a managed PostgreSQL preview: nobody by default,
// the listed accounts while it is tested on production, and everyone ("*")
// at launch. Everyone else meets exactly what an unregistered API route
// answers, so the preview is not discoverable.
type Rollout struct {
	all      bool
	accounts map[string]bool
}

// ParseRollout reads an allowlist variable: comma-separated account UUIDs,
// or "*" for every account. Empty is nobody.
func ParseRollout(name, value string) (Rollout, error) {
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
			return Rollout{}, fmt.Errorf("%s: %q is not an account UUID", name, entry)
		}
		if r.accounts == nil {
			r.accounts = map[string]bool{}
		}
		r.accounts[id.String()] = true
	}
	return r, nil
}

// Allows reports whether an account sees the preview.
func (r Rollout) Allows(accountID string) bool {
	if r.all {
		return true
	}
	id, err := uuid.Parse(accountID)
	return err == nil && r.accounts[id.String()]
}

// Off reports whether no account sees the preview.
func (r Rollout) Off() bool { return !r.all && len(r.accounts) == 0 }

// HidesUpgrade reports whether a request to an upgrade path must answer as
// an unregistered route before any handler runs: always until launch, so a
// method mismatch's 405 and Allow header do not reveal the route either.
func (r Rollout) HidesUpgrade(req *http.Request) bool { return !r.all && IsUpgradePath(req.URL.Path) }

// IsUpgradePath matches /v1/postgres/databases/{id}/upgrade and
// /v1/postgres/databases/{id}/upgrades/{upgrade_id}.
func IsUpgradePath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/v1/postgres/databases/")
	if !ok {
		return false
	}
	id, tail, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		return false
	}
	upgrade, ok := strings.CutPrefix(tail, "upgrades/")
	return tail == "upgrade" || ok && upgrade != "" && !strings.Contains(upgrade, "/")
}

// RolloutRoute answers as an unregistered route, before authentication,
// while no account sees the preview. rollout is read per request.
func RolloutRoute(rollout func() Rollout, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rollout().Off() {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r)
	}
}

// RolloutAccount answers as an unregistered route, right after
// authentication, to an account outside the rollout.
func RolloutAccount(rollout func() Rollout, next httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !rollout().Allows(acct.ID) {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r, acct)
	}
}
