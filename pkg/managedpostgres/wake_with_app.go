package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Wake with app (ADR-20261125105038): an opted-in scale-to-zero database
// starts when an app bound to it wakes, so the provider's cold start overlaps
// the app's instead of following it.

// WakeStartGap is the shortest time between two starts of one database for
// wakes: the shortest suspend timeout a customer can choose.
const WakeStartGap = time.Minute

// wakeAppsRefresh is how often a starter re-reads which apps have an
// opted-in database; a change made through this apid is seen at once.
const wakeAppsRefresh = 30 * time.Second

// ErrWakeWithAppUnsupported: the database cannot start with its app: it is
// always-on, or its backend cannot start a compute.
var ErrWakeWithAppUnsupported = errors.New("managedpostgres: wake with app unsupported")

// ComputeStarter is a provider that can start a database's suspended
// compute. Starting a compute that runs, or one the platform holds, does
// nothing.
type ComputeStarter interface {
	StartCompute(ctx context.Context, providerResourceID string) error
}

// WakeStartClaim is one database a wake claimed to start.
type WakeStartClaim struct {
	DatabaseID         string
	BackendID          string
	BackendFingerprint string
	ProviderResourceID string
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
	starter := &WakeStarter{registry: s.registry, store: store, logger: logger, observe: observe, now: time.Now}
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
func (w *WakeStarter) Handle(ctx context.Context, payload string) {
	var event instanceChanged
	if json.Unmarshal([]byte(payload), &event) != nil || event.AppID == "" || event.Kind == "job" {
		return
	}
	switch event.State {
	case "waking", "cold_booting", "running":
	default:
		return
	}
	if !w.appOptedIn(ctx, event.AppID) {
		return
	}
	claims, err := w.store.ClaimWakeStarts(ctx, event.AppID, WakeStartGap)
	if err != nil {
		w.logger.Debug("managed postgres wake start claim failed", "app_id", event.AppID, "error", err)
		return
	}
	for _, claim := range claims {
		go w.start(context.WithoutCancel(ctx), claim)
	}
}

// appOptedIn reports whether the app has an opted-in database, from a list
// re-read every wakeAppsRefresh. A failed read keeps the last list.
func (w *WakeStarter) appOptedIn(ctx context.Context, appID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.apps == nil || w.now().Sub(w.loadedAt) >= wakeAppsRefresh {
		apps, err := w.store.WakeWithAppApps(ctx)
		if err == nil {
			w.apps, w.loadedAt = apps, w.now()
		} else {
			w.logger.Debug("managed postgres wake apps read failed", "error", err)
		}
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
	if err := starter.StartCompute(ctx, claim.ProviderResourceID); err != nil {
		w.logger.Debug("managed postgres wake start failed", "database_id", claim.DatabaseID, "error", err)
		w.observe(WakeStartFailed)
		return
	}
	w.observe(WakeStartStarted)
}

// RunWakeStarter feeds instance_changed payloads to the service's starter
// until ctx ends; it returns at once when the service has none.
func (s *Service) RunWakeStarter(ctx context.Context, payloads <-chan string) {
	if s.wakeStarter == nil {
		return
	}
	s.wakeStarter.Run(ctx, payloads)
}
