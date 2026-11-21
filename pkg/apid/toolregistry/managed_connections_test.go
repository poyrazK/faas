package toolregistry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"filippo.io/age"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestManagedToolConnectionMemory(t *testing.T) { exerciseManagedConnection(t, state.NewMemStore()) }
func TestManagedToolConnectionPostgres(t *testing.T) {
	exerciseManagedConnection(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exerciseManagedConnection(t *testing.T, store state.Store) {
	m := granttest.NewManaged(t, store, "")
	f := m.Private.Base
	ctx := t.Context()
	status, err := f.Agent.GetDelegatedToolConnection(ctx, m.Grant.ID)
	if err != nil || status.State != "refresh_required" || m.Exchanges.Load() != 0 {
		t.Fatal("status performed renewal or omitted the pending connection", err)
	}
	_, err = f.Other.GetDelegatedToolConnection(ctx, m.Grant.ID)
	requireGrantStatus(t, err, 404)
	_, err = f.Agent.GetToolOAuthConnection(ctx, m.Connection.ID)
	requireGrantStatus(t, err, 403)
	desc, err := f.Agent.GetRegistryTool(ctx, f.PublicationA.ID, "visible")
	if err != nil || len(desc.Grants) != 1 || desc.Grants[0].CredentialBindingSHA256 != m.Grant.Credential.SHA256 {
		t.Fatal("managed grant projection", err)
	}
	id := uuid.NewString()
	in := m.Request(`{"value":9007199254740993}`)
	start, err := f.Agent.ExecutePrivateToolInvocation(ctx, id, in)
	if err != nil || !start.Created {
		t.Fatal("managed invocation", err)
	}
	done := waitPrivate(t, f.Agent, id, "completed")
	if m.Exchanges.Load() != 1 || m.Private.Runtime.Calls.Load() != 1 || done.Outcome == nil || !done.Outcome.Redacted || !bytes.Contains(done.Outcome.Result, []byte("9007199254740993")) {
		t.Fatal("managed call lost redaction, numeric precision or single dispatch")
	}
	if bytes.Contains(done.Outcome.Result, []byte("provider-exec-access-1")) || bytes.Contains(done.Outcome.Result, []byte(f.AgentToken)) || bytes.Contains(done.Outcome.Result, []byte("provider-exec-refresh-initial")) {
		t.Fatal("managed result leaked a credential")
	}
	owner, err := f.Owner.GetToolOAuthConnection(ctx, m.Connection.ID)
	if err != nil || owner.State != "ready" || owner.Generation != 1 || owner.BindingSHA256 != m.Connection.BindingSHA256 {
		t.Fatal("renewal changed authorization or was not durably installed", err)
	}
	internal, err := store.(state.ToolOAuthConnectionStore).GetToolOAuthConnection(ctx, f.Account.ID, m.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := secretbox.OpenBytesMulti([]*age.X25519Identity{m.Private.Identity}, internal.Ciphertext); err == nil {
		t.Fatal("application secret identity opened execution-only credentials")
	}
	namespace, _, err := secretbox.OpenBytesMulti([]*age.X25519Identity{m.AccountIdentity}, internal.Ciphertext)
	if err != nil || namespace != "gregale-tool-oauth:"+m.Connection.BindingSHA256 {
		t.Fatal("account-domain credential envelope was not bound to execution approval", err)
	}
	raw, _ := json.Marshal(owner)
	for _, value := range []string{"provider-exec-access-1", "provider-exec-refresh-1", "provider-exec-refresh-initial", "credentials_ciphertext", "lease_id"} {
		if bytes.Contains(raw, []byte(value)) {
			t.Fatal("owner response exposed credential state")
		}
	}
	status, err = f.Agent.GetDelegatedToolConnection(ctx, m.Grant.ID)
	if err != nil || status.State != "ready" || status.RecoveryAction != "" {
		t.Fatal("connection status after renewal", err)
	}
	raw, _ = json.Marshal(status)
	if strings.Contains(string(raw), "MCP_EXECUTION_REFRESH") || strings.Contains(string(raw), "token_endpoint") {
		t.Fatal("agent status exposed owner configuration")
	}
	retained, err := f.Agent.ExecutePrivateToolInvocation(ctx, id, in)
	if err != nil || retained.Created || m.Exchanges.Load() != 1 {
		t.Fatal("same request repeated renewal or execution", err)
	}
	m.RestartExecutor(t)
	id2 := uuid.NewString()
	if _, err = f.Agent.ExecutePrivateToolInvocation(ctx, id2, m.Request(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitPrivate(t, f.Agent, id2, "completed")
	if m.Exchanges.Load() != 1 || m.Private.Runtime.Calls.Load() != 2 {
		t.Fatal("restart did not reuse the persisted access token")
	}
	if _, err = f.Owner.RevokeToolOAuthConnection(ctx, m.Connection.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Agent.GetDelegatedToolConnection(ctx, m.Grant.ID)
	requireGrantStatus(t, err, 404)
	_, err = f.Agent.GetRegistryTool(ctx, f.PublicationA.ID, "visible")
	requireGrantStatus(t, err, 404)
	_, err = f.Agent.ExecutePrivateToolInvocation(ctx, uuid.NewString(), in)
	requireGrantStatus(t, err, 404)
	_, err = f.Agent.GetDelegatedToolInvocation(ctx, id)
	requireGrantStatus(t, err, 404)
	history, err := f.Owner.GetToolInvocation(ctx, id)
	if err != nil || history.Status != "completed" {
		t.Fatal("owner lost historical invocation evidence", err)
	}
}

func TestManagedToolConfidentialClient(t *testing.T) {
	m := granttest.NewManaged(t, state.NewMemStore(), "")
	f := m.Private.Base
	secret := "client secret%&:crédential"
	m.BasicSecret.Store(secret)
	m.Private.Runtime.Reflections.Store([]string{secret, "provider-exec-refresh-initial"})
	granttest.SealManagedSource(t, m, "MCP_EXECUTION_CLIENT", secret)
	if _, err := f.Owner.RevokeToolOAuthConnection(t.Context(), m.Connection.ID); err != nil {
		t.Fatal(err)
	}
	in := m.Connection.Intent
	in.TokenAuthMethod = "client_secret_basic"
	in.ClientSecretRef = "secret:MCP_EXECUTION_CLIENT"
	connection, err := f.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), in)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour), CredentialConnectionID: connection.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.Grant = grant
	m.Connection = connection
	id := uuid.NewString()
	if _, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), id, m.Request(`{}`)); err != nil {
		t.Fatal(err)
	}
	out := waitPrivate(t, f.Agent, id, "completed")
	if m.Exchanges.Load() != 1 || bytes.Contains(out.Outcome.Result, []byte(secret)) || bytes.Contains(out.Outcome.Result, []byte("provider-exec-refresh-initial")) {
		t.Fatal("client authentication or secret reflection isolation failed")
	}
}

func TestManagedToolCancelledExchangeRequiresReconnect(t *testing.T) {
	m := granttest.NewManaged(t, state.NewMemStore(), "")
	m.Mode.Store(4)
	f := m.Private.Base
	defer func() {
		select {
		case <-m.Release:
		default:
			close(m.Release)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := f.Agent.ExecutePrivateToolInvocation(ctx, uuid.NewString(), m.Request(`{}`))
		done <- err
	}()
	select {
	case <-m.Started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider exchange did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled exchange did not return")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view, err := f.Agent.GetDelegatedToolConnection(t.Context(), m.Grant.ID)
		if err == nil && view.State == "reconnect_required" {
			m.RestartExecutor(t)
			_, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), uuid.NewString(), m.Request(`{}`))
			requireGrantStatus(t, err, 503)
			if m.Exchanges.Load() != 1 || m.Private.Runtime.Calls.Load() != 0 {
				t.Fatal("cancelled exchange was repeated")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("uncertain exchange did not durably require reconnection")
}

func TestManagedToolRefreshFailuresNeverRetry(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []int32{1, 2, 3} {
			t.Run(backend+string(rune('0'+mode)), func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "postgres" {
					store = state.NewPgStore(pgtest.OpenMigrated(t))
				}
				m := granttest.NewManaged(t, store, "")
				f := m.Private.Base
				m.Mode.Store(mode)
				id := uuid.NewString()
				_, err := f.Agent.ExecutePrivateToolInvocation(t.Context(), id, m.Request(`{}`))
				requireGrantStatus(t, err, http.StatusServiceUnavailable)
				if m.Exchanges.Load() != 1 || m.Private.Runtime.Calls.Load() != 0 {
					t.Fatal("failed renewal reached tools or was exchanged twice")
				}
				view, err := f.Agent.GetDelegatedToolConnection(t.Context(), m.Grant.ID)
				if err != nil || view.State != "reconnect_required" || view.RecoveryAction != "reconnect" {
					t.Fatal("provider refusal did not require reconnection", err)
				}
				m.RestartExecutor(t)
				_, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), id, m.Request(`{}`))
				requireGrantStatus(t, err, http.StatusServiceUnavailable)
				_, err = f.Owner.GetToolInvocation(t.Context(), id)
				requireGrantStatus(t, err, 404)
				if m.Exchanges.Load() != 1 {
					t.Fatal("restart re-exchanged a potentially consumed refresh token")
				}
			})
		}
	}
}

func TestManagedToolRefreshSerializationAndRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "single renewal", true: "revoked during exchange"}[revoke], func(t *testing.T) {
			m := granttest.NewManaged(t, state.NewPgStore(pgtest.OpenMigrated(t)), "")
			m.Mode.Store(4)
			f := m.Private.Base
			defer func() {
				select {
				case <-m.Release:
				default:
					close(m.Release)
				}
			}()
			id := uuid.NewString()
			done := make(chan error, 1)
			go func() { _, err := f.Agent.ExecutePrivateToolInvocation(t.Context(), id, m.Request(`{}`)); done <- err }()
			select {
			case <-m.Started:
			case <-time.After(5 * time.Second):
				t.Fatal("renewal did not start")
			}
			view, err := f.Agent.GetDelegatedToolConnection(t.Context(), m.Grant.ID)
			if err != nil || view.State != "refreshing" {
				t.Fatal("lease not exposed as refreshing", err)
			}
			_, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), uuid.NewString(), m.Request(`{}`))
			requireGrantStatus(t, err, 409)
			if revoke {
				if _, err = f.Owner.RevokeToolOAuthConnection(t.Context(), m.Connection.ID); err != nil {
					t.Fatal(err)
				}
			}
			close(m.Release)
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("renewal did not finish")
			}
			if revoke {
				if err == nil {
					t.Fatal("revoked exchange authorized use")
				}
				if m.Private.Runtime.Calls.Load() != 0 {
					t.Fatal("revocation permitted an app call")
				}
				_, err = f.Owner.GetToolInvocation(t.Context(), id)
				requireGrantStatus(t, err, 404)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				waitPrivate(t, f.Agent, id, "completed")
			}
			if m.Exchanges.Load() != 1 {
				t.Fatal("concurrent calls exchanged the same refresh token more than once")
			}
		})
	}
}

func TestManagedToolRoutineRenewalKeepsGrant(t *testing.T) {
	m := granttest.NewManaged(t, state.NewMemStore(), "")
	m.ExpiresIn.Store(31)
	f := m.Private.Base
	binding := m.Grant.Credential.SHA256
	id := uuid.NewString()
	if _, err := f.Agent.ExecutePrivateToolInvocation(t.Context(), id, m.Request(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitPrivate(t, f.Agent, id, "completed")
	timer := time.NewTimer(1200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal("test cancelled")
	case <-timer.C:
	}
	m.RestartExecutor(t)
	id = uuid.NewString()
	if _, err := f.Agent.ExecutePrivateToolInvocation(t.Context(), id, m.Request(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitPrivate(t, f.Agent, id, "completed")
	desc, err := f.Agent.GetRegistryTool(t.Context(), f.PublicationA.ID, "visible")
	if err != nil || len(desc.Grants) != 1 || desc.Grants[0].CredentialBindingSHA256 != binding || m.Exchanges.Load() != 2 {
		t.Fatal("renewal failed to use rotated refresh credentials or changed the grant", err)
	}
}

func TestManagedToolOwnerIntentAndSourcesAreImmutable(t *testing.T) {
	m := granttest.NewManaged(t, state.NewMemStore(), "")
	f := m.Private.Base
	changed := m.Connection.Intent
	changed.Scopes = []string{"tool:read", "tool:write"}
	_, err := f.Owner.PutToolOAuthConnection(t.Context(), m.Connection.ID, changed)
	requireGrantStatus(t, err, 409)
	wrong := m.Connection.Intent
	wrong.App = f.AppB.Slug
	other, err := f.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), wrong)
	if err == nil || other.ID != "" {
		t.Fatal("connection borrowed another app's source")
	}
	granttest.SealManagedSource(t, m, "MCP_EXECUTION_REFRESH", "owner-reconnected-refresh")
	_, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), uuid.NewString(), m.Request(`{}`))
	requireGrantStatus(t, err, 404)
	view, err := f.Owner.GetToolOAuthConnection(t.Context(), m.Connection.ID)
	if err != nil || view.State != "reconnect_required" || m.Exchanges.Load() != 0 {
		t.Fatal("source replacement reused old authorization", err)
	}
}

func TestManagedToolEncryptedStateCannotCrossConnections(t *testing.T) {
	m := granttest.NewManaged(t, state.NewMemStore(), "")
	f := m.Private.Base
	id := uuid.NewString()
	if _, err := f.Agent.ExecutePrivateToolInvocation(t.Context(), id, m.Request(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitPrivate(t, f.Agent, id, "completed")
	connections := f.Store.(state.ToolOAuthConnectionStore)
	saved, err := connections.GetToolOAuthConnection(t.Context(), f.Account.ID, m.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), m.Connection.Intent)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour), CredentialConnectionID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := connections.BeginToolOAuthRenewal(t.Context(), f.Account.ID, other.ID, api.ToolInvocationDelegation{GrantID: grant.ID, KeyID: f.AgentKey.ID, ProjectID: f.ProjectA.ID})
	if err != nil || !claim.Fresh {
		t.Fatal("second connection renewal", err)
	}
	if _, err = connections.FinishToolOAuthRenewal(t.Context(), f.Account.ID, other.ID, claim.Connection.LeaseID, saved.Ciphertext, saved.Record.ExpiresAt, ""); err != nil {
		t.Fatal(err)
	}
	m.Grant = grant
	_, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), uuid.NewString(), m.Request(`{}`))
	requireGrantStatus(t, err, 503)
	if m.Exchanges.Load() != 1 || m.Private.Runtime.Calls.Load() != 1 {
		t.Fatal("copied envelope authorized another connection")
	}
}
