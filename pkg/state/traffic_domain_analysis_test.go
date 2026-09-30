// adr: 375
package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrafficDomainActivationUsesBindingIdentity(t *testing.T) {
	for _, test := range []struct{ name, domain, host string }{
		{"exact", "api.example.test", "api.example.test"},
		{"wildcard", "*.example.test", "nested.api.example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := trafficHostAnalysis{Groups: []trafficHostGroup{{Pattern: test.host, Rows: api.TrafficPolicyMaxHostRules + 1}},
				PrimaryHosts: []string{test.host}}
			after := before
			after.Domains = []trafficHostDomain{{Domain: test.domain, App: "new-owner"}}
			var aggregate *TrafficPolicyAggregateError
			if err := checkTrafficHostAnalysis(t.Context(), before, after); !errors.As(err, &aggregate) || aggregate.Host != test.host {
				t.Fatalf("new binding reused potential primary baseline: %v", err)
			}
			before.Domains = []trafficHostDomain{{Domain: test.domain, App: "old-owner"}}
			if err := checkTrafficHostAnalysis(t.Context(), before, after); !errors.As(err, &aggregate) {
				t.Fatalf("new owner reused old domain baseline: %v", err)
			}
			if err := checkTrafficHostAnalysis(t.Context(), after, after); err != nil {
				t.Fatalf("unchanged legacy binding refused: %v", err)
			}
		})
	}
}

func TestTrafficDomainLanguageMatchesWildcardRouting(t *testing.T) {
	for _, domain := range []string{"*.example.test", "*.nested.example.test", " *.EXAMPLE.TEST ", "*.under_score.example.test", "*.bad*.example.test"} {
		machine := hostAnalysisMachine{nodes: []hostAnalysisNode{newHostAnalysisNode()}, maxNodes: api.TrafficPolicyMaxAnalysisNodes}
		if tokens := trafficDomainHostTokens(domain); tokens != nil {
			if err := machine.addTokens(tokens, hostAnalysisRef{}); err != nil {
				t.Fatal(err)
			}
		}
		for _, host := range []string{"example.test", ".example.test", "api.example.test", "nested.api.example.test", "api.nested.example.test", "badexample.test", "*.example.test", "a*.example.test", "api.under_score.example.test", "api.bad*.example.test", "api.underXscore.example.test"} {
			positions := machine.closure([]int{0})
			for _, character := range host {
				positions = machine.step(positions, character)
			}
			matched := false
			for _, position := range positions {
				matched = matched || len(machine.nodes[position].accepted) != 0
			}
			if want := WildcardMatchesHost(domain, host); matched != want {
				t.Errorf("domain=%q host=%q analysis=%v runtime=%v", domain, host, matched, want)
			}
		}
	}
}

var trafficDomainVerificationModes = []string{"plain", "challenge", "wildcard", "nested", "apex", "literal-marker", "unchanged", "stale-token", "missing", "foreign-policy", "internal", "canceled"}

func testTrafficDomainVerification(t *testing.T, store Store, account Account, app App, mode string, seed func(Account, App, string)) {
	t.Helper()
	domain, host := "api.example.test", "api.example.test"
	if mode == "wildcard" || mode == "nested" || mode == "apex" {
		domain, host = "*.example.test", "api.example.test"
		if mode == "nested" {
			host = "nested.api.example.test"
		} else if mode == "apex" {
			host = "example.test"
		}
	} else if mode == "literal-marker" {
		domain, host = "api_%.example.test", "apiXaa.example.test"
	}
	if mode == "internal" {
		visibility := api.AppVisibilityInternal
		var err error
		app, err = store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateCustomDomain(t.Context(), domain, app.ID, "current-token"); err != nil {
		t.Fatal(err)
	}
	if mode == "unchanged" {
		if err := store.MarkDomainVerified(t.Context(), domain); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.DomainByName(t.Context(), domain)
	if err != nil {
		t.Fatal(err)
	}
	policyAccount, policyApp := account, app
	if mode == "foreign-policy" {
		policyAccount, err = store.CreateAccount(t.Context(), "foreign-domain-policy@example.test", api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		policyApp, err = store.CreateApp(t.Context(), App{AccountID: policyAccount.ID, Slug: "foreign-domain-policy"})
		if err != nil {
			t.Fatal(err)
		}
	}
	seed(policyAccount, policyApp, host)
	apply := func() (bool, error) {
		if mode == "plain" || mode == "unchanged" {
			err := store.MarkDomainVerified(t.Context(), domain)
			return err == nil, err
		}
		ctx := t.Context()
		if mode == "canceled" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		token, lookup := "current-token", domain
		if mode == "stale-token" {
			token = "old-token"
		} else if mode == "missing" {
			lookup = "missing.example.test"
		}
		return store.(CustomDomainChallengeVerifier).MarkDomainVerifiedIfChallenge(ctx, lookup, token)
	}
	matched, err := apply()
	refuse := mode == "plain" || mode == "challenge" || mode == "wildcard" || mode == "nested"
	if refuse {
		var aggregate *TrafficPolicyAggregateError
		if matched || !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" || aggregate.Host != host {
			t.Fatalf("domain activation accepted overload: matched=%v err=%v", matched, err)
		}
	} else if mode == "canceled" {
		if matched || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled verification: matched=%v err=%v", matched, err)
		}
	} else if err != nil || matched == (mode == "stale-token" || mode == "missing") {
		t.Fatalf("verification outcome: matched=%v err=%v", matched, err)
	}
	after, readErr := store.DomainByName(t.Context(), domain)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if refuse || mode == "stale-token" || mode == "missing" || mode == "canceled" {
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("failed verification changed domain/certificate intent: before=%+v after=%+v", before, after)
		}
		if err := store.DeleteEdgeRule(t.Context(), "00000000-0000-0000-0000-000000000377"); err != nil {
			t.Fatal(err)
		}
		if refuse {
			if matched, err := apply(); err != nil || !matched {
				t.Fatalf("verification after policy repair: matched=%v err=%v", matched, err)
			}
		}
	}
}

func TestMemTrafficDomainVerificationRollback(t *testing.T) {
	for _, mode := range trafficDomainVerificationModes {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficDomainVerification(t, m, account, app, mode, func(owner Account, source App, pattern string) {
				in := memTrafficRule(owner, source, pattern, 520)
				id := "00000000-0000-0000-0000-000000000377"
				m.edgeRules[id] = EdgeRule{ID: id, AccountID: owner.ID, AppID: source.ID, MatchHost: pattern, MatchPath: "/", Enabled: true, Kind: in.Kind, Action: in.Action}
			})
		})
	}
}

func TestMemTrafficDomainMetadataAndAppPublication(t *testing.T) {
	m, account, _, app, environment := memTrafficFixture(t)
	visibility := api.AppVisibilityInternal
	app, err := m.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility})
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []CustomDomain{
		{Domain: "public.example.test", AppID: app.ID, VerifiedAt: time.Now()},
		{Domain: "*.example.test", AppID: app.ID, VerifiedAt: time.Now()},
		{Domain: "pending.example.test", AppID: app.ID},
		{Domain: "scoped.example.test", AppID: app.ID, EnvironmentID: environment.ID, VerifiedAt: time.Now()},
	} {
		m.domains[domain.Domain] = domain
	}
	in := memTrafficRule(account, app, "public.example.test", 520)
	m.edgeRules["legacy"] = EdgeRule{ID: "legacy", AccountID: account.ID, AppID: app.ID, MatchHost: in.MatchHost, Enabled: true, Kind: in.Kind, Action: in.Action}
	visibility = api.AppVisibilityPublic
	_, err = m.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility})
	requireMemTrafficAggregate(t, err, "host_rule_projection")
	if m.apps[app.ID].Visibility != api.AppVisibilityInternal {
		t.Fatal("refused domain owner publication changed visibility")
	}
	delete(m.edgeRules, "legacy")
	if _, err := m.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility}); err != nil {
		t.Fatal(err)
	}
	view, err := m.readMemTrafficHostAnalysisLocked(t.Context(), account.ID, memTrafficPolicyChange{})
	if err != nil || len(view.Domains) != 2 {
		t.Fatalf("ordinary verified domain metadata: %+v err=%v", view.Domains, err)
	}
}
