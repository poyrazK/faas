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
	Groups       []trafficHostGroup
	Assets       []trafficHostAsset
	Environments []trafficHostEnvironment
}

type trafficHostEnvironment struct {
	ID, App, Host              string
	Present                    bool
	ContractBytes, Unsupported int64
}

func readTrafficHostAnalysis(ctx context.Context, tx pgx.Tx, account pgtype.UUID) (trafficHostAnalysis, error) {
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
		Defaults: defaults, EnvironmentHostBytes: int32(len(hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix,
			"00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000")))})
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
	return result, prepareTrafficEnvironmentHosts(&result)
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

type hostAnalysisRef struct{ side, group int }

type hostAnalysisNode struct {
	literal  map[rune]int
	any      int
	star     int
	repeat   bool
	accepted []hostAnalysisRef
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
	return hostAnalysisNode{literal: make(map[rune]int), any: -1, star: -1}
}

func (m *hostAnalysisMachine) child(parent int, token rune) (int, error) {
	node := &m.nodes[parent]
	next := -1
	switch token {
	case -1:
		next = node.star
	case -2:
		next = node.any
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
	default:
		node.literal[token] = next
	}
	child := newHostAnalysisNode()
	child.repeat = token == -1
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
		position := 0
		for _, token := range tokens {
			var err error
			position, err = m.child(position, token)
			if err != nil {
				return err
			}
		}
		m.nodes[position].accepted = append(m.nodes[position].accepted, ref)
	}
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
		if star := m.nodes[position].star; star >= 0 {
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
		if node.repeat {
			next = append(next, position)
		}
		if node.any >= 0 {
			next = append(next, node.any)
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
	machine := hostAnalysisMachine{nodes: []hostAnalysisNode{newHostAnalysisNode()}, maxNodes: budgets.nodes}
	for side, view := range views {
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
			if err := machine.add(environment.Host, hostAnalysisRef{side: side, group: -1 - i}); err != nil {
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
		var environments [2]*trafficHostEnvironment
		for _, position := range positions {
			for _, ref := range machine.nodes[position].accepted {
				if ref.group < 0 {
					environments[ref.side] = &views[ref.side].Environments[-1-ref.group]
				} else {
					accepted[ref.side][ref.group] = true
				}
			}
		}
		prior := environmentHostTotals(before, accepted[0], environments[0])
		if environments[0] == nil && environments[1] != nil {
			// A new registered workload URL must fit its limits. An old
			// over-limit selector language is not a serving-policy baseline.
			prior = trafficHostTotals{}
		}
		nextTotals := environmentHostTotals(after, accepted[1], environments[1])
		if environments[0] != nil && environments[1] == nil {
			// A deleted registered URL cannot fall back to route discovery.
			// Removing its app filter does not expose a new serving scope.
			nextTotals = trafficHostTotals{}
		}
		if err := checkHostTotals(prior, nextTotals, ""); err != nil {
			err.Host = hostWitness(states, index)
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

func retainedAnalysisStateBytes(positions []int, key string) int64 {
	return int64(len(key) + 8*len(positions) + api.TrafficPolicyAnalysisStateOverhead)
}
