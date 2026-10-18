package mcpcatalog

import (
	"context"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type environmentObserverStore struct {
	*state.MemStore
	deployment string
}

func (s *environmentObserverStore) ResolveProjectRelease(context.Context, string, string, string) (string, string, error) {
	return "", s.deployment, nil
}

func TestEnvironmentCatalogObserverTarget(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "staging"}[available], func(t *testing.T) {
			ctx := t.Context()
			store := &environmentObserverStore{MemStore: state.NewMemStore()}
			account, e := store.CreateAccount(ctx, "environment-observer@example.com", api.PlanPro)
			if e != nil {
				t.Fatal(e)
			}
			app, e := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "environment-observer", Type: state.AppTypeApp, Status: state.AppActive})
			if e != nil {
				t.Fatal(e)
			}
			dep, e := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:staging"})
			if e != nil {
				t.Fatal(e)
			}
			if e = store.MarkDeploymentLive(ctx, dep.ID); e != nil {
				t.Fatal(e)
			}
			store.deployment = dep.ID
			baseline, e := mcphosting.NewCatalogContract(api.MCPProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{{Name: "reviewed", InputSchema: map[string]any{"type": "object"}}}})
			if e != nil {
				t.Fatal(e)
			}
			raw, e := mcphosting.MarshalContract(baseline)
			if e != nil {
				t.Fatal(e)
			}
			cfg := api.MCPCatalogMonitorConfig{Scope: "staging", Role: "staging-reader", Revision: uuid.NewString(), Enabled: true, Baseline: raw, BaselineSHA256: digest(raw), FreshnessSeconds: 300, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Endpoint: "/mcp", SecretRef: "secret:TOKEN"}}
			if _, e = store.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); e != nil {
				t.Fatal(e)
			}
			identity, e := age.GenerateX25519Identity()
			if e != nil {
				t.Fatal(e)
			}
			sealed, e := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"TOKEN": "STAGING_ONLY"})
			if e != nil {
				t.Fatal(e)
			}
			if e = store.UpsertAppSecretInScope(ctx, account.ID, app.ID, "staging", "TOKEN", sealed); e != nil {
				t.Fatal(e)
			}
			unsealed, probed := 0, 0
			runner := ObserverRunner{Store: store, Domain: "apps.example", Identities: func(context.Context) []*age.X25519Identity { unsealed++; return []*age.X25519Identity{identity} }, Endpoint: func(_ context.Context, a state.App, d state.Deployment) (string, error) {
				if a.ID != app.ID || d.ID != dep.ID || d.Scope != "staging" {
					t.Fatal("wrong environment resolved")
				}
				if !available {
					return "", state.ErrConflict
				}
				return "https://staging-preview.example", nil
			}, Probe: func(_ context.Context, target, token, _ string) ([]byte, error) {
				probed++
				if target != "https://staging-preview.example/mcp" || token != "STAGING_ONLY" {
					t.Fatal("credential routed outside environment", target)
				}
				return raw, nil
			}}
			if e = runner.Tick(ctx); e != nil {
				t.Fatal(e)
			}
			root, e := store.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
			if e != nil {
				t.Fatal(e)
			}
			view, _ := api.CatalogMonitorRoleSnapshot(root, cfg.Role)
			if available {
				if unsealed != 1 || probed != 1 || api.CatalogMonitorSignal(view, app.Slug, time.Now()).State != "healthy" {
					t.Fatal("scoped observation failed", view.ObserverStatus)
				}
			} else if unsealed != 0 || probed != 0 || view.ObserverStatus.FailureReason != "source_unavailable" {
				t.Fatal("unavailable environment accessed credentials")
			}
		})
	}
}
