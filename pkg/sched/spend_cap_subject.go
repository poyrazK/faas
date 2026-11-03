package sched

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// spendCapSubjectTTL bounds how long a cached organization ownership answer
// stands; ownership changes rarely and a stale answer only delays the switch.
const spendCapSubjectTTL = time.Minute

type spendCapSubjects struct {
	mu    sync.Mutex
	byOrg map[string]spendCapSubject
}

type spendCapSubject struct {
	shared bool
	exp    time.Time
}

// spendCapSubject is the billing subject whose spend cap governs app: its
// organization when the organization owns its resources and is billed for
// them, else its account (ADR-20261030093512). meterd bills the same subject
// (meterAppSubject). A failed ownership read falls back to the account, as
// the cap check itself fails open.
func (e *Engine) spendCapSubject(ctx context.Context, app state.App) string {
	if app.OrgID == "" {
		return app.AccountID
	}
	now := time.Now()
	e.capSubjects.mu.Lock()
	entry, ok := e.capSubjects.byOrg[app.OrgID]
	e.capSubjects.mu.Unlock()
	if !ok || now.After(entry.exp) {
		owner, err := state.AppResourceAccount(ctx, e.store, app)
		if err != nil {
			return app.AccountID
		}
		entry = spendCapSubject{shared: owner.ResourceOwnerID() == app.OrgID, exp: now.Add(spendCapSubjectTTL)}
		e.capSubjects.mu.Lock()
		if e.capSubjects.byOrg == nil {
			e.capSubjects.byOrg = map[string]spendCapSubject{}
		}
		e.capSubjects.byOrg[app.OrgID] = entry
		e.capSubjects.mu.Unlock()
	}
	if entry.shared {
		return app.OrgID
	}
	return app.AccountID
}
