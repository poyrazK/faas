//go:build !no_pg

// adr: 570
package state

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficPrimaryMetadataNamespaceAndBounds(t *testing.T) {
	store, pool, account, original := trafficHostPGFixture(t)
	environmentHost := hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix, uuid.NewString(), uuid.NewString())
	environmentSlug, _ := hostidentity.AppSlugFromHost(hostidentity.DeployWildcardSuffix, environmentHost)
	for _, app := range []App{
		{Slug: "deploy-42-shadow", Status: AppActive},
		{Slug: environmentSlug, Status: AppActive},
		{Slug: "internal-primary", Status: AppActive, Visibility: api.AppVisibilityInternal},
		{Slug: "deleted-primary", Status: AppDeleted},
	} {
		app.AccountID = account.ID
		if _, err := store.CreateApp(t.Context(), app); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ name, domain string }{
		{"default", hostidentity.DefaultAppsDomain}, {"custom", "apps.example.test"}, {"disabled", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			suffix := hostidentity.AppsSuffix(test.domain)
			view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), suffix)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, slug := range []string{original.Slug, "deploy-42-shadow", environmentSlug} {
				if host := hostidentity.BuildPrimaryAppHost(suffix, slug); host != "" {
					want = append(want, host)
				}
			}
			sort.Strings(want)
			if !reflect.DeepEqual(view.PrimaryHosts, want) {
				t.Fatalf("primary hosts=%v want=%v", view.PrimaryHosts, want)
			}
			global, err := readTrafficHostAnalysis(t.Context(), tx, pgtype.UUID{}, suffix)
			if err != nil || len(global.PrimaryHosts) != 0 {
				t.Fatalf("owned primary hosts leaked into global route analysis: %+v err=%v", global, err)
			}
		})
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	defaults, err := trafficHostActionDefaults()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		inputs int32
		bytes  int64
	}{
		{"inputs", 1, api.TrafficPolicyMaxAnalysisMetadataBytes}, {"bytes", api.TrafficPolicyMaxAnalysisInputs, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			row, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), tx, sqlc.ReadTrafficHostAnalysisParams{
				AccountID: uuidToPgtype(account.ID), AppsSuffix: ".apps.example.test", MaxInputs: test.inputs, MaxBytes: test.bytes, Defaults: defaults,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(row.Data) != 0 || (row.Inputs <= int64(test.inputs) && row.Bytes <= test.bytes) {
				t.Fatalf("primary metadata escaped scalar bounds: %+v", row)
			}
		})
	}
}

func TestPgTrafficPrimaryActivationIgnoresUnservedNamespaces(t *testing.T) {
	for _, test := range []struct{ name, domain, slug string }{
		{"custom-domain", "apps.example.test", "primary-other-namespace"},
		{"disabled", "", "primary-disabled"},
		{"immutable-deployment", "gregale.dev", "deploy-42-primary"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, pool, account, source := trafficHostPGFixture(t)
			store := NewPgStore(pool, WithTrafficAppsDomain(test.domain))
			action := EdgeRuleAction{Kind: EdgeRuleKindRoute, Route: &EdgeRuleRouteAction{TargetAppSlug: source.Slug},
				Validate: &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 519) + "1e130000]")}}
			encoded, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,'/',true,'route',$4::jsonb)`, account.ID, source.ID, test.slug+".gregale.dev", encoded); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: test.slug, Status: AppActive}); err != nil {
				t.Fatalf("unserved ordinary URL acquired a primary activation baseline: %v", err)
			}
		})
	}
}
