package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

const (
	edgeRuleConvergenceTimeout = 3 * time.Second
	edgeRuleNotificationRetry  = 250 * time.Millisecond
	edgeRulesStateHeader       = "X-Faas-Edge-Rules-State"
	edgeRulesGenerationHeader  = "X-Faas-Edge-Rules-Generation"
)

type edgeRuleGenerationStore interface {
	NextEdgeRuleGeneration(context.Context) (int64, error)
}

// edgeRuleMutationLocker is optional so MemStore-backed unit tests and
// legacy single-box adapters retain the process-local fallback. Production
// PgStore implements it with a session-scoped PostgreSQL advisory lock,
// closing the cross-apid race where two mutations for one app could publish
// generations out of order.
type edgeRuleMutationLocker interface {
	AcquireEdgeRuleMutationLock(context.Context, string) (func(context.Context), error)
}

type edgeRuleConvergence struct {
	notif      Notifier
	events     <-chan db.Notification
	cancel     func()
	unlock     func(context.Context)
	done       sync.Once
	generation int64
	appID      string
	ruleID     string
	operation  string
	hosts      []string
	expected   map[string]struct{}
}

var edgeRuleMutationMu sync.Mutex

func (s *server) prepareEdgeRuleMutation(ctx context.Context, appID, ruleID, operation string, hosts ...string) (*edgeRuleConvergence, error) {
	edgeRuleMutationMu.Lock()
	unlock := func(context.Context) { edgeRuleMutationMu.Unlock() }
	if locker, ok := s.store.(edgeRuleMutationLocker); ok {
		release, err := locker.AcquireEdgeRuleMutationLock(ctx, appID)
		if err != nil {
			edgeRuleMutationMu.Unlock()
			return nil, err
		}
		unlock = func(ctx context.Context) {
			release(ctx)
			edgeRuleMutationMu.Unlock()
		}
	}
	allocator, ok := s.store.(edgeRuleGenerationStore)
	if !ok {
		unlock(ctx)
		return nil, errors.New("edge-rule generation store is unavailable")
	}
	generation, err := allocator.NextEdgeRuleGeneration(ctx)
	if err != nil {
		unlock(ctx)
		return nil, err
	}
	conv := &edgeRuleConvergence{
		notif: s.notif, generation: generation, appID: appID, ruleID: ruleID,
		operation: operation, hosts: canonicalEdgeRuleHosts(hosts), expected: map[string]struct{}{},
		cancel: func() {}, unlock: unlock,
	}
	nodes, err := s.store.ListComputeNodes(ctx, false)
	if err != nil {
		conv.close(ctx)
		return nil, fmt.Errorf("list serving gateways: %w", err)
	}
	for _, node := range nodes {
		role := ""
		if node.Role != nil {
			role = strings.TrimSpace(*node.Role)
		}
		if (role != "compute-only" && role != "compute-node") || node.GatewayTargetURL == nil || strings.TrimSpace(*node.GatewayTargetURL) == "" {
			continue
		}
		conv.expected[node.Name] = struct{}{}
	}
	// A fleet mutation with no serving gateway authority is unsafe: it
	// could report success while an unregistered edge continues serving stale
	// policy. Unnamed legacy single-box installs and local development
	// intentionally have no compute registry.
	if s.edgeRuleFleetRequired && len(conv.expected) == 0 {
		conv.close(ctx)
		return nil, errors.New("no active serving gateways are registered")
	}
	if len(conv.expected) > 0 {
		// Keep the acknowledgement listener alive long enough to publish the
		// post-commit apply even if the HTTP client disconnects immediately
		// after persistence. close still tears it down on every normal path.
		listenCtx, listenCancel := context.WithCancel(context.WithoutCancel(ctx))
		var subscriptionCancel func()
		conv.events, subscriptionCancel, err = s.notif.Subscribe(listenCtx, []string{db.NotifyEdgeRuleAck})
		if err != nil {
			listenCancel()
			conv.close(ctx)
			return nil, fmt.Errorf("subscribe edge-rule acknowledgements: %w", err)
		}
		conv.cancel = func() {
			subscriptionCancel()
			listenCancel()
		}
	}
	if err := conv.notifyAndWait(ctx, "prepare"); err != nil {
		conv.abort(ctx)
		return nil, err
	}
	return conv, nil
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

func (c *edgeRuleConvergence) payload(phase string) (string, error) {
	body, err := json.Marshal(db.EdgeRuleChangedPayload{
		AppID: c.appID, RuleID: c.ruleID, Operation: c.operation, Phase: phase,
		Generation: c.generation, MatchHosts: c.hosts,
	})
	return string(body), err
}

func (c *edgeRuleConvergence) notifyAndWait(ctx context.Context, phase string) error {
	payload, err := c.payload(phase)
	if err != nil {
		return err
	}
	if err := c.notif.Notify(ctx, db.NotifyEdgeRuleChanged, payload); err != nil {
		return fmt.Errorf("publish edge-rule %s generation %d: %w", phase, c.generation, err)
	}
	if len(c.expected) == 0 {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, edgeRuleConvergenceTimeout)
	defer cancel()
	retry := time.NewTicker(edgeRuleNotificationRetry)
	defer retry.Stop()
	seen := make(map[string]struct{}, len(c.expected))
	for len(seen) < len(c.expected) {
		select {
		case <-waitCtx.Done():
			missing := make([]string, 0, len(c.expected)-len(seen))
			for node := range c.expected {
				if _, ok := seen[node]; !ok {
					missing = append(missing, node)
				}
			}
			sort.Strings(missing)
			return fmt.Errorf("edge-rule generation %d phase %s missing gateway acknowledgements: %s", c.generation, phase, strings.Join(missing, ", "))
		case event, ok := <-c.events:
			if !ok {
				return errors.New("edge-rule acknowledgement subscription closed")
			}
			ack, parseErr := db.ParseEdgeRuleAckPayload(event.Payload)
			if parseErr != nil || ack.Generation != c.generation || ack.Phase != phase {
				continue
			}
			if _, expected := c.expected[ack.Node]; expected {
				seen[ack.Node] = struct{}{}
			}
		case <-retry.C:
			// PG_NOTIFY is intentionally ephemeral. Re-publishing the same
			// idempotent phase lets a listener that briefly reconnects join the
			// barrier instead of leaving one replica stale until cache expiry.
			_ = c.notif.Notify(waitCtx, db.NotifyEdgeRuleChanged, payload)
		}
	}
	return nil
}

func (c *edgeRuleConvergence) apply(ctx context.Context, ruleID string) error {
	if c == nil {
		return errors.New("nil edge-rule convergence")
	}
	defer c.close(ctx)
	c.ruleID = ruleID
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), edgeRuleConvergenceTimeout+time.Second)
	defer cancel()
	return c.notifyAndWait(applyCtx, "apply")
}

func (c *edgeRuleConvergence) abort(ctx context.Context) {
	if c == nil {
		return
	}
	defer c.close(ctx)
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	payload, err := c.payload("abort")
	if err == nil {
		_ = c.notif.Notify(abortCtx, db.NotifyEdgeRuleChanged, payload)
	}
}

func (c *edgeRuleConvergence) close(ctx context.Context) {
	if c == nil {
		return
	}
	c.done.Do(func() {
		c.cancel()
		c.unlock(ctx)
	})
}

func (c *edgeRuleConvergence) setResponseState(w http.ResponseWriter, state string) {
	if c == nil {
		return
	}
	w.Header().Set(edgeRulesStateHeader, state)
	w.Header().Set(edgeRulesGenerationHeader, fmt.Sprintf("%d", c.generation))
}
