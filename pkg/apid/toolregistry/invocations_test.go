package toolregistry_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolinvocation/invocationtest"
)

func TestToolInvocationsMemory(t *testing.T) { exerciseInvocations(t, state.NewMemStore()) }
func TestToolInvocationsPostgres(t *testing.T) {
	exerciseInvocations(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exerciseInvocations(t *testing.T, store state.Store) {
	t.Helper()
	f := invocationtest.New(t, store, "open")
	ctx := t.Context()
	id := uuid.NewString()
	in := f.Intent([]byte(`{"value":9007199254740993}`))
	begin, err := f.Client.BeginToolInvocation(ctx, id, in)
	if err != nil || !begin.Created || begin.Record.Status != "queued" {
		t.Fatalf("begin: %+v %v", begin, err)
	}
	if begin.Record.Target.DeploymentID != f.Deployment.ID || begin.Record.Target.AppID != f.App.ID {
		t.Fatal("version resolved to a different deployment")
	}
	again, err := f.Client.BeginToolInvocation(ctx, id, in)
	if err != nil || again.Created {
		t.Fatal("duplicate begin authorized another worker")
	}
	changed := in
	changed.ArgumentsSHA256 = strings.Repeat("f", 64)
	if _, err = f.Client.BeginToolInvocation(ctx, id, changed); err == nil {
		t.Fatal("request identity reused with different arguments")
	}
	changed = in
	changed.AllowToolExecution = false
	if _, err = f.Client.BeginToolInvocation(ctx, uuid.NewString(), changed); err == nil {
		t.Fatal("catalog discovery granted invocation permission")
	}
	changed = in
	changed.Name = "unpublished"
	if _, err = f.Client.BeginToolInvocation(ctx, uuid.NewString(), changed); err == nil {
		t.Fatal("unpublished tool dispatched")
	}
	changed = in
	changed.Version = strings.Repeat("f", 64)
	if _, err = f.Client.BeginToolInvocation(ctx, uuid.NewString(), changed); err == nil {
		t.Fatal("missing version fell back to latest")
	}
	// Distinct processes can race; the row lock grants exactly one dispatch.
	var count atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			receipt, err := f.Client.DispatchToolInvocation(ctx, id)
			if err != nil {
				t.Error(err)
				return
			}
			if receipt.DispatchGranted {
				count.Add(1)
			}
		})
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("dispatch grants=%d", count.Load())
	}
	cancel, err := f.Client.CancelToolInvocation(ctx, id)
	if err != nil || cancel.Record.Status != "running" || !cancel.Record.CancellationRequested {
		t.Fatal("running cancellation claimed remote effects were undone")
	}
	out, err := toolinvocation.Result([]byte(`{"content":[{"type":"text","text":"received"}],"structuredContent":{"n":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	finish, err := f.Client.FinishToolInvocation(ctx, id, out)
	if err != nil || finish.Record.Status != "completed" || !bytes.Contains(finish.Record.Outcome.Result, []byte("9007199254740993")) {
		t.Fatalf("finish: %+v %v", finish, err)
	}
	if _, err = f.Client.FinishToolInvocation(ctx, id, out); err != nil {
		t.Fatal("identical terminal evidence was not idempotent")
	}
	other, _ := toolinvocation.Result([]byte(`{"content":[{"type":"text","text":"different"}]}`))
	if _, err = f.Client.FinishToolInvocation(ctx, id, other); err == nil {
		t.Fatal("completed result overwritten")
	}
	cancelledID := uuid.NewString()
	if _, err = f.Client.BeginToolInvocation(ctx, cancelledID, in); err != nil {
		t.Fatal(err)
	}
	cancel, err = f.Client.CancelToolInvocation(ctx, cancelledID)
	if err != nil || cancel.Record.Status != "cancelled" || cancel.Record.DispatchedAt != nil {
		t.Fatal("queued cancellation did not prevent dispatch")
	}
	grant, err := f.Client.DispatchToolInvocation(ctx, cancelledID)
	if err != nil || grant.DispatchGranted {
		t.Fatal("cancelled call dispatched")
	}
	// Runtime data and invocation identities stay behind the account boundary.
	otherAccount, err := store.CreateAccount(ctx, "other-invocation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/v1/tool-invocations/"+id, nil)
	request.SetPathValue("id", id)
	for _, test := range []struct {
		account state.Account
		key     *state.APIKey
		want    int
	}{
		{otherAccount, nil, 404}, {f.Account, &state.APIKey{AppID: f.App.ID}, 403}, {f.Account, &state.APIKey{ProjectLimited: true}, 403},
		{f.Account, &state.APIKey{Scopes: []string{api.ScopeAgent, api.ScopeDeployWrite}}, 403}, {f.Account, &state.APIKey{OrgID: "foreign-org"}, 403},
	} {
		w := httptest.NewRecorder()
		r := request.WithContext(middleware.WithPrincipal(request.Context(), test.account, test.key, nil))
		toolregistry.Invocations(store)(w, r, test.account)
		if w.Code != test.want {
			t.Fatalf("authority boundary: %d %s", w.Code, w.Body.String())
		}
	}
	// Re-reading an old request cannot rebind it when its deployment retires.
	if err = store.UpdateDeploymentStatus(ctx, f.Deployment.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	again, err = f.Client.BeginToolInvocation(ctx, id, in)
	if err != nil || again.Created || again.Record.Status != "completed" {
		t.Fatal("retired version prevented receipt recovery")
	}
	if _, err = f.Client.BeginToolInvocation(ctx, uuid.NewString(), in); err == nil {
		t.Fatal("retired version silently substituted")
	}
	// The journal contains hashes and target metadata, never argument bodies.
	raw, _ := json.Marshal(again.Record)
	if bytes.Contains(raw, []byte("9007199254740993")) && !bytes.Contains(raw, []byte("result_base64")) {
		t.Fatal("argument body persisted")
	}
}

func TestToolInvocationActiveQuotaAndBindingRecheck(t *testing.T) {
	f := invocationtest.New(t, state.NewMemStore(), "open")
	in := f.Intent([]byte(`{"value":1}`))
	ids := []string{}
	for range api.ToolInvocationsActiveMax {
		id := uuid.NewString()
		ids = append(ids, id)
		if _, err := f.Client.BeginToolInvocation(t.Context(), id, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Client.BeginToolInvocation(t.Context(), uuid.NewString(), in); err == nil {
		t.Fatal("active-call quota not enforced")
	}
	if old, err := f.Client.BeginToolInvocation(t.Context(), ids[0], in); err != nil || old.Created {
		t.Fatal("quota prevented retained receipt recovery")
	}
	if _, err := f.Client.CancelToolInvocation(t.Context(), ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Client.BeginToolInvocation(t.Context(), uuid.NewString(), in); err != nil {
		t.Fatal("queued cancellation retained an active slot")
	}
	if err := f.Store.UpdateDeploymentStatus(t.Context(), f.Deployment.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Client.DispatchToolInvocation(t.Context(), ids[1]); err == nil {
		t.Fatal("dispatch ignored a changed deployment binding")
	}
}
