package toolregistry_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestManagedPublicationMemory(t *testing.T) { exerciseManagedPublication(t, state.NewMemStore()) }
func TestManagedPublicationPostgres(t *testing.T) {
	exerciseManagedPublication(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exerciseManagedPublication(t *testing.T, store state.Store) {
	m := granttest.NewManagedPublication(t, store)
	p := m.Publication
	m.ExpiresIn.Store(1)
	pending, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
	if err != nil || pending.Status != "pending" || m.Exchanges.Load() != 0 || p.Calls.Load() != 0 {
		t.Fatal("review renewed or executed without owner approval", err)
	}
	_, err = p.Publisher.DecideToolPublicationVerification(t.Context(), p.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: pending.RequestSHA256, CredentialConnectionID: m.Connection.ID})
	requireGrantStatus(t, err, 404)
	_, err = p.Owner.DecideToolPublicationVerification(t.Context(), p.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: pending.RequestSHA256, CredentialConnectionID: m.Connection.ID, CredentialSecretRef: "secret:MCP_PUBLICATION_TEST"})
	requireGrantStatus(t, err, 400)
	m.Approve(t)
	if m.Exchanges.Load() != 0 {
		t.Fatal("approval performed a provider exchange")
	}
	p.Wait(t, "completed")
	// Let the first access token actually expire, then approve a distinct review.
	time.Sleep(1100 * time.Millisecond)
	cached, err := p.Owner.GetToolOAuthConnection(t.Context(), m.Connection.ID)
	if err != nil || cached.ExpiresAt == nil || cached.ExpiresAt.After(time.Now()) {
		t.Fatal("setup token did not expire", err)
	}
	newManagedPublicationReview(t, m)
	m.ExpiresIn.Store(3600)
	m.Approve(t)
	done := p.Wait(t, "completed")
	if m.Exchanges.Load() != 2 || p.Calls.Load() != 2 || done.Outcome == nil || !done.Outcome.OK {
		t.Fatalf("expired credential did not renew once: exchanges=%d calls=%d", m.Exchanges.Load(), p.Calls.Load())
	}
	for range 2 {
		retained, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
		if err != nil || retained.Status != "completed" {
			t.Fatal("retained verification", err)
		}
	}
	public, err := p.Publisher.GetToolPublication(t.Context(), p.Publication.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(public)
	for _, forbidden := range []string{m.Connection.ID, "publication-access-", "publication-refresh-", "MCP_PUBLICATION_REFRESH", "credentials_ciphertext", "lease_id", m.Provider.URL} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("publisher received private connection material: %s", forbidden)
		}
	}
	internal, err := store.(state.ToolOAuthConnectionStore).GetToolOAuthConnection(t.Context(), p.Base.Account.ID, m.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = secretbox.OpenBytesMulti([]*age.X25519Identity{p.Identity}, internal.Ciphertext); err == nil {
		t.Fatal("app identity opened managed credentials")
	}
	if _, _, err = secretbox.OpenBytesMulti([]*age.X25519Identity{m.AccountIdentity}, internal.Ciphertext); err != nil {
		t.Fatal("account identity could not open managed credentials", err)
	}
	if _, err = store.UpdateDeploymentTraffic(t.Context(), p.Candidate.ID, 100, p.Publication.Intent.ExpectedServingDeploymentID); err != nil {
		t.Fatal(err)
	}
	_, err = p.Publisher.FinishToolPublication(t.Context(), p.Publication.ID, api.ToolPublicationOutcome{Status: "completed", DeploymentID: p.Candidate.ID, CatalogSHA256: p.Request.CatalogSHA256, Catalog: done.Outcome.Catalog, ProtocolVerified: true, Checks: done.Outcome.Checks})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Base.Agent.GetRegistryTool(t.Context(), p.Publication.ID, "visible")
	requireGrantStatus(t, err, 404)
	g, err := p.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: p.Base.AgentKey.ID, Version: p.Publication.ID, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour), AllowInvocation: true, CredentialConnectionID: m.Connection.ID})
	if err != nil {
		t.Fatal(err)
	}
	in := api.PrivateToolInvocationRequest{GrantID: g.ID, ArgumentsJSON: `{}`, Intent: api.ToolInvocationIntent{Version: p.Publication.ID, Name: "visible", ArgumentsSHA256: toolpublication.Digest([]byte(`{}`)), AccessMode: "external-oauth", CredentialBindingSHA256: g.Credential.SHA256, TimeoutSeconds: 15, AllowToolExecution: true}}
	id := uuid.NewString()
	if _, err = p.Base.Agent.ExecutePrivateToolInvocation(t.Context(), id, in); err != nil {
		t.Fatal(err)
	}
	waitPrivate(t, p.Base.Agent, id, "completed")
	if m.Exchanges.Load() != 2 || p.Calls.Load() != 3 {
		t.Fatal("invocation failed to reuse the renewed token")
	}
	if _, err = p.Owner.RevokeToolGrant(t.Context(), g.ID); err != nil {
		t.Fatal(err)
	}
	_, err = p.Base.Agent.ExecutePrivateToolInvocation(t.Context(), uuid.NewString(), in)
	requireGrantStatus(t, err, 404)
	if _, err = p.Owner.RevokeToolOAuthConnection(t.Context(), m.Connection.ID); err != nil {
		t.Fatal(err)
	}
	if p.Calls.Load() != 3 {
		t.Fatal("revocation dispatched a tool")
	}
}

func newManagedPublicationReview(t *testing.T, m *granttest.ManagedPublicationFixture) {
	t.Helper()
	p := m.Publication
	id := strings.ReplaceAll(uuid.NewString(), "-", "") + strings.ReplaceAll(uuid.NewString(), "-", "")
	out, err := p.Publisher.BeginToolPublication(t.Context(), id, p.Publication.Intent)
	if err != nil || !out.Created {
		t.Fatal("new explicit publication review", err)
	}
	p.Publication = out.Record
}

func TestManagedPublicationRefreshFailures(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, mode := range []int32{1, 2, 3} {
			t.Run(map[bool]string{false: "memory", true: "postgres"}[pg]+string(rune('0'+mode)), func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if pg {
					store = state.NewPgStore(pgtest.OpenMigrated(t))
				}
				m := granttest.NewManagedPublication(t, store)
				p := m.Publication
				m.Mode.Store(mode)
				m.Approve(t)
				done := p.Wait(t, "failed")
				if done.Outcome == nil || done.Outcome.Code != "reconnect_required" || p.Calls.Load() != 0 || m.Exchanges.Load() != 1 {
					t.Fatalf("failed refresh did not fence fixtures: %+v", done)
				}
				for range 2 {
					_, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
					if err != nil {
						t.Fatal(err)
					}
				}
				c, err := p.Owner.GetToolOAuthConnection(t.Context(), m.Connection.ID)
				if err != nil || c.State != "reconnect_required" || m.Exchanges.Load() != 1 {
					t.Fatal("uncertain refresh was replayed", err)
				}
			})
		}
	}
}

func TestManagedPublicationApprovalBinding(t *testing.T) {
	for _, mismatch := range []string{"issuer", "resource", "scopes", "guidance pending"} {
		t.Run(mismatch, func(t *testing.T) {
			m := granttest.NewManagedPublication(t, state.NewMemStore())
			p := m.Publication
			in := m.Connection.Intent
			switch mismatch {
			case "issuer":
				in.Issuer = "https://other-issuer.example"
			case "resource":
				in.Resource = "https://other-resource.example/mcp"
			case "scopes":
				in.Scopes = []string{"tool:other"}
			case "guidance pending":
				in.Guided, in.AuthorizationEndpoint, in.RefreshTokenSecretRef = true, m.Provider.URL+"/authorize", ""
			}
			c, err := p.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), in)
			if err != nil {
				t.Fatal(err)
			}
			v, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Owner.DecideToolPublicationVerification(t.Context(), p.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: v.RequestSHA256, CredentialConnectionID: c.ID})
			if err == nil || m.Exchanges.Load() != 0 || p.Calls.Load() != 0 {
				t.Fatal("unreviewed connection gained publication authority")
			}
		})
	}
}

func TestManagedPublicationSecretReflection(t *testing.T) {
	for _, value := range []string{"publication-access-1", "publication-refresh-initial", "publication-refresh-1"} {
		t.Run(value, func(t *testing.T) {
			m := granttest.NewManagedPublication(t, state.NewMemStore())
			p := m.Publication
			p.CatalogReflection.Store(value)
			m.Approve(t)
			done := p.Wait(t, "failed")
			raw, _ := json.Marshal(done)
			if done.Outcome == nil || len(done.Outcome.Catalog) != 0 || strings.Contains(string(raw), value) || p.Calls.Load() != 0 {
				t.Fatal("reflected managed credential reached fixtures or retained evidence")
			}
		})
	}
}

func TestManagedPublicationUnreadableSourceRequiresReconnect(t *testing.T) {
	m := granttest.NewManagedPublication(t, state.NewMemStore())
	p := m.Publication
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secretbox.SealOne(other.Recipient(), "MCP_PUBLICATION_REFRESH", "publication-refresh-initial", api.MCPCatalogOAuthMaxTokenBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Base.Store.UpsertAppSecretWithKidInScope(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, api.DefaultEnvScope, "MCP_PUBLICATION_REFRESH", other.Recipient().String(), cipher); err != nil {
		t.Fatal(err)
	}
	m.Connection, err = p.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), m.Connection.Intent)
	if err != nil {
		t.Fatal(err)
	}
	m.Approve(t)
	done := p.Wait(t, "failed")
	c, err := p.Owner.GetToolOAuthConnection(t.Context(), m.Connection.ID)
	if err != nil || c.State != "reconnect_required" || done.Outcome == nil || done.Outcome.Code != "reconnect_required" || m.Exchanges.Load() != 0 || p.Calls.Load() != 0 {
		t.Fatal("unreadable source left a renewable lease or dispatched credentials", err)
	}
}

func TestManagedPublicationRevocationDuringRefresh(t *testing.T) {
	for _, connection := range []bool{false, true} {
		t.Run(map[bool]string{false: "approval", true: "connection"}[connection], func(t *testing.T) {
			m := granttest.NewManagedPublication(t, state.NewMemStore())
			p := m.Publication
			m.Mode.Store(4)
			v := m.Approve(t)
			if _, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request); err != nil {
				t.Fatal(err)
			}
			select {
			case <-m.Started:
			case <-time.After(5 * time.Second):
				t.Fatal("refresh did not start")
			}
			if connection {
				if _, err := p.Owner.RevokeToolOAuthConnection(t.Context(), m.Connection.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := p.Owner.DecideToolPublicationVerification(t.Context(), p.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: v.RequestSHA256, Revoke: true}); err != nil {
					t.Fatal(err)
				}
			}
			close(m.Release)
			want := "revoked"
			if connection {
				want = "failed"
			}
			p.Wait(t, want)
			if !connection {
				// The approval can be revoked before the OAuth refresh finishes.
				// Closing the executor at that point cancels the in-flight refresh.
				deadline := time.Now().Add(5 * time.Second)
				for {
					c, err := p.Owner.GetToolOAuthConnection(t.Context(), m.Connection.ID)
					if err != nil {
						t.Fatal(err)
					}
					if c.State == "ready" && c.Generation == 1 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("refresh did not settle after approval revocation: state=%s generation=%d", c.State, c.Generation)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			p.Base.PrivateExecutor.Close()
			if p.Calls.Load() != 0 || m.Exchanges.Load() != 1 {
				t.Fatal("revocation permitted a later fixture dispatch")
			}
			c, err := p.Owner.GetToolOAuthConnection(t.Context(), m.Connection.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !connection && (c.State != "ready" || c.Generation != 1) {
				t.Fatal("accepted refresh result was not safely retained after approval revocation")
			}
		})
	}
}

func TestManagedPublicationConcurrentRefresh(t *testing.T) {
	m := granttest.NewManagedPublication(t, state.NewMemStore())
	p := m.Publication
	m.Mode.Store(4)
	m.Approve(t)
	first := p.Publication.ID
	if _, err := p.Publisher.VerifyToolPublication(t.Context(), first, p.Request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.Started:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not start")
	}
	newManagedPublicationReview(t, m)
	m.Approve(t)
	if _, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request); err != nil {
		t.Fatal(err)
	}
	close(m.Release)
	p.Wait(t, "completed")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, err := p.Publisher.VerifyToolPublication(t.Context(), first, p.Request)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.Exchanges.Load() != 1 || p.Calls.Load() != 2 {
		t.Fatalf("shared renewal was repeated or distinct approved tests were lost: exchanges=%d calls=%d", m.Exchanges.Load(), p.Calls.Load())
	}
}
