package main

// manifest_realtime_endpoints.go — realtime endpoints and consumer groups in
// gregale.yaml (ADR-20261019102657). A deploy reconciles the deployed app's
// slugged endpoints before its realtime triggers, inside the same rollback
// transaction, and returns each slug's endpoint ID so triggers can name an
// endpoint by slug.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// manifestRealtimeEndpointClient is the API surface endpoint reconciliation
// needs.
type manifestRealtimeEndpointClient interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	ListManagedRealtimeEndpoints(ctx context.Context, slug string) ([]api.ManagedRealtimeEndpointResponse, error)
	CreateManagedRealtimeEndpoint(ctx context.Context, slug string, req api.CreateManagedRealtimeEndpointRequest) (api.ManagedRealtimeEndpointResponse, error)
	UpdateManagedRealtimeEndpoint(ctx context.Context, slug, id string, req api.UpdateManagedRealtimeEndpointRequest) (api.ManagedRealtimeEndpointResponse, error)
	DeleteManagedRealtimeEndpoint(ctx context.Context, slug, id string) error
	ListRealtimeConsumerGroups(ctx context.Context, slug, id, channel string) ([]api.RealtimeConsumerGroup, error)
	CreateRealtimeConsumerGroup(ctx context.Context, slug, id, channel string, request api.RealtimeConsumerGroupCreateRequest) (api.RealtimeConsumerGroup, error)
	DeleteRealtimeConsumerGroup(ctx context.Context, slug, id, channel, group string) error
	SetSecretWithScope(ctx context.Context, slug, key, value, scope string) error
	UnsetSecretWithScope(ctx context.Context, slug, key, scope string) error
	RestartAppFresh(ctx context.Context, slug string) (api.AppRestartResponse, error)
}

// reconcileManifestRealtime reconciles slug's realtime endpoints, then its
// realtime triggers with endpoint slugs resolved.
func reconcileManifestRealtime(ctx context.Context, client any, slug string, m *gregalemanifest.Manifest, txn *manifestCronTransaction) error {
	endpoints, err := reconcileManifestRealtimeEndpoints(ctx, client, slug, m, txn)
	if err != nil {
		return err
	}
	return reconcileManifestRealtimeTriggers(ctx, client, slug, m, txn, endpoints)
}

// reconcileManifestRealtimeEndpoints creates and updates slug's declared
// endpoints, disables slugged endpoints the manifest no longer declares, and
// creates missing consumer groups, recording an undo step for each change.
// It never deletes an existing endpoint or group: both hold messages.
func reconcileManifestRealtimeEndpoints(ctx context.Context, client any, slug string, m *gregalemanifest.Manifest, txn *manifestCronTransaction) (map[string]string, error) {
	desired, manage := m.Realtime.EndpointsFor(slug, slug)
	if !manage {
		return nil, nil
	}
	rt, ok := client.(manifestRealtimeEndpointClient)
	if !ok {
		if len(desired) == 0 {
			return nil, nil
		}
		return nil, errors.New("realtime endpoint reconciliation is unavailable")
	}
	existing, err := rt.ListManagedRealtimeEndpoints(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("list realtime endpoints: %w", err)
	}
	current := map[string]api.ManagedRealtimeEndpointResponse{}
	for _, e := range existing {
		if e.Slug != "" {
			current[e.Slug] = e
		}
	}
	r := realtimeEndpointReconcile{client: rt, app: slug, txn: txn}
	ids := map[string]string{}
	for _, want := range desired {
		have, found := current[want.Slug]
		delete(current, want.Slug)
		id, err := r.apply(ctx, want, have, found)
		if err != nil {
			return nil, fmt.Errorf("realtime endpoint %q: %w", want.Slug, err)
		}
		ids[want.Slug] = id
		if err := r.groups(ctx, id, want); err != nil {
			return nil, fmt.Errorf("realtime endpoint %q: %w", want.Slug, err)
		}
	}
	for _, stale := range slices.Sorted(maps.Keys(current)) {
		if err := r.disable(ctx, current[stale]); err != nil {
			return nil, fmt.Errorf("disable realtime endpoint %q: %w", stale, err)
		}
	}
	return ids, nil
}

type realtimeEndpointReconcile struct {
	client manifestRealtimeEndpointClient
	app    string
	txn    *manifestCronTransaction
}

func (r realtimeEndpointReconcile) undo(description string, step func(context.Context) error) {
	r.txn.steps = append(r.txn.steps, manifestCronRollbackStep{description: description, undo: step})
}

// realtimeEndpointPolicy is the declared state compared on every deploy.
type realtimeEndpointPolicy struct {
	authMode, issuer, jwksURL     string
	audience, algorithms, origins []string
	connect, message, disconnect  string
	maxConnections                int
	maxMessageBytes, maxAge       int64
	enabled                       bool
	channelRules                  []api.RealtimeChannelRule
}

func (r realtimeEndpointReconcile) desiredPolicy(ctx context.Context, want gregalemanifest.RealtimeEndpoint) (realtimeEndpointPolicy, error) {
	p := realtimeEndpointPolicy{origins: append([]string{}, want.AllowedOrigins...), maxConnections: want.MaxConnections,
		maxMessageBytes: want.MaxMessageBytes, maxAge: want.MaxConnectionAgeSeconds, enabled: want.Enabled == nil || *want.Enabled,
		connect: api.DefaultRealtimeConnectPath, message: api.DefaultRealtimeMessagePath, disconnect: api.DefaultRealtimeDisconnectPath,
		channelRules: want.APIChannelRules()}
	if c := want.Callbacks; c != nil {
		p.connect, p.message, p.disconnect = orDefault(c.Connect, p.connect), orDefault(c.Message, p.message), orDefault(c.Disconnect, p.disconnect)
	}
	switch {
	case want.OIDC != nil:
		p.authMode, p.issuer, p.jwksURL = api.RealtimeAuthModeOIDCJWT, want.OIDC.Issuer, want.OIDC.JWKSURL
		p.audience, p.algorithms = append([]string{}, want.OIDC.Audience...), append([]string{}, want.OIDC.Algorithms...)
	case want.Auth == gregalemanifest.RealtimeAuthNone:
		p.authMode = api.RealtimeAuthModeNone
	default:
		if !applicationAuthCLI() {
			return p, fmt.Errorf("auth: %s names an Auth app, which is not available", want.Auth)
		}
		issuer, err := resolveAuthIssuer(ctx, r.client, want.Auth)
		if err != nil {
			return p, fmt.Errorf("resolve Auth app %s: %w", want.Auth, err)
		}
		p.authMode, p.issuer, p.jwksURL = api.RealtimeAuthModeOIDCJWT, issuer.Issuer, issuer.JWKSURL
		p.audience, p.algorithms = []string{issuer.Audience}, []string{"ES256"}
		// The Auth app's callbacks keep the server's default paths.
		p.connect, p.message, p.disconnect = api.DefaultRealtimeConnectPath, api.DefaultRealtimeMessagePath, api.DefaultRealtimeDisconnectPath
	}
	return p, nil
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func policyOf(e api.ManagedRealtimeEndpointResponse) realtimeEndpointPolicy {
	return realtimeEndpointPolicy{authMode: e.AuthMode, issuer: e.AuthIssuer, jwksURL: e.AuthJWKSURL,
		audience: append([]string{}, e.AuthAudience...), algorithms: append([]string{}, e.AuthAlgorithms...),
		origins: append([]string{}, e.AllowedOrigins...), connect: e.ConnectPath, message: e.MessagePath, disconnect: e.DisconnectPath,
		maxConnections: e.MaxConnections, maxMessageBytes: e.MaxMessageBytes, maxAge: e.MaxConnectionAgeSeconds, enabled: e.Enabled,
		channelRules: append([]api.RealtimeChannelRule{}, e.ChannelRules...)}
}

func (p realtimeEndpointPolicy) equal(o realtimeEndpointPolicy) bool {
	return p.authMode == o.authMode && p.issuer == o.issuer && p.jwksURL == o.jwksURL &&
		slices.Equal(p.audience, o.audience) && slices.Equal(p.algorithms, o.algorithms) && slices.Equal(p.origins, o.origins) &&
		p.connect == o.connect && p.message == o.message && p.disconnect == o.disconnect &&
		p.maxConnections == o.maxConnections && p.maxMessageBytes == o.maxMessageBytes && p.maxAge == o.maxAge && p.enabled == o.enabled &&
		slices.Equal(p.channelRules, o.channelRules)
}

// update writes every declared field; the server keeps zero limits as its
// own defaults.
func (p realtimeEndpointPolicy) update() api.UpdateManagedRealtimeEndpointRequest {
	mode, issuer, jwks := p.authMode, p.issuer, p.jwksURL
	audience, algorithms, origins := p.audience, p.algorithms, p.origins
	connect, message, disconnect := p.connect, p.message, p.disconnect
	maxConnections, maxMessageBytes, maxAge, enabled := p.maxConnections, p.maxMessageBytes, p.maxAge, p.enabled
	rules := append([]api.RealtimeChannelRule{}, p.channelRules...)
	req := api.UpdateManagedRealtimeEndpointRequest{AuthMode: &mode, AllowedOrigins: &origins, ChannelRules: &rules,
		ConnectPath: &connect, MessagePath: &message, DisconnectPath: &disconnect,
		MaxConnections: &maxConnections, MaxMessageBytes: &maxMessageBytes, MaxConnectionAgeSeconds: &maxAge, Enabled: &enabled}
	if mode == api.RealtimeAuthModeOIDCJWT {
		req.AuthIssuer, req.AuthJWKSURL, req.AuthAudience, req.AuthAlgorithms = &issuer, &jwks, &audience, &algorithms
	}
	return req
}

func (r realtimeEndpointReconcile) apply(ctx context.Context, want gregalemanifest.RealtimeEndpoint, have api.ManagedRealtimeEndpointResponse, found bool) (string, error) {
	policy, err := r.desiredPolicy(ctx, want)
	if err != nil {
		return "", err
	}
	if !found {
		return r.create(ctx, want, policy)
	}
	before := policyOf(have)
	if before.equal(policy) {
		return have.ID, nil
	}
	if _, err := r.client.UpdateManagedRealtimeEndpoint(ctx, r.app, have.ID, policy.update()); err != nil {
		return "", err
	}
	r.undo("restore realtime endpoint "+want.Slug, func(ctx context.Context) error {
		_, err := r.client.UpdateManagedRealtimeEndpoint(ctx, r.app, have.ID, before.update())
		return err
	})
	return have.ID, nil
}

// create makes the endpoint with a fresh callback token sealed where its
// callbacks run: the Auth app for `auth: <app>`, otherwise the deployed app as
// REALTIME_<SLUG>_CALLBACK_TOKEN. That app restarts fresh to load the token.
func (r realtimeEndpointReconcile) create(ctx context.Context, want gregalemanifest.RealtimeEndpoint, p realtimeEndpointPolicy) (string, error) {
	token, err := randomRealtimeToken(32)
	if err != nil {
		return "", err
	}
	owner, secret := r.app, "REALTIME_"+strings.ToUpper(strings.ReplaceAll(want.Slug, "-", "_"))+"_CALLBACK_TOKEN"
	callbackURL := ""
	if want.Auth != "" && want.Auth != gregalemanifest.RealtimeAuthNone {
		suffix, err := randomRealtimeToken(4)
		if err != nil {
			return "", err
		}
		owner, secret = want.Auth, "AUTH_REALTIME_CALLBACK_TOKEN_"+strings.ToUpper(suffix)
		callbackURL = strings.TrimSuffix(p.issuer, "/auth/v1")
	} else {
		app, err := r.client.GetApp(ctx, r.app)
		if err != nil {
			return "", fmt.Errorf("read app %s: %w", r.app, err)
		}
		if callbackURL, err = authExternalOrigin(app); err != nil {
			return "", fmt.Errorf("app %s callback URL: %w", r.app, err)
		}
	}
	if err := r.client.SetSecretWithScope(ctx, owner, secret, token, api.DefaultEnvScope); err != nil {
		return "", fmt.Errorf("seal the callback token in %s: %w", owner, err)
	}
	enabled := p.enabled
	req := api.CreateManagedRealtimeEndpointRequest{Slug: want.Slug, CallbackURL: callbackURL, CallbackAuthToken: token,
		ConnectPath: p.connect, MessagePath: p.message, DisconnectPath: p.disconnect, AuthMode: p.authMode,
		AllowedOrigins: p.origins, MaxConnections: p.maxConnections, MaxMessageBytes: p.maxMessageBytes,
		MaxConnectionAgeSeconds: p.maxAge, Enabled: &enabled, ChannelRules: p.channelRules}
	if p.authMode == api.RealtimeAuthModeOIDCJWT {
		req.AuthIssuer, req.AuthJWKSURL, req.AuthAudience, req.AuthAlgorithms = p.issuer, p.jwksURL, p.audience, p.algorithms
	}
	created, err := r.client.CreateManagedRealtimeEndpoint(ctx, r.app, req)
	if err != nil {
		_ = r.client.UnsetSecretWithScope(ctx, owner, secret, api.DefaultEnvScope)
		return "", err
	}
	r.undo("remove realtime endpoint "+want.Slug, func(ctx context.Context) error {
		err := r.client.DeleteManagedRealtimeEndpoint(ctx, r.app, created.ID)
		return errors.Join(err, r.client.UnsetSecretWithScope(ctx, owner, secret, api.DefaultEnvScope))
	})
	if _, err := r.client.RestartAppFresh(ctx, owner); err != nil {
		PrintWarn(osStderr, "realtime endpoint %s exists, but restarting %s failed (%v); run `gregale app %s restart --fresh` before clients connect", want.Slug, owner, err, owner)
	}
	return created.ID, nil
}

func (r realtimeEndpointReconcile) disable(ctx context.Context, have api.ManagedRealtimeEndpointResponse) error {
	if !have.Enabled {
		return nil
	}
	off, on := false, true
	if _, err := r.client.UpdateManagedRealtimeEndpoint(ctx, r.app, have.ID, api.UpdateManagedRealtimeEndpointRequest{Enabled: &off}); err != nil {
		return err
	}
	PrintWarn(osStderr, "realtime endpoint %s is no longer declared and was disabled; delete it with `gregale realtime delete %s %s`", have.Slug, r.app, have.ID)
	r.undo("re-enable realtime endpoint "+have.Slug, func(ctx context.Context) error {
		_, err := r.client.UpdateManagedRealtimeEndpoint(ctx, r.app, have.ID, api.UpdateManagedRealtimeEndpointRequest{Enabled: &on})
		return err
	})
	return nil
}

// groups creates the declared consumer groups that do not exist yet. Their
// settings are not reconciled after creation: a group's lease and attempts
// change through its own API, and a group is never deleted by a deploy.
func (r realtimeEndpointReconcile) groups(ctx context.Context, id string, want gregalemanifest.RealtimeEndpoint) error {
	byChannel := map[string]map[string]bool{}
	for _, g := range want.ConsumerGroups {
		have, ok := byChannel[g.Channel]
		if !ok {
			listed, err := r.client.ListRealtimeConsumerGroups(ctx, r.app, id, g.Channel)
			if err != nil {
				return fmt.Errorf("list consumer groups on %s: %w", g.Channel, err)
			}
			have = map[string]bool{}
			for _, existing := range listed {
				have[existing.Name] = true
			}
			byChannel[g.Channel] = have
		}
		if have[g.Name] {
			continue
		}
		if _, err := r.client.CreateRealtimeConsumerGroup(ctx, r.app, id, g.Channel, api.RealtimeConsumerGroupCreateRequest{
			Name: g.Name, AfterSequence: "0", LeaseSeconds: g.LeaseSeconds, MaxAttempts: g.MaxAttempts,
		}); err != nil {
			return fmt.Errorf("create consumer group %s on %s: %w", g.Name, g.Channel, err)
		}
		have[g.Name] = true
		channel, name := g.Channel, g.Name
		r.undo("remove consumer group "+name, func(ctx context.Context) error {
			return r.client.DeleteRealtimeConsumerGroup(ctx, r.app, id, channel, name)
		})
	}
	return nil
}

func randomRealtimeToken(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate a callback token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
