package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestMemVersionedRunTasks(t *testing.T) { exerciseRunTasks(t, state.NewMemStore()) }
func exerciseRunTasks(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "tasks-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.test/hooks", ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect", CallbackAuthTokenSealed: []byte("sealed"), AuthTokenSealed: []byte("sealed"), Enabled: true}, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	request, meta, err := runtask.Normalize(api.PublishExecutionTaskRequest{Name: "report", Version: 1, AgentEndpointIDs: []string{ep.ID}, Definition: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default x => x"}}, acct.Plan)
	if err != nil {
		t.Fatal(err)
	}
	meta.ID = uuid.NewString()
	v := state.ExecutionTaskVersion{ID: meta.ID, AccountID: acct.ID, Name: meta.Name, Version: meta.Version, Digest: meta.Digest, Runtime: meta.Runtime, Metadata: meta, SealedDefinition: []byte("account-sealed-code"), PayloadKID: "test-kid"}
	stored, replayed, err := s.PublishExecutionTaskVersion(ctx, v)
	if err != nil || replayed || stored.ID != v.ID {
		t.Fatal("publish", err)
	}
	copy := v
	copy.ID = uuid.NewString()
	copy.Metadata.ID = copy.ID
	again, replayed, err := s.PublishExecutionTaskVersion(ctx, copy)
	if err != nil || !replayed || again.ID != v.ID {
		t.Fatal("publish replay", err)
	}
	copy.Digest = string(make([]byte, 64))
	copy.Metadata.Digest = copy.Digest
	if _, _, err := s.PublishExecutionTaskVersion(ctx, copy); err == nil {
		t.Fatal("immutable version changed")
	}
	callback := &state.ExecutionTaskCallback{EndpointID: ep.ID, ThreadID: "thread", ToolCallID: "call", ProviderCallID: "provider-call", IssuedAt: time.Now().UTC().Truncate(time.Microsecond)}
	identity := admissionIdentity(acct.ID, "task-admission")
	identity.TaskCallback = callback
	params := executionTestParams(t, acct.ID, time.Now().UTC(), 0, "delivery-sealed")
	params.Request, _ = request.Definition.Resolve(acct.Plan)
	params.SourceBytes = len(request.Definition.Source)
	params.InputBytes = 4
	params.Task = &state.ExecutionTaskAdmission{VersionID: v.ID, RequestDigest: identity.Fingerprint, Callback: callback}
	blockedCallback := *callback
	blockedCallback.EndpointID = uuid.NewString()
	blockedIdentity := identity
	blockedIdentity.TaskCallback = &blockedCallback
	blockedParams := params
	blockedAdmission := *params.Task
	blockedAdmission.Callback = &blockedCallback
	blockedParams.Task = &blockedAdmission
	if _, _, err := s.AdmitExecution(ctx, blockedIdentity, func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return blockedParams, nil
	}); !errors.Is(err, state.ErrExecutionTaskUnavailable) {
		t.Fatal("unpublished endpoint admitted a task", err)
	}
	blockedRows, err := s.ListExecutions(ctx, acct.ID, 10, 0)
	if err != nil || len(blockedRows) != 0 {
		t.Fatal("refused endpoint consumed admission", err)
	}
	prepared := 0
	prepare := func(ctx context.Context, reader state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		prepared++
		if _, err := reader.(state.ExecutionTaskVersionReader).ExecutionTaskVersionByName(ctx, acct.ID, v.Name, v.Version); err != nil {
			return params, err
		}
		return params, nil
	}
	run, replayed, err := s.AdmitExecution(ctx, identity, prepare)
	if err != nil || replayed || run.Task == nil {
		t.Fatal("invoke", err, run.Task)
	}
	if err := s.RetireExecutionTaskVersion(ctx, acct.ID, v.Name, v.Version); err != nil {
		t.Fatal(err)
	}
	// Callback identity remains bound even when the ordinary idempotency key changes.
	identity.KeyHash = sha256.Sum256([]byte("new-request-key"))
	identity.LegacyKey = "new-request-key"
	retry, replayed, err := s.AdmitExecution(ctx, identity, prepare)
	if err != nil || !replayed || retry.ID != run.ID || prepared != 1 {
		t.Fatal("callback replay after retirement", err, replayed, prepared)
	}
	identity.Fingerprint[31] ^= 1
	if _, _, err := s.AdmitExecution(ctx, identity, prepare); !errors.Is(err, state.ErrExecutionAdmissionConflict) {
		t.Fatal("changed callback request", err)
	}
	version, err := s.ExecutionTaskVersionByName(ctx, acct.ID, v.Name, v.Version)
	if err != nil || len(version.SealedDefinition) != 0 || version.Status != "retired" {
		t.Fatal("retirement retained source", err)
	}
	link, err := s.ExecutionTaskInvocationByID(ctx, acct.ID, run.ID)
	if err != nil || link.Callback.ToolCallID != "call" || link.Ref.ID != v.ID {
		t.Fatal("continuation link", err)
	}
	if _, err := s.ExecutionTaskInvocationByID(ctx, uuid.NewString(), run.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross account receipt", err)
	}
	if _, err := s.RequestExecutionCancellation(ctx, acct.ID, run.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingExecutionTaskCallbacks(ctx, "", api.ExecutionTaskCallbackBatch)
	if err != nil || len(pending) != 1 {
		t.Fatal("terminal continuation missing", err)
	}
	after, err := s.PendingExecutionTaskCallbacks(ctx, run.ID, api.ExecutionTaskCallbackBatch)
	if err != nil || len(after) != 0 {
		t.Fatal("continuation cursor did not advance", err)
	}
	if err := s.MarkExecutionTaskCallbackDelivered(ctx, acct.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingExecutionTaskCallbacks(ctx, "", api.ExecutionTaskCallbackBatch)
	if err != nil || len(pending) != 0 {
		t.Fatal("acknowledged continuation still pending", err)
	}
	rows, err := s.ListExecutions(ctx, acct.ID, 10, 0)
	if err != nil || len(rows) != 1 || rows[0].Task == nil {
		t.Fatal("receipt lineage", err)
	}
}
