package toolregistry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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

func TestGuidedToolConnectionMemory(t *testing.T) { exerciseGuidedConnection(t, state.NewMemStore()) }
func TestGuidedToolConnectionPostgres(t *testing.T) {
	exerciseGuidedConnection(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exerciseGuidedConnection(t *testing.T, store state.Store) {
	m := granttest.NewGuided(t, store, "")
	f := m.Private.Base
	ctx := t.Context()
	if m.Connection.State != "authorization_pending" || m.Connection.Intent.RefreshTokenSecretRef != "" {
		t.Fatal("guided connection required a source secret")
	}
	_, err := f.Owner.PutToolGrant(ctx, uuid.NewString(), api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour), CredentialConnectionID: m.Connection.ID})
	requireGrantStatus(t, err, 404)
	status, err := f.Owner.GetToolOAuthAuthorization(ctx, m.Connection.ID)
	if err != nil || status.State != "not_started" {
		t.Fatal("initial authorization status", err)
	}
	_, err = f.Agent.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, api.ToolOAuthAuthorizationRequest{Action: "begin", RedirectURI: "http://127.0.0.1:34567/callback"})
	requireGrantStatus(t, err, 403)
	start := m.Begin(t)
	_, err = f.Other.GetToolOAuthAuthorization(ctx, m.Connection.ID)
	requireGrantStatus(t, err, 403)
	again := m.Begin(t)
	if again.AuthorizationURL != start.AuthorizationURL || again.CallbackState != start.CallbackState {
		t.Fatal("same begin replaced pending authority")
	}
	_, err = f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, api.ToolOAuthAuthorizationRequest{Action: "begin", RedirectURI: "http://127.0.0.1:34568/callback"})
	requireGrantStatus(t, err, 409)
	connections := store.(state.ToolOAuthConnectionStore)
	flow, err := connections.GetToolOAuthAuthorization(ctx, f.Account.ID, m.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(flow.Ciphertext, []byte(start.CallbackState)) {
		t.Fatal("callback nonce was not sealed")
	}
	if _, _, err = secretbox.OpenBytesMulti([]*age.X25519Identity{m.Private.Identity}, flow.Ciphertext); err == nil {
		t.Fatal("runtime identity opened PKCE verifier")
	}
	callback := m.Callback(t, start)
	wrong := callback
	wrong.State = strings.Repeat("A", 43)
	_, err = f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, wrong)
	requireGrantStatus(t, err, 409)
	wrong = callback
	wrong.Issuer = "https://wrong.example"
	_, err = f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, wrong)
	requireGrantStatus(t, err, 400)
	if m.CodeExchanges.Load() != 0 {
		t.Fatal("unbound callback exchanged credentials")
	}
	m.RestartExecutor(t)
	out, err := f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, callback)
	if err != nil || out.State != "connected" {
		t.Fatal("authorization after API restart", err)
	}
	flow, err = connections.GetToolOAuthAuthorization(ctx, f.Account.ID, m.Connection.ID)
	if err != nil || flow.Phase != "completed" || len(flow.Ciphertext) != 0 {
		t.Fatal("consumed verifier retained", err)
	}
	_, err = f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, callback)
	requireGrantStatus(t, err, 409)
	g := m.Grant(t)
	m.RestartExecutor(t)
	id := uuid.NewString()
	in := guidedInvocation(f, g, `{"value":9007199254740993}`)
	if _, err = f.Agent.ExecutePrivateToolInvocation(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	done := waitPrivate(t, f.Agent, id, "completed")
	if m.CodeExchanges.Load() != 1 || m.RefreshExchanges.Load() != 0 || m.Private.Runtime.Calls.Load() != 1 || done.Outcome == nil || !done.Outcome.Redacted || !bytes.Contains(done.Outcome.Result, []byte("9007199254740993")) {
		t.Fatal("guided call did not use the installed token once")
	}
	if bytes.Contains(done.Outcome.Result, []byte("guided-access-1")) || bytes.Contains(done.Outcome.Result, []byte("guided-refresh-1")) {
		t.Fatal("guided credential reflection escaped redaction")
	}
	c, err := connections.GetToolOAuthConnection(ctx, f.Account.ID, m.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = secretbox.OpenBytesMulti([]*age.X25519Identity{m.Private.Identity}, c.Ciphertext); err == nil {
		t.Fatal("runtime identity opened execution tokens")
	}
	owner, err := f.Owner.GetToolOAuthConnection(ctx, m.Connection.ID)
	if err != nil || owner.Generation != 1 || owner.BindingSHA256 != m.Connection.BindingSHA256 {
		t.Fatal("authorization changed immutable connection binding", err)
	}
	raw, _ := json.Marshal(owner)
	for _, value := range []string{callback.Code, start.CallbackState, "VERIFIER", "guided-access-1", "guided-refresh-1"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("owner projection exposed exchange credentials")
		}
	}
	if _, err = f.Owner.RevokeToolOAuthConnection(ctx, m.Connection.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Agent.GetDelegatedToolConnection(ctx, g.ID)
	requireGrantStatus(t, err, 404)
}

func guidedInvocation(f *granttest.Fixture, g api.ToolGrantRecord, args string) api.PrivateToolInvocationRequest {
	return api.PrivateToolInvocationRequest{GrantID: g.ID, ArgumentsJSON: args, Intent: api.ToolInvocationIntent{Version: f.PublicationA.ID, Name: "visible", ArgumentsSHA256: toolpublication.Digest([]byte(args)), AccessMode: "external-oauth", TimeoutSeconds: 15, AllowToolExecution: true, CredentialBindingSHA256: g.Credential.SHA256}}
}

func TestGuidedToolAuthorizationFailuresNeverReplay(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []int32{1, 2, 3, 5, 6, 7} {
			t.Run(backend+string(rune('0'+mode)), func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "postgres" {
					store = state.NewPgStore(pgtest.OpenMigrated(t))
				}
				m := granttest.NewGuided(t, store, "")
				f := m.Private.Base
				m.Mode.Store(mode)
				callback := m.Callback(t, m.Begin(t))
				out, err := f.Owner.AuthorizeToolOAuthConnection(t.Context(), m.Connection.ID, callback)
				if mode == 3 {
					if err != nil || out.State != "authorization_denied" {
						t.Fatal("denied consent", err)
					}
				} else {
					requireGrantStatus(t, err, 503)
				}
				m.RestartExecutor(t)
				_, err = f.Owner.AuthorizeToolOAuthConnection(t.Context(), m.Connection.ID, callback)
				requireGrantStatus(t, err, 409)
				_, err = f.Owner.AuthorizeToolOAuthConnection(t.Context(), m.Connection.ID, api.ToolOAuthAuthorizationRequest{Action: "begin", RedirectURI: "http://127.0.0.1:34567/callback"})
				requireGrantStatus(t, err, 409)
				status, err := f.Owner.GetToolOAuthAuthorization(t.Context(), m.Connection.ID)
				if err != nil || status.State != "reconnect_required" || m.Private.Runtime.Calls.Load() != 0 {
					t.Fatal("failed authorization remained usable", err)
				}
				want := int32(1)
				if mode == 3 {
					want = 0
				}
				if m.CodeExchanges.Load() != want {
					t.Fatal("authorization code replayed")
				}
			})
		}
	}
}

func TestGuidedToolAuthorizationRevocationAndConcurrency(t *testing.T) {
	m := granttest.NewGuided(t, state.NewPgStore(pgtest.OpenMigrated(t)), "")
	m.Mode.Store(4)
	f := m.Private.Base
	ctx := t.Context()
	callback := m.Callback(t, m.Begin(t))
	done := make(chan error, 1)
	defer func() {
		select {
		case <-m.Release:
		default:
			close(m.Release)
		}
	}()
	go func() {
		_, err := f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, callback)
		done <- err
	}()
	select {
	case <-m.Started:
	case <-time.After(5 * time.Second):
		t.Fatal("code exchange did not start")
	}
	_, err := f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, callback)
	requireGrantStatus(t, err, 409)
	if _, err = f.Owner.RevokeToolOAuthConnection(t.Context(), m.Connection.ID); err != nil {
		t.Fatal(err)
	}
	close(m.Release)
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("revoked connection installed token")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late exchange did not finish")
	}
	c, err := f.Store.(state.ToolOAuthConnectionStore).GetToolOAuthConnection(t.Context(), f.Account.ID, m.Connection.ID)
	if err != nil || c.Record.State != "revoked" || len(c.Ciphertext) != 0 || m.CodeExchanges.Load() != 1 {
		t.Fatal("revocation lost the renewal fence", err)
	}
}

func TestGuidedToolAuthorizationCancelledExchange(t *testing.T) {
	m := granttest.NewGuided(t, state.NewMemStore(), "")
	m.Mode.Store(4)
	f := m.Private.Base
	callback := m.Callback(t, m.Begin(t))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	defer func() {
		select {
		case <-m.Release:
		default:
			close(m.Release)
		}
	}()
	done := make(chan error, 1)
	go func() { _, err := f.Owner.AuthorizeToolOAuthConnection(ctx, m.Connection.ID, callback); done <- err }()
	select {
	case <-m.Started:
	case <-time.After(5 * time.Second):
		t.Fatal("code exchange did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled exchange did not return")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := f.Owner.GetToolOAuthAuthorization(t.Context(), m.Connection.ID)
		if err == nil && out.State == "reconnect_required" {
			m.RestartExecutor(t)
			_, err = f.Owner.AuthorizeToolOAuthConnection(t.Context(), m.Connection.ID, callback)
			requireGrantStatus(t, err, http.StatusConflict)
			if m.CodeExchanges.Load() != 1 {
				t.Fatal("interrupted code exchanged twice")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("interrupted issuance did not require reconnection")
}

func TestGuidedToolConnectionRenewsWithSameGrant(t *testing.T) {
	m := granttest.NewGuided(t, state.NewMemStore(), "")
	m.ExpiresIn.Store(31)
	m.Connect(t)
	g := m.Grant(t)
	m.ExpiresIn.Store(3600)
	time.Sleep(1200 * time.Millisecond)
	m.RestartExecutor(t)
	f := m.Private.Base
	id := uuid.NewString()
	if _, err := f.Agent.ExecutePrivateToolInvocation(t.Context(), id, guidedInvocation(f, g, `{}`)); err != nil {
		t.Fatal(err)
	}
	waitPrivate(t, f.Agent, id, "completed")
	current, err := f.Owner.GetToolGrant(t.Context(), g.ID)
	if err != nil || current.Credential.SHA256 != g.Credential.SHA256 || m.CodeExchanges.Load() != 1 || m.RefreshExchanges.Load() != 1 {
		t.Fatal("routine renewal changed guided authorization", err)
	}
}

func TestGuidedConsumedAttemptSurvivesRestart(t *testing.T) {
	m := granttest.NewGuided(t, state.NewPgStore(pgtest.OpenMigrated(t)), "")
	f := m.Private.Base
	first := m.Begin(t)
	firstCallback := m.Callback(t, first)
	second := m.CreateConnection(t)
	m.Connection = second
	next := m.Begin(t)
	_, err := f.Owner.AuthorizeToolOAuthConnection(t.Context(), second.ID, firstCallback)
	requireGrantStatus(t, err, 409)
	connections := f.Store.(state.ToolOAuthConnectionStore)
	if _, err = connections.ConsumeToolOAuthAuthorization(t.Context(), uuid.NewString(), second.ID, toolpublication.Digest([]byte(next.CallbackState))); err == nil {
		t.Fatal("cross-account callback found the connection")
	}
	if _, err = connections.ConsumeToolOAuthAuthorization(t.Context(), f.Account.ID, second.ID, toolpublication.Digest([]byte(next.CallbackState))); err != nil {
		t.Fatal(err)
	}
	m.RestartExecutor(t)
	status, err := f.Owner.GetToolOAuthAuthorization(t.Context(), second.ID)
	if err != nil || status.State != "authorization_uncertain" {
		t.Fatal("consumed issuance was lost on restart", err)
	}
	_, err = f.Owner.AuthorizeToolOAuthConnection(t.Context(), second.ID, m.Callback(t, next))
	requireGrantStatus(t, err, 409)
	if m.CodeExchanges.Load() != 0 {
		t.Fatal("restart retried an already consumed authorization")
	}
}
