// adr: 375
package state

import (
	"errors"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

func trafficNamespaceVerdict(machine *hostAnalysisMachine, host string) (platform, syntactic bool) {
	positions := machine.closure([]int{0})
	for _, character := range host {
		positions = machine.step(positions, character)
	}
	for _, position := range positions {
		for _, ref := range machine.nodes[position].accepted {
			platform, syntactic = platform || ref.platform, syntactic || ref.syntactic
		}
	}
	return
}

func TestTrafficReservationNamespacesMatchPublicParsers(t *testing.T) {
	random := rand.New(rand.NewSource(375))
	for _, suffix := range []string{hostidentity.DeployWildcardSuffix, ".apps.example.test", ""} {
		machine := hostAnalysisMachine{nodes: []hostAnalysisNode{newHostAnalysisNode()}, maxNodes: api.TrafficPolicyMaxAnalysisNodes}
		if err := machine.addTrafficNamespaces(0, suffix); err != nil {
			t.Fatal(err)
		}
		hosts := []string{"", "tag-" + suffix, "nested.tag-any" + suffix, "arbitrary" + suffix,
			"deploy-0-web.gregale.dev", "deploy-01-web.gregale.dev", "deploy-1-web.gregale.dev", "deploy-1--.gregale.dev",
			"deploy-1-web_.gregale.dev", "deploy-1-web.x.gregale.dev", "deploy-" + strconv.Itoa(math.MaxInt) + "-web.gregale.dev",
			"deploy-9223372036854775808-web.gregale.dev", "deploy-999999999999999999999-web.gregale.dev"}
		for i := 0; i < 100; i++ {
			var environment, app uuid.UUID
			_, _ = random.Read(environment[:])
			_, _ = random.Read(app[:])
			host := hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix, environment.String(), app.String())
			hosts = append(hosts, host, strings.ToUpper(host), host[:29]+"b"+host[30:], host[:56]+"7"+host[57:], "nested."+host,
				"deploy-"+strconv.FormatInt(random.Int63(), 10)+"-web-1.gregale.dev")
		}
		for _, host := range hosts {
			platform, syntactic := trafficNamespaceVerdict(&machine, host)
			_, wantPlatform := hostidentity.AppSlugFromHost(suffix, host)
			_, alias := hostidentity.DeploymentAliasLabelFromHost(suffix, host)
			_, _, environment := hostidentity.EnvironmentIDsFromHost(hostidentity.DeployWildcardSuffix, host)
			_, _, revision := hostidentity.DeploymentScopeFromHost(hostidentity.DeployWildcardSuffix, host)
			if platform != wantPlatform || syntactic != (alias || environment || revision) {
				t.Fatalf("namespace language differs from runtime: suffix=%q host=%q got=%v/%v want=%v/%v", suffix, host, platform, syntactic, wantPlatform, alias || environment || revision)
			}
		}
	}
}

func TestTrafficGlobalReservationsAndNewlyUnclaimedAllowance(t *testing.T) {
	for _, test := range []struct{ kind, claim, host string }{
		{"domain", "API.EXAMPLE.TEST", "api.example.test"},
		{"domain", "*.example.test", "nested.api.example.test"},
		{"tenant", "tenant.example.test", "tenant.example.test"},
		{"primary", "existing.apps.example.test", "existing.apps.example.test"},
	} {
		t.Run(test.kind+"/"+test.host, func(t *testing.T) {
			before := trafficHostAnalysis{GlobalRoutes: true, AppsSuffix: ".apps.example.test",
				Reservations: []trafficHostReservation{{Kind: test.kind, Host: test.claim}},
				Groups:       []trafficHostGroup{analysisGroup(test.host, api.TrafficPolicyMaxHostBytes+1)}}
			// A policy wholly inside a reservation does not consume global
			// discovery allowance, even if the claim cannot currently serve.
			empty := before
			empty.Groups = nil
			if err := checkTrafficHostAnalysis(t.Context(), empty, before); err != nil {
				t.Fatalf("reserved host consumed global allowance: %v", err)
			}
			after := before
			after.Reservations = nil
			var aggregate *TrafficPolicyAggregateError
			if err := checkTrafficHostAnalysis(t.Context(), before, after); !errors.As(err, &aggregate) || aggregate.Host != test.host {
				t.Fatalf("newly unclaimed host inherited legacy allowance: %v", err)
			}
		})
	}
}

func TestTrafficGlobalReservationPrecedenceAndLookalikes(t *testing.T) {
	for _, host := range []string{"missing.apps.example.test", "api.underXscore.example.test", "api.percent123.example.test"} {
		view := trafficHostAnalysis{GlobalRoutes: true, AppsSuffix: ".apps.example.test",
			Reservations: []trafficHostReservation{{Kind: "domain", Host: "missing.apps.example.test"}, {Kind: "domain", Host: "*.under_score.example.test"}, {Kind: "domain", Host: "*.percent%.example.test"}},
			Groups:       []trafficHostGroup{analysisGroup(host, api.TrafficPolicyMaxHostBytes+1)}}
		before := view
		before.Groups = nil
		var aggregate *TrafficPolicyAggregateError
		if err := checkTrafficHostAnalysis(t.Context(), before, view); !errors.As(err, &aggregate) || aggregate.Host != host {
			t.Fatalf("irrelevant reservation hid unclaimed host %q: %v", host, err)
		}
	}
}

func TestTrafficDomainRemovalSelectsWinningBinding(t *testing.T) {
	const host = "nested.api.example.test"
	app, environment := uuid.NewString(), uuid.NewString()
	for _, mode := range []string{"new-fallback", "unverified-blocker", "still-shadowed", "already-selected", "scoped-empty", "scoped-fallback"} {
		t.Run(mode, func(t *testing.T) {
			view := trafficHostAnalysis{Groups: []trafficHostGroup{{App: app, Pattern: host, Kind: string(EdgeRuleKindHeaders), Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes + 1}}}
			removed := trafficDomainClaim{Domain: host, App: "foreign", Account: "foreign", Eligible: mode != "unverified-blocker"}
			fallback := trafficDomainClaim{Domain: "*.example.test", App: app, Account: "owner", Eligible: true}
			wantRefusal := true
			var retained []trafficDomainClaim
			if mode == "still-shadowed" {
				removed.Domain = "*.api.example.test"
				retained = append(retained, trafficDomainClaim{Domain: host, App: "blocked", Account: "other"})
				wantRefusal = false
			} else if mode == "already-selected" {
				removed.Domain, fallback.Domain = "*.example.test", "*.api.example.test"
				wantRefusal = false
			} else if strings.HasPrefix(mode, "scoped-") {
				fallback.Environment = environment
				view.Environments = []trafficHostEnvironment{{ID: environment, App: app, Present: mode == "scoped-empty"}}
				if err := prepareTrafficEnvironmentHosts(&view); err != nil {
					t.Fatal(err)
				}
				wantRefusal = mode == "scoped-fallback"
			}
			before := append(append([]trafficDomainClaim{removed, fallback}, retained...), trafficDomainClaim{Domain: "irrelevant.test", App: "irrelevant"})
			after := append([]trafficDomainClaim{fallback}, retained...)
			err := checkTrafficDomainRemovalOwner(t.Context(), view, before, after, "owner", trafficHostAnalysis{}, trafficHostAnalysis{})
			var aggregate *TrafficPolicyAggregateError
			if wantRefusal {
				if !errors.As(err, &aggregate) || aggregate.Host != host || aggregate.Scope != "host_compiled_projection_estimate" {
					t.Fatalf("exposed binding retained legacy allowance: %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexposed or replaced policy refused removal: %v", err)
			}
		})
	}
}
