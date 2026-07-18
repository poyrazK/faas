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

// stripePushKey is the (account, hour) dedupe key the hourly Stripe
// pusher uses; declared above MemStore so the struct field below can
// reference it.
type stripePushKey struct {
	accountID string
	hour      time.Time
}

// MemStore is an in-memory Store for tests and local development. It is safe for
// concurrent use and enforces the same uniqueness constraints as the schema
// (unique email, unique slug, unique key hash) so tests exercise real error
// paths. It is NOT durable — production uses the Postgres store.
type MemStore struct {
	mu        sync.Mutex
	accounts  map[string]Account
	keys      map[string]APIKey
	keyByHash map[string]string
	apps      map[string]App
	// githubBindings is keyed by appID. Holds the (install_id,
	// repo_full_name, production_branch) tuple the /oauth/callback
	// handler writes after verifying the install against api.github.com
	// (review findings #1 + #2 closure, ADR-012).
	githubBindings map[string]GitHubBinding
	deployments    map[string]Deployment
	builds         map[string]Build
	domains        map[string]CustomDomain
	crons          map[string]Cron
	instances      map[string]Instance
	// loginTokens is keyed by the hex-encoded SHA-256 hash of the
	// raw token (so the binary []byte hash from ConsumeLoginToken
	// matches the map key format used in MemStore everywhere else).
	loginTokens map[string]LoginToken
	// deploymentLogs is keyed by deployment_id; the inner slice is
	// append-ordered (which matches the Postgres seq order). MemStore
	// mirrors the bigserial PK by appending + assigning a monotonic
	// per-deployment counter so cursor pagination stays identical
	// to the production shape.
	deploymentLogs map[string][]LogEntry
	deploymentSeq  map[string]int64
	snapshots      []Snapshot
	events         []Event
	// usage holds one row per (instance, minute) — mirrors PgStore's
	// usage_minutes PK. Aggregated into `usageByMonth` (per app, per
	// calendar month) so UsageByMonth can keep returning the spec §10
	// per-app shape unchanged. (M7 fix; the previous shape was wrong.)
	usage        []usageMinute
	usageByMonth []Usage
	idem         map[string]idemEntry
	// stripeByCustomer is the reverse-lookup index used by
	// AccountByStripeCustomerID; keyed by Stripe `cus_…` ID.
	stripeByCustomer map[string]string
	// stripePushHours tracks which (account, hour) pairs the hourly
	// Stripe pusher has already pushed; prevents double-billing on
	// redelivery.
	stripePushHours map[stripePushKey]struct{}
}

type idemEntry struct {
	status  int
	body    []byte
	created time.Time
}

// usageMinute mirrors the production schema (PK (instance_id, minute)).
// Spec §10 keeps per-app aggregates for the dashboard, but the SQL contract
// is per-instance — accumulating mb_seconds on conflict is the atomic
// operation the minute sampler relies on. MemStore matches that shape so
// tests are truthful for M7. Aggregated into `usageByMonth` for the
// per-app read shape the rest of the system expects.
type usageMinute struct {
	AccountID  string
	AppID      string
	InstanceID string
	Minute     time.Time
	MBSeconds  int64
	Requests   int64
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		accounts:       map[string]Account{},
		keys:           map[string]APIKey{},
		keyByHash:      map[string]string{},
		apps:           map[string]App{},
		githubBindings: map[string]GitHubBinding{},
		deployments:    map[string]Deployment{},
		builds:         map[string]Build{},
		domains:        map[string]CustomDomain{},
		crons:          map[string]Cron{},
		instances:      map[string]Instance{},
		loginTokens:    map[string]LoginToken{},
		deploymentLogs: map[string][]LogEntry{},
		deploymentSeq:  map[string]int64{},
		snapshots:      []Snapshot{},
		events:         []Event{},
		usage:          []usageMinute{},
		usageByMonth:   []Usage{},
		idem:           map[string]idemEntry{},
		// stripeByCustomer is the reverse-lookup map AccountByStripeCustomerID
		// walks; populated by UpdateAccountStripeCustomerID.
		stripeByCustomer: map[string]string{},
		// stripePushHours is the per-(account, hour) dedupe set the
		// meterd hourly pusher reads/writes.
		stripePushHours: map[stripePushKey]struct{}{},
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

// UpdateAccountStripeCustomerID records the Stripe `cus_…` ID. MemStore
// keeps an index map for O(1) webhook lookup; PgStore mirrors with a
// schema-level unique index (added in Slice 2's migration).
func (m *MemStore) UpdateAccountStripeCustomerID(_ context.Context, id, stripeCustomerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.StripeCustomerID = stripeCustomerID
	m.accounts[id] = a
	// Maintain the reverse-lookup map for AccountByStripeCustomerID.
	for k, v := range m.stripeByCustomer {
		if v == id && k != stripeCustomerID {
			delete(m.stripeByCustomer, k)
			break
		}
	}
	m.stripeByCustomer[stripeCustomerID] = id
	return nil
}

// AccountByStripeCustomerID is the reverse-lookup the Stripe webhook
// uses to find the account behind an event's `customer` field. O(1) via
// the index map; PgStore implements this with a unique index.
func (m *MemStore) AccountByStripeCustomerID(_ context.Context, stripeCustomerID string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.stripeByCustomer[stripeCustomerID]
	if !ok {
		return Account{}, ErrNotFound
	}
	a, ok := m.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

// ListAllAccounts walks the account map under the store mutex. The
// meterd quota + Stripe-push loops both call this; bounded by the
// customer count on the one box.
func (m *MemStore) ListAllAccounts(_ context.Context) ([]Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Account, 0, len(m.accounts))
	for _, a := range m.accounts {
		out = append(out, a)
	}
	return out, nil
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

// RecordGitHubBinding persists the (app → installation_id, repo,
// branch) tuple. Idempotent: re-binding overwrites. Refuses if the
// (install_id, repo) pair is already claimed by a different app
// (mirrors the apps_github_install_repo_uniq partial index in
// migration 00007 — the §11 least-privilege audit invariant).
func (m *MemStore) RecordGitHubBinding(_ context.Context, appID string, installID int64, repoFullName, productionBranch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return ErrNotFound
	}
	for otherAppID, b := range m.githubBindings {
		if otherAppID == appID {
			continue
		}
		if b.InstallID == installID && b.RepoFullName == repoFullName {
			return fmt.Errorf("state: github binding already held by app %s", otherAppID)
		}
	}
	m.githubBindings[appID] = GitHubBinding{
		AppID:            appID,
		InstallID:        installID,
		RepoFullName:     repoFullName,
		ProductionBranch: productionBranch,
	}
	return nil
}

// GitHubBindingForApp returns the persisted binding for an app, or
// ErrNotFound if the app has never been GitHub-connected.
func (m *MemStore) GitHubBindingForApp(_ context.Context, appID string) (GitHubBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.githubBindings[appID]
	if !ok || b.InstallID == 0 {
		return GitHubBinding{}, ErrNotFound
	}
	return b, nil
}

// InstallationIDForRepo is the reverse lookup githubd's checks.go
// uses to mint the right per-install access token for a push
// (review finding #1+#2 closure for the M7.5 OAuth path).
// Returns ErrNotFound if no app is bound to repoFullName. The map
// scan is O(apps bound to GitHub); at v1.0 scale (≤100 apps per
// account on the Scale plan, §4.2 limits) this is cheaper than
// maintaining a second repo→install index.
func (m *MemStore) InstallationIDForRepo(_ context.Context, repoFullName string) (int64, error) {
	if repoFullName == "" {
		return 0, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.githubBindings {
		if b.RepoFullName == repoFullName && b.InstallID != 0 {
			return b.InstallID, nil
		}
	}
	return 0, ErrNotFound
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

// ListDeploymentsForAccount walks every app the account owns, collects
// its deployments, and returns them sorted DESC by created_at with
// before acting as the inclusive upper bound. Cursor pagination
// (before→NextBefore) lets the dashboard page backwards without an
// offset scan.
func (m *MemStore) ListDeploymentsForAccount(_ context.Context, accountID string, before time.Time, limit int) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{})
	for _, a := range m.apps {
		if a.AccountID == accountID && a.Status != AppDeleted {
			owned[a.ID] = struct{}{}
		}
	}
	var all []Deployment
	for _, d := range m.deployments {
		if _, ok := owned[d.AppID]; !ok {
			continue
		}
		// First page (before.IsZero()): include everything created at
		// or before "before". Subsequent pages skip rows whose
		// CreatedAt >= since the caller passed the previous
		// response's last-seen CreatedAt as the "before" cursor.
		if !before.IsZero() && !d.CreatedAt.Before(before) {
			continue
		}
		all = append(all, d)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
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

func (m *MemStore) UpdateCron(_ context.Context, id string, schedule, path *string, enabled *bool, createdAt *time.Time) (Cron, error) {
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
	if createdAt != nil {
		c.CreatedAt = *createdAt
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

// MarkCronFired stamps the cron row's LastFiredAt field. Used by the
// schedd dispatch loop after a synthetic cron request has been
// dispatched through gatewayd (spec §4.4, M7).
func (m *MemStore) MarkCronFired(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok {
		return ErrNotFound
	}
	c.LastFiredAt = at
	m.crons[id] = c
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

// ListInstancesForAccount joins the instance set against the app set
// in-memory; the production path is a single SQL query (pgstore). Used
// by the meterd quota loop on Free hard-stop (spec §4.7).
func (m *MemStore) ListInstancesForAccount(_ context.Context, accountID string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{}, len(m.apps))
	for _, a := range m.apps {
		if a.AccountID == accountID {
			owned[a.ID] = struct{}{}
		}
	}
	var out []Instance
	for _, ins := range m.instances {
		if _, ok := owned[ins.AppID]; ok {
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

// AppendUsage upserts one (instance, minute) usage row and updates the
// per-(account, app, month) aggregate so UsageByMonth keeps returning the
// spec §10 shape without re-scanning the per-minute rows. Mirrors the
// production INSERT … ON CONFLICT (instance_id, minute) DO UPDATE semantics:
// multiple writes for the same minute accumulate mb_seconds + requests.
func (m *MemStore) AppendUsage(_ context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := minute.UTC().Truncate(time.Minute)
	for i := range m.usage {
		if m.usage[i].InstanceID == instanceID && m.usage[i].Minute.Equal(key) {
			m.usage[i].MBSeconds += mbSeconds
			m.usage[i].Requests += requests
			m.recomputeMonthLocked(accountID, appID, key)
			return nil
		}
	}
	m.usage = append(m.usage, usageMinute{
		AccountID: accountID, AppID: appID, InstanceID: instanceID,
		Minute: key, MBSeconds: mbSeconds, Requests: requests,
	})
	m.recomputeMonthLocked(accountID, appID, key)
	return nil
}

// UsageByMonth returns the per-app aggregates for one (account, month) —
// the read shape the dashboard and the meter aggregator rely on. The
// per-minute grain is internal; consumers see the rolled-up row.
func (m *MemStore) UsageByMonth(_ context.Context, accountID string, month time.Time) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	var out []Usage
	for _, u := range m.usageByMonth {
		if u.AccountID == accountID && u.Month.Equal(key) {
			out = append(out, u)
		}
	}
	return out, nil
}

// UsageByHour returns the per-app usage rows whose minute ∈ [start, end).
// The Stripe pusher calls this hourly; MemStore synthesizes the per-hour
// rollup from the per-minute rows on the fly — matches what PgStore would
// do in SQL.
func (m *MemStore) UsageByHour(_ context.Context, accountID string, start, end time.Time) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type hourAgg struct {
		AccountID string
		AppID     string
		MBSeconds int64
		Requests  int64
	}
	bucket := map[appHourKey]hourAgg{}
	for _, u := range m.usage {
		if u.AccountID != accountID {
			continue
		}
		if u.Minute.Before(start) || !u.Minute.Before(end) {
			continue
		}
		k := appHourKey{AccountID: u.AccountID, AppID: u.AppID}
		a := bucket[k]
		a.AccountID = u.AccountID
		a.AppID = u.AppID
		a.MBSeconds += u.MBSeconds
		a.Requests += u.Requests
		bucket[k] = a
	}
	out := make([]Usage, 0, len(bucket))
	for _, a := range bucket {
		out = append(out, Usage{
			AccountID: a.AccountID, AppID: a.AppID,
			Month: start, MBSeconds: a.MBSeconds, Requests: a.Requests,
		})
	}
	return out, nil
}

// HasStripePushHour + RecordStripePushHour implement the pkg/stripex
// PushDedupe interface. The MemStore keeps a flat set keyed by
// (account, hour); PgStore keeps a dedicated table.
func (m *MemStore) HasStripePushHour(_ context.Context, accountID string, hour time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.stripePushHours[stripePushKey{accountID: accountID, hour: hour.UTC()}]
	return ok, nil
}

func (m *MemStore) RecordStripePushHour(_ context.Context, accountID string, hour time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stripePushHours == nil {
		m.stripePushHours = map[stripePushKey]struct{}{}
	}
	m.stripePushHours[stripePushKey{accountID: accountID, hour: hour.UTC()}] = struct{}{}
	return nil
}

type appHourKey struct {
	AccountID string
	AppID     string
}

// recomputeMonthLocked rebuilds the (account, app, month) aggregate from
// every per-minute row that falls in the calendar month. Called under m.mu
// from AppendUsage; the slice scan is O(rows-in-month) which on a one-box
// stays bounded (one minute × max_concurrency(plan) × apps).
func (m *MemStore) recomputeMonthLocked(accountID, appID string, minute time.Time) {
	month := time.Date(minute.Year(), minute.Month(), 1, 0, 0, 0, 0, time.UTC)
	var mbSec, req int64
	for _, u := range m.usage {
		if u.AccountID != accountID || u.AppID != appID {
			continue
		}
		if u.Minute.Year() != month.Year() || u.Minute.Month() != month.Month() {
			continue
		}
		mbSec += u.MBSeconds
		req += u.Requests
	}
	// Drop the existing row for this (account, app, month) if any, then append.
	for i := range m.usageByMonth {
		if m.usageByMonth[i].AccountID == accountID &&
			m.usageByMonth[i].AppID == appID &&
			m.usageByMonth[i].Month.Equal(month) {
			m.usageByMonth = append(m.usageByMonth[:i], m.usageByMonth[i+1:]...)
			break
		}
	}
	m.usageByMonth = append(m.usageByMonth, Usage{
		AccountID: accountID, AppID: appID, Month: month,
		MBSeconds: mbSec, Requests: req,
	})
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

// IssueLoginToken stores a magic-link token hash → account_id mapping
// with the given expiry. The hash is the SHA-256 of the raw token
// (32-byte hex); see pkg/api.HashAPIKey for the canonical hash fn.
// Re-issue of the same hash is a no-op (the entry is overwritten).
func (m *MemStore) IssueLoginToken(_ context.Context, tokenHash []byte, accountID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loginTokens == nil {
		m.loginTokens = map[string]LoginToken{}
	}
	m.loginTokens[string(tokenHash)] = LoginToken{
		TokenHash: append([]byte(nil), tokenHash...),
		AccountID: accountID,
		ExpiresAt: expiresAt,
	}
	return nil
}

// ConsumeLoginToken marks the token consumed in a single critical
// section and returns the bound account_id. A replay returns
// ErrNotFound. Expired tokens also return ErrNotFound (we don't leak
// whether the token was real-but-stale vs never-existed).
func (m *MemStore) ConsumeLoginToken(_ context.Context, tokenHash []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tok, ok := m.loginTokens[string(tokenHash)]
	if !ok {
		return "", ErrNotFound
	}
	if tok.ConsumedAt != nil {
		delete(m.loginTokens, string(tokenHash))
		return "", ErrNotFound
	}
	if !tok.ExpiresAt.After(time.Now()) {
		delete(m.loginTokens, string(tokenHash))
		return "", ErrNotFound
	}
	now := time.Now()
	tok.ConsumedAt = &now
	m.loginTokens[string(tokenHash)] = tok
	return tok.AccountID, nil
}

// DeleteOldLoginTokens prunes tokens whose expires_at < before, even
// if they were never consumed. Returns the number removed. Used by
// the maintenance job (or a test cleanup hook).
func (m *MemStore) DeleteOldLoginTokens(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for k, tok := range m.loginTokens {
		if tok.ExpiresAt.Before(before) {
			delete(m.loginTokens, k)
			removed++
		}
	}
	return removed, nil
}

// AppendDeploymentLog records one line of build output. Returns the
// assigned seq (monotonic per deployment). MemStore mimics the
// Postgres bigserial cursor so cursor pagination (`seq < before`)
// works the same as production.
func (m *MemStore) AppendDeploymentLog(_ context.Context, deploymentID, stream, line string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deploymentSeq[deploymentID]++
	seq := m.deploymentSeq[deploymentID]
	if m.deploymentLogs == nil {
		m.deploymentLogs = map[string][]LogEntry{}
	}
	m.deploymentLogs[deploymentID] = append(m.deploymentLogs[deploymentID], LogEntry{
		DeploymentID: deploymentID,
		Seq:          seq,
		Stream:       stream,
		Line:         line,
		WrittenAt:    time.Now().UTC(),
	})
	return seq, nil
}

// ListDeploymentLogs returns the page of rows whose seq < beforeSeq
// (zero → all rows), in DESC seq order, capped at limit. hasMore is
// true when there are older rows still to fetch (rows == limit AND
// there's at least one more behind it).
func (m *MemStore) ListDeploymentLogs(_ context.Context, deploymentID string, beforeSeq int64, limit int) ([]LogEntry, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = clampLogLimit(limit)
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.deploymentLogs[deploymentID]
	if len(all) == 0 {
		return nil, false, nil
	}
	// Walk backwards (highest seq first) so the page is newest-first
	// regardless of insert order — matches production's ORDER BY seq DESC.
	out := make([]LogEntry, 0, limit)
	olderRemaining := false
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		if beforeSeq > 0 && e.Seq >= beforeSeq {
			continue
		}
		if len(out) >= limit {
			// Page is full. Stop only when we've also confirmed
			// there's at least one row behind us we'd otherwise
			// have included. Older rows survive any older iteration
			// (i > 0 and a row at i-1 with seq < beforeSeq).
			for j := i - 1; j >= 0; j-- {
				if beforeSeq == 0 || all[j].Seq < beforeSeq {
					olderRemaining = true
					break
				}
			}
			break
		}
		out = append(out, e)
	}
	return out, olderRemaining, nil
}

// compile-time check that MemStore satisfies Store.
var _ Store = (*MemStore)(nil)
