package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const resourceSubscriptionTTL = 2 * time.Minute
const resourceSubscriptionPoll = 5 * time.Second
const maxListenResources = 4
const maxResourceSubscriptions = 16
const maxResourceListenStreams = 8

type resourceListenKey struct{}
type resourceListenGroup struct {
	session       *mcp.ServerSession
	ctx           context.Context
	cancel        context.CancelFunc
	uris          []string
	ready         chan struct{}
	subscriptions map[string]*resourceSubscription
}
type resourceSubscription struct {
	sendMu                          sync.Mutex
	args                            appArgs
	view                            string
	appID, projectID, environmentID string
	digest                          [32]byte
}
type resourceSubscriptions struct {
	mu      sync.Mutex
	groups  map[*resourceListenGroup]struct{}
	poll    time.Duration
	ttl     time.Duration
	stopped bool
}

func registerResourceSubscriptions(s *mcp.Server, m *management) {
	m.subscriptions = &resourceSubscriptions{groups: map[*resourceListenGroup]struct{}{}, poll: resourceSubscriptionPoll, ttl: resourceSubscriptionTTL}
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "subscriptions/listen" {
				return m.listenResources(ctx, next, method, req)
			}
			result, err := next(ctx, method, req)
			// Do not advertise a legacy subscribe contract that the stateless bridge
			// cannot retain. Ordinary resource reads remain available on legacy clients.
			if method == "initialize" && err == nil {
				if r, ok := result.(*mcp.InitializeResult); ok && r.ProtocolVersion < "2026-07-28" && r.Capabilities != nil && r.Capabilities.Resources != nil {
					copyResult := *r
					caps := *r.Capabilities
					resources := *caps.Resources
					resources.Subscribe = false
					caps.Resources = &resources
					copyResult.Capabilities = &caps
					result = &copyResult
				}
			}
			return result, err
		}
	})
	s.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "notifications/subscriptions/acknowledged" {
				if group, _ := ctx.Value(resourceListenKey{}).(*resourceListenGroup); group != nil {
					if params, ok := req.GetParams().(*mcp.SubscriptionsAcknowledgedParams); ok {
						if params.Meta == nil {
							params.Meta = mcp.Meta{}
						}
						expiry, _ := group.ctx.Deadline()
						params.Meta["gregale_subscription_expires_at"] = expiry.UTC().Format(time.RFC3339Nano)
					}
				}
				result, err := next(ctx, method, req)
				if err == nil {
					if group, _ := ctx.Value(resourceListenKey{}).(*resourceListenGroup); group != nil {
						close(group.ready)
					}
				}
				return result, err
			}
			if method == "notifications/resources/updated" {
				return m.sendResourceUpdate(next, method, req) //nolint:contextcheck // SDK broadcast detaches context; recipient listen owns cancellation and expiry.
			}
			return next(ctx, method, req)
		}
	})
}

func (m *management) listenResources(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.SubscriptionsListenParams)
	session, sessionOK := req.GetSession().(*mcp.ServerSession)
	if !ok || !sessionOK || params.Notifications == nil {
		return nil, m.contextError(invalid("invalid_subscription", "notifications are required"))
	}
	initialized := session.InitializeParams()
	if initialized == nil || initialized.ProtocolVersion < "2026-07-28" {
		return nil, m.contextError(invalid("unsupported_subscription", "subscriptions/listen requires MCP 2026-07-28"))
	}
	uris := params.Notifications.ResourceSubscriptions
	if len(uris) > maxListenResources {
		return nil, m.contextError(invalid("subscription_capacity", "at most four resource URIs may be listened to"))
	}
	seen := map[string]bool{}
	for _, uri := range uris {
		args, view, err := parseContextURI(uri)
		if err != nil {
			return nil, m.contextError(err)
		}
		if args.App == "" || (view != "release" && view != "health" && view != "budgets" && view != "regressions") || seen[uri] {
			return nil, m.contextError(invalid("invalid_subscription", "subscribe to distinct app release, health, budgets or app-wide regressions resources"))
		}
		seen[uri] = true
	}
	manager := m.subscriptions
	manager.mu.Lock()
	if manager.stopped {
		manager.mu.Unlock()
		return nil, m.contextError(invalid("subscription_closed", "connection subscriptions are closed"))
	}
	count, sessionCount, slots := len(manager.groups), 0, 0
	for g := range manager.groups {
		slots += len(g.uris)
		if g.session == session {
			sessionCount++
			for _, uri := range g.uris {
				if seen[uri] {
					manager.mu.Unlock()
					return nil, m.contextError(invalid("duplicate_subscription", "resource already has an active listen in this session"))
				}
			}
		}
	}
	if count >= maxResourceListenStreams || sessionCount >= maxListenResources || slots+len(uris) > maxResourceSubscriptions {
		manager.mu.Unlock()
		return nil, m.contextError(invalid("subscription_capacity", "connection subscription capacity reached"))
	}
	deadline := time.Now().Add(manager.ttl)
	if expiry, ok := ctx.Value(authorizationLeaseExpiresKey{}).(time.Time); ok && expiry.Before(deadline) {
		deadline = expiry
	}
	lifeCtx, cancel := context.WithDeadline(ctx, deadline)
	group := &resourceListenGroup{session: session, ctx: lifeCtx, cancel: cancel, uris: append([]string(nil), uris...), ready: make(chan struct{}), subscriptions: map[string]*resourceSubscription{}}
	groupCtx := context.WithValue(lifeCtx, resourceListenKey{}, group)
	group.ctx = groupCtx
	manager.groups[group] = struct{}{}
	m.opts.Telemetry.change("listen_streams", 1)
	m.opts.Telemetry.change("resource_slots", float64(len(uris)))
	manager.mu.Unlock()
	defer func() {
		cancel()
		manager.mu.Lock()
		delete(manager.groups, group)
		m.opts.Telemetry.change("listen_streams", -1)
		m.opts.Telemetry.change("resource_slots", -float64(len(uris)))
		manager.mu.Unlock()
	}()
	// The SDK owns registration, acknowledgement and unregistration. Keep the
	// group until next returns, after SDK cleanup, so a stale cleanup cannot
	// remove a replacement listen for the same session/URI.
	return next(groupCtx, method, req)
}

func (m *management) subscribeResource(ctx context.Context, req *mcp.SubscribeRequest) error {
	group, _ := ctx.Value(resourceListenKey{}).(*resourceListenGroup)
	if group == nil {
		return m.contextError(invalid("unsupported_subscription", "resource subscriptions require MCP 2026-07-28 subscriptions/listen"))
	}
	args, view, err := parseContextURI(req.Params.URI)
	if err != nil {
		return m.contextError(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, min(m.opts.Timeout, 5*time.Second))
	defer cancel()
	pin, digest, err := m.subscriptionSnapshot(readCtx, args, view, nil)
	if err != nil {
		return m.contextError(err)
	}
	pin.digest = digest
	m.subscriptions.mu.Lock()
	group.subscriptions[req.Params.URI] = pin
	first := len(group.subscriptions) == 1
	m.subscriptions.mu.Unlock()
	if first {
		workerCtx, finish, err := m.lifecycle.enter(ctx, true)
		if err != nil {
			return m.contextError(err)
		}
		go func(ctx context.Context) { defer finish(); m.pollResourceGroup(ctx, group) }(workerCtx)
	}
	return nil
}
func (m *management) unsubscribeResource(ctx context.Context, req *mcp.UnsubscribeRequest) error {
	if group, _ := ctx.Value(resourceListenKey{}).(*resourceListenGroup); group != nil {
		group.cancel()
	}
	return nil
}

func (m *management) subscriptionSnapshot(ctx context.Context, args appArgs, view string, pin *resourceSubscription) (subscription *resourceSubscription, digest [32]byte, err error) {
	start := time.Now()
	defer func() { m.opts.Telemetry.observe("subscription/read", "none", telemetryOutcome(ctx, nil, err), start) }()
	var zero [32]byte
	release, err := m.admission.acquire(ctx, false)
	if err != nil {
		return nil, zero, err
	}
	defer release()
	if !m.opts.planStore.available() {
		return nil, zero, invalid("state_unavailable", "bridge state is unavailable")
	}
	if err := m.authorizeArgs(ctx, args); err != nil {
		return nil, zero, err
	}
	if view == "regressions" {
		return m.regressionSubscriptionSnapshot(ctx, args, pin)
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, zero, err
	}
	if app.ID == "" || app.Slug != args.App || !m.appIDAllowed(app.ID) {
		return nil, zero, invalid("app_access_denied", "resource app identity is outside consent")
	}
	if pin != nil && (pin.appID != app.ID || pin.projectID != env.ProjectID || pin.environmentID != env.ID) {
		return nil, zero, invalid("identity_changed", "resource identity changed; inspect before subscribing again")
	}
	value, err := m.contextResourceValue(ctx, args, view)
	if err != nil {
		return nil, zero, err
	}
	fresh, currentEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, zero, err
	}
	if fresh.ID != app.ID || currentEnv.ID != env.ID || currentEnv.ProjectID != env.ProjectID {
		return nil, zero, invalid("identity_changed", "resource identity changed during inspection")
	}
	if err := m.authorizeArgs(ctx, args); err != nil {
		return nil, zero, err
	}
	safe, err := m.safeOutput(value)
	if err != nil {
		return nil, zero, err
	}
	digest, err = resourceSemanticDigest(safe.(json.RawMessage), time.Now())
	return &resourceSubscription{args: args, view: view, appID: app.ID, projectID: env.ProjectID, environmentID: env.ID}, digest, err
}

func (m *management) pollResourceGroup(ctx context.Context, group *resourceListenGroup) {
	select {
	case <-group.ready:
	case <-ctx.Done():
		return
	}
	timer := time.NewTimer(m.subscriptions.poll)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		for _, uri := range group.uris {
			m.subscriptions.mu.Lock()
			pin := group.subscriptions[uri]
			m.subscriptions.mu.Unlock()
			if pin == nil {
				continue
			}
			readCtx, cancel := context.WithTimeout(ctx, min(m.opts.Timeout, 5*time.Second))
			_, digest, err := m.subscriptionSnapshot(readCtx, pin.args, pin.view, pin)
			cancel()
			if requestBusy(err) {
				continue
			}
			if err != nil {
				group.cancel()
				return
			}
			m.subscriptions.mu.Lock()
			unchanged := digest == pin.digest
			m.subscriptions.mu.Unlock()
			if unchanged {
				continue
			}
			// The send middleware gates each individual recipient before the SDK
			// forwards this URI-only invalidation. Never embed resource contents.
			if err := m.resourceServer.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: uri}); err != nil {
				group.cancel()
				return
			}
		}
		timer.Reset(m.subscriptions.poll)
	}
}

func (m *management) sendResourceUpdate(next mcp.MethodHandler, method string, req mcp.Request) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.ResourceUpdatedNotificationParams)
	session, sessionOK := req.GetSession().(*mcp.ServerSession)
	if !ok || !sessionOK {
		return nil, nil
	}
	var group *resourceListenGroup
	var pin *resourceSubscription
	m.subscriptions.mu.Lock()
	for g := range m.subscriptions.groups {
		if g.session == session {
			if p := g.subscriptions[params.URI]; p != nil {
				group, pin = g, p
				break
			}
		}
	}
	m.subscriptions.mu.Unlock()
	if group == nil || group.ctx.Err() != nil {
		return nil, nil
	}
	pin.sendMu.Lock()
	defer pin.sendMu.Unlock()
	// ResourceUpdated uses SDK subscription metadata to route to this listen's
	// stream. Derive the send from its live request instead of the SDK's detached
	// broadcast context, keeping cancellation, expiry and recipient consent.
	readCtx, cancel := context.WithTimeout(group.ctx, min(m.opts.Timeout, 5*time.Second))
	defer cancel()
	_, digest, err := m.subscriptionSnapshot(readCtx, pin.args, pin.view, pin)
	if requestBusy(err) {
		return nil, nil
	}
	if err != nil {
		group.cancel()
		return nil, nil
	}
	m.subscriptions.mu.Lock()
	unchanged := digest == pin.digest
	m.subscriptions.mu.Unlock()
	if unchanged {
		return nil, nil
	}
	sendCtx, stop := context.WithTimeout(group.ctx, 200*time.Millisecond)
	defer stop()
	result, err := next(sendCtx, method, req)
	if err != nil {
		group.cancel()
	} else {
		m.subscriptions.mu.Lock()
		pin.digest = digest
		if m.opts.Telemetry != nil {
			m.opts.Telemetry.updates.Inc()
		}
		m.subscriptions.mu.Unlock()
	}
	return result, err
}
func (m *management) stopResourceSubscriptions() {
	if m == nil || m.subscriptions == nil {
		return
	}
	m.subscriptions.mu.Lock()
	m.subscriptions.stopped = true
	var cancel []context.CancelFunc
	for g := range m.subscriptions.groups {
		cancel = append(cancel, g.cancel)
	}
	m.subscriptions.mu.Unlock()
	for _, stop := range cancel {
		stop()
	}
}

func resourceSemanticDigest(data json.RawMessage, now time.Time) ([32]byte, error) {
	var value any
	if !json.Valid(data) {
		return [32]byte{}, invalid("invalid_response", "invalid resource snapshot JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // Preserve changes to integer counters beyond float64 precision.
	if err := decoder.Decode(&value); err != nil {
		return [32]byte{}, err
	}
	if root, ok := value.(map[string]any); ok {
		// Only the snapshot's collection clock is volatile. Detector and triage
		// timestamps remain meaningful, including timestamps in nested rows.
		delete(root, "snapshot_observed_at")
	}
	stripResourceObservationTimes(value, now)
	encoded, err := json.Marshal(value)
	return sha256.Sum256(encoded), err
}
func stripResourceObservationTimes(value any, now time.Time) {
	switch v := value.(type) {
	case map[string]any:
		if evaluated, ok := v["evaluated_at"].(string); ok {
			number, _ := v["valid_for_seconds"].(json.Number)
			ttl, ttlErr := number.Float64()
			at, err := time.Parse(time.RFC3339Nano, evaluated)
			v["evidence_fresh"] = err == nil && ttlErr == nil && ttl > 0 && !at.After(now) && now.Sub(at) <= time.Duration(min(ttl, api.AppHealthEvidenceMaxAge.Seconds())*float64(time.Second))
		}
		for _, key := range []string{"checked_at", "evaluated_at", "metrics_as_of", "window_start", "window_end", "observation_anchor", "updated_at", "start", "end"} {
			delete(v, key)
		}
		for _, child := range v {
			stripResourceObservationTimes(child, now)
		}
	case []any:
		for _, child := range v {
			stripResourceObservationTimes(child, now)
		}
	}
}
