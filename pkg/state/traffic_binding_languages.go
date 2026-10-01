// adr: 375
package state

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

type trafficHostReservation struct{ Kind, Host string }

type trafficDomainClaim struct {
	Domain, App, Environment, Account, RedirectApp string
	Eligible                                       bool
}

func trafficDomainClaimPrecedes(a, b trafficDomainClaim) bool {
	aw, bw := IsWildcardCustomDomain(a.Domain), IsWildcardCustomDomain(b.Domain)
	if aw != bw {
		return !aw
	}
	return len(a.Domain) > len(b.Domain) || len(a.Domain) == len(b.Domain) && a.Domain < b.Domain
}

func selectTrafficDomainBindings(bindings map[trafficHostDomain]*trafficHostEnvironment, claim *trafficDomainClaim, blocked bool) {
	for binding := range bindings {
		if binding.Tenant == "" && binding.Domain != "" && (blocked || !IsWildcardCustomDomain(binding.Domain) && strings.ContainsRune(binding.Domain, '*') || claim == nil || !claim.Eligible ||
			binding != (trafficHostDomain{Domain: claim.Domain, App: claim.App, Environment: claim.Environment})) {
			delete(bindings, binding)
		}
	}
}

func checkTrafficBoundHost(ctx context.Context, views [2]trafficHostAnalysis, scopes [2]trafficHostScopeIndex, accepted [2]map[int]bool, bindings [2]map[trafficHostDomain]*trafficHostEnvironment, reserved [2]bool) error {
	if !views[1].GlobalRoutes {
		return checkTrafficHostBindings(ctx, views, scopes, accepted, bindings)
	}
	var totals [2]trafficHostTotals
	for side, view := range views {
		if !reserved[side] {
			totals[side] = scopes[side].totals(view, accepted[side], nil)
		}
	}
	if err := checkHostTotals(totals[0], totals[1], ""); err != nil {
		return err
	}
	return nil
}

func (m *hostAnalysisMachine) addTrafficBindingLanguages(ctx context.Context, side int, view trafficHostAnalysis) error {
	if view.GlobalRoutes || view.SelectDomains || view.AllowGlobalRoutes || len(view.Tenants) > 0 {
		if err := m.addTrafficNamespaces(side, view.AppsSuffix); err != nil {
			return err
		}
	}
	for i, claim := range view.DomainClaims {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tokens := trafficDomainHostTokens(claim.Domain); tokens != nil {
			if err := m.addTokens(tokens, hostAnalysisRef{side: side, group: i, claim: true}); err != nil {
				return err
			}
		}
	}
	if view.SelectTenants && view.TenantSurfaces {
		for i, claim := range view.TenantClaims {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := m.addTokens([]rune(claim.Host), hostAnalysisRef{side: side, group: i, tenantClaim: true}); err != nil {
				return err
			}
		}
	}
	for _, reservation := range view.Reservations {
		if err := ctx.Err(); err != nil {
			return err
		}
		var tokens []rune
		ref := hostAnalysisRef{side: side, reservation: true}
		if reservation.Host == "" {
			continue
		}
		switch reservation.Kind {
		case "primary":
			if _, ok := hostidentity.AppSlugFromHost(view.AppsSuffix, reservation.Host); !ok {
				continue
			}
			tokens = []rune(reservation.Host)
			ref.reservation, ref.primaryReservation = false, true
		case "domain":
			// The reservation query tries exact citext identity even for
			// a wildcard row. Its literal asterisk claim blocks discovery.
			if err := m.addTokens([]rune(strings.ToLower(reservation.Host)), ref); err != nil {
				return err
			}
			if IsWildcardCustomDomain(reservation.Host) {
				tokens = trafficDomainHostTokens(reservation.Host)
			}
		case "tenant":
			tokens = []rune(reservation.Host)
		}
		if tokens != nil {
			if err := m.addTokens(tokens, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

func trafficAcceptedRoute(view trafficHostAnalysis, accepted map[int]bool) bool {
	for i := range accepted {
		if view.Groups[i].Kind == string(EdgeRuleKindRoute) && view.Groups[i].Environment == "" {
			return true
		}
	}
	return false
}

func (m *hostAnalysisMachine) addTrafficNamespaces(side int, appsSuffix string) error {
	ref := hostAnalysisRef{side: side, syntactic: true}
	if appsSuffix != "" {
		if err := m.addTokens(append([]rune{-5, -4}, []rune(appsSuffix)...), hostAnalysisRef{side: side, platform: true}); err != nil {
			return err
		}
		if err := m.addTokens(append(append([]rune("tag-"), -4), []rune(appsSuffix)...), ref); err != nil {
			return err
		}
	}
	if err := m.addEnvironmentNamespace(ref); err != nil {
		return err
	}
	return m.addRevisionNamespace(ref)
}

func (m *hostAnalysisMachine) privateNode() (int, error) {
	if len(m.nodes) >= m.maxNodes {
		return 0, analysisLimit("automaton_nodes", "nodes", int64(m.maxNodes), int64(len(m.nodes)+1))
	}
	position := len(m.nodes)
	m.nodes = append(m.nodes, newHostAnalysisNode())
	return position, nil
}

func (m *hostAnalysisMachine) privateProgram(prefix string) (int, error) {
	position, err := m.privateNode()
	if err != nil {
		return 0, err
	}
	m.nodes[0].epsilon = append(m.nodes[0].epsilon, position)
	return m.literalPath(position, prefix)
}

func (m *hostAnalysisMachine) literalPath(position int, text string) (int, error) {
	for _, character := range text {
		var err error
		position, err = m.child(position, character)
		if err != nil {
			return 0, err
		}
	}
	return position, nil
}

func (m *hostAnalysisMachine) characterClass(position int, characters string) (int, error) {
	next, err := m.privateNode()
	if err != nil {
		return 0, err
	}
	for _, character := range characters {
		m.nodes[position].literal[character] = next
	}
	return next, nil
}

func (m *hostAnalysisMachine) addEnvironmentNamespace(ref hostAnalysisRef) error {
	position, err := m.privateProgram("env-")
	if err != nil {
		return err
	}
	// 128 UUID bits: 25 complete base32 symbols, then three bits plus
	// canonical zero padding. Noncanonical encodings are not reserved.
	for identity := 0; identity < 2; identity++ {
		for i := 0; i < 26; i++ {
			characters := "abcdefghijklmnopqrstuvwxyz234567"
			if i == 25 {
				characters = "aeimquy4"
			}
			position, err = m.characterClass(position, characters)
			if err != nil {
				return err
			}
		}
		if identity == 0 {
			position, err = m.literalPath(position, "-")
			if err != nil {
				return err
			}
		}
	}
	position, err = m.literalPath(position, hostidentity.DeployWildcardSuffix)
	if err == nil {
		m.nodes[position].accepted = append(m.nodes[position].accepted, ref)
	}
	return err
}

func (m *hostAnalysisMachine) addRevisionNamespace(ref hostAnalysisRef) error {
	start, err := m.privateProgram("deploy-")
	if err != nil {
		return err
	}
	slugStart, err := m.privateNode()
	if err != nil {
		return err
	}
	slug, err := m.characterClass(slugStart, "abcdefghijklmnopqrstuvwxyz0123456789-")
	if err != nil {
		return err
	}
	for _, character := range "abcdefghijklmnopqrstuvwxyz0123456789-" {
		m.nodes[slug].literal[character] = slug
	}
	end, err := m.literalPath(slug, hostidentity.DeployWildcardSuffix)
	if err != nil {
		return err
	}
	m.nodes[end].accepted = append(m.nodes[end].accepted, ref)
	maximum := strconv.Itoa(math.MaxInt)
	states := map[[2]int]int{{0, 0}: start}
	var digits func(int, int) (int, error)
	digits = func(length, relation int) (int, error) {
		key := [2]int{length, relation}
		position, exists := states[key]
		if !exists {
			var err error
			position, err = m.privateNode()
			if err != nil {
				return 0, err
			}
			states[key] = position
		}
		if length > 0 && (length < len(maximum) || relation <= 0) {
			m.nodes[position].literal['-'] = slugStart
		}
		if length == len(maximum) {
			return position, nil
		}
		for character := byte('0'); character <= '9'; character++ {
			if length == 0 && character == '0' {
				continue
			}
			nextRelation := relation
			if relation == 0 {
				nextRelation = strings.Compare(string(character), string(maximum[length]))
			}
			if length+1 == len(maximum) && nextRelation > 0 {
				continue
			}
			nextKey := [2]int{length + 1, nextRelation}
			next, ok := states[nextKey]
			if !ok {
				var err error
				next, err = digits(length+1, nextRelation)
				if err != nil {
					return 0, err
				}
			}
			m.nodes[position].literal[rune(character)] = next
		}
		return position, nil
	}
	_, err = digits(0, 0)
	return err
}
