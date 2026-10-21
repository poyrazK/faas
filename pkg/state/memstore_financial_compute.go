package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var _ FinancialComputeStore = (*MemStore)(nil)

func (m *MemStore) CancelFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	m.mu.Lock()
	a, job := m.financialComputeJobAttempts[r.InstanceID]
	m.mu.Unlock()
	if job {
		if a.NodeID != r.NodeID {
			return out, financial.ErrComputeFence
		}
		r = a.request(r.Incarnation)
	} else if r.AppID != "" {
		app, readErr := m.AppByID(ctx, r.AppID)
		if readErr != nil {
			return out, readErr
		}
		r.AccountID = app.AccountID
	} else {
		return out, financial.ErrComputeFence
	}
	err = m.computeTransaction(ctx, r.AccountID, func(tx *memFinancialComputeTxn) error { var err error; out, err = computeCancel(tx, r); return err })
	return out, err
}

type financialComputeBook = financial.ComputeBook
type financialComputeLease = financial.ComputeLease
type financialComputeOwner struct {
	Incarnation       string
	ObservedAt        time.Time
	EgressIncarnation string
}
type memFinancialComputeTxn struct {
	m            *MemStore
	account      string
	now          time.Time
	books        map[string]financial.ComputeBook
	leases       map[string]financial.ComputeLease
	pendingHolds []financialComputeHold
}

func computeBookKey(account, app string, month time.Time) string {
	return account + "/" + app + "/" + month.UTC().Format(time.RFC3339)
}
func (t *memFinancialComputeTxn) clock() time.Time { return t.now }
func (t *memFinancialComputeTxn) book(app string, month time.Time) (financial.ComputeBook, error) {
	key := computeBookKey(t.account, app, month)
	if b, ok := t.books[key]; ok {
		return b, nil
	}
	b, ok := t.m.financialComputeBooks[key]
	if !ok {
		return b, ErrNotFound
	}
	return b, nil
}
func (t *memFinancialComputeTxn) putBook(b financial.ComputeBook) error {
	t.books[computeBookKey(b.AccountID, b.ScopeKey(), b.PeriodStart)] = b
	return nil
}
func (t *memFinancialComputeTxn) lease(id string) (financial.ComputeLease, error) {
	l, ok := t.leases[id]
	if !ok {
		l, ok = t.m.financialComputeLeases[id]
	}
	if !ok || l.AccountID != t.account {
		return financial.ComputeLease{}, ErrNotFound
	}
	return l, nil
}
func (t *memFinancialComputeTxn) putLease(l financial.ComputeLease) error {
	if old, ok := t.m.financialComputeLeases[l.ID]; ok && (old.AccountID != l.AccountID || old.AppID != l.AppID || old.JobID != l.JobID || old.JobRunID != l.JobRunID || old.JobTaskIndex != l.JobTaskIndex || old.Fence != l.Fence || old.InstanceID != l.InstanceID || old.NodeID != l.NodeID || old.Incarnation != l.Incarnation) {
		return financial.ErrComputeFence
	}
	for _, old := range t.m.financialComputeLeases {
		if old.ID != l.ID && old.InstanceID == l.InstanceID && old.NodeID == l.NodeID && old.SettledAt == nil && l.SettledAt == nil {
			return ErrConflict
		}
	}
	t.leases[l.ID] = l
	return nil
}
func (t *memFinancialComputeTxn) active(app string) ([]financial.ComputeLease, error) {
	out := []financial.ComputeLease{}
	project, isProject := financial.ComputeProjectID(app)
	for _, l := range t.m.financialComputeLeases {
		if l.AccountID != t.account || l.SettledAt != nil {
			continue
		}
		if isProject && t.projectLeaseInScope(l, project) || !isProject && (app == "" || l.WorkloadKey() == app) {
			out = append(out, l)
		}
	}
	if len(out) > api.FinancialComputeActiveMax {
		return nil, financial.ErrComputeUnavailable
	}
	return out, nil
}
func (t *memFinancialComputeTxn) policies(app string) ([]FinancialBudget, error) {
	out := []FinancialBudget{}
	for _, p := range t.m.financialBudgets {
		if p.AccountID == t.account && computePolicyMatches(p, app) && p.DeletedAt == nil {
			out = append(out, cloneFinancialBudget(p))
		}
	}
	return out, nil
}
func (t *memFinancialComputeTxn) holds() ([]financialComputeHold, error) {
	return t.m.financialComputeHolds[t.account], nil
}
func (t *memFinancialComputeTxn) putHold(h financialComputeHold) error {
	t.pendingHolds = append(t.pendingHolds, h)
	return nil
}
func (t *memFinancialComputeTxn) owner(node, incarnation string) (bool, error) {
	o, ok := t.m.financialComputeOwners[node]
	return ok && t.m.computeNodes[node].Active && o.Incarnation == incarnation && t.m.computeNodeRuntimeIncarnations[node] == incarnation, nil
}
func (m *MemStore) computeTransaction(ctx context.Context, account string, fn func(*memFinancialComputeTxn) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if uuid.Validate(account) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[account]; !ok {
		return ErrNotFound
	}
	t := newMemComputeTxn(m, account)
	err := fn(t)
	if err == nil || errors.Is(err, financial.ErrComputeBudget) {
		t.commit()
	}
	return err
}
func newMemComputeTxn(m *MemStore, account string) *memFinancialComputeTxn {
	now := time.Now().UTC()
	if m.clock != nil {
		now = m.clock().UTC()
	}
	return &memFinancialComputeTxn{m: m, account: account, now: now, books: map[string]financial.ComputeBook{}, leases: map[string]financial.ComputeLease{}}
}
func (t *memFinancialComputeTxn) commit() {
	m := t.m
	if m.financialComputeBooks == nil {
		m.financialComputeBooks = map[string]financial.ComputeBook{}
		m.financialComputeLeases = map[string]financial.ComputeLease{}
		m.financialComputeHolds = map[string][]financialComputeHold{}
	}
	for k, v := range t.books {
		m.financialComputeBooks[k] = v
	}
	for k, v := range t.leases {
		m.financialComputeLeases[k] = v
	}
	for _, h := range t.pendingHolds {
		found := false
		for _, old := range m.financialComputeHolds[t.account] {
			if old == h {
				found = true
			}
		}
		if !found {
			m.financialComputeHolds[t.account] = append(m.financialComputeHolds[t.account], h)
		}
	}
}
func (m *MemStore) AcquireFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	err = m.computeTransaction(ctx, r.AccountID, func(t *memFinancialComputeTxn) error {
		if r.JobRunID != "" {
			a, ok := m.financialComputeJobAttempts[r.InstanceID]
			if !ok || !computeJobClaimMatches(a, r) {
				return financial.ErrComputeFence
			}
			r.JobID = a.JobID
			valid, err := t.jobCurrent(financial.ComputeLease{InstanceID: r.InstanceID})
			if err != nil || !valid {
				return errors.Join(financial.ErrComputeFence, err)
			}
		} else {
			app, ok := m.apps[r.AppID]
			if !ok || app.AccountID != r.AccountID || app.Status == AppDeleted {
				return ErrNotFound
			}
		}
		project, policies, err := computeWorkloadStrictPolicies(t, r.WorkloadKey())
		if err != nil {
			return err
		}
		if len(policies) > 0 {
			if err := t.ensureAccount(policies); err != nil {
				return err
			}
			if err := t.ensureProject(project, policies); err != nil {
				return err
			}
			if _, err = t.book(r.WorkloadKey(), financial.ComputeMonth(t.now)); errors.Is(err, ErrNotFound) {
				err = t.initialize(r.WorkloadKey(), false)
			}
			if err != nil {
				return err
			}
		}
		out, err = computeAcquire(t, r)
		return err
	})
	return out, err
}
func (m *MemStore) RenewFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	err = m.computeTransaction(ctx, r.AccountID, func(t *memFinancialComputeTxn) error { out, err = computeMutate(t, r, false); return err })
	return out, err
}
func (m *MemStore) SettleFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	err = m.computeTransaction(ctx, r.AccountID, func(t *memFinancialComputeTxn) error { out, err = computeMutate(t, r, true); return err })
	return out, err
}
func (m *MemStore) RegisterFinancialComputeOwner(ctx context.Context, node, incarnation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if uuid.Validate(node) != nil || uuid.Validate(incarnation) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[node]; !ok {
		return ErrNotFound
	}
	if m.computeNodeRuntimeIncarnations[node] != incarnation {
		return financial.ErrComputeFence
	}
	if m.financialComputeOwners == nil {
		m.financialComputeOwners = map[string]financialComputeOwner{}
	}
	m.financialComputeOwners[node] = financialComputeOwner{Incarnation: incarnation, ObservedAt: time.Now().UTC(), EgressIncarnation: incarnation}
	return nil
}

func (m *MemStore) FinancialComputeOwnerValid(ctx context.Context, node, incarnation string) (valid bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owner, ok := m.financialComputeOwners[node]
	return ok && m.computeNodes[node].Active && owner.Incarnation == incarnation && m.computeNodeRuntimeIncarnations[node] == incarnation, nil
}

func (m *MemStore) FinancialComputeRetired(ctx context.Context, node, instance string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []financial.ComputeLease{}
	for _, l := range m.financialComputeLeases {
		if l.NodeID == node && l.InstanceID == instance {
			rows = append(rows, l)
		}
	}
	l, ok := latestComputeLease(rows)
	return ok && l.SettledAt != nil, nil
}

func (m *MemStore) FinancialComputeLease(ctx context.Context, account, id string) (financial.ComputeLease, bool, error) {
	if err := ctx.Err(); err != nil {
		return financial.ComputeLease{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.financialComputeLeases[id]
	if !ok || l.AccountID != account {
		return financial.ComputeLease{}, false, nil
	}
	if l.SettledAt != nil {
		at := *l.SettledAt
		l.SettledAt = &at
	}
	return l, true, nil
}

func (m *MemStore) FinancialComputeInstanceLease(ctx context.Context, account, app, instance string) (financial.ComputeLease, bool, error) {
	if err := ctx.Err(); err != nil {
		return financial.ComputeLease{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []financial.ComputeLease{}
	for _, l := range m.financialComputeLeases {
		if l.AccountID == account && l.WorkloadKey() == app && l.InstanceID == instance {
			rows = append(rows, l)
		}
	}
	l, ok := latestComputeLease(rows)
	return l, ok, nil
}
func (m *MemStore) FinancialComputeNodeLeases(ctx context.Context, node string) ([]financial.ComputeLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []financial.ComputeLease{}
	for _, l := range m.financialComputeLeases {
		if l.NodeID == node && l.SettledAt == nil {
			out = append(out, l)
		}
	}
	return out, nil
}
func (m *MemStore) FinancialComputePolicies(ctx context.Context, account, app string) ([]FinancialBudget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return computeWorkloadPolicies(newMemComputeTxn(m, account), app)
}
func (m *MemStore) FinancialComputeBalance(ctx context.Context, p FinancialBudget, now time.Time) (out financial.ComputeBalance, err error) {
	err = m.computeTransaction(ctx, p.AccountID, func(t *memFinancialComputeTxn) error { var err error; out, err = computeBalance(t, p, now); return err })
	return out, err
}
func (m *MemStore) FinancialComputeBillingSeconds(ctx context.Context, account, app, instance string, start time.Time, seconds int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []financial.ComputeLease{}
	for _, l := range m.financialComputeLeases {
		if l.AccountID == account && l.WorkloadKey() == app && l.InstanceID == instance {
			rows = append(rows, l)
		}
	}
	covered := m.computeMonthCoveredLocked(account, app, start)
	price := m.financialPriceAtLocked(account, "compute", start, "")
	return computeBillingSeconds(rows, start, seconds, price.Price, covered), nil
}

func (m *MemStore) FinancialComputeAppManaged(ctx context.Context, account, app string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, _ := newMemComputeTxn(m, account).workloadProject(app)
	for _, b := range m.financialComputeBooks {
		if b.AccountID == account && (b.ScopeKey() == app || b.ScopeKey() == "" || project != "" && b.ProjectID == project) {
			return true, nil
		}
	}
	return false, nil
}
func (m *MemStore) FinancialComputeFloorCovered(ctx context.Context, account, app string, at time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.computeMonthCoveredLocked(account, app, at), nil
}
func (m *MemStore) computeMonthCoveredLocked(account, app string, at time.Time) bool {
	if _, ok := m.financialComputeBooks[computeBookKey(account, "", financial.ComputeMonth(at))]; ok {
		return true
	}
	if _, ok := m.financialComputeBooks[computeBookKey(account, app, financial.ComputeMonth(at))]; ok {
		return true
	}
	t := newMemComputeTxn(m, account)
	project, _ := t.workloadProject(app)
	if _, ok := m.financialComputeBooks[computeBookKey(account, financial.ComputeProjectKey(project), financial.ComputeMonth(at))]; project != "" && ok {
		return true
	}
	_, policies, _ := computeWorkloadStrictPolicies(t, app)
	for _, p := range policies {
		if p.UpdatedAt.Before(financial.ComputeMonth(at).AddDate(0, 1, 0)) {
			return true
		}
	}
	return false
}

func (m *MemStore) FinancialComputeReady(ctx context.Context, account, app string) (ready bool, err error) {
	err = m.computeTransaction(ctx, account, func(t *memFinancialComputeTxn) error { ready = t.ready(app, true); return nil })
	return ready, err
}
func (t *memFinancialComputeTxn) ready(app string, quiet bool) bool {
	if job, ok := computeJobKey(app); ok {
		return t.jobReady(job)
	}
	if project, ok := financial.ComputeProjectID(app); ok {
		return t.projectReady(project, quiet)
	}
	if app == "" {
		return t.accountReady()
	}
	m := t.m
	month := financial.ComputeMonth(t.now)
	since := month
	appRow, ok := m.apps[app]
	if !ok || appRow.AccountID != t.account {
		return false
	}
	if appRow.CreatedAt.After(since) {
		since = appRow.CreatedAt
	}
	if m.financialRetainedFrom.After(since) {
		return false
	}
	for id, n := range m.computeNodes {
		if !n.Active {
			continue
		}
		o, ok := m.financialComputeOwners[id]
		if !ok || o.Incarnation != m.computeNodeRuntimeIncarnations[id] || t.now.Sub(o.ObservedAt) > 3*api.FinancialComputeOwnerHeartbeat {
			return false
		}
	}
	for minute := since.Truncate(time.Minute); minute.Before(t.now.Truncate(time.Minute)); minute = minute.Add(time.Minute) {
		if !m.financialSamplingWindows[minute].compute {
			return false
		}
	}
	if quiet {

		for _, i := range m.instances {
			if i.AppID == app && State(i.State).CountsForRAM() {
				return false
			}
		}
		for _, task := range m.appTasks {
			if task.AppID == app && (task.Status == "restoring" || task.Status == "running") {
				return false
			}
		}
	}
	active, err := t.active(app)
	return err == nil && len(active) == 0
}
func (t *memFinancialComputeTxn) initialize(app string, quiet bool) error {
	if job, ok := computeJobKey(app); ok {
		return t.initializeJob(job)
	}
	if project, ok := financial.ComputeProjectID(app); ok {
		return t.initializeProject(project, quiet)
	}
	if app == "" {
		return t.initializeAccount()
	}
	if !t.ready(app, quiet) {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.book(app, month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: t.account, AppID: app, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	total := int64(0)
	for _, row := range t.m.financialEvidence {
		e := row.Evidence
		if e.AccountID != t.account || e.Attribution.AppID != app || e.Meter != "compute" || e.Quantity <= 0 || e.Start.Before(month) || !e.Start.Before(b.PeriodEnd) {
			continue
		}
		var p FinancialPriceSnapshot
		for _, candidate := range t.m.financialPrices {
			if candidate.AccountID == t.account && candidate.Price.Version == row.PriceVersion && candidate.Price.Meter == "compute" {
				p = candidate
				break
			}
		}
		amount, err := computeBaselineAmount(e.Quantity, p.Price)
		if err != nil {
			return err
		}
		if amount > api.FinancialBudgetMoneyMax-total {
			return financial.ErrOverflow
		}
		total += amount
	}
	b.Committed = max(b.Committed, total)
	return t.putBook(b)
}
