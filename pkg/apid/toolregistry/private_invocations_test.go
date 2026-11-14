package toolregistry_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func privateRequest(p *granttest.PrivateFixture, raw string) api.PrivateToolInvocationRequest {
	return api.PrivateToolInvocationRequest{GrantID: p.Grant.ID, Intent: api.ToolInvocationIntent{Version: p.Base.PublicationA.ID, Name: "visible", AccessMode: "external-oauth", ArgumentsSHA256: toolpublication.Digest([]byte(raw)), CredentialBindingSHA256: p.Grant.Credential.SHA256, TimeoutSeconds: 15, AllowToolExecution: true}, ArgumentsJSON: raw}
}
func waitPrivate(t *testing.T, c *api.Client, id, want string) api.ToolInvocationRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err := c.GetDelegatedToolInvocation(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status == want {
			return record
		}
		if record.Status != "running" && record.Status != "queued" {
			t.Fatalf("private invocation ended %s; want %s", record.Status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("private invocation did not finish")
	return api.ToolInvocationRecord{}
}
func TestPrivateToolInvocationMemory(t *testing.T) { exercisePrivateInvocation(t, state.NewMemStore()) }
func TestPrivateToolInvocationPostgres(t *testing.T) {
	exercisePrivateInvocation(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func exercisePrivateInvocation(t *testing.T, store state.Store) {
	p := granttest.NewPrivate(t, store, "")
	f := p.Base
	ctx := t.Context()
	desc, err := f.Agent.GetRegistryTool(ctx, f.PublicationA.ID, "visible")
	if err != nil || len(desc.Grants) != 1 || desc.Grants[0].CredentialBindingSHA256 != p.Grant.Credential.SHA256 {
		t.Fatalf("private descriptor: %+v %v", desc, err)
	}
	raw, _ := json.Marshal(desc)
	if strings.Contains(string(raw), p.Token) || strings.Contains(string(raw), "MCP_TOOL_TOKEN") {
		t.Fatal("descriptor exposed credential source or value")
	}
	in := privateRequest(p, `{"value":9007199254740993}`)
	id := uuid.NewString()
	begun, err := f.Agent.ExecutePrivateToolInvocation(ctx, id, in)
	if err != nil || !begun.Created {
		t.Fatal("private begin", err)
	}
	done := waitPrivate(t, f.Agent, id, "completed")
	if p.Runtime.Calls.Load() != 1 || done.Outcome == nil || !done.Outcome.Redacted || strings.Contains(string(done.Outcome.Result), p.Token) || strings.Contains(string(done.Outcome.Result), f.AgentToken) || !strings.Contains(string(done.Outcome.Result), "9007199254740993") {
		t.Fatal("private result leaked credentials, lost integers or repeated effects")
	}
	fresh := api.NewClient(f.Native.URL, f.AgentToken)
	repeated, err := fresh.ExecutePrivateToolInvocation(ctx, id, in)
	if err != nil || repeated.Created || p.Runtime.Calls.Load() != 1 {
		t.Fatal("private retry repeated execution", err)
	}
	changed := in
	changed.ArgumentsJSON = `{"value":2}`
	changed.Intent.ArgumentsSHA256 = toolpublication.Digest([]byte(changed.ArgumentsJSON))
	_, err = fresh.ExecutePrivateToolInvocation(ctx, id, changed)
	requireGrantStatus(t, err, http.StatusConflict)
	for _, client := range []*api.Client{f.Other, f.Agent} {
		wrong := in
		if client == f.Agent {
			wrong.Intent.Name = "hidden"
		}
		_, err = client.ExecutePrivateToolInvocation(ctx, uuid.NewString(), wrong)
		requireGrantStatus(t, err, http.StatusNotFound)
	}
	_, err = f.Agent.DispatchDelegatedToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusNotFound)
	_, err = f.Agent.FinishDelegatedToolInvocation(ctx, id, api.ToolInvocationOutcome{Status: "outcome_unknown", Code: "worker_interrupted"})
	requireGrantStatus(t, err, http.StatusNotFound)
	if _, err = f.Owner.RevokeToolGrant(ctx, p.Grant.ID); err != nil {
		t.Fatal(err)
	}
	_, err = fresh.GetDelegatedToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusNotFound)
	_, err = fresh.ExecutePrivateToolInvocation(ctx, uuid.NewString(), in)
	requireGrantStatus(t, err, http.StatusNotFound)
	owner, err := f.Owner.GetToolInvocation(ctx, id)
	if err != nil || owner.Status != "completed" || p.Runtime.Calls.Load() != 1 {
		t.Fatal("owner lost private history", err)
	}
}

func TestPrivateToolInvocationFailureRecovery(t *testing.T) {
	for _, mode := range []int32{1, 2, 3, 4} {
		t.Run(string(rune('0'+mode)), func(t *testing.T) {
			p := granttest.NewPrivate(t, state.NewMemStore(), "")
			p.Runtime.Mode.Store(mode)
			id := uuid.NewString()
			in := privateRequest(p, `{}`)
			if _, err := p.Base.Agent.ExecutePrivateToolInvocation(t.Context(), id, in); err != nil {
				t.Fatal(err)
			}
			want := "outcome_unknown"
			if mode == 1 {
				want = "not_started"
			}
			if mode == 3 {
				want = "tool_error"
			}
			if mode == 4 {
				select {
				case <-p.Runtime.Started:
				case <-time.After(3 * time.Second):
					t.Fatal("private call did not start")
				}
				if _, err := p.Base.Agent.CancelDelegatedToolInvocation(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			waitPrivate(t, p.Base.Agent, id, want)
			calls := p.Runtime.Calls.Load()
			if _, err := p.Base.Agent.ExecutePrivateToolInvocation(t.Context(), id, in); err != nil || p.Runtime.Calls.Load() != calls {
				t.Fatal("failed private attempt repeated", err)
			}
		})
	}
}

func TestPrivateToolCredentialRotationBlocksDispatch(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if postgres {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			p := granttest.NewPrivate(t, store, "")
			f := p.Base
			in := privateRequest(p, `{}`)
			id := uuid.NewString()
			target := api.ToolInvocationTarget{App: f.AppA.Slug, AppID: f.AppA.ID, DeploymentID: f.PublicationA.Outcome.DeploymentID, DeploymentSourceSHA256: f.PublicationA.DeploymentSourceSHA256, CatalogSHA256: f.PublicationA.Outcome.CatalogSHA256, CatalogContractSHA256: toolpublication.Digest(f.PublicationA.Outcome.Catalog), EndpointPath: "/mcp"}
			d := api.ToolInvocationDelegation{GrantID: p.Grant.ID, KeyID: f.AgentKey.ID, ProjectID: f.ProjectA.ID}
			delegated := store.(state.DelegatedToolInvocationStore)
			if _, err := delegated.BeginDelegatedToolInvocation(t.Context(), f.Account.ID, id, in.Intent, target, d); err != nil {
				t.Fatal(err)
			}
			p.Rotate(t, "rotated-provider-token")
			_, err := delegated.ChangeDelegatedToolInvocation(t.Context(), f.Account.ID, id, d, "dispatch", nil)
			if !errors.Is(err, state.ErrNotFound) {
				t.Fatal("rotation did not fence dispatch", err)
			}
			_, err = f.Agent.GetRegistryTool(t.Context(), f.PublicationA.ID, "visible")
			requireGrantStatus(t, err, http.StatusNotFound)
			_, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), uuid.NewString(), in)
			requireGrantStatus(t, err, http.StatusNotFound)
			if p.Runtime.Calls.Load() != 0 {
				t.Fatal("rotated credential was used")
			}
		})
	}
}

// Context cancellation after a successful native receipt must not own the worker.
func TestPrivateToolInvocationOutlivesSubmittingRequest(t *testing.T) {
	p := granttest.NewPrivate(t, state.NewMemStore(), "")
	ctx, cancel := context.WithCancel(t.Context())
	id := uuid.NewString()
	if _, err := p.Base.Agent.ExecutePrivateToolInvocation(ctx, id, privateRequest(p, `{}`)); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitPrivate(t, p.Base.Agent, id, "completed")
}

func TestPrivateToolCredentialProviderRefusalIsNotRetried(t *testing.T) {
	p := granttest.NewPrivate(t, state.NewMemStore(), "")
	f := p.Base
	p.Rotate(t, "unaccepted-provider-token")
	grant, err := f.Owner.PutToolGrant(t.Context(), uuid.NewString(), p.Grant.Intent)
	if err != nil {
		t.Fatal(err)
	}
	p.Grant = grant
	id := uuid.NewString()
	in := privateRequest(p, `{}`)
	if _, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), id, in); err != nil {
		t.Fatal(err)
	}
	waitPrivate(t, f.Agent, id, "not_started")
	if _, err = f.Agent.ExecutePrivateToolInvocation(t.Context(), id, in); err != nil || p.Runtime.Calls.Load() != 0 {
		t.Fatal("provider refusal retried a private tool", err)
	}
}
