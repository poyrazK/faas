package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	// cliAuthCodes is keyed by the SHA-256 hash of the raw code
	// (same key format as loginTokens). AccountID is empty until the
	// dashboard claims the code; the claim statement fills it in
	// atomically. See pkg/state/types.go CliAuthCode.
	cliAuthCodes map[string]CliAuthCode
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
	// gdprRequests is the in-memory mirror of the gdpr_requests ledger
	// row. MemStore does not auto-cascade on DeleteAccount (the
	// production pgstore does), but AppendGdprRequest rows here are
	// also intentionally NOT pruned — a unit test that asserts "after
	// DeleteAccount, the GDPR ledger still has the delete row" needs
	// them to survive.
	gdprRequests []GdprRequest
	// stripePushHours tracks which (account, hour) pairs the hourly
	// Stripe pusher has already pushed; prevents double-billing on
	// redelivery.
	stripePushHours map[stripePushKey]struct{}
	// secrets is keyed by (app_id, key) per the schema's PRIMARY KEY.
	// Value carries account_id for the ownership check on delete.
	secrets map[secretKey]AppSecret
	// computeNodes mirrors the compute_nodes table; keyed by id (issue
	// #97 / ADR-025 axis 3). The synthetic 'default-local' row is
	// seeded by NewMemStore so tests don't have to call
	// CreateComputeNode to exercise the single-box path. Production
	// (PgStore) gets the same row from migrations/00024_compute_nodes.
	computeNodes map[string]ComputeNode
}

// secretKey mirrors the app_secrets PRIMARY KEY (app_id, key). The
// MemStore uses the same composite-key shape so tests don't drift from
// production behavior.
type secretKey struct {
	AppID string
	Key   string
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

// NewMemStore returns an empty in-memory store with the synthetic
// 'default-local' compute_node row seeded (issue #97 / ADR-025 axis 3).
// The seed mirrors migrations/00024_compute_nodes.sql so unit tests
// don't have to call CreateComputeNode to exercise the single-box path.
// Production (PgStore) gets the same row from the migration.
func NewMemStore() *MemStore {
	m := &MemStore{
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
		cliAuthCodes:   map[string]CliAuthCode{},
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
		// gdprRequests starts empty; AppendGdprRequest appends.
		gdprRequests: nil,
		// stripePushHours is the per-(account, hour) dedupe set the
		// meterd hourly pusher reads/writes.
		stripePushHours: map[stripePushKey]struct{}{},
		secrets:         map[secretKey]AppSecret{},
		// computeNodes is empty here; seedDefaultLocalNodeLocked
		// inserts the synthetic default-local row below.
		computeNodes: map[string]ComputeNode{},
	}
	// Auto-seed default-local. Done after the struct literal so the
	// seeded row carries a real id and created_at timestamp. Mirrors
	// migrations/00024_compute_nodes.sql: the only way a test's
	// single-box flow can fail is if the seed's contract drifts from
	// the migration's seed (e.g., target_url mismatch); both land in
	// the test 00024_compute_nodes_test.go.
	m.seedDefaultLocalNodeLocked()
	return m
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

// UpdateAccountStripeSubscriptionItem stamps the Stripe metered
// subscription item ID (si_…) on the account row (issue #52). MemStore
// does not maintain a reverse-lookup index — only meterd walks
// forward from the account list. PgStore mirrors the column shape.
func (m *MemStore) UpdateAccountStripeSubscriptionItem(_ context.Context, id, subItem string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.StripeSubscriptionItem = subItem
	m.accounts[id] = a
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

// CreateAppIfUnderQuota is the MemStore mirror of PgStore.CreateAppIfUnderQuota.
// The TOCTOU is impossible here because every mutation holds m.mu for
// the full check + insert — two goroutines serialize on the same lock,
// so a Free account that already holds 1 app always sees observed=1 on
// the second call. The handler's CreateApp call site becomes store-
// agnostic.
func (m *MemStore) CreateAppIfUnderQuota(_ context.Context, app App, limits api.Limits) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[app.AccountID]; !ok {
		return App{}, ErrNotFound
	}
	// 1. Authoritative count under the same lock. Mirrors the predicate
	//    PgStore uses against the apps table.
	observed := 0
	for _, a := range m.apps {
		if a.AccountID == app.AccountID && (a.Status == AppActive || a.Status == AppEvictedCold) {
			observed++
		}
	}
	if observed >= limits.DeployedApps {
		return App{}, &QuotaError{Limit: limits.DeployedApps, Observed: observed}
	}
	// 2. Conditional insert. Slug uniqueness is enforced by the same
	//    loop CreateApp uses; returning ErrConflict keeps the wire
	//    contract identical to PgStore's apps.slug unique-index path.
	for _, a := range m.apps {
		if a.Slug == app.Slug && a.Status != AppDeleted {
			return App{}, ErrConflict
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
	if p.SetMinInstances {
		a.MinInstances = derefInt(p.MinInstances)
	}
	m.apps[id] = a
	return a, nil
}

// RenameApp atomically swaps an app's slug (issue #63). Scans the
// in-memory map under lock for the (accountID, oldSlug) pair; rejects
// newSlug collisions with ErrConflict so tests can exercise the same
// 409 surface PgStore produces from the apps.slug unique constraint.
func (m *MemStore) RenameApp(_ context.Context, accountID, oldSlug, newSlug string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *App
	for i := range m.apps {
		a := m.apps[i]
		if a.AccountID == accountID && a.Slug == oldSlug && a.Status != AppDeleted {
			cp := a
			found = &cp
			break
		}
	}
	if found == nil {
		return App{}, ErrNotFound
	}
	for i := range m.apps {
		other := m.apps[i]
		if other.ID != found.ID && other.Slug == newSlug && other.Status != AppDeleted {
			return App{}, ErrConflict
		}
	}
	found.Slug = newSlug
	m.apps[found.ID] = *found
	return *found, nil
}

// SetAppMinInstances stamps the per-app floor (ux_spec §6.5). Plan-tier
// gating is the apid handler's job — the store writes the column
// unconditionally. Returns ErrNotFound when the app is gone so a
// redelivered PATCH returns 404 cleanly.
func (m *MemStore) SetAppMinInstances(_ context.Context, appID string, min int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	a.MinInstances = min
	m.apps[appID] = a
	return nil
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

// CreateDeployment mirrors PgStore.CreateDeployment's active-app gate
// (PR-A). Both stores must reject deployments against AppDeleted or
// missing apps with ErrNotFound — apid's s.notFound relies on this
// to return 404. The mutex already serialises the check + insert
// together, so the gate is race-free here without a tx.
//
// PR-B: the prior-deployment supersede is folded into the same
// critical section as the INSERT, mirroring PgStore's tx-wrapped
// shape. We walk m.deployments for the most-recent row whose status
// is in the "current world" set (pending/live/building/imaging/
// snapshotting), flip it to 'superseded' in the map, then insert
// the new row. The race-free supersede closes the same TOCTOU the
// image: branch had before, and gives the tarball branch the parity
// it has always lacked.
func (m *MemStore) CreateDeployment(_ context.Context, d Deployment) (Deployment, Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[d.AppID]
	if !ok || app.Status == AppDeleted {
		return Deployment{}, Deployment{}, ErrNotFound
	}

	// Find the most-recent non-terminal deployment row for this app.
	// O(N) over the map is fine at one-box scale; spec §6 keeps the
	// rows-per-app bounded by the build cadence.
	var (
		priorID  string
		prior    Deployment
		hasPrior bool
	)
	var maxCreated time.Time
	for id, existing := range m.deployments {
		if existing.AppID != d.AppID {
			continue
		}
		switch existing.Status {
		case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive:
			// current world
		default:
			continue
		}
		if !hasPrior || existing.CreatedAt.After(maxCreated) {
			priorID = id
			prior = existing
			maxCreated = existing.CreatedAt
			hasPrior = true
		}
	}
	if hasPrior {
		prior.Status = DeploySuperseded
		m.deployments[priorID] = prior
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
	return d, prior, nil
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

// ListDeploymentsForApp mirrors PgStore.ListDeploymentsForApp: `limit <= 0`
// means "no row cap" (every remaining row after offset). F-10: see PgStore
// doc for the asymmetry that this version already conformed to.
func (m *MemStore) ListDeploymentsForApp(_ context.Context, appID string, limit, offset int) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
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

func (m *MemStore) SetDeploymentRootfs(_ context.Context, id, path, key string, bytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	// Issue #96 / ADR-025 axis 2 (PR #116): mirror PgStore — both
	// rootfs_path and rootfs_key are stamped on the same mutation so
	// the in-memory store tracks Postgres' column-pair contract.
	d.RootfsPath = path
	d.RootfsKey = key
	d.RootfsBytes = bytes
	m.deployments[id] = d
	return nil
}

// SetDeploymentFailed mirrors PgStore.SetDeploymentFailed (ADR-021):
// status pinned to 'failed'; error_code is the RFC 7807 code lifted
// from pkg/api.SentinelToCode; error keeps the free-text message.
// Returns the refreshed row.
func (m *MemStore) SetDeploymentFailed(_ context.Context, id, code, message string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	d.Status = DeployFailed
	d.Error = message
	d.ErrorCode = code
	m.deployments[id] = d
	return d, nil
}

// --- Builds -----------------------------------------------------------------

func (m *MemStore) CreateBuild(_ context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[deploymentID]; !ok {
		return Build{}, fmt.Errorf("state: build for unknown deployment %q", deploymentID)
	}
	b := Build{ID: newID(), DeploymentID: deploymentID, Kind: kind, SourceBytes: sourceBytes, Status: BuildQueued, LogPath: logPath, EnqueuedAt: time.Now()}
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

// ClaimQueuedBuild atomically flips queued → running under m.mu (PR-A
// review fix). Returns ErrNotFound if the row is missing or already
// non-queued — the caller drops the build. Same contract as
// PgStore.ClaimQueuedBuild.
func (m *MemStore) ClaimQueuedBuild(_ context.Context, id string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok || b.Status != BuildQueued {
		return Build{}, ErrNotFound
	}
	b.Status = BuildRunning
	b.StartedAt = time.Now()
	m.builds[id] = b
	return b, nil
}

// ClaimNextQueuedBuild mirrors PgStore.ClaimNextQueuedBuild (PR-B). The
// MemStore mutex is the equivalent of FOR UPDATE SKIP LOCKED here:
// only one claimer exists in-process, but the shape mirrors Postgres
// 1:1 so unit tests catch logic bugs without races. Picks the earliest
// EnqueuedAt row whose status is BuildQueued, flips to BuildRunning,
// sets StartedAt = now(). Returns ErrNotFound when the queue is empty.
func (m *MemStore) ClaimNextQueuedBuild(_ context.Context) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var (
		pick     string
		earliest time.Time
		found    bool
	)
	for id, b := range m.builds {
		if b.Status != BuildQueued {
			continue
		}
		if !found || b.EnqueuedAt.Before(earliest) {
			pick = id
			earliest = b.EnqueuedAt
			found = true
		}
	}
	if !found {
		return Build{}, ErrNotFound
	}
	b := m.builds[pick]
	b.Status = BuildRunning
	b.StartedAt = time.Now()
	m.builds[pick] = b
	return b, nil
}

// RequeueBuild resets a running build row back to queued with
// enqueued_at untouched (PR-B). Mirrors PgStore.RequeueBuild.
func (m *MemStore) RequeueBuild(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return ErrNotFound
	}
	if b.Status != BuildRunning {
		return ErrNotFound
	}
	b.Status = BuildQueued
	b.StartedAt = time.Time{}
	m.builds[id] = b
	return nil
}

// ListStaleQueuedBuilds mirrors PgStore.ListStaleQueuedBuilds (PR-A).
// Same predicate (BuildQueued AND enqueued_at older than threshold),
// same sort order (oldest first so the reaper drains the backlog
// deterministically). Walks m.builds under m.mu — the queue is
// shallow per spec §9, so an O(N) scan per tick is fine.
func (m *MemStore) ListStaleQueuedBuilds(_ context.Context, threshold time.Duration) ([]Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-threshold)
	out := make([]Build, 0)
	for _, b := range m.builds {
		if b.Status != BuildQueued {
			continue
		}
		if !b.EnqueuedAt.Before(cutoff) {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].EnqueuedAt.Before(out[j].EnqueuedAt)
	})
	return out, nil
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

func (m *MemStore) CreateInstance(_ context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Stamp started_at on creation for every state (commit 3, mirrors
	// the Postgres trigger in migration 00015). The MemStore previously
	// only stamped it on "running" rows, which left watchdog tests
	// fishing for NULLs on WAKING/COLD_BOOTING fixtures. Keeping that
	// late stamp behaviour would force every fixture to call
	// SetInstanceRuntime first, which makes the watchdog tests
	// describe a state-machine shape that no production code reaches.
	//
	// nodeID is the compute_node the instance lives on
	// (issue #97 / ADR-025 axis 3). MemStore does NOT enforce the
	// FK to compute_nodes(id) — the production constraint lives in
	// migrations/00024_compute_nodes. A test that passes an
	// arbitrary nodeID here will succeed; the constraint divergence
	// is intentional so unit tests can construct instance rows
	// without seeding compute_nodes first. The engine's Wake flow
	// resolves the id via ComputeNodeByName before reaching here,
	// so production callers always have a real id.
	//
	// wakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23). An empty wakeID triggers the MemStore's own default
	// — newID() — mirroring PgStore's coalesce(...gen_random_uuid()) so
	// ad-hoc test fixtures that don't thread wake_id through still get
	// a non-empty value. Production callers (schedd's Wake) supply a
	// UUIDv7 minted Go-side for time-ordered values.
	ins := Instance{
		ID:           newID(),
		AppID:        appID,
		DeploymentID: deploymentID,
		State:        state,
		RAMMB:        ramMB,
		NodeID:       nodeID,
		StartedAt:    time.Now(),
	}
	if wakeID != "" {
		ins.WakeID = wakeID
	} else {
		// Mirror PgStore's `coalesce(nullif($6, ''), gen_random_uuid())`
		// default with a real UUIDv4 here. newID() returns 32 hex
		// chars (not a hyphenated UUID), which broke uuid.Parse
		// assertions in tests exercising the wake_id contract via
		// MemStore (gaps analysis 2026-07-23 review finding #2).
		// Test fixtures that don't thread wake_id through still get
		// a non-empty, parseable value.
		ins.WakeID = uuid.NewString()
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

// ListLatestInstancesForApp returns up to `limit` rows for appID
// ordered by started_at DESC. Mirror of the PgStore method added
// alongside the dashboard "Recent wakes" feature (gaps analysis
// 2026-07-23). limit ≤ 0 returns an empty slice — fail closed
// rather than rendering an unbounded table. After the in-place sort
// we slice to limit; the sort is O(n log n) but bounded by the
// MemStore instance count, which is tiny in tests.
func (m *MemStore) ListLatestInstancesForApp(ctx context.Context, appID string, limit int) ([]Instance, error) {
	if limit <= 0 {
		return nil, nil
	}
	all, err := m.ListInstancesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// ListAllInstances returns every instance whose state is one schedd's
// idle reaper considers live (running, waking, cold_booting,
// snapshotting). Sorted DESC by StartedAt to match the partial index
// shape in migration 00009 — pkg/state.pgstore orders the same way in
// SQL, so tests and production behave identically.
func (m *MemStore) ListAllInstances(_ context.Context) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		switch ins.State {
		case string(StateRunning), string(StateWaking), string(StateColdBooting), string(StateSnapshotting):
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

// ListLatestInstancePerApp returns the most-recently-started instance
// for each app owned by the account (PR #48 follow-up). Used by the
// dashboard cold-wake badge so one query replaces N per-app
// ListInstancesForApp calls. Apps with no instance rows are absent
// from the returned map — the dashboard treats that as ◌ sleeping
// via BadgeForDefault.
func (m *MemStore) ListLatestInstancePerApp(_ context.Context, accountID string) (map[string]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{}, len(m.apps))
	for _, a := range m.apps {
		if a.AccountID == accountID {
			owned[a.ID] = struct{}{}
		}
	}
	out := map[string]Instance{}
	for _, ins := range m.instances {
		if _, ok := owned[ins.AppID]; !ok {
			continue
		}
		cur, seen := out[ins.AppID]
		if !seen || ins.StartedAt.After(cur.StartedAt) {
			out[ins.AppID] = ins
		}
	}
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

// UpdateInstanceStateWithTimestamp mirrors PgStore's variant. Mirrors
// the §6.1 watchdog's need to know "time of entry into current
// state" for SNAPSHOTTING rows; parked_at is the column the watchdog
// reads on that state.
func (m *MemStore) UpdateInstanceStateWithTimestamp(_ context.Context, id, state string, parkedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.State = state
	ins.ParkedAt = parkedAt
	m.instances[id] = ins
	return nil
}

// UpdateInstanceStateToTerminal mirrors PgStore's variant. Writes the
// new state AND stamps terminal_at on the same locked read-modify-write
// (PR #74). Engine.transition routes here for {STOPPED, FAILED}; today
// no caller writes a different timestamp column for those states.
func (m *MemStore) UpdateInstanceStateToTerminal(_ context.Context, id, state string, terminalAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.State = state
	ts := terminalAt
	ins.TerminalAt = &ts
	m.instances[id] = ins
	return nil
}

// ListInstancesInTerminalStatesOlderThan is the §17 retention sweep's
// lookup (PR #74). Mirrors ListInstancesByStatesOlderThan but reads
// terminal_at instead of the state-aware started_at/parked_at pair.
func (m *MemStore) ListInstancesInTerminalStatesOlderThan(_ context.Context, states []State, threshold time.Time) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wanted := make(map[State]bool, len(states))
	for _, s := range states {
		wanted[s] = true
	}
	var out []Instance
	for _, ins := range m.instances {
		if !wanted[State(ins.State)] {
			continue
		}
		if ins.TerminalAt == nil {
			continue
		}
		if !ins.TerminalAt.Before(threshold) {
			continue
		}
		out = append(out, ins)
	}
	return out, nil
}

// DeleteInstance removes an instance row unconditionally (PR #74).
// Returns ErrNotFound when the row is already gone — the retention
// sweep swallows that case for redelivery. There are no FK cascades;
// events.subject and usage_minutes.instance_id carry no FK today.
func (m *MemStore) DeleteInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.instances[id]; !ok {
		return ErrNotFound
	}
	delete(m.instances, id)
	return nil
}

// ListInstancesByStatesOlderThan is the watchdog's lookup (commit 3,
// spec §6.1). Mirrors PgStore: coalesce started_at / parked_at on the
// age comparison.
func (m *MemStore) ListInstancesByStatesOlderThan(_ context.Context, states []State, threshold time.Time) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wanted := make(map[State]bool, len(states))
	for _, s := range states {
		wanted[s] = true
	}
	var out []Instance
	for _, ins := range m.instances {
		if !wanted[State(ins.State)] {
			continue
		}
		age := ins.StartedAt
		if State(ins.State) == StateSnapshotting {
			age = ins.ParkedAt
		}
		if age.IsZero() {
			continue
		}
		if age.Before(threshold) {
			out = append(out, ins)
		}
	}
	return out, nil
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
// subsequent inserts for the same (deployment_id, fc_version, storage_key)
// collide as ErrConflict so imaged's idempotent retry is silent. The legacy
// `path` uniqueness key was dropped with #96 slice 3 (storage_key is the
// only blob locator and uniqueness is implicit on its value).

func (m *MemStore) CreateSnapshot(_ context.Context, snap Snapshot) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// StorageKey is required on both backends (see PgStore for the
	// rationale). The in-memory store doesn't have a DB DEFAULT to
	// fall back on, so the contract is enforced here as well —
	// silently storing "" would propagate to the GC loop and have it
	// Storage.Delete under an empty key (a no-op for every backend
	// since none accept "").
	if snap.StorageKey == "" {
		return Snapshot{}, errors.New("state: MemStore.CreateSnapshot: storage_key required (populate via sched.SnapshotMemKey at the call site)")
	}
	if snap.ID == "" {
		snap.ID = uuid.NewString()
	}
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = time.Now()
	}
	for _, existing := range m.snapshots {
		if existing.DeploymentID == snap.DeploymentID && existing.FCVersion == snap.FCVersion && existing.StorageKey == snap.StorageKey {
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

// ListSnapshotsForGC joins snapshots → deployments → apps in-memory and
// filters out snapshots belonging to soft-deleted apps (apps.status='deleted').
// MemStore doesn't index the join; the O(N×M) scan is fine for the test
// harness, which seeds at most a few dozen rows. The slice is sorted
// newest-first to match PgStore.
func (m *MemStore) ListSnapshotsForGC(_ context.Context) ([]SnapshotForGC, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	appByID := make(map[string]App, len(m.apps))
	for _, a := range m.apps {
		appByID[a.ID] = a
	}
	depByID := make(map[string]Deployment, len(m.deployments))
	for _, d := range m.deployments {
		depByID[d.ID] = d
	}
	var out []SnapshotForGC
	for _, s := range m.snapshots {
		if s.Stale {
			continue
		}
		dep, ok := depByID[s.DeploymentID]
		if !ok {
			continue
		}
		app, ok := appByID[dep.AppID]
		if !ok || app.Status == AppDeleted {
			continue
		}
		out = append(out, SnapshotForGC{
			ID:           s.ID,
			DeploymentID: s.DeploymentID,
			AppID:        app.ID,
			AccountID:    app.AccountID,
			FCVersion:    s.FCVersion,
			MemBytes:     s.MemBytes,
			DiskBytes:    s.DiskBytes,
			// #96 / ADR-025 axis 2: forward the canonical storage
			// key so imaged's GC loop can Storage.Delete under it
			// without a second hop through Snapshot.
			StorageKey: s.StorageKey,
			Stale:      s.Stale,
			CreatedAt:  s.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// DeleteSnapshotsByID removes the named snapshot rows in-place. Returns
// the number of rows actually removed; a second call with the same ids
// returns 0. Never deletes the last live snapshot of a non-deleted app
// — that invariant would break the "always have a cold-bootable or
// snapshot-restoreable deployment" rule (spec §6.2-3). MemStore is
// permissive here because it's test-only; PgStore is authoritative.
func (m *MemStore) DeleteSnapshotsByID(_ context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.snapshots[:0]
	var removed int64
	for _, s := range m.snapshots {
		if _, drop := idSet[s.ID]; drop {
			removed++
			continue
		}
		kept = append(kept, s)
	}
	m.snapshots = append([]Snapshot(nil), kept...)
	return removed, nil
}

// MarkAllSnapshotsStaleByFCVersion mirrors the SQL UPDATE: every non-stale
// row whose fc_version != currentVersion is flipped. ADR-005.
func (m *MemStore) MarkAllSnapshotsStaleByFCVersion(_ context.Context, currentVersion string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for i := range m.snapshots {
		if !m.snapshots[i].Stale && m.snapshots[i].FCVersion != currentVersion {
			m.snapshots[i].Stale = true
			n++
		}
	}
	return n, nil
}

// MarkOldSnapshotsStale flips the given IDs to stale=true (no-op if absent).
// Used by the per-app "current + previous" enforcement in the GC.
func (m *MemStore) MarkOldSnapshotsStale(_ context.Context, beforeSnapshotIDs []string) (int64, error) {
	if len(beforeSnapshotIDs) == 0 {
		return 0, nil
	}
	idSet := make(map[string]struct{}, len(beforeSnapshotIDs))
	for _, id := range beforeSnapshotIDs {
		idSet[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for i := range m.snapshots {
		if _, ok := idSet[m.snapshots[i].ID]; ok {
			m.snapshots[i].Stale = true
			n++
		}
	}
	return n, nil
}

// DeleteSnapshotsStaleOlderThan mirrors the SQL DELETE … WHERE stale=true
// AND created_at < now()-retention. MemStore uses time.Now for the cutoff
// (deterministic tests pass a future/injected CreatedAt at seed time).
func (m *MemStore) DeleteSnapshotsStaleOlderThan(_ context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention)
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	kept := m.snapshots[:0]
	for i := range m.snapshots {
		if m.snapshots[i].Stale && m.snapshots[i].CreatedAt.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, m.snapshots[i])
	}
	m.snapshots = append([]Snapshot(nil), kept...)
	return n, nil
}

// --- Audit ------------------------------------------------------------------

// parseSubjectID accepts either a canonical UUID (with hyphens) or the
// 32-char hex form that MemStore's newID() emits, and returns the
// canonical *uuid.UUID either way. Returns nil on any parse failure so
// callers can treat "unparseable" the same as "no subject" (which is
// what the audit-log filter expects: no row would have produced a
// garbage ID). The fix for the silent-drop bug surfaced by the audit-log
// PR's tests: engine.go hands us hex IDs (newID output) and uuid.Parse
// rejects them, so Subject stayed nil even though we said we set it.
func parseSubjectID(s string) *uuid.UUID {
	if s == "" {
		return nil
	}
	if u, err := uuid.Parse(s); err == nil {
		return &u
	}
	if len(s) == 32 {
		if b, err := hex.DecodeString(s); err == nil {
			u := uuid.UUID(b)
			return &u
		}
	}
	return nil
}

// --- Compute nodes (issue #97 / ADR-025 axis 3) ---------------------------
//
// Mirrors the compute_nodes table. The synthetic 'default-local' row
// is auto-seeded by NewMemStore via seedDefaultLocalNodeLocked (same
// shape as migrations/00024_compute_nodes.sql's seed) so single-box
// tests don't have to call CreateComputeNode. Tests exercising the
// multi-node path add additional rows via CreateComputeNode; the
// per-vm overhead (8 MB) is referenced from pkg/api.PerVMOverheadMB
// — the single source of truth shared with PgStore.ComputeNodeUsedMB,
// sched.Ledger's reservation math, and the §4.7 billing model.

// seedDefaultLocalNodeLocked inserts the synthetic single-host vmmd
// row. Called once by NewMemStore after the struct literal so the
// seeded row carries a real id + created_at. Idempotent on a fresh
// store; production never calls this (the migration handles it).
func (m *MemStore) seedDefaultLocalNodeLocked() {
	now := time.Now()
	id := newID()
	m.computeNodes[id] = ComputeNode{
		ID:                 id,
		Name:               DefaultLocalNodeName,
		TargetURL:          "unix:///run/faas/vmmd.sock",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
		Active:             true,
		LastHeartbeatAt:    now,
		CreatedAt:          now,
	}
}

func (m *MemStore) ActiveComputeNodes(_ context.Context) ([]ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeNode, 0, len(m.computeNodes))
	for _, n := range m.computeNodes {
		if n.Active {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListAllComputeNodes returns every compute_node row including
// inactive ones, ordered by name. apid's GET /v1/compute-nodes
// operator surface (PR #114) calls this so a recently-drained
// node stays visible for ops dashboards. The fleet is
// single-digit for v1.0; the slice alloc is fine.
func (m *MemStore) ListAllComputeNodes(_ context.Context) ([]ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeNode, 0, len(m.computeNodes))
	for _, n := range m.computeNodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) ComputeNodeByID(_ context.Context, id string) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[id]
	if !ok {
		return ComputeNode{}, ErrNotFound
	}
	return n, nil
}

func (m *MemStore) ComputeNodeByName(_ context.Context, name string) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.computeNodes {
		if n.Name == name {
			return n, nil
		}
	}
	return ComputeNode{}, ErrNotFound
}

// ComputeNodeUsedMB returns the Σ(ram_mb + api.PerVMOverheadMB) for
// live instances on the given node. Live = state ∈ {'waking',
// 'cold_booting', 'running'} per §6.2-2 re-stated per-node. The
// 8 MB overhead matches pkg/state/pgstore.go's aggregate query and
// the billing model in spec §4.7 — single source of truth in
// pkg/api.PerVMOverheadMB (F-1 in PR #112 review).
func (m *MemStore) ComputeNodeUsedMB(_ context.Context, nodeID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var used int64
	for _, ins := range m.instances {
		if ins.NodeID != nodeID {
			continue
		}
		switch ins.State {
		case "waking", "cold_booting", "running":
			used += int64(ins.RAMMB + api.PerVMOverheadMB)
		}
	}
	return used, nil
}

func (m *MemStore) HeartbeatComputeNode(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[nodeID]
	if !ok {
		return ErrNotFound
	}
	n.LastHeartbeatAt = time.Now()
	m.computeNodes[nodeID] = n
	return nil
}

// MarkComputeNodeInactive flips active=false on the row (PR #114).
// Idempotent — flipping an inactive row keeps active=false, no
// observable change. The row is preserved so an operator can
// re-enable it (a future admin endpoint will hit a re-activate
// path; today nothing does).
func (m *MemStore) MarkComputeNodeInactive(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[nodeID]
	if !ok {
		return ErrNotFound
	}
	n.Active = false
	m.computeNodes[nodeID] = n
	return nil
}

func (m *MemStore) CreateComputeNode(_ context.Context, node ComputeNode) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Unique-name enforcement mirrors the production UNIQUE constraint
	// on name. The MemStore uses a name → id map lookup for the same
	// effect — tests that pass a duplicate name get ErrConflict.
	for _, existing := range m.computeNodes {
		if existing.Name == node.Name {
			return ComputeNode{}, ErrConflict
		}
	}
	n := node
	if n.ID == "" {
		n.ID = newID()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	if n.LastHeartbeatAt.IsZero() {
		n.LastHeartbeatAt = n.CreatedAt
	}
	m.computeNodes[n.ID] = n
	return n, nil
}

// UpsertComputeNode mirrors pgstore's INSERT ... ON CONFLICT DO UPDATE
// (issue #98 / ADR-028). vmmd's self-registration calls this at startup
// — a node that has already been registered has its capacity refreshed
// and is reactivated (active=true), even if an operator had previously
// drained it. The loop-then-store mirrors a write-then-map in the
// MemStore: cheaper than a SELECT-then-UPDATE for tests that hammer the
// path. CreatedAt stays monotonic on conflict.
func (m *MemStore) UpsertComputeNode(_ context.Context, node ComputeNode) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var existing *ComputeNode
	for id, current := range m.computeNodes {
		if current.Name == node.Name {
			current := current
			existing = &current
			delete(m.computeNodes, id)
			break
		}
	}
	n := node
	if existing != nil {
		n.ID = existing.ID
		n.CreatedAt = existing.CreatedAt
	} else if n.ID == "" {
		n.ID = newID()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	if n.LastHeartbeatAt.IsZero() {
		n.LastHeartbeatAt = n.CreatedAt
	}
	n.Active = true
	m.computeNodes[n.ID] = n
	return n, nil
}

// SetComputeNodeActive flips active on a row by id (issue #98 /
// ADR-028). The watchdog drains a stale node to false; the heartbeat
// goroutine reanimates a drained node to true on the next successful
// dial. MemStore flips the flag in place; production also flips but
// additionally fires the compute_node_changed pg_notify trigger so
// gatewayd sees the change without polling.
func (m *MemStore) SetComputeNodeActive(_ context.Context, id string, active bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[id]
	if !ok {
		return ErrNotFound
	}
	n.Active = active
	m.computeNodes[id] = n
	return nil
}

// ListComputeNodes returns every row in name order (issue #98 /
// ADR-028). When includeInactive is false, drained rows are filtered
// out — placement-equivalent semantics, backed by the partial
// compute_nodes_active_idx on the production side.
func (m *MemStore) ListComputeNodes(_ context.Context, includeInactive bool) ([]ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeNode, 0, len(m.computeNodes))
	for _, n := range m.computeNodes {
		if !includeInactive && !n.Active {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteComputeNode hard-deletes a row by id (issue #98 / ADR-028).
// Mirrors pgstore's semantics: ErrNotFound when no row matches. The
// caller (apid's DELETE ?hard=1) is responsible for refusing on the
// synthetic default-local row — see cmd/apid/compute_nodes.go.
func (m *MemStore) DeleteComputeNode(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[id]; !ok {
		return ErrNotFound
	}
	delete(m.computeNodes, id)
	return nil
}

// AppendEvent (commit 4) fixes two pre-existing bugs that the audit-log
// PR surfaced. Before: the row's Subject pointer was dropped on the
// floor (line 1226-1227 had a dead type-assertion placeholder and
// the Event literal never set Subject), so ListEvents could never
// filter by subject. After: parse the string into a *uuid.UUID and
// copy Data so a caller can reuse the byte slice. The hex form is
// accepted too (see parseSubjectID).
func (m *MemStore) AppendEvent(_ context.Context, actor, kind string, subject *string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subj *uuid.UUID
	if subject != nil {
		subj = parseSubjectID(*subject)
	}
	e := Event{
		At:      time.Now(),
		Actor:   actor,
		Kind:    kind,
		Subject: subj,
		Data:    append([]byte(nil), data...),
	}
	m.events = append(m.events, e)
	return nil
}

func (m *MemStore) ListEvents(_ context.Context, subject string, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subj *uuid.UUID
	if subject != "" {
		subj = parseSubjectID(subject)
		if subj == nil {
			// Unparseable filter — no row would have produced it,
			// return empty rather than silently matching everything.
			return nil, nil
		}
	}
	var out []Event
	for i := len(m.events) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		e := m.events[i]
		// Match either: no subject filter, OR the row's Subject
		// pointer is non-nil and equals the filter. The pre-fix
		// && false made this branch dead; tests caught it.
		if subj == nil || (e.Subject != nil && *e.Subject == *subj) {
			out = append(out, e)
		}
	}
	return out, nil
}

// --- Usage ------------------------------------------------------------------

// AppendUsage writes one (instance, minute) usage row and updates the
// per-(account, app, month) aggregate so UsageByMonth keeps returning the
// spec §10 shape without re-scanning the per-minute rows. Idempotent on
// (instance_id, minute): a redelivered minute is a no-op (first write
// wins). Mirrors the production INSERT … ON CONFLICT (instance_id, minute)
// DO NOTHING semantics in pgstore.go — see M7 hardening PR
// feat/m7-beta-hardening for the audit that surfaced this contract change.
func (m *MemStore) AppendUsage(_ context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := minute.UTC().Truncate(time.Minute)
	for i := range m.usage {
		if m.usage[i].InstanceID == instanceID && m.usage[i].Minute.Equal(key) {
			// Idempotent: redelivered minute is a no-op. The first tick
			// wins; restart-driven redelivery does not inflate billing.
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

// HasStripePushHour + RecordStripePushHour implement the pkg/billing/stripe
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

// IssueCliAuthCode stores a freshly-minted code's SHA-256 hash with
// no account binding (AccountID empty). The hash key format matches
// loginTokens (the binary []byte hash used as a string key). A
// re-issue of the same hash is a no-op overwriting the entry, which
// matches the production on-conflict-do-nothing semantics.
func (m *MemStore) IssueCliAuthCode(_ context.Context, tokenHash []byte, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cliAuthCodes == nil {
		m.cliAuthCodes = map[string]CliAuthCode{}
	}
	m.cliAuthCodes[string(tokenHash)] = CliAuthCode{
		TokenHash: append([]byte(nil), tokenHash...),
		ExpiresAt: expiresAt,
	}
	return nil
}

// PeekCliAuthCode returns the row's status without mutating it. Used
// by the dashboard GET /cli-auth render to decide whether to show the
// email-input form or the "code unavailable" error page.
func (m *MemStore) PeekCliAuthCode(_ context.Context, tokenHash []byte) (api.CliAuthStatus, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.cliAuthCodes[string(tokenHash)]
	if !ok {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if !row.ExpiresAt.After(time.Now()) {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if row.ConsumedAt != nil {
		return api.CliAuthStatusConsumed, row.AccountID, nil
	}
	return api.CliAuthStatusPending, row.AccountID, nil
}

// ClaimCliAuthCode atomically transitions pending → consumed and
// binds account_id. Error shapes mirror PgStore (review finding F5):
//
//	ErrNotFound  — row missing OR expired (never minted or TTL passed)
//	ErrConflict  — row exists but already claimed by a prior call
//
// The CAS-equivalent for MemStore is the m.mu serializing all
// readers/writers; the second concurrent caller observes the
// first's write (AccountID != "") and returns ErrConflict.
//
// IMPORTANT: this MUST NOT touch ConsumedAt — that field is the
// exclusive mint-gate for ConsumeCliAuthCode. Pre-setting it here
// would short-circuit the CAS that the CLI's exchange relies on to
// mint exactly one API key per code (review finding F4).
func (m *MemStore) ClaimCliAuthCode(_ context.Context, tokenHash []byte, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.cliAuthCodes[string(tokenHash)]
	if !ok {
		return ErrNotFound
	}
	if !row.ExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	if row.AccountID != "" {
		// A prior claim already bound the row to some account_id.
		// Dashboard POST never re-claims, so this is either a retry
		// (user double-clicked) or a parallel claim race. Either
		// way the row is no longer pending → ErrConflict so the
		// handler can render "Code already used".
		return ErrConflict
	}
	row.AccountID = accountID
	m.cliAuthCodes[string(tokenHash)] = row
	return nil
}

// ConsumeCliAuthCode is the CLI's poll-side read PLUS mint gate.
// Atomic CAS (mirrors ConsumeLoginToken): only mutates consumed_at
// on the FIRST call, returns the bound account_id exactly once.
// A buggy or replaying CLI cannot mint multiple keys for the same
// code (review finding F4).
//
// Filter: `account_id` must be non-empty (Claim must have run
// first) — without it the row is still pending and the CLI should
// keep polling, not see (Consumed,"") which would mint a key for an
// unbound account.
//
// Return contract:
//
//	pending (or empty account_id) → (Pending,  "",       nil)        keep polling
//	consumed (first call)        → (Consumed, acct_id,  nil)        mint API key
//	replay / expired / unknown    → (Expired, "",        ErrNotFound)
func (m *MemStore) ConsumeCliAuthCode(_ context.Context, tokenHash []byte) (api.CliAuthStatus, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.cliAuthCodes[string(tokenHash)]
	if !ok {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if !row.ExpiresAt.After(time.Now()) {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if row.AccountID == "" || row.ConsumedAt != nil {
		// Either still pending (dashboard hasn't claimed yet) or
		// already consumed (replay). The caller distinguishes via
		// the consumed_at nil check.
		if row.ConsumedAt != nil {
			return api.CliAuthStatusExpired, "", ErrNotFound
		}
		return api.CliAuthStatusPending, "", nil
	}
	now := time.Now()
	row.ConsumedAt = &now
	m.cliAuthCodes[string(tokenHash)] = row
	return api.CliAuthStatusConsumed, row.AccountID, nil
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

// --- customer secrets (spec §11/G2) -----------------------------------------
//
// Mirror of the PgStore implementations above. Plaintext VALUES never enter
// the MemStore — callers (apid handlers, schedd) pass ciphertext only. The
// MemStore's role is to model the (account_id, app_id, key) row shape + the
// ownership checks so unit tests can verify quota / list / delete logic
// without touching Postgres.

// UpsertAppSecret inserts or replaces the (account_id, app_id, key) row.
// updated_at is bumped on every call so schedd's wake staging observes a
// fresh mtime even when the ciphertext is identical (rotation flows
// re-seal with the same plaintext).
func (m *MemStore) UpsertAppSecret(_ context.Context, accountID, appID, key string, ciphertext []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := secretKey{AppID: appID, Key: key}
	existing, ok := m.secrets[k]
	now := time.Now()
	if !ok {
		m.secrets[k] = AppSecret{AccountID: accountID, AppID: appID, Key: key, Ciphertext: ciphertext, CreatedAt: now, UpdatedAt: now}
		return nil
	}
	if existing.AccountID != accountID {
		return ErrNotFound
	}
	existing.Ciphertext = ciphertext
	existing.UpdatedAt = now
	m.secrets[k] = existing
	return nil
}

// DeleteAppSecret removes the (account_id, app_id, key) row. Returns
// ErrNotFound when no row matches — same semantics as PgStore so the
// handler renders 400 CodeSecretNotFound.
func (m *MemStore) DeleteAppSecret(_ context.Context, accountID, appID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := secretKey{AppID: appID, Key: key}
	row, ok := m.secrets[k]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.secrets, k)
	return nil
}

// ListAppSecrets returns every secret on the app, scoped to accountID.
// Order: by key ASC (matches PgStore ORDER BY).
func (m *MemStore) ListAppSecrets(_ context.Context, accountID, appID string) ([]AppSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppSecret
	for _, s := range m.secrets {
		if s.AppID != appID || s.AccountID != accountID {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// CountAppSecrets is the quota helper. Mirrors PgStore.CountAppSecrets.
func (m *MemStore) CountAppSecrets(_ context.Context, accountID, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.secrets {
		if s.AppID == appID && s.AccountID == accountID {
			n++
		}
	}
	return n, nil
}

// --- G6 account self-service (spec §17 G6, ADR-021) -------------------------
//
// MemStore mirrors PgStore for the G6 endpoints so handler tests
// exercise the same shape the production store enforces. The grace
// window lives in state.DeletionGraceDuration (the MemStore enforces
// the same constant the production timer uses).

// DeletionGraceDuration returns the customer-visible grace window the
// customer has to restore their account. MemStore and PgStore share
// the constant so handler tests don't drift from production behavior.
func DeletionGraceDuration() time.Duration { return 30 * 24 * time.Hour }

// DeleteAccount walks the FK graph in dependency order under a single
// m.mu lock. The dependency order matches the PgStore tx so a redelivered
// grace tick finds the same idempotent answer.
func (m *MemStore) DeleteAccount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	// Conditional-delete mirrors the PgStore SQL `WHERE id=$1 AND
	// status='deleted_pending'`. We refuse to delete a row that's not
	// in deleted_pending so the restore→tick race closes identically
	// to the production path: if RestoreAccount flipped the row back
	// to active in between ListAllAccounts and DeleteAccount, the
	// grace timer gets ErrNotFound and swallows it.
	if a.Status != AccountDeletedPending {
		return ErrNotFound
	}
	// Drop children first so the parent's final delete is the sentinel.
	for k := range m.secrets {
		if m.secrets[k].AccountID == id {
			delete(m.secrets, k)
		}
	}
	for domain, d := range m.domains {
		if app, ok := m.apps[d.AppID]; ok && app.AccountID == id {
			delete(m.domains, domain)
		}
	}
	for cid, c := range m.crons {
		if app, ok := m.apps[c.AppID]; ok && app.AccountID == id {
			delete(m.crons, cid)
		}
	}
	for iid, ins := range m.instances {
		if app, ok := m.apps[ins.AppID]; ok && app.AccountID == id {
			delete(m.instances, iid)
		}
	}
	// Snapshots + builds are keyed by deployment_id; resolve the
	// deployment set first.
	deletedDeployments := map[string]struct{}{}
	for did, d := range m.deployments {
		if app, ok := m.apps[d.AppID]; ok && app.AccountID == id {
			deletedDeployments[did] = struct{}{}
			delete(m.deployments, did)
		}
	}
	for i := len(m.snapshots) - 1; i >= 0; i-- {
		if _, ok := deletedDeployments[m.snapshots[i].DeploymentID]; ok {
			m.snapshots = append(m.snapshots[:i], m.snapshots[i+1:]...)
		}
	}
	for bid, b := range m.builds {
		if _, ok := deletedDeployments[b.DeploymentID]; ok {
			delete(m.builds, bid)
		}
	}
	for aid, a := range m.apps {
		if a.AccountID == id {
			delete(m.apps, aid)
			delete(m.githubBindings, aid)
		}
	}
	for kid, k := range m.keys {
		if k.AccountID == id {
			delete(m.keys, kid)
			delete(m.keyByHash, hex.EncodeToString(k.Hash))
		}
	}
	for k := range m.idem {
		// The MemStore idem key shape is "accountID\x00key" — strip
		// the prefix to find this account's bucket.
		if strings.HasPrefix(k, id+"\x00") {
			delete(m.idem, k)
		}
	}
	// usage_minutes + usageByMonth aggregates are keyed by accountID
	// (no separate owner column); filter and rewrite both slices.
	var kept []usageMinute
	for _, u := range m.usage {
		if u.AccountID != id {
			kept = append(kept, u)
		}
	}
	m.usage = kept
	var keptMonth []Usage
	for _, u := range m.usageByMonth {
		if u.AccountID != id {
			keptMonth = append(keptMonth, u)
		}
	}
	m.usageByMonth = keptMonth
	// Finally: clear stripeByCustomer reverse-index, then the parent.
	for sc, acid := range m.stripeByCustomer {
		if acid == id {
			delete(m.stripeByCustomer, sc)
		}
	}
	// Audit events (spec §17 G6 right-to-erasure). Drop events whose
	// subject is the account id or whose data->>account_id matches.
	// Mirrors the PgStore cascade; a non-JSON Data is left alone (the
	// parser below bails on the first byte).
	idUUID, _ := uuid.Parse(id)
	var keptEvents []Event
	for _, e := range m.events {
		if e.Subject != nil && idUUID != uuid.Nil && *e.Subject == idUUID {
			continue
		}
		if len(e.Data) > 0 {
			var payload map[string]any
			if err := json.Unmarshal(e.Data, &payload); err == nil {
				if v, ok := payload["account_id"].(string); ok && v == id {
					continue
				}
			}
		}
		keptEvents = append(keptEvents, e)
	}
	m.events = keptEvents
	delete(m.accounts, id)
	return nil
}

// ListBuildsForAccount returns every build tied to the account.
func (m *MemStore) ListBuildsForAccount(_ context.Context, accountID string) ([]Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ownedDeployments := map[string]struct{}{}
	for _, d := range m.deployments {
		if app, ok := m.apps[d.AppID]; ok && app.AccountID == accountID {
			ownedDeployments[d.ID] = struct{}{}
		}
	}
	var out []Build
	for _, b := range m.builds {
		if _, ok := ownedDeployments[b.DeploymentID]; ok {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// ListCronsForAccount returns every cron tied to the account.
func (m *MemStore) ListCronsForAccount(_ context.Context, accountID string) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Cron
	for _, c := range m.crons {
		if app, ok := m.apps[c.AppID]; ok && app.AccountID == accountID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// UsageByAccount returns the per-month roll-up. Mirrors the PgStore
// shape (per-app, per-month aggregated mb_seconds + requests).
func (m *MemStore) UsageByAccount(_ context.Context, accountID string, since time.Time) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type bucket struct{ mb, req int64 }
	agg := map[string]*bucket{}
	for _, u := range m.usage {
		if u.AccountID != accountID {
			continue
		}
		if !since.IsZero() && u.Minute.Before(since) {
			continue
		}
		key := u.AppID + "\x00" + u.Minute.Format("2006-01")
		if _, ok := agg[key]; !ok {
			agg[key] = &bucket{}
		}
		agg[key].mb += u.MBSeconds
		agg[key].req += u.Requests
	}
	out := make([]Usage, 0, len(agg))
	for key, b := range agg {
		parts := strings.SplitN(key, "\x00", 2)
		month, _ := time.Parse("2006-01", parts[1])
		out = append(out, Usage{
			AccountID: accountID, AppID: parts[0], Month: month,
			MBSeconds: b.mb, Requests: b.req,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].Month.Before(out[j].Month)
	})
	return out, nil
}

// MarkAccountDeletionPending flips the account into deleted_pending.
// Idempotent: if already pending, the original timestamp survives so
// the grace window's anchor stays at the customer's first ask.
func (m *MemStore) MarkAccountDeletionPending(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.Status == AccountDeletedPending && a.DeletionRequestedAt != nil {
		return nil
	}
	a.Status = AccountDeletedPending
	now := time.Now().UTC()
	if a.DeletionRequestedAt == nil {
		a.DeletionRequestedAt = &now
	}
	m.accounts[id] = a
	return nil
}

// RestoreAccount flips status back to active and clears
// deletion_requested_at iff inside the 30-day grace window. Past
// grace → ErrConflict.
func (m *MemStore) RestoreAccount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.Status != AccountDeletedPending || a.DeletionRequestedAt == nil {
		return ErrConflict
	}
	if time.Since(*a.DeletionRequestedAt) > DeletionGraceDuration() {
		return ErrConflict
	}
	a.Status = AccountActive
	a.DeletionRequestedAt = nil
	m.accounts[id] = a
	return nil
}

// AppendGdprRequest records the action on the in-memory ledger. Mirrors
// PgStore: no auto-prune on DeleteAccount (the production table also
// outlives the account row), so a test can assert the audit row
// against the email + timestamp after the account row is gone.
func (m *MemStore) AppendGdprRequest(_ context.Context, r GdprRequest) error {
	if r.ID == "" {
		return fmt.Errorf("AppendGdprRequest: id is required")
	}
	if r.RequestedAt.IsZero() {
		r.RequestedAt = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gdprRequests = append(m.gdprRequests, r)
	return nil
}

// ListGdprRequestsForAccount returns the rows in requested_at desc
// order, bounded by limit. limit <= 0 returns no rows (mirrors the
// PgStore guard).
func (m *MemStore) ListGdprRequestsForAccount(_ context.Context, accountID string, limit int) ([]GdprRequest, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]GdprRequest, 0)
	for i := len(m.gdprRequests) - 1; i >= 0 && len(out) < limit; i-- {
		if m.gdprRequests[i].AccountID == accountID {
			out = append(out, m.gdprRequests[i])
		}
	}
	return out, nil
}

// CompleteGdprRequest stamps completed_at on the most recent
// un-completed row of (account_id, action) in the in-memory ledger.
// Returns ErrNotFound when no matching row exists so callers can skip
// stale ticks without logging noise.
func (m *MemStore) CompleteGdprRequest(_ context.Context, accountID, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for i := len(m.gdprRequests) - 1; i >= 0; i-- {
		r := &m.gdprRequests[i]
		if r.AccountID == accountID && r.Action == GdprAction(action) && r.CompletedAt.IsZero() {
			r.CompletedAt = now
			return nil
		}
	}
	return ErrNotFound
}

// LoadAndStampLastQuotaWarning mirrors PgStore.LoadAndStampLastQuotaWarning
// for the in-memory implementation. Same contract:
//   - First call of the UTC day → (false, nil) and the row's stamp is
//     set to the supplied anchor's midnight.
//   - Same-day repeat → (true, nil) and the row's stamp stays put.
//   - Missing id → ErrNotFound.
func (m *MemStore) LoadAndStampLastQuotaWarning(_ context.Context, id string, day time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return false, ErrNotFound
	}
	dayStart := day.UTC().Truncate(24 * time.Hour)
	if a.LastQuotaWarningAt != nil && !a.LastQuotaWarningAt.Before(dayStart) {
		return true, nil
	}
	a.LastQuotaWarningAt = &dayStart
	m.accounts[id] = a
	return false, nil
}

// ClearQuotaWarning mirrors PgStore.ClearQuotaWarning. No-op when the
// row is gone or the stamp is already nil.
func (m *MemStore) ClearQuotaWarning(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return nil
	}
	a.LastQuotaWarningAt = nil
	m.accounts[id] = a
	return nil
}

// MarkDunningStep mirrors PgStore.MarkDunningStep. The MemStore enforces
// the same compare-and-flip semantics: the row's status must match
// `from` for the transition to land (else ErrNotFound — the
// redelivery-race guard), and past_due_at is stamped only when
// transitioning into past_due (coalesce preserves any pre-existing
// stamp). The from==to case is NOT short-circuited — it's the
// backfill-stamp path used by pkg/meter.Dunning to plant a stamp on
// a legacy row that entered past_due before the migration column
// existed.
func (m *MemStore) MarkDunningStep(_ context.Context, id string, from, to AccountStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.Status != from {
		return ErrNotFound
	}
	a.Status = to
	if to == AccountPastDue && a.PastDueAt == nil {
		now := time.Now().UTC()
		a.PastDueAt = &now
	}
	m.accounts[id] = a
	return nil
}

// SetPastDueAtForTest is the test-only backdoor pkg/meter.Dunning tests
// use to plant a deterministic PastDueAt. Production never calls it —
// the only PastDueAt writer is MarkDunningStep (via the apid webhook
// path) which stamps time.Now(). The dunning timer compares against
// PastDueAt so the only way to exercise the 7d/21d thresholds in a
// sub-second test is to bypass MarkDunningStep's now()-stamp.
//
// Prefixed with "ForTest" so a `go vet -tests-only` or production
// audit can find it; not in pkg/state.Store (no public surface for
// ad-hoc PastDueAt writes).
func (m *MemStore) SetPastDueAtForTest(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	stamp := at.UTC()
	a.PastDueAt = &stamp
	m.accounts[id] = a
	return nil
}

// SetDeletionRequestedAtForTest is the test-only backdoor the
// RestoreAccount-past-grace test uses to fast-forward the 30-day grace
// window without sleeping the test suite. Same `*_ForTest` naming
// convention as SetPastDueAtForTest (production audit-friendly); not
// part of the Store interface.
func (m *MemStore) SetDeletionRequestedAtForTest(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	stamp := at.UTC()
	a.DeletionRequestedAt = &stamp
	m.accounts[id] = a
	return nil
}

// compile-time check that MemStore satisfies Store.
var _ Store = (*MemStore)(nil)

// BackdateForTest rewinds the row's started_at to the supplied
// absolute timestamp. Used by the §6.1 watchdog tests in pkg/sched
// to fabricate a stuck-WAKING/COLD_BOOTING row whose age exceeds
// the budget. Production wiring does not need this — Postgres
// timestamps are real.
func (m *MemStore) BackdateForTest(id string, startedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ins, ok := m.instances[id]; ok {
		ins.StartedAt = startedAt
		m.instances[id] = ins
	}
}

// SetParkedAtForTest stamps the row's parked_at. Used by the
// watchdog tests to fabricate a stuck-SNAPSHOTTING row — the
// watchdog anchors SNAPSHOTTING age on parked_at, not started_at,
// because started_at is creation time.
func (m *MemStore) SetParkedAtForTest(id string, parkedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ins, ok := m.instances[id]; ok {
		ins.ParkedAt = parkedAt
		m.instances[id] = ins
	}
}

// SetTerminalAtForTest stamps the row's terminal_at. Used by the §17
// retention sweep tests in pkg/sched to fabricate terminal rows
// (STOPPED / FAILED) whose age exceeds the configured retention
// window. Production wiring does not need this — Engine.transition
// stamps terminal_at atomically via UpdateInstanceStateToTerminal.
func (m *MemStore) SetTerminalAtForTest(id string, terminalAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ins, ok := m.instances[id]; ok {
		ts := terminalAt
		ins.TerminalAt = &ts
		m.instances[id] = ins
	}
}

// SetSnapshotStorageKeyForTest is the F-2 test seam: the engine's
// Wake fallback for empty StorageKey (engine.go:251-254) needs a
// pre-migration row shape. The MemStore's CreateSnapshot rejects
// empty values by contract (F-1), so the only way to fabricate the
// pre-migration shape is to mutate a stored row directly. Production
// wiring does not need this — the migration's backfill UPDATE plus
// the empty-key fallback in Wake cover the real transition.
func (m *MemStore) SetSnapshotStorageKeyForTest(deploymentID, storageKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.snapshots {
		if s.DeploymentID == deploymentID {
			m.snapshots[i].StorageKey = storageKey
			return
		}
	}
}
