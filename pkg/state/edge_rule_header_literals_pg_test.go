//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261108110000 — amendment 2026-11-09: a gateway rolled back to a
// release that predates sealed header values registers a new boot without
// resetting the column it does not know, and stops counting as able.
func TestPgGatewayEdgeHeaderSecretsFollowTheBoot(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	node := createTrafficObservationNode(t, store)
	epoch, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, uuid.NewString(), 0)
	if err != nil {
		t.Fatal(err)
	}
	features := state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local", EdgeHeaderSecrets: true}
	if err := store.ReportGatewayTrafficRuntime(ctx, epoch, features); err != nil {
		t.Fatal(err)
	}
	if !findTrafficObservation(t, store, node.Name).EdgeHeaderSecrets {
		t.Fatal("a reported capability does not read back")
	}
	// What an N-1 gatewayd-internal does on start: a new boot and a report,
	// both written without the column.
	if _, err := pool.Exec(ctx, `UPDATE gateway_traffic_runtime_observations
		SET boot_id = $2, generation = nextval(pg_get_serial_sequence('gateway_traffic_runtime_observations', 'generation')), reported_at = clock_timestamp()
		WHERE node_name = $1`, node.Name, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if findTrafficObservation(t, store, node.Name).EdgeHeaderSecrets {
		t.Fatal("a gateway rolled back to an older release still counts as opening sealed values")
	}
}

// adr: 20261108110000 — amendment 2026-11-09: the backfill scans find rules
// and versions storing a request header value as written, and a version is
// rewritten in place with the digest the version trigger computes.
func TestPgStore_EdgeRuleHeaderLiterals(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "header-literals")
	params := pgSampleEdgeRuleParams(acct, app, "literals.example.com")
	params.Kind = state.EdgeRuleKindHeaders
	params.Action = state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "Authorization", Action: "set", Value: "Bearer legacy"}},
	}}
	rule, err := s.CreateEdgeRule(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	sealedParams := params
	sealedParams.MatchPath = "/sealed"
	sealedParams.Action = state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "Authorization", Action: "set", Secret: true, SecretID: uuid.NewString()}},
	}}
	if _, err := s.CreateEdgeRule(ctx, sealedParams); err != nil {
		t.Fatal(err)
	}

	var found *state.EdgeRuleActionRef
	for after := ""; ; {
		page, err := s.ListEdgeRulesWithRequestHeaderLiterals(ctx, after, 500)
		if err != nil {
			t.Fatal(err)
		}
		for i := range page {
			if page[i].ID == rule.ID {
				found = &page[i]
			}
			if page[i].Action.Headers != nil && len(page[i].Action.Headers.RequestHeaders) > 0 && page[i].Action.Headers.RequestHeaders[0].SecretID != "" {
				t.Fatalf("a sealed rule was listed: %+v", page[i])
			}
		}
		if len(page) < 500 {
			break
		}
		after = page[len(page)-1].ID
	}
	if found == nil || found.Action.Headers.RequestHeaders[0].Value != "Bearer legacy" {
		t.Fatalf("legacy rule not listed: %+v", found)
	}

	var version *state.EdgeRuleSetVersionSnapshot
	for after := int64(0); ; {
		page, err := s.ListEdgeRuleSetVersionsWithRequestHeaderLiterals(ctx, after, 500)
		if err != nil {
			t.Fatal(err)
		}
		for i := range page {
			if page[i].AppID == app {
				version = &page[i]
			}
		}
		if len(page) < 500 {
			break
		}
		after = page[len(page)-1].Cursor
	}
	if version == nil {
		t.Fatal("no version of the app listed")
	}
	replacement := state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "Authorization", Action: "set", Secret: true, SecretID: uuid.NewString()}},
	}}
	ok, err := s.ReplaceEdgeRuleSetVersionActions(ctx, *version, map[string]state.EdgeRuleAction{rule.ID: replacement})
	if err != nil || !ok {
		t.Fatalf("replace = %v, %v", ok, err)
	}
	if ok, err := s.ReplaceEdgeRuleSetVersionActions(ctx, *version, map[string]state.EdgeRuleAction{rule.ID: replacement}); err != nil || ok {
		t.Fatalf("stale replace = %v, %v; want it refused", ok, err)
	}
	got, err := s.GetEdgeRuleSetVersion(ctx, app, version.Version)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got.Rules {
		if r.ID == rule.ID && (r.Action.Headers.RequestHeaders[0].Value != "" || r.Action.Headers.RequestHeaders[0].SecretID != replacement.Headers.RequestHeaders[0].SecretID) {
			t.Fatalf("version rule = %+v", r.Action.Headers.RequestHeaders)
		}
		if r.ID != rule.ID && r.MatchPath != "/sealed" {
			t.Fatalf("unexpected rule %+v", r)
		}
	}
	var consistent bool
	if err := pool.QueryRow(ctx, `SELECT rules_sha256 = encode(sha256(convert_to(rules::text, 'UTF8')), 'hex')
		FROM edge_rule_set_versions WHERE app_id = $1 AND version = $2`, app, version.Version).Scan(&consistent); err != nil || !consistent {
		t.Fatalf("digest matches the trigger's: %v, %v", consistent, err)
	}
}
