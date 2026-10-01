// adr: 375
package state

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var trafficDomainRemovalModes = []string{"exact", "unverified", "wildcard", "scoped", "global-route", "target-policy", "still-shadowed", "already-selected", "namespace"}
var trafficDomainRemovalForms = []string{"plain", "activity", "owned", "owned_activity"}

func testTrafficDomainRemoval(t *testing.T, store Store, app App, peerAccount Account, peer App, mode, form string, seed func(EdgeRule), countOutbox func() int) {
	t.Helper()
	removed, fallback, host := "api.example.test", "*.example.test", "api.example.test"
	if mode == "wildcard" || mode == "still-shadowed" {
		removed, host = "*.api.example.test", "nested.api.example.test"
	} else if mode == "already-selected" {
		removed, fallback, host = "*.example.test", "*.api.example.test", "nested.api.example.test"
	} else if mode == "namespace" {
		removed, host = "missing.gregale.dev", "missing.gregale.dev"
	}
	if _, err := store.CreateCustomDomain(t.Context(), removed, app.ID, "private-original-token"); err != nil {
		t.Fatal(err)
	}
	if mode != "unverified" {
		if err := store.MarkDomainVerified(t.Context(), removed); err != nil {
			t.Fatal(err)
		}
	}
	if mode != "global-route" && mode != "target-policy" && mode != "namespace" {
		if mode == "scoped" {
			project, err := store.CreateProject(t.Context(), Project{AccountID: peerAccount.ID, Slug: "removal-scoped", ScanSource: ProjectScanSourceCompose})
			if err != nil {
				t.Fatal(err)
			}
			peer, err = store.CreateApp(t.Context(), App{AccountID: peerAccount.ID, ProjectID: project.ID, WorkloadName: "web", Slug: "removal-scoped-web", Status: AppActive})
			if err != nil {
				t.Fatal(err)
			}
			environment, err := store.ProjectEnvironmentBySlug(t.Context(), peerAccount.ID, project.ID, "production")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.(interface {
				CreateCustomDomainInEnvironmentIfUnderQuota(context.Context, string, string, string, string, int, int) (CustomDomain, error)
			}).CreateCustomDomainInEnvironmentIfUnderQuota(t.Context(), fallback, peer.ID, environment.ID, "fallback-token", 100, 500); err != nil {
				t.Fatal(err)
			}
		} else if _, err := store.CreateCustomDomain(t.Context(), fallback, peer.ID, "fallback-token"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDomainVerified(t.Context(), fallback); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "still-shadowed" {
		// An unverified exact claim continues to block the wildcard after
		// the narrower wildcard is removed.
		if _, err := store.CreateCustomDomain(t.Context(), host, app.ID, "blocking-token"); err != nil {
			t.Fatal(err)
		}
	}
	defaults := store.(interface {
		SetDefaultCustomDomain(context.Context, string, string) error
		DefaultCustomDomain(context.Context, string) (string, error)
	})
	hasDefault := mode != "unverified" && !IsWildcardCustomDomain(removed)
	if hasDefault {
		if err := defaults.SetDefaultCustomDomain(t.Context(), app.ID, removed); err != nil {
			t.Fatal(err)
		}
	}
	legacy := memTrafficRule(peerAccount, peer, host, 520)
	if mode == "target-policy" {
		// Discovery itself fits; its newly selected account policy does not.
		if _, err := store.CreateEdgeRule(t.Context(), memTrafficRule(peerAccount, peer, host, 0)); err != nil {
			t.Fatal(err)
		}
		legacy.Kind, legacy.Action.Kind, legacy.Action.Route = EdgeRuleKindHeaders, EdgeRuleKindHeaders, nil
		legacy.Action.Headers = &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Private", Action: "set", Value: "private-policy"}}}
	}
	rule := EdgeRule{ID: uuid.NewString(), AccountID: peerAccount.ID, AppID: peer.ID, MatchHost: host, MatchPath: "/", Enabled: true, Kind: legacy.Kind, Action: legacy.Action}
	seed(rule)
	before, err := store.DomainByName(t.Context(), removed)
	if err != nil {
		t.Fatal(err)
	}
	activity := OrgActivity{OrgID: uuid.New(), Kind: "domain.removed", ActorType: OrgActivityActorSystem,
		ActorLabel: "removal-test", ResourceType: "domain", ResourceID: removed, ResourceLabel: removed,
		SourceType: "domain.removed", SourceID: "removal-" + form}
	apply := func() (int64, error) {
		switch form {
		case "plain":
			return 0, store.DeleteCustomDomain(t.Context(), removed)
		case "activity":
			return store.(OrgActivityDomainMutationStore).DeleteCustomDomainWithActivity(t.Context(), removed, activity)
		case "owned":
			return 0, store.(CustomDomainRemovalOwnerStore).DeleteCustomDomainForApp(t.Context(), removed, app.ID)
		default:
			return store.(CustomDomainRemovalOwnerStore).DeleteCustomDomainForAppWithActivity(t.Context(), removed, app.ID, activity)
		}
	}
	outboxBefore := countOutbox()
	allowed := mode == "still-shadowed" || mode == "already-selected" || mode == "namespace"
	id, err := apply()
	if !allowed {
		var aggregate *TrafficPolicyAggregateError
		if id != 0 || !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
			t.Fatalf("removal exposed an overloaded policy: outbox=%d err=%v", id, err)
		}
		if after, err := store.DomainByName(t.Context(), removed); err != nil || !reflect.DeepEqual(before, after) || countOutbox() != outboxBefore {
			t.Fatalf("refused removal changed domain or activity: domain=%q err=%v", after.Domain, err)
		}
		if hasDefault {
			if selected, err := defaults.DefaultCustomDomain(t.Context(), app.ID); err != nil || selected != removed {
				t.Fatalf("refusal changed default selection: %q/%v", selected, err)
			}
		}
		if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
			t.Fatal(err)
		}
		id, err = apply()
	}
	if err != nil || strings.Contains(form, "activity") != (id > 0) {
		t.Fatalf("removal/repair failed: outbox=%d err=%v", id, err)
	}
	if _, err := store.DomainByName(t.Context(), removed); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removal retained its claim: %v", err)
	}
	wantOutbox := outboxBefore
	if strings.Contains(form, "activity") {
		wantOutbox++
	}
	if countOutbox() != wantOutbox {
		t.Fatal("removal activity was not atomic")
	}
	if hasDefault {
		if selected, err := defaults.DefaultCustomDomain(t.Context(), app.ID); selected != "" || err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatalf("removed default remained selected: %q/%v", selected, err)
		}
	}
}

func TestMemTrafficDomainRemovalRefusalRepairAndReservationPrecedence(t *testing.T) {
	for _, mode := range trafficDomainRemovalModes {
		for _, form := range trafficDomainRemovalForms {
			t.Run(mode+"/"+form, func(t *testing.T) {
				m, _, _, app, _ := memTrafficFixture(t)
				peerAccount, err := m.CreateAccount(t.Context(), "removal-peer@example.test", api.PlanScale)
				if err != nil {
					t.Fatal(err)
				}
				peer, err := m.CreateApp(t.Context(), App{AccountID: peerAccount.ID, Slug: "removal-peer", Status: AppActive})
				if err != nil {
					t.Fatal(err)
				}
				testTrafficDomainRemoval(t, m, app, peerAccount, peer, mode, form,
					func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() int { return len(m.orgActivityOutbox) })
			})
		}
	}
}

func TestMemTrafficDomainRemovalExpectedOwnerAndCancellation(t *testing.T) {
	m, _, _, app, _ := memTrafficFixture(t)
	if _, err := m.CreateCustomDomain(t.Context(), "owner.example.test", app.ID, "private-token"); err != nil {
		t.Fatal(err)
	}
	before := m.domains["owner.example.test"]
	if err := m.DeleteCustomDomainForApp(t.Context(), before.Domain, "wrong-app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale authorization removed a claim: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.DeleteCustomDomainForApp(ctx, before.Domain, app.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled removal did not refuse: %v", err)
	}
	if !reflect.DeepEqual(before, m.domains[before.Domain]) || len(m.orgActivityOutbox) != 0 {
		t.Fatal("stale/canceled removal changed intent")
	}
}
