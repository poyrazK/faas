package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// MemStore is an in-memory Store for tests and local development. It is safe for
// concurrent use and enforces the same uniqueness constraints as the schema
// (unique email, unique slug, unique key hash) so tests exercise real error
// paths. It is NOT durable — production uses the Postgres store.
type MemStore struct {
	mu          sync.Mutex
	accounts    map[string]Account
	keys        map[string]APIKey
	keyByHash   map[string]string
	apps        map[string]App
	deployments map[string]Deployment
	builds      map[string]Build
	domains     map[string]CustomDomain
	crons       map[string]Cron
	instances   map[string]Instance
	snapshots   []Snapshot
	events      []Event
	usage       []Usage
	idem        map[string]idemEntry
}

type idemEntry struct {
	status  int
	body    []byte
	created time.Time
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		accounts:    map[string]Account{},
		keys:        map[string]APIKey{},
		keyByHash:   map[string]string{},
		apps:        map[string]App{},
		deployments: map[string]Deployment{},
		builds:      map[string]Build{},
		domains:     map[string]CustomDomain{},
		crons:       map[string]Cron{},
		instances:   map[string]Instance{},
		snapshots:   []Snapshot{},
		idem:        map[string]idemEntry{},
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Accounts ---------------------------------------------------------------

func (m *MemStore) CreateAccount(_ context.Context, email string, plan api.Plan) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.Email == email {
			return Account{}, fmt.Errorf("state: account with email %q exists", email)
		}
	}
	a := Account{ID: newID(), Email: email, Plan: plan, Status: AccountActive, CreatedAt: time.Now()}
	m.accounts[a.ID] = a
	return a, nil
}

func (m *MemStore) AccountByID(_ context.Context, id string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

func (m *MemStore) AccountByEmail(_ context.Context, email string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.Email == email {
			return a, nil
		}
	}
	return Account{}, ErrNotFound
}

func (m *MemStore) AccountByKeyHash(_ context.Context, hash []byte) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	accountID, ok := m.keyByHash[hex.EncodeToString(hash)]
	if !ok {
		return Account{}, ErrNotFound
	}
	return m.accounts[accountID], nil
}

func (m *MemStore) UpdateAccountPlan(_ context.Context, id string, plan api.Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.Plan = plan
	m.accounts[id] = a
	return nil
}

func (m *MemStore) UpdateAccountStatus(_ context.Context, id string, status AccountStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.Status = status
	m.accounts[id] = a
	return nil
}

// --- API keys ---------------------------------------------------------------

func (m *MemStore) CreateAPIKey(_ context.Context, accountID string, hash []byte, label string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hex.EncodeToString(hash)
	if _, dup := m.keyByHash[h]; dup {
		return APIKey{}, fmt.Errorf("state: duplicate key hash")
	}
	k := APIKey{ID: newID(), AccountID: accountID, Hash: hash, Label: label, CreatedAt: time.Now()}
	m.keys[k.ID] = k
	m.keyByHash[h] = accountID
	return k, nil
}

func (m *MemStore) DeleteAPIKey(_ context.Context, accountID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.keys, keyID)
	delete(m.keyByHash, hex.EncodeToString(k.Hash))
	return nil
}

func (m *MemStore) ListAPIKeys(_ context.Context, accountID string) ([]APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIKey
	for _, k := range m.keys {
		if k.AccountID == accountID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) TouchKeyLastUsed(_ context.Context, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok {
		return ErrNotFound
	}
	k.LastUsedAt = time.Now()
	m.keys[keyID] = k
	return nil
}

// --- Apps -------------------------------------------------------------------

func (m *MemStore) CreateApp(_ context.Context, app App) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.Slug == app.Slug && a.Status != AppDeleted {
			return App{}, fmt.Errorf("state: slug %q already taken", app.Slug)
		}
	}
	if app.ID == "" {
		app.ID = newID()
	}
	if app.CreatedAt.IsZero() {
		app.CreatedAt = time.Now()
	}
	if app.Status == "" {
		app.Status = AppActive
	}
	m.apps[app.ID] = app
	return app, nil
}

func (m *MemStore) AppByID(_ context.Context, id string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, ErrNotFound
	}
	return a, nil
}

func (m *MemStore) AppBySlug(_ context.Context, slug string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.Slug == slug && a.Status != AppDeleted {
			return a, nil
		}
	}
	return App{}, ErrNotFound
}

func (m *MemStore) ListApps(_ context.Context, accountID string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.AccountID == accountID && a.Status != AppDeleted {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListAllApps(_ context.Context) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.Status != AppDeleted {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) CountDeployedApps(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, a := range m.apps {
		if a.AccountID == accountID && (a.Status == AppActive || a.Status == AppEvictedCold) {
			n++
		}
	}
	return n, nil
}

func (m *MemStore) UpdateApp(_ context.Context, id string, p UpdateAppParams) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, ErrNotFound
	}
	if p.RAMMB != nil {
		a.RAMMB = *p.RAMMB
	}
	if p.SetIdleTimeout {
		a.IdleTimeoutS = derefInt(p.IdleTimeoutS)
	}
	if p.MaxConcurrency != nil {
		a.MaxConcurrency = *p.MaxConcurrency
	}
	if p.Status != nil {
		a.Status = *p.Status
	}
	if p.Manifest != nil {
		a.Manifest = *p.Manifest
	}
	m.apps[id] = a
	return a, nil
}

func (m *MemStore) DeleteApp(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return ErrNotFound
	}
	a.Status = AppDeleted
	m.apps[id] = a
	return nil
}

// --- Deployments ------------------------------------------------------------

func (m *MemStore) CreateDeployment(_ context.Context, d Deployment) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[d.AppID]; !ok {
		return Deployment{}, fmt.Errorf("state: deployment for unknown app %q", d.AppID)
	}
	if d.ID == "" {
		d.ID = newID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	if d.Status == "" {
		d.Status = DeployPending
	}
	if d.Kind == "" {
		d.Kind = DeploymentKindImage
	}
	m.deployments[d.ID] = d
	return d, nil
}

func (m *MemStore) DeploymentByID(_ context.Context, id string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	return d, nil
}

func (m *MemStore) LatestDeployment(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) LiveDeployment(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && d.Status == DeployLive && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) LatestSupersededDeployment(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && d.Status == DeploySuperseded && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) ListDeploymentsForApp(_ context.Context, appID string, limit, offset int) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []Deployment
	for _, d := range m.deployments {
		if d.AppID == appID {
			all = append(all, d)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	if offset >= len(all) {
		return nil, nil
	}
	all = all[offset:]
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all, nil
}

func (m *MemStore) UpdateDeploymentStatus(_ context.Context, id string, status DeploymentStatus, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.Status = status
	d.Error = errMsg
	m.deployments[id] = d
	return nil
}

func (m *MemStore) MarkDeploymentSuperseded(ctx context.Context, id string) error {
	return m.UpdateDeploymentStatus(ctx, id, DeploySuperseded, "")
}

func (m *MemStore) MarkDeploymentLive(ctx context.Context, id string) error {
	return m.UpdateDeploymentStatus(ctx, id, DeployLive, "")
}

func (m *MemStore) SetDeploymentRootfs(_ context.Context, id, path string, bytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.RootfsPath = path
	d.RootfsBytes = bytes
	m.deployments[id] = d
	return nil
}

// --- Builds -----------------------------------------------------------------

func (m *MemStore) CreateBuild(_ context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[deploymentID]; !ok {
		return Build{}, fmt.Errorf("state: build for unknown deployment %q", deploymentID)
	}
	b := Build{ID: newID(), DeploymentID: deploymentID, Kind: kind, SourceBytes: sourceBytes, Status: BuildQueued, LogPath: logPath}
	m.builds[b.ID] = b
	return b, nil
}

func (m *MemStore) BuildByID(_ context.Context, id string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return Build{}, ErrNotFound
	}
	return b, nil
}

func (m *MemStore) BuildByDeployment(_ context.Context, deploymentID string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Build
	found := false
	for _, b := range m.builds {
		if b.DeploymentID == deploymentID && (!found || b.StartedAt.After(latest.StartedAt)) {
			latest, found = b, true
		}
	}
	if !found {
		return Build{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) UpdateBuildStatus(_ context.Context, id string, status BuildStatus, fc FailureClass, started, finished bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return ErrNotFound
	}
	b.Status = status
	if fc != "" {
		b.FailureClass = fc
	}
	now := time.Now()
	if started {
		b.StartedAt = now
	}
	if finished {
		b.FinishedAt = now
	}
	m.builds[id] = b
	return nil
}

// --- Custom domains ---------------------------------------------------------

func (m *MemStore) CreateCustomDomain(_ context.Context, domain, appID, token string) (CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.domains[domain]; dup {
		return CustomDomain{}, fmt.Errorf("state: domain %q already exists", domain)
	}
	d := CustomDomain{Domain: domain, AppID: appID, ChallengeToken: token}
	m.domains[domain] = d
	return d, nil
}

func (m *MemStore) DomainByName(_ context.Context, domain string) (CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok {
		return CustomDomain{}, ErrNotFound
	}
	return d, nil
}

func (m *MemStore) ListDomainsForApp(_ context.Context, appID string) ([]CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomDomain
	for _, d := range m.domains {
		if d.AppID == appID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

func (m *MemStore) ListDomainsForAccount(_ context.Context, accountID string) ([]CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomDomain
	for _, d := range m.domains {
		app, ok := m.apps[d.AppID]
		if !ok {
			continue
		}
		if app.AccountID == accountID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

func (m *MemStore) MarkDomainVerified(_ context.Context, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok {
		return ErrNotFound
	}
	d.VerifiedAt = time.Now()
	m.domains[domain] = d
	return nil
}

func (m *MemStore) DeleteCustomDomain(_ context.Context, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[domain]; !ok {
		return ErrNotFound
	}
	delete(m.domains, domain)
	return nil
}

// --- Crons ------------------------------------------------------------------

func (m *MemStore) CreateCron(_ context.Context, appID, schedule, path string, enabled bool) (Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return Cron{}, fmt.Errorf("state: cron for unknown app %q", appID)
	}
	c := Cron{ID: newID(), AppID: appID, Schedule: schedule, Path: path, Enabled: enabled, CreatedAt: time.Now()}
	m.crons[c.ID] = c
	return c, nil
}

func (m *MemStore) CronByID(_ context.Context, id string) (Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok {
		return Cron{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) UpdateCron(_ context.Context, id string, schedule, path *string, enabled *bool) (Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok {
		return Cron{}, ErrNotFound
	}
	if schedule != nil {
		c.Schedule = *schedule
	}
	if path != nil {
		c.Path = *path
	}
	if enabled != nil {
		c.Enabled = *enabled
	}
	m.crons[id] = c
	return c, nil
}

func (m *MemStore) DeleteCron(_ context.Context, id, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok || c.AppID != appID {
		return ErrNotFound
	}
	delete(m.crons, id)
	return nil
}

func (m *MemStore) ListCronsForApp(_ context.Context, appID string) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Cron
	for _, c := range m.crons {
		if c.AppID == appID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListEnabledCrons(_ context.Context) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Cron
	for _, c := range m.crons {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

// --- Instances --------------------------------------------------------------

func (m *MemStore) CreateInstance(_ context.Context, appID, deploymentID, state string, ramMB int) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins := Instance{ID: newID(), AppID: appID, DeploymentID: deploymentID, State: state, RAMMB: ramMB}
	if state == "running" {
		ins.StartedAt = time.Now()
	}
	m.instances[ins.ID] = ins
	return ins, nil
}

func (m *MemStore) InstanceByID(_ context.Context, id string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return Instance{}, ErrNotFound
	}
	return ins, nil
}

func (m *MemStore) ListInstancesForApp(_ context.Context, appID string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if ins.AppID == appID {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func (m *MemStore) UpdateInstanceState(_ context.Context, id, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.State = state
	m.instances[id] = ins
	return nil
}

func (m *MemStore) SetInstanceRuntime(_ context.Context, id, netns, hostIP string, guestUID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.Netns = netns
	ins.HostIP = hostIP
	ins.GuestUID = guestUID
	ins.StartedAt = time.Now()
	m.instances[id] = ins
	return nil
}

func (m *MemStore) RunningInstanceForApp(_ context.Context, appID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest Instance
	found := false
	for _, ins := range m.instances {
		if ins.AppID != appID || ins.State != "running" {
			continue
		}
		if !found || ins.StartedAt.After(newest.StartedAt) {
			newest = ins
			found = true
		}
	}
	if !found {
		return Instance{}, ErrNotFound
	}
	return newest, nil
}

func (m *MemStore) TouchInstancesLastSeen(_ context.Context, touches []InstanceTouch) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	applied := 0
	for _, t := range touches {
		ins, ok := m.instances[t.InstanceID]
		if !ok {
			continue
		}
		ins.LastRequestAt = t.LastRequest
		m.instances[t.InstanceID] = ins
		applied++
	}
	return applied, nil
}

// --- snapshots --------------------------------------------------------------
//
// MemStore's snapshot table mirrors the Postgres semantics: First row wins,
// subsequent inserts for the same (deployment_id, fc_version, path) collide
// as ErrConflict so imaged's idempotent retry is silent.

func (m *MemStore) CreateSnapshot(_ context.Context, snap Snapshot) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if snap.ID == "" {
		snap.ID = uuid.NewString()
	}
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = time.Now()
	}
	for _, existing := range m.snapshots {
		if existing.DeploymentID == snap.DeploymentID && existing.FCVersion == snap.FCVersion && existing.Path == snap.Path {
			return Snapshot{}, ErrConflict
		}
	}
	m.snapshots = append(m.snapshots, snap)
	return snap, nil
}

func (m *MemStore) LatestSnapshot(_ context.Context, deploymentID string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Snapshot
	found := false
	for _, s := range m.snapshots {
		if s.DeploymentID != deploymentID || s.Stale {
			continue
		}
		if !found || s.CreatedAt.After(latest.CreatedAt) {
			latest = s
			found = true
		}
	}
	if !found {
		return Snapshot{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) MarkSnapshotStale(_ context.Context, snapshotID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.snapshots {
		if m.snapshots[i].ID == snapshotID {
			m.snapshots[i].Stale = true
			return nil
		}
	}
	return ErrNotFound
}

// --- Audit ------------------------------------------------------------------

func (m *MemStore) AppendEvent(_ context.Context, actor, kind string, subject *string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subj *interface{ ID() string }
	_ = subj
	e := Event{At: time.Now(), Actor: actor, Kind: kind, Data: append([]byte(nil), data...)}
	m.events = append(m.events, e)
	return nil
}

func (m *MemStore) ListEvents(_ context.Context, subject string, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for i := len(m.events) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		e := m.events[i]
		if subject == "" || (e.Subject != nil && false) {
			out = append(out, e)
		}
	}
	return out, nil
}

// --- Usage ------------------------------------------------------------------

func (m *MemStore) AppendUsage(_ context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, u := range m.usage {
		if u.AccountID == accountID && u.AppID == appID && u.Month.Equal(minute) {
			m.usage[i].MBSeconds += mbSeconds
			m.usage[i].Requests += requests
			return nil
		}
	}
	m.usage = append(m.usage, Usage{AccountID: accountID, AppID: appID, Month: minute, MBSeconds: mbSeconds, Requests: requests})
	return nil
}

func (m *MemStore) UsageByMonth(_ context.Context, accountID string, month time.Time) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Usage
	for _, u := range m.usage {
		if u.AccountID == accountID && u.Month.Equal(month) {
			out = append(out, u)
		}
	}
	return out, nil
}

// --- Idempotency ------------------------------------------------------------

func (m *MemStore) GetIdempotent(_ context.Context, accountID, key string) (int, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.idem[accountID+"\x00"+key]
	if !ok || time.Since(e.created) > 24*time.Hour {
		return 0, nil, ErrNotFound
	}
	return e.status, e.body, nil
}

func (m *MemStore) PutIdempotent(_ context.Context, accountID, key string, status int, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idem[accountID+"\x00"+key] = idemEntry{status: status, body: append([]byte(nil), body...), created: time.Now()}
	return nil
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// compile-time check that MemStore satisfies Store.
var _ Store = (*MemStore)(nil)
