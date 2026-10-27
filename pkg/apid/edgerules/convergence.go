package edgerules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	EdgeRuleConvergenceTimeout = 3 * time.Second
	EdgeRuleNotificationRetry  = 250 * time.Millisecond
	edgeRulesStateHeader       = "X-Faas-Edge-Rules-State"
	edgeRulesGenerationHeader  = "X-Faas-Edge-Rules-Generation"
)

type edgeRuleGenerationStore interface {
	NextEdgeRuleGeneration(context.Context) (int64, error)
}

// EdgeRuleMutationLocker is optional so MemStore-backed unit tests and
// legacy single-box adapters retain the process-local fallback. Production
// PgStore implements it with a session-scoped PostgreSQL advisory lock,
// closing the cross-apid race where two mutations for one app could publish
// generations out of order.
type EdgeRuleMutationLocker interface {
	AcquireEdgeRuleMutationLock(context.Context, string) (func(context.Context), error)
}

type EdgeRuleConvergence struct {
	Notif             Notifier
	Events            <-chan db.Notification
	Cancel            func()
	Unlock            func(context.Context)
	done              sync.Once
	Generation        int64
	AppID             string
	AccountID         string
	RuleID            string
	Operation         string
	Hosts             []string
	Expected          map[string]struct{}
	OnAcknowledgement func(context.Context, string) error
}

// edgeRuleAccountLocks serializes this process's mutations per account
// before they contend for the PostgreSQL advisory locks.
var edgeRuleAccountLocks = keyedLocks{locks: map[string]*keyedLock{}}

func (s *Service) PrepareEdgeRuleMutation(ctx context.Context, appID, ruleID, operation string, hosts ...string) (*EdgeRuleConvergence, error) {
	unlock, err := s.LockEdgeRuleMutations(ctx, []string{appID})
	if err != nil {
		return nil, err
	}
	return s.PrepareEdgeRuleMutationLocked(ctx, appID, ruleID, operation, unlock, hosts...)
}

// LockEdgeRuleMutations serializes edge-rule mutations of the apps' accounts,
// then of the apps (ADR-091 amendment 2026-10-27). Two apps of one account
// can fence the same host, and a gateway drops that fence on the newest
// generation's apply, so one account's generations must commit in order;
// other accounts' mutations proceed in parallel. An app whose account cannot
// be read is locked by app only, as apids before the account lock did.
func (s *Service) LockEdgeRuleMutations(ctx context.Context, appIDs []string) (func(context.Context), error) {
	store := s.Host.Store()
	accounts := make([]string, 0, len(appIDs))
	processKeys := make([]string, 0, 2*len(appIDs))
	for _, appID := range appIDs {
		if app, err := store.AppByID(ctx, appID); err == nil && app.AccountID != "" {
			accounts = append(accounts, app.AccountID)
			processKeys = append(processKeys, "account:"+app.AccountID)
		}
		// "account:" sorts before "app:", so every caller takes accounts first.
		processKeys = append(processKeys, "app:"+appID)
	}
	releaseProcess := edgeRuleAccountLocks.lock(processKeys)
	var release func(context.Context)
	var err error
	switch locker := store.(type) {
	case state.EdgeRuleAccountMutationLocker:
		release, err = locker.AcquireEdgeRuleAccountMutationLocks(ctx, accounts, appIDs)
	case EdgeRuleMutationLocker:
		release, err = acquireAppLocks(ctx, locker, appIDs)
	default:
		release = func(context.Context) {}
	}
	if err != nil {
		releaseProcess()
		return nil, err
	}
	var once sync.Once
	return func(ctx context.Context) {
		once.Do(func() {
			release(ctx)
			releaseProcess()
		})
	}, nil
}

func acquireAppLocks(ctx context.Context, locker EdgeRuleMutationLocker, appIDs []string) (func(context.Context), error) {
	apps := slices.Clone(appIDs)
	slices.Sort(apps)
	apps = slices.Compact(apps)
	releases := make([]func(context.Context), 0, len(apps))
	releaseAll := func(ctx context.Context) {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i](ctx)
		}
	}
	for _, app := range apps {
		release, err := locker.AcquireEdgeRuleMutationLock(ctx, app)
		if err != nil {
			releaseAll(ctx)
			return nil, err
		}
		releases = append(releases, release)
	}
	return releaseAll, nil
}

// keyedLocks is a set of mutexes keyed by string; an entry lives while a
// caller holds or waits for it.
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu   sync.Mutex
	refs int
}

// lock takes every key's mutex in sorted order and returns the release.
func (k *keyedLocks) lock(keys []string) func() {
	keys = slices.Clone(keys)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	held := make([]*keyedLock, 0, len(keys))
	for _, key := range keys {
		k.mu.Lock()
		l := k.locks[key]
		if l == nil {
			l = &keyedLock{}
			k.locks[key] = l
		}
		l.refs++
		k.mu.Unlock()
		l.mu.Lock()
		held = append(held, l)
	}
	return func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].mu.Unlock()
			k.mu.Lock()
			if held[i].refs--; held[i].refs == 0 {
				delete(k.locks, keys[i])
			}
			k.mu.Unlock()
		}
	}
}

// Batch callers take LockEdgeRuleMutations once for every app they change.
// Each convergence owns only its subscription; the batch retains those locks
// until every prepare, intent transaction, and apply attempt has finished.
func (s *Service) PrepareEdgeRuleMutationLocked(ctx context.Context, appID, ruleID, operation string, unlock func(context.Context), hosts ...string) (*EdgeRuleConvergence, error) {
	allocator, ok := s.Host.Store().(edgeRuleGenerationStore)
	if !ok {
		unlock(ctx)
		return nil, errors.New("edge-rule generation store is unavailable")
	}
	generation, err := allocator.NextEdgeRuleGeneration(ctx)
	if err != nil {
		unlock(ctx)
		return nil, err
	}
	// The owner scopes the gateway fence to this account's apps. A failed
	// lookup leaves it empty, which fences every app on the hosts: the
	// conservative pre-scoping behaviour, never a skipped fence.
	var accountID string
	if app, lookupErr := s.Host.Store().AppByID(ctx, appID); lookupErr == nil {
		accountID = app.AccountID
	}
	conv := &EdgeRuleConvergence{
		Notif: s.Host.Notifier(), Generation: generation, AppID: appID, AccountID: accountID, RuleID: ruleID,
		Operation: operation, Hosts: canonicalEdgeRuleHosts(hosts), Expected: map[string]struct{}{},
		Cancel: func() {}, Unlock: unlock,
	}
	nodes, err := s.ServingEdgeRuleNodes(ctx)
	if err != nil {
		conv.Close(ctx)
		return nil, fmt.Errorf("list serving gateways: %w", err)
	}
	for _, node := range nodes {
		conv.Expected[node] = struct{}{}
	}
	// A fleet mutation with no serving gateway authority is unsafe: it
	// could report success while an unregistered edge continues serving stale
	// policy. Unnamed legacy single-box installs and local development
	// intentionally have no compute registry.
	if s.FleetRequired && len(conv.Expected) == 0 {
		conv.Close(ctx)
		return nil, errors.New("no active serving gateways are registered")
	}
	if len(conv.Expected) > 0 {
		// Keep the acknowledgement listener alive long enough to publish the
		// post-commit apply even if the HTTP client disconnects immediately
		// after persistence. close still tears it down on every normal path.
		listenCtx, listenCancel := context.WithCancel(context.WithoutCancel(ctx))
		var subscriptionCancel func()
		conv.Events, subscriptionCancel, err = s.Host.Notifier().Subscribe(listenCtx, []string{db.NotifyEdgeRuleAck})
		if err != nil {
			listenCancel()
			conv.Close(ctx)
			return nil, fmt.Errorf("subscribe edge-rule acknowledgements: %w", err)
		}
		conv.Cancel = func() {
			subscriptionCancel()
			listenCancel()
		}
	}
	if err := conv.notifyAndWait(ctx, "prepare"); err != nil {
		conv.Abort(ctx)
		return nil, err
	}
	return conv, nil
}

// ServingEdgeRuleNodes lists the gateways a mutation waits for. A gateway
// whose edge-rule listener stopped reporting (ADR-091 amendment 2026-10-27)
// is left out: waiting for it would fail every account's mutations until it
// came back. It catches up within the 30 s rule cache TTL once it does.
func (s *Service) ServingEdgeRuleNodes(ctx context.Context) ([]string, error) {
	nodes, err := s.Host.Store().ListComputeNodes(ctx, false)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, node := range nodes {
		role := ""
		if node.Role != nil {
			role = strings.TrimSpace(*node.Role)
		}
		if (role == "compute-only" || role == "compute-node") && node.GatewayTargetURL != nil && strings.TrimSpace(*node.GatewayTargetURL) != "" {
			seen[node.Name] = struct{}{}
		}
	}
	if liveness, ok := s.Host.Store().(state.StaleEdgeListenerStore); ok && len(seen) > 0 {
		stale, err := liveness.StaleEdgeListenerNodes(ctx, state.EdgeListenerStaleAfter)
		if err != nil {
			return nil, err
		}
		for _, node := range stale {
			if _, ok := seen[node]; ok {
				delete(seen, node)
				s.Host.Logger().Warn("edge-rule convergence skips a gateway whose listener is stale",
					"node", node, "stale_after", state.EdgeListenerStaleAfter)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for node := range seen {
		out = append(out, node)
	}
	sort.Strings(out)
	return out, nil
}

func canonicalEdgeRuleHosts(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			seen[host] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for host := range seen {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

func (c *EdgeRuleConvergence) payload(phase string) (string, error) {
	body, err := json.Marshal(db.EdgeRuleChangedPayload{
		AppID: c.AppID, AccountID: c.AccountID, RuleID: c.RuleID, Operation: c.Operation, Phase: phase,
		Generation: c.Generation, MatchHosts: c.Hosts,
	})
	return string(body), err
}

func (c *EdgeRuleConvergence) notifyAndWait(ctx context.Context, phase string) error {
	payload, err := c.payload(phase)
	if err != nil {
		return err
	}
	if err := c.Notif.Notify(ctx, db.NotifyEdgeRuleChanged, payload); err != nil {
		return fmt.Errorf("publish edge-rule %s generation %d: %w", phase, c.Generation, err)
	}
	if len(c.Expected) == 0 {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, EdgeRuleConvergenceTimeout)
	defer cancel()
	retry := time.NewTicker(EdgeRuleNotificationRetry)
	defer retry.Stop()
	seen := make(map[string]struct{}, len(c.Expected))
	for len(seen) < len(c.Expected) {
		select {
		case <-waitCtx.Done():
			missing := make([]string, 0, len(c.Expected)-len(seen))
			for node := range c.Expected {
				if _, ok := seen[node]; !ok {
					missing = append(missing, node)
				}
			}
			sort.Strings(missing)
			return fmt.Errorf("edge-rule generation %d phase %s missing gateway acknowledgements: %s", c.Generation, phase, strings.Join(missing, ", "))
		case event, ok := <-c.Events:
			if !ok {
				return errors.New("edge-rule acknowledgement subscription closed")
			}
			ack, parseErr := db.ParseEdgeRuleAckPayload(event.Payload)
			if parseErr != nil || ack.Generation != c.Generation || ack.Phase != phase {
				continue
			}
			if _, expected := c.Expected[ack.Node]; expected {
				if _, duplicate := seen[ack.Node]; duplicate {
					continue
				}
				if c.OnAcknowledgement != nil {
					if err := c.OnAcknowledgement(waitCtx, ack.Node); err != nil {
						return fmt.Errorf("persist edge-rule acknowledgement: %w", err)
					}
				}
				seen[ack.Node] = struct{}{}
			}
		case <-retry.C:
			// PG_NOTIFY is intentionally ephemeral. Re-publishing the same
			// idempotent phase lets a listener that briefly reconnects join the
			// barrier instead of leaving one replica stale until cache expiry.
			_ = c.Notif.Notify(waitCtx, db.NotifyEdgeRuleChanged, payload)
		}
	}
	return nil
}

func (c *EdgeRuleConvergence) Apply(ctx context.Context, ruleID string) error {
	if c == nil {
		return errors.New("nil edge-rule convergence")
	}
	defer c.Close(ctx)
	c.RuleID = ruleID
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), EdgeRuleConvergenceTimeout+time.Second)
	defer cancel()
	return c.notifyAndWait(applyCtx, "apply")
}

func (c *EdgeRuleConvergence) Abort(ctx context.Context) {
	if c == nil {
		return
	}
	defer c.Close(ctx)
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	payload, err := c.payload("abort")
	if err == nil {
		_ = c.Notif.Notify(abortCtx, db.NotifyEdgeRuleChanged, payload)
	}
}

func (c *EdgeRuleConvergence) Close(ctx context.Context) {
	if c == nil {
		return
	}
	c.done.Do(func() {
		c.Cancel()
		c.Unlock(ctx)
	})
}

func (c *EdgeRuleConvergence) SetResponseState(w http.ResponseWriter, state string) {
	if c == nil {
		return
	}
	w.Header().Set(edgeRulesStateHeader, state)
	w.Header().Set(edgeRulesGenerationHeader, fmt.Sprintf("%d", c.Generation))
}
