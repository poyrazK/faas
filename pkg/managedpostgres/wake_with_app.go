package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Wake with app (ADR-20261125105038): an opted-in scale-to-zero database
// starts when an app bound to it wakes, so the provider's cold start overlaps
// the app's instead of following it.

// WakeStartGap is the shortest time between two starts of one database for
// wakes: the shortest suspend timeout a customer can choose.
const WakeStartGap = time.Duration(api.ManagedPostgresSuspendTimeoutMinSeconds) * time.Second

// wakeStartRetry is how soon a wake may try again after a claim that started
// nothing (the compute was already running) or failed: a running compute
// can suspend before the gap ends, and the next wake must still start it.
const wakeStartRetry = 10 * time.Second

// wakeAppsRefresh is how often a starter re-reads which apps have an
// opted-in database; a change made through this apid is seen at once. A
// failed read waits for the same interval.
const wakeAppsRefresh = 30 * time.Second

// wakeStoreTimeout bounds each catalog read and claim, so a slow catalog
// never holds up the notification loop.
const wakeStoreTimeout = 5 * time.Second

// ErrWakeWithAppUnsupported: the database cannot start with its app: it is
// always-on, or its backend cannot start a compute.
var ErrWakeWithAppUnsupported = errors.New("managedpostgres: wake with app unsupported")

// ComputeStarter is a provider that can start a database's suspended
// compute. Starting a compute that runs, or one the platform holds, does
// nothing and reports false.
type ComputeStarter interface {
	StartCompute(ctx context.Context, providerResourceID string) (started bool, err error)
}

// WakeStartClaim is one database a wake claimed to start.
type WakeStartClaim struct {
	DatabaseID         string
	BackendID          string
	BackendFingerprint string
	ProviderResourceID string
	// ClaimedAt is the stamp the claim wrote.
	ClaimedAt time.Time
}

// WakeWithAppStore keeps which databases start with their apps.
type WakeWithAppStore interface {
	SetWakeWithApp(ctx context.Context, databaseID string, enabled bool) error
	// WakeWithApp reports which of the databases are opted in.
	WakeWithApp(ctx context.Context, databaseIDs []string) (map[string]bool, error)
	// WakeWithAppApps lists the apps with a live binding to an opted-in
	// database.
	WakeWithAppApps(ctx context.Context) (map[string]bool, error)
	// ClaimWakeStarts claims, and stamps, the opted-in ready scale-to-zero
	// databases bound to the app that were not started within gap.
	ClaimWakeStarts(ctx context.Context, appID string, gap time.Duration) ([]WakeStartClaim, error)
	// DeferWakeStart moves a claim's stamp back so the database can be
	// claimed again retry after the claim instead of gap after; it does
	// nothing when a later claim replaced the stamp.
	DeferWakeStart(ctx context.Context, claim WakeStartClaim, gap, retry time.Duration) error
}

// WakeWithApp reports which of the databases start with their apps.
func (s *Service) WakeWithApp(ctx context.Context, databases []Database) (map[string]bool, error) {
	store, ok := s.store.(WakeWithAppStore)
	if !ok || len(databases) == 0 {
		return map[string]bool{}, nil
	}
	ids := make([]string, 0, len(databases))
	for _, database := range databases {
		ids = append(ids, database.ID)
	}
	return store.WakeWithApp(ctx, ids)
}

// SetWakeWithApp turns starting a database with its apps on or off. Turning
// it on can raise spend, so it is refused while operations are closed and
// passes the usage guardrail; turning it off is always taken.
func (s *Service) SetWakeWithApp(ctx context.Context, accountID, databaseID string, enabled bool) error {
	if accountID == "" || databaseID == "" {
		return ErrInvalid
	}
	store, ok := s.store.(WakeWithAppStore)
	if !ok {
		return ErrUnsupported
	}
	database, err := s.Get(ctx, accountID, databaseID)
	if err != nil {
		return err
	}
	if !enabled {
		return store.SetWakeWithApp(ctx, database.ID, false)
	}
	if !database.Spec.ScaleToZero {
		return ErrWakeWithAppUnsupported
	}
	if database.State != StateReady {
		return ErrConflict
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return err
	}
	if _, ok := backend.Provider.(ComputeStarter); !ok {
		return ErrWakeWithAppUnsupported
	}
	if !s.operationsEnabled() {
		return ErrProvisioningClosed
	}
	if s.admit != nil {
		if err := s.admit(ctx, PolicySubject(ctx, accountID)); err != nil {
			return err
		}
	}
	if err := store.SetWakeWithApp(ctx, database.ID, true); err != nil {
		return err
	}
	if s.wakeStarter != nil {
		s.wakeStarter.invalidate()
	}
	return nil
}

// WakeStartOutcome labels one start attempt.
type WakeStartOutcome string

const (
	WakeStartStarted     WakeStartOutcome = "started"
	WakeStartRunning     WakeStartOutcome = "running"
	WakeStartFailed      WakeStartOutcome = "failed"
	WakeStartUnsupported WakeStartOutcome = "unsupported"
)

// WakeStarter starts opted-in databases when their apps wake. Every apid
// runs one; a database's claim decides which one starts it.
type WakeStarter struct {
	registry *Registry
	store    WakeWithAppStore
	logger   *slog.Logger
	observe  func(WakeStartOutcome)
	now      func() time.Time
	// operationsEnabled is the service's kill switch: while operations are
	// closed no wake starts a database.
	operationsEnabled func() bool

	mu       sync.Mutex
	apps     map[string]bool
	loadedAt time.Time
}

// NewWakeStarter returns the service's starter, or nil when its store
// cannot keep the setting. observe may be nil.
func (s *Service) NewWakeStarter(logger *slog.Logger, observe func(WakeStartOutcome)) *WakeStarter {
	store, ok := s.store.(WakeWithAppStore)
	if !ok {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if observe == nil {
		observe = func(WakeStartOutcome) {}
	}
	operationsEnabled := s.operationsEnabled
	if operationsEnabled == nil {
		operationsEnabled = func() bool { return true }
	}
	starter := &WakeStarter{registry: s.registry, store: store, logger: logger, observe: observe, now: time.Now,
		operationsEnabled: operationsEnabled}
	s.wakeStarter = starter
	return starter
}

func (w *WakeStarter) invalidate() {
	w.mu.Lock()
	w.loadedAt = time.Time{}
	w.mu.Unlock()
}

// Run handles instance_changed payloads until ctx ends or the channel
// closes. Each start runs on its own, so a slow provider never delays the
// next wake.
func (w *WakeStarter) Run(ctx context.Context, payloads <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case payload, ok := <-payloads:
			if !ok {
				return
			}
			w.Handle(ctx, payload)
		}
	}
}

type instanceChanged struct {
	AppID string `json:"app_id"`
	State string `json:"state"`
	Kind  string `json:"kind"`
}

// Handle starts the app's opted-in databases when the payload is a wake.
// Only an instance entering waking or cold_booting is a wake: schedd enters
// one of them for every wake (a request, a prewarm, a cron); a later
// running, or one after a live migration, is not.
func (w *WakeStarter) Handle(ctx context.Context, payload string) {
	var event instanceChanged
	if json.Unmarshal([]byte(payload), &event) != nil || event.AppID == "" || event.Kind == "job" {
		return
	}
	switch event.State {
	case "waking", "cold_booting":
	default:
		return
	}
	if !w.operationsEnabled() || !w.appOptedIn(ctx, event.AppID) {
		return
	}
	claimCtx, cancel := context.WithTimeout(ctx, wakeStoreTimeout)
	defer cancel()
	claims, err := w.store.ClaimWakeStarts(claimCtx, event.AppID, WakeStartGap)
	if err != nil {
		w.logger.Debug("managed postgres wake start claim failed", "app_id", event.AppID, "error", err)
		return
	}
	for _, claim := range claims {
		go w.start(context.WithoutCancel(ctx), claim)
	}
}

// appOptedIn reports whether the app has an opted-in database, from a list
// re-read every wakeAppsRefresh.
func (w *WakeStarter) appOptedIn(ctx context.Context, appID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.loadedAt.IsZero() || w.now().Sub(w.loadedAt) >= wakeAppsRefresh {
		readCtx, cancel := context.WithTimeout(ctx, wakeStoreTimeout)
		apps, err := w.store.WakeWithAppApps(readCtx)
		cancel()
		if err == nil {
			w.apps = apps
		} else {
			w.logger.Debug("managed postgres wake apps read failed", "error", err)
		}
		// A failed read keeps the last list and waits a refresh before the
		// next, so a struggling catalog is not read once per transition.
		w.loadedAt = w.now()
	}
	return w.apps[appID]
}

func (w *WakeStarter) start(ctx context.Context, claim WakeStartClaim) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	backend, err := w.registry.Resolve(claim.BackendID, claim.BackendFingerprint)
	if err != nil {
		w.observe(WakeStartFailed)
		return
	}
	starter, ok := backend.Provider.(ComputeStarter)
	if !ok {
		w.observe(WakeStartUnsupported)
		return
	}
	started, err := starter.StartCompute(ctx, claim.ProviderResourceID)
	switch {
	case err != nil:
		w.logger.Debug("managed postgres wake start failed", "database_id", claim.DatabaseID, "error", err)
		w.observe(WakeStartFailed)
	case started:
		w.observe(WakeStartStarted)
		return
	default:
		w.observe(WakeStartRunning)
	}
	// Nothing started: a wake soon after must be able to try again.
	if err := w.store.DeferWakeStart(ctx, claim, WakeStartGap, wakeStartRetry); err != nil {
		w.logger.Debug("managed postgres wake start defer failed", "database_id", claim.DatabaseID, "error", err)
	}
}

// HasWakeStarter reports whether the service runs a wake starter.
func (s *Service) HasWakeStarter() bool { return s.wakeStarter != nil }

// RunWakeStarter feeds instance_changed payloads to the service's starter
// until ctx ends; it returns at once when the service has none.
func (s *Service) RunWakeStarter(ctx context.Context, payloads <-chan string) {
	if s.wakeStarter == nil {
		return
	}
	s.wakeStarter.Run(ctx, payloads)
}
