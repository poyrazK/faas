package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRunCustomerControls(t *testing.T) { exerciseRunCustomerControls(t, state.NewMemStore()) }
func runControlInt(n int64) *int64            { return &n }
func exerciseRunCustomerControls(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	keyHash := []byte(uuid.NewString())
	key, err := s.CreateAPIKey(ctx, a.ID, keyHash, "customer agent", []string{api.ScopeRunsRead, api.ScopeRunsWrite})
	if err != nil {
		t.Fatal(err)
	}
	key, err = s.APIKeyByHash(ctx, keyHash)
	if err != nil {
		t.Fatal(err)
	}
	p := executionTestParams(t, a.ID, time.Now().UTC(), 0, "sealed-private-source")
	p.RunsPrincipalID = &key.RunsPrincipalID
	reserved := int64(p.Request.Limits.MemoryMB+api.PerVMOverheadMB) * int64(p.Request.Limits.TimeoutMS)
	settled := int64(p.Request.Limits.MemoryMB+api.PerVMOverheadMB) * 100
	limits := api.RunBudgetLimits{MaxConcurrent: runControlInt(1), MaxInvocationsPerDay: runControlInt(2), MaxComputeMBMSPerDay: runControlInt(reserved + settled)}
	policy, err := s.PutRunBudget(ctx, a.ID, "customer", "customer-42", api.SetRunBudgetRequest{Enabled: true, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.PutRunBinding(ctx, a.ID, "api_key", key.ID, api.SetRunAttributionBindingRequest{CustomerRef: policy.Ref, AgentRef: "report-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if binding.SubjectID != key.ID || binding.Revision != 1 {
		t.Fatal("public key binding", binding)
	}
	if _, err := s.PutRunBinding(ctx, a.ID, "api_key", key.ID, api.SetRunAttributionBindingRequest{CustomerRef: policy.Ref}); !errors.Is(err, state.ErrRunControlConflict) {
		t.Fatal("stale binding update", err)
	}
	webhookLimits, _ := api.LimitsFor(a.Plan)
	hook, err := s.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{AccountID: a.ID, TargetURL: "https://example.test/budgets", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{api.RunBudgetThresholdWebhookEvent}}, webhookLimits)
	if err != nil {
		t.Fatal("budget receiver", err)
	}
	identity := admissionIdentity(a.ID, "customer-first")
	prepare := func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return p, nil
	}
	type result struct {
		row      state.Execution
		replayed bool
		err      error
	}
	ch := make(chan result, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			id := admissionIdentity(a.ID, fmt.Sprintf("concurrent-%d", i))
			if i == 0 {
				id = identity
			}
			row, replayed, err := s.AdmitExecution(ctx, id, prepare)
			ch <- result{row, replayed, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(ch)
	var first state.Execution
	successes := 0
	for r := range ch {
		if r.err == nil {
			successes++
			first = r.row
		} else {
			var budget *state.RunBudgetError
			if !errors.As(r.err, &budget) || budget.Metric != "concurrent" {
				t.Fatal("unexpected concurrent refusal", r.err)
			}
		}
	}
	if successes != 1 || first.Attribution == nil || first.Attribution.CustomerRef != policy.Ref || first.Attribution.AgentRef != "report-agent" {
		t.Fatal("concurrent customer admission", successes, first.Attribution)
	}
	u, err := s.GetRunBudget(ctx, a.ID, "customer", policy.Ref)
	if err != nil || u.Usage.Active != 1 || u.Usage.Invocations != 1 || u.Usage.ComputeReservedMBMS != reserved {
		t.Fatal("one reservation", u, err)
	}
	// Recover the winning identity through an ordinary replay without preparing
	// again; the callback-independent contract is tested with a known fresh key.
	if _, err := s.PutRunBudget(ctx, a.ID, "customer", policy.Ref, api.SetRunBudgetRequest{ExpectedRevision: 1, Enabled: false, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		id := admissionIdentity(a.ID, fmt.Sprintf("concurrent-%d", i))
		if i == 0 {
			id = identity
		}
		row, replayed, err := s.AdmitExecution(ctx, id, func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
			return p, errors.New("must replay before controls or preparation")
		})
		if err == nil {
			if !replayed || row.ID != first.ID {
				t.Fatal("wrong replay", row.ID, replayed)
			}
			break
		} else if i == 7 {
			t.Fatal("winning identity did not replay", err)
		}
	}
	rotatedHash := []byte(uuid.NewString())
	rotated, _, err := s.RotateAPIKey(ctx, a.ID, key.ID, rotatedHash, "rotated", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err = s.APIKeyByHash(ctx, rotatedHash)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.GetRunBinding(ctx, a.ID, "api_key", rotated.ID)
	if err != nil || b.SubjectID != rotated.ID || b.Revision != 1 {
		t.Fatal("rotation lost binding", b, err)
	}
	p.RunsPrincipalID = &rotated.RunsPrincipalID
	if _, err := s.CreateExecution(ctx, p); err == nil {
		t.Fatal("raw source bypassed disabled customer policy")
	}
	p.CustomerRef = "another-customer"
	if _, err := s.CreateExecution(ctx, p); !errors.Is(err, state.ErrRunAttributionDenied) {
		t.Fatal("spoofed attribution", err)
	}
	p.CustomerRef = ""
	base := time.Now().UTC()
	claim, err := s.ClaimExecutionForAccount(ctx, a.ID, "budget-test", base, time.Minute)
	if err != nil || claim.ID != first.ID {
		t.Fatal("claim", err)
	}
	if _, err := s.MarkExecutionRunning(ctx, first.ID, *claim.LeaseToken, base.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	completion := state.CompleteExecutionParams{ID: first.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: []byte(`{"rows":3}`), Usage: api.ExecutionUsage{WallTimeMS: 100, CPUTimeMS: 20, PeakMemoryMB: 16}, FinishedAt: base.Add(101 * time.Millisecond)}
	if _, err := s.CompleteExecution(ctx, completion); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteExecution(ctx, completion); err == nil {
		t.Fatal("stale completion accepted")
	}
	u, err = s.GetRunBudget(ctx, a.ID, "customer", policy.Ref)
	if err != nil || u.Usage.Active != 0 || u.Usage.ComputeReservedMBMS != 0 || u.Usage.ComputeUsedMBMS != settled || u.Usage.Invocations != 1 {
		t.Fatal("settlement", u, err)
	}
	policy, err = s.PutRunBudget(ctx, a.ID, "customer", policy.Ref, api.SetRunBudgetRequest{ExpectedRevision: 2, Enabled: true, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	secondIdentity := admissionIdentity(a.ID, "customer-second")
	second, _, err := s.AdmitExecution(ctx, secondIdentity, prepare)
	if err != nil {
		t.Fatal("settled capacity not reusable", err)
	}
	if _, err := s.RequestExecutionCancellation(ctx, a.ID, second.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestExecutionCancellation(ctx, a.ID, second.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AdmitExecution(ctx, admissionIdentity(a.ID, "customer-third"), prepare)
	var budget *state.RunBudgetError
	if !errors.As(err, &budget) || budget.Metric != "invocations" {
		t.Fatal("daily count bypass", err)
	}
	u, err = s.GetRunBudget(ctx, a.ID, "customer", policy.Ref)
	if err != nil || u.Usage.Invocations != 2 || u.Usage.Active != 0 || u.Usage.ComputeUsedMBMS != settled {
		t.Fatal("repeat completion changed allowance", u, err)
	}
	seen := map[string]bool{}
	for _, alert := range u.Alerts {
		if seen[alert.EventID] {
			t.Fatal("duplicate alert")
		}
		seen[alert.EventID] = true
	}
	deliveries, _, err := s.ListAccountReleaseWebhookDeliveries(ctx, a.ID, hook.ID, 100, "")
	if err != nil || len(deliveries) != len(u.Alerts) {
		t.Fatal("missing or duplicate durable budget deliveries", len(deliveries), len(u.Alerts), err)
	}
	for _, delivery := range deliveries {
		var alert api.RunBudgetAlert
		if json.Unmarshal(delivery.Payload, &alert) != nil || !seen[alert.EventID] || strings.Contains(string(delivery.Payload), "sealed-private-source") {
			t.Fatal("unsafe or unbound budget alert", delivery.ID)
		}
	}
	if len(u.Alerts) < 4 {
		t.Fatal("missing thresholds", u.Alerts)
	}
	if err := s.DeleteRunBinding(ctx, a.ID, "api_key", rotated.ID, 1); err != nil {
		t.Fatal(err)
	}
	b, err = s.GetRunBinding(ctx, a.ID, "api_key", rotated.ID)
	if err != nil || b.Enabled || b.Revision != 2 {
		t.Fatal("missing binding tombstone", b, err)
	}
	if _, err := s.PutRunBinding(ctx, a.ID, "api_key", rotated.ID, api.SetRunAttributionBindingRequest{ExpectedRevision: 1, CustomerRef: policy.Ref}); !errors.Is(err, state.ErrRunControlConflict) {
		t.Fatal("stale binding resurrected", err)
	}
	if _, err := s.PutRunBinding(ctx, a.ID, "api_key", rotated.ID, api.SetRunAttributionBindingRequest{ExpectedRevision: 2, CustomerRef: policy.Ref, AgentRef: "replacement-agent"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListRunHistory(ctx, state.RunHistoryQuery{AccountID: a.ID, PrincipalID: rotated.RunsPrincipalID, Filter: api.RunHistoryFilter{CustomerRef: policy.Ref, AgentRef: "report-agent"}, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("scoped history", rows, err)
	}
	if rows[0].Attribution.AgentRef != "report-agent" || rows[1].Attribution.AgentRef != "report-agent" {
		t.Fatal("binding changed historical attribution")
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), key.RunsPrincipalID) || strings.Contains(string(raw), "sealed-private-source") || strings.Contains(string(raw), `"rows":3`) {
		t.Fatal("history leaked private identity, payload or result")
	}
	for _, q := range []state.RunHistoryQuery{{AccountID: a.ID, PrincipalID: uuid.NewString()}, {AccountID: uuid.NewString(), Wide: true}} {
		r, err := s.ListRunHistory(ctx, q)
		if err != nil || len(r) != 0 {
			t.Fatal("history scope leak", r, err)
		}
	}
	if _, err := s.GetRunBudget(ctx, uuid.NewString(), "customer", policy.Ref); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account policy", err)
	}
	other, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRunBudget(ctx, other.ID, "customer", policy.Ref, api.SetRunBudgetRequest{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRunBinding(ctx, other.ID, "api_key", rotated.ID, api.SetRunAttributionBindingRequest{CustomerRef: policy.Ref}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account binding", err)
	}
}

func TestMemRunTaskBudgetAcrossVersions(t *testing.T) {
	exerciseRunTaskBudgetAcrossVersions(t, state.NewMemStore())
}

func TestMemRunComputeReservationRace(t *testing.T) {
	exerciseRunComputeReservationRace(t, state.NewMemStore())
}
func exerciseRunComputeReservationRace(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	hash := []byte(uuid.NewString())
	key, err := s.CreateAPIKey(ctx, a.ID, hash, "compute agent", []string{api.ScopeRunsWrite})
	if err != nil {
		t.Fatal(err)
	}
	key, err = s.APIKeyByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	p := executionTestParams(t, a.ID, time.Now().UTC(), 0, "sealed")
	p.RunsPrincipalID = &key.RunsPrincipalID
	maximum := int64(p.Request.Limits.MemoryMB+api.PerVMOverheadMB) * int64(p.Request.Limits.TimeoutMS)
	if _, err := s.PutRunBudget(ctx, a.ID, "customer", "compute-only", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxComputeMBMSPerDay: &maximum}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRunBinding(ctx, a.ID, "api_key", key.ID, api.SetRunAttributionBindingRequest{CustomerRef: "compute-only"}); err != nil {
		t.Fatal(err)
	}
	prepare := func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return p, nil
	}
	type result struct {
		index int
		row   state.Execution
		err   error
	}
	ch := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			row, _, err := s.AdmitExecution(ctx, admissionIdentity(a.ID, fmt.Sprintf("compute-%d", i)), prepare)
			ch <- result{i, row, err}
		}(i)
	}
	close(start)
	winner := state.Execution{}
	loser := -1
	for range 2 {
		r := <-ch
		if r.err == nil {
			if winner.ID != "" {
				t.Fatal("compute allowance oversubscribed")
			}
			winner = r.row
		} else {
			var budget *state.RunBudgetError
			if !errors.As(r.err, &budget) || budget.Metric != "compute_mb_ms" {
				t.Fatal("wrong compute refusal", r.err)
			}
			loser = r.index
		}
	}
	if winner.ID == "" || loser < 0 {
		t.Fatal("missing concurrent outcome")
	}
	if _, err := s.RequestExecutionCancellation(ctx, a.ID, winner.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	r, replayed, err := s.AdmitExecution(ctx, admissionIdentity(a.ID, fmt.Sprintf("compute-%d", loser)), prepare)
	if err != nil || replayed || r.ID == winner.ID {
		t.Fatal("refusal consumed identity or reservation", err, replayed)
	}
	u, err := s.GetRunBudget(ctx, a.ID, "customer", "compute-only")
	if err != nil || u.Usage.ComputeReservedMBMS != maximum || u.Usage.ComputeUsedMBMS != 0 || u.Usage.Invocations != 2 {
		t.Fatal("compute reservation reconciliation", u, err)
	}
}
func controlTaskParams(t *testing.T, s contractTestStore, account string, version int) state.CreateExecutionParams {
	t.Helper()
	request, meta, err := runtask.Normalize(api.PublishExecutionTaskRequest{Name: "report", Version: version, Definition: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default x => x"}}, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	meta.ID = uuid.NewString()
	v := state.ExecutionTaskVersion{ID: meta.ID, AccountID: account, Name: meta.Name, Version: meta.Version, Digest: meta.Digest, Runtime: meta.Runtime, Metadata: meta, SealedDefinition: []byte("sealed"), PayloadKID: "test"}
	if _, _, err := s.PublishExecutionTaskVersion(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	p := executionTestParams(t, account, time.Now().UTC(), 0, "sealed")
	p.Request, _ = request.Definition.Resolve(api.PlanPro)
	p.SourceBytes = len(request.Definition.Source)
	p.InputBytes = 4
	p.Task = &state.ExecutionTaskAdmission{VersionID: v.ID}
	return p
}
func exerciseRunTaskBudgetAcrossVersions(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p1 := controlTaskParams(t, s, a.ID, 1)
	p2 := controlTaskParams(t, s, a.ID, 2)
	policy, err := s.PutRunBudget(ctx, a.ID, "task", "report", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: runControlInt(1)}})
	if err != nil {
		t.Fatal(err)
	}
	id1 := admissionIdentity(a.ID, "version-one")
	p1.Task.RequestDigest = id1.Fingerprint
	r, _, err := s.AdmitExecution(ctx, id1, func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return p1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	id2 := admissionIdentity(a.ID, "version-two")
	p2.Task.RequestDigest = id2.Fingerprint
	prepare := func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return p2, nil
	}
	if _, _, err := s.AdmitExecution(ctx, id2, prepare); err == nil {
		t.Fatal("changing versions bypassed task cap")
	}
	u, err := s.GetRunBudget(ctx, a.ID, "task", "report")
	if err != nil || u.Usage.Invocations != 1 {
		t.Fatal("refusal consumed invocation", u, err)
	}
	if _, err := s.PutRunBudget(ctx, a.ID, "task", "report", api.SetRunBudgetRequest{ExpectedRevision: policy.Revision, Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: runControlInt(2)}}); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.AdmitExecution(ctx, id2, prepare)
	if err != nil || second.Task.Version != 2 {
		t.Fatal("refusal consumed retry identity", err)
	}
	page, err := s.ListRunHistory(ctx, state.RunHistoryQuery{AccountID: a.ID, Wide: true, Filter: api.RunHistoryFilter{TaskName: "report", TaskVersion: 1}, Limit: 10})
	if err != nil || len(page) != 1 || page[0].ID != r.ID {
		t.Fatal("task/version filter", page, err)
	}
	if _, err := s.RequestExecutionCancellation(ctx, a.ID, r.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestExecutionCancellation(ctx, a.ID, second.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
