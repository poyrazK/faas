// adr: 375
package state

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TrafficPolicyAggregateError describes an unsaved host aggregate. Compiled
// bytes are an upper bound for the compiler's escaped JSON, not wire bytes.
type TrafficPolicyAggregateError struct {
	Scope, Unit, Host string
	Limit, Observed   int64
}

func (e *TrafficPolicyAggregateError) Error() string {
	return fmt.Sprintf("state: %s for host %q is %d %s, above %d", e.Scope, e.Host, e.Observed, e.Unit, e.Limit)
}

type TrafficPolicyAnalysisError struct {
	Scope, Unit     string
	Limit, Observed int64
}

func (e *TrafficPolicyAnalysisError) Error() string {
	return fmt.Sprintf("state: traffic policy analysis %s is %d %s, above %d", e.Scope, e.Observed, e.Unit, e.Limit)
}

type trafficHostGroup struct {
	App, Pattern, Kind, Preset string
	Environment                string
	Rows, Canonical, Compiled  int64
	Unsupported                int64
}

type trafficHostAsset struct {
	ID       string
	Compiled int64
}

type trafficHostAnalysis struct {
	Groups            []trafficHostGroup
	Assets            []trafficHostAsset
	Environments      []trafficHostEnvironment
	PrimaryHosts      []string
	AliasHosts        []string
	RevisionHosts     []string
	Domains           []trafficHostDomain
	Tenants           []trafficHostTenant
	Reservations      []trafficHostReservation
	GlobalRoutes      bool
	AppsSuffix        string               `json:"-"`
	SelectDomains     bool                 `json:"-"`
	AllowGlobalRoutes bool                 `json:"-"`
	DomainClaims      []trafficDomainClaim `json:"-"`
	TenantClaims      []trafficTenantClaim `json:"-"`
	SelectTenants     bool                 `json:"-"`
	TenantSurfaces    bool                 `json:"-"`
}

// Binding identity gives a new publication no legacy selector allowance.
// An empty Environment retains ordinary account-wide compilation.
type trafficHostDomain struct{ Domain, App, Environment, Tenant string }

type trafficHostTenant struct{ Host, App, Surface, ID, PlatformTenant string }

type trafficHostEnvironment struct {
	ID, App, Host              string
	Present                    bool
	ContractBytes, Unsupported int64
}

func readTrafficHostAnalysis(ctx context.Context, tx pgx.Tx, account pgtype.UUID, appsSuffix string) (trafficHostAnalysis, error) {
	defaults, err := trafficHostActionDefaults()
	if err != nil {
		return trafficHostAnalysis{}, err
	}
	queries := sqlc.New()
	configured, err := queries.ConfigureTrafficPolicyAnalysisTimeout(ctx, tx, fmt.Sprintf("%dms", api.TrafficPolicyAnalysisSQLTimeout.Milliseconds()))
	if err != nil {
		return trafficHostAnalysis{}, fmt.Errorf("state: configure traffic analysis timeout: %w", err)
	}
	start := time.Now()
	row, err := queries.ReadTrafficHostAnalysis(ctx, tx, sqlc.ReadTrafficHostAnalysisParams{
		AccountID: account, MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes,
		// Scoped domains can be longer than a generated environment host.
		// Reserve maximum hostname length and JSON escape amplification.
		Defaults: defaults, AppsSuffix: appsSuffix, DeploySuffix: hostidentity.DeployWildcardSuffix, EnvironmentHostBytes: 6 * api.TrafficPolicyMaxHostnameBytes})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.QueryCanceled {
			limit := api.TrafficPolicyAnalysisSQLTimeout.Milliseconds()
			return trafficHostAnalysis{}, analysisLimit("database_time", "milliseconds", limit, max(limit+1, time.Since(start).Milliseconds()))
		}
		return trafficHostAnalysis{}, fmt.Errorf("state: read traffic host analysis: %w", mapErr(err))
	}
	if _, err := queries.RestoreTrafficPolicyStatementTimeout(ctx, tx, configured.Prior); err != nil {
		return trafficHostAnalysis{}, fmt.Errorf("state: restore policy statement timeout: %w", err)
	}
	if row.Inputs > api.TrafficPolicyMaxAnalysisInputs {
		return trafficHostAnalysis{}, analysisLimit("inputs", "groups", api.TrafficPolicyMaxAnalysisInputs, row.Inputs)
	}
	if row.Bytes > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return trafficHostAnalysis{}, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, row.Bytes)
	}
	var result trafficHostAnalysis
	if err := json.Unmarshal(row.Data, &result); err != nil {
		return result, fmt.Errorf("state: decode traffic host analysis: %w", err)
	}
	result.PrimaryHosts = servingTrafficPrimaryHosts(appsSuffix, result.PrimaryHosts)
	result.RevisionHosts = servingTrafficRevisionHosts(result.RevisionHosts)
	result.AppsSuffix = appsSuffix
	return result, prepareTrafficEnvironmentHosts(&result)
}

func servingTrafficPrimaryHosts(appsSuffix string, candidates []string) []string {
	var hosts []string
	for _, host := range candidates {
		slug, ok := hostidentity.AppSlugFromHost(appsSuffix, host)
		if ok && hostidentity.BuildPrimaryAppHost(appsSuffix, slug) == host {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func prepareTrafficEnvironmentHosts(view *trafficHostAnalysis) error {
	hosts := make(map[string]string, len(view.Environments))
	for i := range view.Environments {
		environment := &view.Environments[i]
		environment.Host = hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix, environment.ID, environment.App)
		if environment.Host == "" {
			return analysisLimit("environment_identity", "bindings", 0, 1)
		}
		hosts[environment.ID+"\x00"+environment.App] = environment.Host
	}
	for i := range view.Groups {
		group := &view.Groups[i]
		if group.Environment == "" {
			continue
		}
		group.Pattern = hosts[group.Environment+"\x00"+group.App]
		if group.Pattern == "" {
			return analysisLimit("environment_identity", "bindings", 0, 1)
		}
	}
	return nil
}

// SQL fills missing mandatory fields using the Go model's own zero values.
// No customer action body crosses the boundary for this normalization.
func trafficHostActionDefaults() ([]byte, error) {
	var action EdgeRuleAction
	value := reflect.ValueOf(&action).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.Pointer {
			field.Set(reflect.New(field.Type().Elem()))
		}
	}
	return json.Marshal(struct {
		Action   EdgeRuleAction
		HeaderOp EdgeRuleHeaderOp
	}{action, EdgeRuleHeaderOp{}})
}

func analysisLimit(scope, unit string, limit, observed int64) error {
	return &TrafficPolicyAnalysisError{Scope: scope, Unit: unit, Limit: limit, Observed: observed}
}

type trafficHostTotals struct{ rows, compiledRows, canonical, compiled, contractBytes int64 }

func (v trafficHostTotals) exceeds() bool {
	return v.rows > api.TrafficPolicyMaxHostRules || v.compiledRows > api.TrafficPolicyMaxHostRules ||
		v.canonical > api.TrafficPolicyMaxHostBytes || v.compiled > api.TrafficPolicyMaxHostBytes || v.contractBytes > api.TrafficPolicyMaxContractBytes
}

type hostAnalysisRef struct {
	side, group                                                 int
	ordinary                                                    bool
	domain, tenant, tenantClaim                                 bool
	claim, reservation, primaryReservation, platform, syntactic bool
}

type hostAnalysisNode struct {
	literal             map[rune]int
	any                 int
	star                int
	repeat              bool
	domainStar          int
	domainRepeat        bool
	labelStar, labelAny int
	labelRepeat         bool
	epsilon             []int
	accepted            []hostAnalysisRef
}

type hostAnalysisMachine struct {
	nodes    []hostAnalysisNode
	maxNodes int
}

type hostAnalysisBudgets struct {
	nodes, states           int
	stateBytes, transitions int64
}

func trafficHostAnalysisBudgets() hostAnalysisBudgets {
	return hostAnalysisBudgets{api.TrafficPolicyMaxAnalysisNodes, api.TrafficPolicyMaxAnalysisStates,
		api.TrafficPolicyMaxAnalysisStateBytes, api.TrafficPolicyMaxAnalysisTransitions}
}

func newHostAnalysisNode() hostAnalysisNode {
	return hostAnalysisNode{literal: make(map[rune]int), any: -1, star: -1, domainStar: -1, labelStar: -1, labelAny: -1}
}

func (m *hostAnalysisMachine) child(parent int, token rune) (int, error) {
	node := &m.nodes[parent]
	next := -1
	switch token {
	case -1:
		next = node.star
	case -2:
		next = node.any
	case -3:
		next = node.domainStar
	case -4:
		next = node.labelStar
	case -5:
		next = node.labelAny
	default:
		if child, exists := node.literal[token]; exists {
			next = child
		}
	}
	if next >= 0 {
		return next, nil
	}
	if len(m.nodes) >= m.maxNodes {
		return 0, analysisLimit("automaton_nodes", "nodes", int64(m.maxNodes), int64(len(m.nodes)+1))
	}
	next = len(m.nodes)
	switch token {
	case -1:
		node.star = next
	case -2:
		node.any = next
	case -3:
		node.domainStar = next
	case -4:
		node.labelStar = next
	case -5:
		node.labelAny = next
	default:
		node.literal[token] = next
	}
	child := newHostAnalysisNode()
	child.repeat = token == -1
	child.domainRepeat = token == -3
	child.labelRepeat = token == -4
	m.nodes = append(m.nodes, child)
	return next, nil
}

// The SQL predicate accepts either the literal selector or its LIKE form.
// LIKE's underscore matches one rune; backslash escapes the following rune.
func (m *hostAnalysisMachine) add(pattern string, ref hostAnalysisRef) error {
	literal := []rune(pattern)
	translated := []rune(strings.NewReplacer("*", "%", "?", "_").Replace(pattern))
	like := make([]rune, 0, len(translated))
	for i := 0; i < len(translated); i++ {
		switch translated[i] {
		case '%':
			like = append(like, -1)
		case '_':
			like = append(like, -2)
		case '\\':
			i++
			if i == len(translated) {
				// An invalid LIKE cannot select a host. Keep the exact branch
				// so existing invalid selectors can still be reduced or removed.
				like = nil
				i = len(translated)
				continue
			}
			like = append(like, translated[i])
		default:
			like = append(like, translated[i])
		}
	}
	branches := [][]rune{literal}
	if like != nil {
		branches = append(branches, like)
	}
	for _, tokens := range branches {
		if err := m.addTokens(tokens, ref); err != nil {
			return err
		}
	}
	return nil
}

func (m *hostAnalysisMachine) addTokens(tokens []rune, ref hostAnalysisRef) error {
	position := 0
	for _, token := range tokens {
		var err error
		position, err = m.child(position, token)
		if err != nil {
			return err
		}
	}
	m.nodes[position].accepted = append(m.nodes[position].accepted, ref)
	return nil
}

func (m *hostAnalysisMachine) closure(positions []int) []int {
	seen := make(map[int]bool, len(positions))
	for i := 0; i < len(positions); i++ {
		position := positions[i]
		if seen[position] {
			continue
		}
		seen[position] = true
		positions = append(positions, m.nodes[position].epsilon...)
		if star := m.nodes[position].star; star >= 0 {
			positions = append(positions, star)
		}
		if star := m.nodes[position].domainStar; star >= 0 {
			positions = append(positions, star)
		}
		if star := m.nodes[position].labelStar; star >= 0 {
			positions = append(positions, star)
		}
	}
	result := make([]int, 0, len(seen))
	for position := range seen {
		result = append(result, position)
	}
	sort.Ints(result)
	return result
}

func (m *hostAnalysisMachine) step(positions []int, character rune) []int {
	next := make([]int, 0, len(positions))
	for _, position := range positions {
		node := m.nodes[position]
		if node.repeat || node.domainRepeat && character != '*' || node.labelRepeat && character != '.' {
			next = append(next, position)
		}
		if node.any >= 0 {
			next = append(next, node.any)
		}
		if node.labelAny >= 0 && character != '.' {
			next = append(next, node.labelAny)
		}
		if child, exists := node.literal[character]; exists {
			next = append(next, child)
		}
	}
	return m.closure(next)
}

func analysisStateKey(positions []int) string {
	encoded := make([]byte, 4*len(positions))
	for i, position := range positions {
		binary.LittleEndian.PutUint32(encoded[4*i:], uint32(position))
	}
	return string(encoded)
}

func (m *hostAnalysisMachine) alphabet(positions []int) []rune {
	literals := make(map[rune]bool)
	for _, position := range positions {
		if m.nodes[position].domainRepeat {
			literals['*'] = true // This character has a distinct transition.
		}
		if m.nodes[position].labelRepeat || m.nodes[position].labelAny >= 0 {
			literals['.'] = true
		}
		for literal := range m.nodes[position].literal {
			literals[literal] = true
		}
	}
	// One representative of all remaining characters has the same transition.
	other := rune('a')
	for literals[other] {
		other++
		if other == 0xD800 {
			other = 0xE000
		}
	}
	literals[other] = true
	result := make([]rune, 0, len(literals))
	for literal := range literals {
		result = append(result, literal)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func hostTotals(view trafficHostAnalysis, accepted map[int]bool) trafficHostTotals {
	totals := trafficHostTotals{canonical: 2, compiled: 2}
	presets := make(map[string]bool)
	for index := range accepted {
		group := view.Groups[index]
		totals.rows += group.Rows
		totals.compiledRows += group.Rows
		totals.canonical += group.Canonical
		totals.compiled += group.Compiled
		if group.Preset != "" {
			presets[group.Preset] = true
		}
	}
	for _, asset := range view.Assets {
		if presets[asset.ID] {
			totals.compiled += asset.Compiled
		}
	}
	return totals
}

func environmentHostTotals(view trafficHostAnalysis, accepted map[int]bool, environment *trafficHostEnvironment) trafficHostTotals {
	raw := make(map[int]bool)
	compiled := make(map[int]bool)
	for index := range accepted {
		group := view.Groups[index]
		if group.Environment == "" {
			raw[index] = true
			if environment == nil || group.App == environment.App && (!environment.Present ||
				group.Kind != string(EdgeRuleKindHeaders) && group.Kind != string(EdgeRuleKindCORSA)) {
				compiled[index] = true
			}
		} else if environment != nil && group.App == environment.App && group.Environment == environment.ID {
			compiled[index] = true
		}
	}
	beforeFilter := hostTotals(view, raw)
	result := hostTotals(view, compiled)
	result.rows, result.canonical = beforeFilter.rows, beforeFilter.canonical
	if environment != nil {
		result.contractBytes = environment.ContractBytes
	}
	return result
}

type hostAnalysisState struct {
	positions []int
	parent    int
	character rune
}

func hostWitness(states []hostAnalysisState, index int) string {
	var characters []rune
	for index > 0 {
		characters = append(characters, states[index].character)
		index = states[index].parent
	}
	for i, j := 0, len(characters)-1; i < j; i, j = i+1, j-1 {
		characters[i], characters[j] = characters[j], characters[i]
	}
	return string(characters)
}

func checkHostTotals(before, after trafficHostTotals, host string) *TrafficPolicyAggregateError {
	for _, bound := range []struct {
		scope, unit        string
		prior, next, limit int64
	}{
		{"host_rule_count", "rules", before.rows, after.rows, api.TrafficPolicyMaxHostRules},
		{"host_compiled_rule_count", "rules", before.compiledRows, after.compiledRows, api.TrafficPolicyMaxHostRules},
		{"host_rule_projection", "bytes", before.canonical, after.canonical, api.TrafficPolicyMaxHostBytes},
		{"host_compiled_projection_estimate", "bytes", before.compiled, after.compiled, api.TrafficPolicyMaxHostBytes},
		{"environment_edge_policy", "bytes", before.contractBytes, after.contractBytes, api.TrafficPolicyMaxContractBytes},
	} {
		if bound.next > bound.limit && bound.next > bound.prior {
			return &TrafficPolicyAggregateError{Scope: bound.scope, Unit: bound.unit, Host: host, Limit: bound.limit, Observed: bound.next}
		}
	}
	return nil
}

func checkTrafficHostAnalysis(ctx context.Context, before, after trafficHostAnalysis) error {
	return checkTrafficHostAnalysisWithBudgets(ctx, before, after, trafficHostAnalysisBudgets())
}

func checkTrafficHostAnalysisWithBudgets(ctx context.Context, before, after trafficHostAnalysis, budgets hostAnalysisBudgets) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, group := range after.Groups {
		if group.Unsupported > 0 {
			return analysisLimit("stored_action_shape", "fields", 0, group.Unsupported)
		}
	}
	for _, environment := range after.Environments {
		if environment.Unsupported > 0 {
			return analysisLimit("stored_environment_shape", "fields", 0, environment.Unsupported)
		}
	}
	if reflect.DeepEqual(before, after) {
		return nil
	}
	all := make(map[int]bool, len(after.Groups))
	for i := range after.Groups {
		all[i] = true
	}
	union := hostTotals(after, all)
	for _, environment := range after.Environments {
		union.contractBytes = max(union.contractBytes, environment.ContractBytes)
	}
	if !union.exceeds() {
		return nil // Even the union of all groups fits, so every host fits.
	}
	views := [2]trafficHostAnalysis{before, after}
	var scopes [2]trafficHostScopeIndex
	for side, view := range views {
		var err error
		scopes[side], err = indexTrafficHostScopes(ctx, view)
		if err != nil {
			return err
		}
	}
	machine := hostAnalysisMachine{nodes: []hostAnalysisNode{newHostAnalysisNode()}, maxNodes: budgets.nodes}
	for side, view := range views {
		if err := machine.addTrafficBindingLanguages(ctx, side, view); err != nil {
			return err
		}
		for i, group := range view.Groups {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := machine.add(group.Pattern, hostAnalysisRef{side: side, group: i}); err != nil {
				return err
			}
		}
		for i, environment := range view.Environments {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := machine.addTokens([]rune(environment.Host), hostAnalysisRef{side: side, group: -1 - i}); err != nil {
				return err
			}
		}
		for _, host := range append(append(append([]string(nil), view.PrimaryHosts...), view.AliasHosts...), view.RevisionHosts...) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := machine.addTokens([]rune(host), hostAnalysisRef{side: side, ordinary: true}); err != nil {
				return err
			}
		}
		for i, domain := range view.Domains {
			if err := ctx.Err(); err != nil {
				return err
			}
			tokens := trafficDomainHostTokens(domain.Domain)
			if tokens != nil {
				if err := machine.addTokens(tokens, hostAnalysisRef{side: side, group: i, domain: true}); err != nil {
					return err
				}
			}
		}
		for i, tenant := range view.Tenants {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := machine.addTokens([]rune(tenant.Host), hostAnalysisRef{side: side, group: i, tenant: true}); err != nil {
				return err
			}
		}
	}
	initial := machine.closure([]int{0})
	states := []hostAnalysisState{{positions: initial, parent: -1}}
	key := analysisStateKey(initial)
	seen := map[string]bool{key: true}
	stateBytes, transitions := retainedAnalysisStateBytes(initial, key), int64(0)
	if stateBytes > budgets.stateBytes {
		return analysisLimit("state_memory", "bytes", budgets.stateBytes, stateBytes)
	}
	for index := 0; index < len(states); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		positions := states[index].positions
		accepted := [2]map[int]bool{make(map[int]bool), make(map[int]bool)}
		bindings := [2]map[trafficHostDomain]*trafficHostEnvironment{make(map[trafficHostDomain]*trafficHostEnvironment), make(map[trafficHostDomain]*trafficHostEnvironment)}
		var claims [2]*trafficDomainClaim
		var tenantClaims [2]*trafficTenantClaim
		var reserved, primaryReserved, platform, syntactic [2]bool
		for _, position := range positions {
			for _, ref := range machine.nodes[position].accepted {
				switch {
				case ref.claim:
					claim := &views[ref.side].DomainClaims[ref.group]
					if claims[ref.side] == nil || trafficDomainClaimPrecedes(*claim, *claims[ref.side]) {
						claims[ref.side] = claim
					}
				case ref.tenantClaim:
					tenantClaims[ref.side] = &views[ref.side].TenantClaims[ref.group]
				case ref.reservation:
					reserved[ref.side] = true
				case ref.primaryReservation:
					primaryReserved[ref.side] = true
				case ref.platform:
					platform[ref.side] = true
				case ref.syntactic:
					syntactic[ref.side] = true
				case ref.domain:
					domain := views[ref.side].Domains[ref.group]
					bindings[ref.side][domain] = scopes[ref.side].environments[trafficHostDomain{App: domain.App, Environment: domain.Environment}]
				case ref.tenant:
					tenant := views[ref.side].Tenants[ref.group]
					bindings[ref.side][trafficHostDomain{Domain: tenant.Host, App: tenant.App, Tenant: tenant.Surface + "/" + tenant.ID + "/" + tenant.PlatformTenant}] = nil
				case ref.ordinary:
					bindings[ref.side][trafficHostDomain{}] = nil
				case ref.group < 0:
					environment := &views[ref.side].Environments[-1-ref.group]
					bindings[ref.side][trafficHostDomain{App: environment.App, Environment: environment.ID}] = environment
				default:
					accepted[ref.side][ref.group] = true
				}
			}
		}
		for side, view := range views {
			if platform[side] || syntactic[side] {
				for binding := range bindings[side] {
					if binding.Tenant != "" {
						delete(bindings[side], binding)
					}
				}
			}
			if view.SelectDomains {
				selectTrafficDomainBindings(bindings[side], claims[side], platform[side] || syntactic[side])
			}
			if view.SelectTenants {
				selectTrafficTenantBindings(bindings[side], tenantClaims[side], view.TenantSurfaces, platform[side] || syntactic[side])
			}
			reserved[side] = syntactic[side] || platform[side] && primaryReserved[side] || !platform[side] && reserved[side]
			if view.AllowGlobalRoutes && !reserved[side] && trafficAcceptedRoute(view, accepted[side]) {
				bindings[side][trafficHostDomain{App: "@global-route"}] = nil
			}
		}
		if err := checkTrafficBoundHost(ctx, views, scopes, accepted, bindings, reserved); err != nil {
			var aggregate *TrafficPolicyAggregateError
			if !errors.As(err, &aggregate) {
				return err
			}
			aggregate.Host = hostWitness(states, index)
			return err
		}
		for _, character := range machine.alphabet(positions) {
			if err := ctx.Err(); err != nil {
				return err
			}
			transitions++
			if transitions > budgets.transitions {
				return analysisLimit("transitions", "transitions", budgets.transitions, transitions)
			}
			next := machine.step(positions, character)
			if len(next) == 0 {
				continue
			}
			key := analysisStateKey(next)
			if seen[key] {
				continue
			}
			stateBytes += retainedAnalysisStateBytes(next, key)
			if stateBytes > budgets.stateBytes {
				return analysisLimit("state_memory", "bytes", budgets.stateBytes, stateBytes)
			}
			if len(states) >= budgets.states {
				return analysisLimit("states", "states", int64(budgets.states), int64(len(states)+1))
			}
			seen[key] = true
			states = append(states, hostAnalysisState{positions: next, parent: index, character: character})
		}
	}
	return nil
}

func trafficDomainHostTokens(domain string) []rune {
	if suffix, wildcard := WildcardDomainSuffix(domain); wildcard {
		// Runtime accepts a strict suffix, including nested subdomains, but
		// excludes hosts containing any asterisk. Other legacy punctuation is
		// literal, unlike edge-rule selectors.
		if strings.ContainsRune(suffix, '*') {
			return nil
		}
		return append([]rune{-3}, []rune("."+suffix)...)
	}
	return []rune(strings.ToLower(domain))
}

func retainedAnalysisStateBytes(positions []int, key string) int64 {
	return int64(len(key) + 8*len(positions) + api.TrafficPolicyAnalysisStateOverhead)
}
