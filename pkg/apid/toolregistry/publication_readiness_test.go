package toolregistry_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestReadinessPublicationMemory(t *testing.T) {
	for _, version := range []string{mcphosting.ProtocolVersion, mcphosting.LegacyProtocolVersion} {
		t.Run(version, func(t *testing.T) { exerciseReadinessPublication(t, state.NewMemStore(), version) })
	}
}
func TestReadinessPublicationPostgres(t *testing.T) {
	for _, version := range []string{mcphosting.ProtocolVersion, mcphosting.LegacyProtocolVersion} {
		t.Run(version, func(t *testing.T) { exerciseReadinessPublication(t, state.NewPgStore(pgtest.OpenMigrated(t)), version) })
	}
}

func exerciseReadinessPublication(t *testing.T, store state.Store, version string) {
	p := granttest.NewPublication(t, store)
	policy := granttest.ConfigurePublicationReadiness(t, p, version)
	v, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
	if err != nil || v.Status != "pending" || p.ReadinessCalls.Load() != 0 || p.Calls.Load() != 0 || !toolpublication.ValidID(v.ReadinessArtifactSHA256) {
		t.Fatalf("readiness review performed effects: %+v %v", v, err)
	}
	_, err = p.Owner.DecideToolPublicationVerification(t.Context(), p.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: v.RequestSHA256, CredentialSecretRef: "secret:MCP_PUBLICATION_TEST"})
	requireGrantStatus(t, err, 400)
	p.Approve(t)
	done := p.Wait(t, "completed")
	if done.Outcome == nil || len(done.Outcome.Readiness) != 2 || p.Calls.Load() != 1 || p.ReadinessCalls.Load() == 0 || p.ReadinessToolCalls.Load() != 0 {
		t.Fatalf("separate readiness and fixture authority failed: %+v", done)
	}
	record, err := p.Publisher.GetToolPublication(t.Context(), p.Publication.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(record)
	for _, secret := range []string{"MCP_READINESS_", "readiness-reader-token", "readiness-writer-token", "MCP_PUBLICATION_TEST"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("publication exposed readiness credentials or references")
		}
	}
	expected := toolpublication.ReadinessExpectation(done)
	expected.PolicyRevision = policy.Revision
	ctx := state.WithMCPReadinessExpectation(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, expected)
	if _, err = store.UpdateDeploymentTraffic(ctx, p.Candidate.ID, 100, p.Publication.Intent.ExpectedServingDeploymentID); err != nil {
		t.Fatal("fresh role observations did not permit guarded promotion", err)
	}
	_, err = p.Publisher.FinishToolPublication(t.Context(), p.Publication.ID, api.ToolPublicationOutcome{Status: "completed", DeploymentID: p.Candidate.ID, CatalogSHA256: p.Request.CatalogSHA256, Catalog: done.Outcome.Catalog, ProtocolVerified: true, Checks: done.Outcome.Checks})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Base.Agent.GetRegistryTool(t.Context(), p.Publication.ID, "visible")
	requireGrantStatus(t, err, 404)
	for range 2 {
		_, err = p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
		if err != nil {
			t.Fatal(err)
		}
	}
	if p.Calls.Load() != 1 || p.ReadinessToolCalls.Load() != 0 {
		t.Fatal("retained verification replayed fixture effects")
	}
}

func TestReadinessPublicationBlocksChangedAuthority(t *testing.T) {
	for _, mode := range []string{"policy", "disabled policy", "rotated credential"} {
		t.Run(mode, func(t *testing.T) {
			store := state.NewMemStore()
			p := granttest.NewPublication(t, store)
			policy := granttest.ConfigurePublicationReadiness(t, p, mcphosting.ProtocolVersion)
			p.Approve(t)
			if mode == "rotated credential" {
				cipher, err := secretbox.SealOne(p.Identity.Recipient(), "MCP_READINESS_READER", "replaced-reader-token", api.MCPCatalogOAuthMaxTokenBytes)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.UpsertAppSecretWithKidInScope(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, api.DefaultEnvScope, "MCP_READINESS_READER", p.Identity.Recipient().String(), cipher); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := store.SetMCPReadinessPolicy(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, readinessPolicyRequest(policy, mode != "disabled policy"))
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
			requireGrantStatus(t, err, 409)
			if p.Calls.Load() != 0 || p.ReadinessCalls.Load() != 0 {
				t.Fatal("changed approval dispatched credentials")
			}
		})
	}
}

func readinessPolicyRequest(p api.MCPReadinessPolicy, enabled bool) api.SetMCPReadinessPolicyRequest {
	return api.SetMCPReadinessPolicyRequest{ExpectedRevision: p.Revision, Enabled: enabled, Endpoint: p.Endpoint, MaxAgeSeconds: p.MaxAgeSeconds, Roles: p.Roles, Reason: "changed readiness contract"}
}

func TestReadinessPublicationFailureAndMidFixturePolicyChange(t *testing.T) {
	for _, mode := range []string{"failed role", "policy during fixture", "reflected role token"} {
		t.Run(mode, func(t *testing.T) {
			store := state.NewMemStore()
			p := granttest.NewPublication(t, store)
			policy := granttest.ConfigurePublicationReadiness(t, p, mcphosting.ProtocolVersion)
			switch mode {
			case "failed role":
				p.ReadinessDrift.Store(true)
			case "reflected role token":
				p.CatalogReflection.Store("readiness-reader-token")
			case "policy during fixture":
				var once sync.Once
				p.OnFixture.Store(func() {
					once.Do(func() {
						if _, err := store.SetMCPReadinessPolicy(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, readinessPolicyRequest(policy, true)); err != nil {
							t.Error(err)
						}
					})
				})
			}
			p.Approve(t)
			done := p.Wait(t, "failed")
			want := int32(0)
			if mode == "policy during fixture" {
				want = 1
			}
			if done.Outcome == nil || done.Outcome.OK || len(done.Outcome.Catalog) != 0 || p.Calls.Load() != want || p.ReadinessToolCalls.Load() != 0 {
				t.Fatal("failed role checks registered a catalog or replayed fixtures")
			}
			dep, err := store.DeploymentByID(t.Context(), p.Candidate.ID)
			if err != nil || dep.TrafficPercent != 0 {
				t.Fatal("failed candidate was promoted", err)
			}
		})
	}
}

func TestReadinessPublicationAtomicPromotionIgnoresOverrides(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if pg {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			p := granttest.NewPublication(t, store)
			policy := granttest.ConfigurePublicationReadiness(t, p, mcphosting.ProtocolVersion)
			p.Approve(t)
			done := p.Wait(t, "completed")
			expected := toolpublication.ReadinessExpectation(done)
			expected.PolicyRevision = policy.Revision
			readiness := store.(state.MCPReadinessStore)
			subject, err := readiness.MCPReadinessSubject(t.Context(), p.Base.Account.ID, p.Candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = readiness.RecordMCPReadiness(t.Context(), subject, api.MCPReadinessEvidence{Role: "reader", Passed: false}); err != nil {
				t.Fatal(err)
			}
			if _, err = readiness.CreateMCPReadinessOverride(t.Context(), p.Base.Account.ID, p.Candidate.ID, api.MCPReadinessOverrideRequest{ExpectedRevision: policy.Revision, Reason: "explicit recovery for another operation"}); err != nil {
				t.Fatal(err)
			}
			ctx := state.WithMCPReadinessExpectation(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, expected)
			if _, err = store.UpdateDeploymentTraffic(ctx, p.Candidate.ID, 100, p.Publication.Intent.ExpectedServingDeploymentID); err == nil {
				t.Fatal("an override bypassed the publication's exact role observations")
			}
			dep, err := store.DeploymentByID(t.Context(), p.Candidate.ID)
			if err != nil || dep.TrafficPercent != 0 || p.Calls.Load() != 1 {
				t.Fatal("failed promotion changed traffic or fixture count", err)
			}
		})
	}
}

func TestReadinessPublicationManagedRole(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, mode := range []string{"cached", "refresh", "lost"} {
			t.Run(map[bool]string{false: "memory", true: "postgres"}[pg]+"/"+mode, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if pg {
					store = state.NewPgStore(pgtest.OpenMigrated(t))
				}
				p := granttest.NewPublication(t, store)
				policy := granttest.ConfigurePublicationReadiness(t, p, mcphosting.ProtocolVersion)
				exchanges := granttest.ConfigurePublicationReadinessOAuth(t, p, policy, mode)
				p.Approve(t)
				if exchanges.Load() != 0 {
					t.Fatal("approval refreshed a readiness grant")
				}
				want := "completed"
				if mode == "lost" {
					want = "failed"
				}
				p.Wait(t, want)
				calls := int32(1)
				if mode == "lost" {
					calls = 0
				}
				refreshes := int32(1)
				if mode == "cached" {
					refreshes = 0
				}
				if p.Calls.Load() != calls || p.ReadinessToolCalls.Load() != 0 || exchanges.Load() != refreshes {
					t.Fatal("readiness OAuth reused fixture authority or replayed a renewal")
				}
				for range 2 {
					if _, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request); err != nil {
						t.Fatal(err)
					}
				}
				if exchanges.Load() != refreshes || p.Calls.Load() != calls {
					t.Fatal("retained verification repeated effects")
				}
			})
		}
	}
}
