// adr: 375
package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicRouteGraphsKey struct{}

// This graph survives replacement of the gateway matcher view with the full
// owner policy. Dispatch verification compares against the original choice.
type publicRouteGraphs struct {
	owner      *gatewaydEdgeRules
	generation uint64
	hosts      map[string]*gateway.HostEntry
	claims     map[string]gateway.PublicRouteSourcePolicy
}

// Configure once during wiring, before publishing the matcher to handlers.
func (g *gatewaydEdgeRules) withPublicHostRouter(router pgRouter) *gatewaydEdgeRules {
	g.publicHostSource = func(host string) *gateway.PublicAppPolicySource { return router.policySource(host, "") }
	return g
}

func (g *gatewaydEdgeRules) RequiresOwnerPolicySnapshot() bool {
	if g == nil {
		return false
	}
	_, ok := g.store.(state.PublicHostPolicySnapshotStore)
	return ok
}

func (g *gatewaydEdgeRules) pinPublicRouteGraph(ctx context.Context, host string) (context.Context, error) {
	if len(host) > api.TrafficPolicyMaxHostnameBytes {
		return nil, errors.New("public policy hostname exceeds the DNS name bound")
	}
	if g.publicHostSource == nil {
		return nil, errors.New("public route ownership resolver is unavailable")
	}
	generation := g.cache.Generation()
	prior, pinned := ctx.Value(publicRouteGraphsKey{}).(publicRouteGraphs)
	if g.Converging(host) || pinned && (prior.owner != g || prior.generation != generation) {
		return nil, errors.New("public route graph changed during resolution")
	}
	bounded, cancel := context.WithTimeout(ctx, api.TrafficPublicHostReadTimeout)
	defer cancel()
	var entry *gateway.HostEntry
	var claim gateway.PublicRouteSourcePolicy
	err := g.store.(state.PublicHostPolicySnapshotStore).WithPublicHostPolicySnapshot(bounded, func(reader state.PublicHostPolicyReader) error {
		app, found, err := resolvePublicPolicyIdentity(bounded, reader.NewProjectionReader(), g.publicHostSource(host))
		if err != nil {
			return err
		}
		claim = gateway.PublicRouteSourcePolicy{Source: app.PublicPolicySource, AppID: app.ID, AccountID: app.AccountID, Found: found}
		if err := validatePublicRouteClaim(host, claim); err != nil {
			return err
		}
		var rules []state.EdgeRule
		if claim.Source.CanSubstitute {
			rules, err = reader.PublicHostEdgeRules(bounded, host, claim.AccountID, true)
			if err != nil {
				return err
			}
		}
		entry, err = g.compileHostRules(bounded, host, rules)
		return err
	})
	if err == nil {
		err = bounded.Err()
	}
	if err != nil {
		return nil, err
	}
	if generation != g.cache.Generation() || g.Converging(host) {
		return nil, errors.New("public route graph changed during resolution")
	}
	if err := entry.SealPolicy(); err != nil {
		return nil, err
	}
	hosts := make(map[string]*gateway.HostEntry, len(prior.hosts)+1)
	claims := make(map[string]gateway.PublicRouteSourcePolicy, len(prior.claims)+1)
	for key, value := range prior.hosts {
		hosts[key] = value
		claims[key] = prior.claims[key]
	}
	if previous, exists := claims[host]; exists && !samePublicRouteClaim(previous, claim) {
		return nil, errors.New("public route ownership changed during resolution")
	}
	hosts[host] = entry
	claims[host] = claim
	ctx = context.WithValue(ctx, publicRouteGraphsKey{}, publicRouteGraphs{owner: g, generation: generation, hosts: hosts, claims: claims})
	return gateway.WithPinnedHostPolicy(ctx, host, entry)
}

func resolvePublicCompiledPolicy(ctx context.Context, reader state.PublicHostPolicyReader, app *gateway.App, found bool, source *gateway.PublicAppPolicySource) error {
	graphs, pinned := ctx.Value(publicRouteGraphsKey{}).(publicRouteGraphs)
	if !pinned {
		return nil
	}
	hosts := make([]string, 0, len(graphs.hosts))
	for host := range graphs.hosts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	if err := verifyPublicRouteClaims(ctx, reader, graphs, hosts, app, found, source); err != nil {
		return err
	}
	if !found {
		if !source.CanSubstitute {
			return nil
		}
		for _, host := range hosts {
			claim := graphs.claims[host]
			if !claim.Source.CanSubstitute {
				continue
			}
			rules, err := reader.PublicHostEdgeRules(ctx, host, claim.AccountID, true)
			if err != nil {
				return err
			}
			entry, err := graphs.owner.compileHostRules(ctx, host, rules)
			if err != nil {
				return err
			}
			if err := verifyPublicRouteGraph(graphs.hosts[host], entry, claim.AccountID); err != nil {
				return err
			}
		}
		return nil
	}
	if app.AccountID == "" {
		return errors.New("public edge policy owner unavailable")
	}
	app.PublicCompiledPolicies = make(map[string]*gateway.HostEntry, len(hosts))
	for _, host := range hosts {
		entry, err := graphs.owner.compilePublicOwnerPolicy(ctx, reader, host, app.AccountID, graphs.claims[host].Source)
		if err != nil {
			return err
		}
		if source.CanSubstitute || source.Slug != "" {
			if err := verifyPublicRouteGraph(graphs.hosts[host], entry, app.AccountID); err != nil {
				return err
			}
		}
		app.PublicCompiledPolicies[host] = entry
	}
	return nil
}

func validatePublicRouteClaim(host string, claim gateway.PublicRouteSourcePolicy) error {
	if claim.Source == nil || claim.Source.Host != host || claim.Source.Slug != "" || claim.Source.Revision == "" ||
		claim.Found && (claim.AppID == "" || claim.AccountID == "") ||
		!claim.Found && (claim.AppID != "" || claim.AccountID != "") {
		return errors.New("public route ownership claim is invalid")
	}
	return nil
}

func samePublicRouteNamespace(a, b *gateway.PublicAppPolicySource) bool {
	return a != nil && b != nil && a.AppsSuffix == b.AppsSuffix && a.DeploySuffix == b.DeploySuffix && a.TenantSurfaces == b.TenantSurfaces
}

func samePublicRouteClaim(a, b gateway.PublicRouteSourcePolicy) bool {
	return a.Found == b.Found && a.AppID == b.AppID && a.AccountID == b.AccountID &&
		samePublicRouteNamespace(a.Source, b.Source) && a.Source.Host == b.Source.Host &&
		a.Source.Revision == b.Source.Revision && a.Source.CanSubstitute == b.Source.CanSubstitute
}

func verifyPublicRouteClaims(ctx context.Context, reader state.PublicHostPolicyReader, graphs publicRouteGraphs, hosts []string, app *gateway.App, found bool, source *gateway.PublicAppPolicySource) error {
	if graphs.owner == nil || len(graphs.claims) != len(hosts) {
		return errors.New("public route ownership claims are unavailable")
	}
	for _, host := range hosts {
		claim := graphs.claims[host]
		if err := validatePublicRouteClaim(host, claim); err != nil {
			return err
		}
		if graphs.owner.publicHostSource == nil || !samePublicRouteNamespace(claim.Source, graphs.owner.publicHostSource(host)) {
			return errors.New("public route namespace changed before owner admission")
		}
		resolved, exists, err := resolvePublicPolicyIdentity(ctx, reader.NewProjectionReader(), claim.Source)
		if err != nil {
			return err
		}
		fresh := gateway.PublicRouteSourcePolicy{Source: resolved.PublicPolicySource, AppID: resolved.ID, AccountID: resolved.AccountID, Found: exists}
		if !samePublicRouteClaim(claim, fresh) || found && claim.Found && claim.AccountID != app.AccountID {
			return errors.New("public route ownership changed before owner admission")
		}
		if source.Host == host && (!samePublicRouteNamespace(claim.Source, source) || claim.Found != found ||
			claim.AppID != app.ID || claim.AccountID != app.AccountID || claim.Source.CanSubstitute != source.CanSubstitute) {
			return errors.New("public route namespace changed before owner admission")
		}
	}
	return nil
}

func verifyPublicRouteGraph(prior, fresh *gateway.HostEntry, account string) error {
	before, err := gateway.PublicRouteGraphRevision(prior, account)
	if err != nil {
		return err
	}
	after, err := gateway.PublicRouteGraphRevision(fresh, account)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("public route graph changed before owner admission")
	}
	return nil
}

// All reads happen before the compiled cache lookup, inside the app resolver's
// transaction. Cache entries can save compilation, never replace a fresh read.
func (g *gatewaydEdgeRules) compilePublicOwnerPolicy(ctx context.Context, reader state.PublicHostPolicyReader, host, account string, source *gateway.PublicAppPolicySource) (*gateway.HostEntry, error) {
	rules, err := reader.PublicHostEdgeRules(ctx, host, account, false)
	if err != nil {
		return nil, err
	}
	bundle := &publicEdgeCompileStore{PublicHostPolicyReader: reader, rules: rules, presets: make(map[string]state.CorsPreset)}
	compiler := &gatewaydEdgeRules{store: bundle, validate: g.validate, metrics: g.metrics}
	rules, err = publicEnvironmentEdgeRules(ctx, reader, compiler, host, source, rules)
	if err != nil {
		return nil, err
	}
	bundle.rules = rules
	if len(rules) > api.TrafficPolicyMaxHostRules {
		return nil, errors.New("public compiled policy exceeds the rule limit")
	}
	if err := bundle.readPresets(ctx); err != nil {
		return nil, err
	}
	revision := reader.PublicHostPolicyRevision()
	key := "public-owner:" + host + "\x00" + account
	if entry, hit := g.cache.GetHost(key); hit && entry.PublicSourceRevision == revision {
		entry.Host = host // The cache key includes the owner; the sealed policy names the actual host.
		return entry, nil
	}
	generation := g.cache.Generation()
	entry, err := compiler.compileHostRules(ctx, host, rules)
	if err != nil {
		return nil, err
	}
	entry.PublicSourceRevision = revision
	if err := entry.SealPolicy(); err != nil {
		return nil, err
	}
	g.cache.PutIfGeneration(key, entry, generation)
	return entry, nil
}

// Resolve the actual binding, including configured namespace precedence,
// before selecting an environment. A shadowed domain cannot supply an overlay.
func publicEnvironmentEdgeRules(ctx context.Context, reader state.PublicHostPolicyReader, compiler *gatewaydEdgeRules, host string, source *gateway.PublicAppPolicySource, rules []state.EdgeRule) ([]state.EdgeRule, error) {
	if len(host) > api.TrafficPolicyMaxHostnameBytes || source == nil || source.Host != host {
		return nil, errors.New("public environment policy source is unavailable")
	}
	app, found, err := resolvePublicPolicyIdentity(ctx, reader.NewProjectionReader(), source)
	if err != nil {
		return nil, err
	}
	if !found || app.PublicEnvironmentID == "" {
		return rules, nil
	}
	return compiler.environmentPolicyRules(ctx, host, app.PublicEnvironmentID, app.ID, rules)
}

type publicEdgeCompileStore struct {
	state.PublicHostPolicyReader
	rules   []state.EdgeRule
	presets map[string]state.CorsPreset
}

func (s *publicEdgeCompileStore) MatchEdgeRulesForHost(context.Context, string) ([]state.EdgeRule, error) {
	return s.rules, nil
}

func (s *publicEdgeCompileStore) GetCorsPresetByID(_ context.Context, account, id string) (state.CorsPreset, error) {
	preset, found := s.presets[account+"\x00"+id]
	if !found {
		return state.CorsPreset{}, state.ErrNotFound
	}
	return preset, nil
}

func (s *publicEdgeCompileStore) readPresets(ctx context.Context) error {
	encoded, err := json.Marshal(s.rules)
	if err != nil {
		return err
	}
	bytes := len(encoded)
	if bytes > api.TrafficPolicyMaxHostBytes {
		return errors.New("public compiled policy exceeds the projection limit")
	}
	keys := make(map[string]struct{ account, id string })
	for _, rule := range s.rules {
		if rule.Enabled && rule.Kind == state.EdgeRuleKindCORSA && rule.Action.CORS != nil && rule.Action.CORS.CorsPresetID != nil {
			id := *rule.Action.CORS.CorsPresetID
			keys[rule.AccountID+"\x00"+id] = struct{ account, id string }{rule.AccountID, id}
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		ref := keys[key]
		preset, err := s.PublicHostPolicyReader.GetCorsPresetByID(ctx, ref.account, ref.id)
		if err != nil {
			return err
		}
		s.presets[key] = preset
		encoded, err := json.Marshal(preset)
		if err != nil {
			return err
		}
		bytes += len(encoded)
		if bytes > api.TrafficPolicyMaxHostBytes {
			return errors.New("public compiled policy exceeds the projection limit")
		}
	}
	return nil
}
