//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

const gitOpsNextBody = "reviewed next revision"

type nativeGitOpsJourney struct {
	h                            *e2etest.Harness
	store                        *state.PgStore
	client                       *api.Client
	key                          string
	project                      state.Project
	app, worker                  state.App
	source                       state.EnvironmentGitSource
	original                     api.EnvironmentDefinition
	provider                     gitOpsSourceProvider
	pendingImage, candidateImage string
	pullEntered                  <-chan struct{}
	releasePull                  func()
	bindingID                    string
	trafficFailures              <-chan error
}

func (j *nativeGitOpsJourney) run(t *testing.T) {
	var baseline, changedServing api.EnvironmentGitOpsStatusResponse
	var pending, changed, rolledBack api.ApproveEnvironmentGitRevisionResponse
	if !t.Run("baseline_serving", func(t *testing.T) {
		// Activation is deliberately distinct from serving: a real customer
		// queue delivery is required before this mixed graph can be applied.
		j.waitActivated(t, j.source.ApprovedRevisionID, j.source.Generation)
		firstDelivery := j.sendQueue(t, 0)
		j.bindingID = firstDelivery.QueueBindingID
		j.waitQueue(t, firstDelivery, false)
		baseline = j.waitApplied(t, j.source.ApprovedRevisionID, j.source.Generation)
	}) {
		return
	}
	stopTraffic := j.startTraffic(t)
	defer stopTraffic()
	j.waitHTTPInFlight(t)

	if !t.Run("source_outage_restart", func(t *testing.T) {
		t.Log("GitOps native resilience: source outage and apid restart")
		j.provider.setUnavailable(true)
		j.waitStatus(t, "source outage", api.EnvironmentGitSourcePollCheckInterval+time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
			return s.Source.SourceErrorCode == j.provider.outageCode()
		})
		j.assertAuthority(t, j.source.ApprovedRevisionID, j.source.Generation)
		j.waitQueue(t, j.sendQueue(t, 0), false)
		j.waitHTTPInFlight(t)
		if err := j.h.RestartAPID(); err != nil {
			t.Fatal(err)
		}
		j.assertAuthority(t, j.source.ApprovedRevisionID, j.source.Generation)
		j.provider.setUnavailable(false)
		j.waitStatus(t, "source recovery", 2*time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
			return s.Source.SourceErrorCode == "" && s.Source.SourceCommitSHA == strings.Repeat("a", 40)
		})
	}) {
		return
	}
	if !t.Run("held_revision_supersession", func(t *testing.T) {
		t.Log("GitOps native resilience: crash during preparation and supersede the held revision")
		j.addRevision(t, strings.Repeat("b", 40), j.pendingImage, 1)
		pending = j.approve(t, strings.Repeat("b", 40))
		select {
		case <-j.pullEntered:
		case err := <-j.trafficFailures:
			t.Fatalf("customer traffic failed during preparation: %v", err)
		case <-time.After(3 * time.Minute):
			t.Fatal("pending revision never reached its held image pull")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
		j.waitStatus(t, "pending preparation", time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
			return s.WorkloadEvidence != nil && s.WorkloadEvidence.RevisionID == pending.Revision.ID &&
				s.WorkloadEvidence.GraphPhase == "preparing" && !s.WorkloadEvidence.Activated
		})
		j.waitHTTPInFlight(t)
		if err := j.h.RestartAPID(); err != nil {
			t.Fatal(err)
		}
		j.assertGatewayBody(t, helloBody)
		j.addRevision(t, strings.Repeat("c", 40), j.candidateImage, 2)
		changed = j.approve(t, strings.Repeat("c", 40))
		if changed.Source.Generation <= pending.Source.Generation {
			t.Fatal("supersession did not advance the approved generation")
		}
		j.releasePull()
		activated := j.waitActivated(t, changed.Revision.ID, changed.Source.Generation)
		if activated.WorkloadEvidence.Serving || activated.Source.AppliedRevisionID == changed.Revision.ID {
			t.Fatal("new mixed graph claimed serving before its customer queue delivery")
		}
	}) {
		return
	}
	if !t.Run("scheduler_restart_inflight", func(t *testing.T) {
		t.Log("GitOps native resilience: scheduler crash after activation with a delivery in flight")
		inFlight := j.sendQueue(t, 10000)
		j.waitQueue(t, inFlight, true)
		j.waitHTTPInFlight(t)
		if err := j.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		j.assertGatewayBody(t, gitOpsNextBody)
		if err := j.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		if err := j.h.RestartAPID(); err != nil {
			t.Fatal(err)
		}
		j.waitQueue(t, inFlight, false)
		// A replayed accepted delivery may finish on a retained deployment. A
		// separate fresh delivery must prove the newly selected consumer serves.
		j.waitQueue(t, j.sendQueue(t, 0), false)
		changedServing = j.waitApplied(t, changed.Revision.ID, changed.Source.Generation)
		j.assertQueueContract(t, 2)
		j.assertNoSupersededServing(t, pending.Source.Generation)
	}) {
		return
	}
	if !t.Run("reviewed_rollback", func(t *testing.T) {
		t.Log("GitOps native resilience: reviewed rollback with accepted queue work")
		rollbackDelivery := j.sendQueue(t, 10000)
		j.waitQueue(t, rollbackDelivery, true)
		j.waitHTTPInFlight(t)
		rolledBack = j.approve(t, strings.Repeat("a", 40))
		if rolledBack.Revision.ID != j.source.ApprovedRevisionID || rolledBack.Source.Generation <= changed.Source.Generation {
			t.Fatal("rollback did not reapprove the original immutable revision at a new generation")
		}
		rollbackActivated := j.waitActivated(t, rolledBack.Revision.ID, rolledBack.Source.Generation)
		if rollbackActivated.WorkloadEvidence.GraphID == baseline.WorkloadEvidence.GraphID ||
			rollbackActivated.WorkloadEvidence.GraphID == changedServing.WorkloadEvidence.GraphID {
			t.Fatal("rollback reused an earlier graph's qualification authority")
		}
		j.waitQueue(t, rollbackDelivery, false)
		j.waitQueue(t, j.sendQueue(t, 0), false)
		j.waitApplied(t, rolledBack.Revision.ID, rolledBack.Source.Generation)
		j.assertQueueContract(t, 1)
		j.assertGatewayBody(t, helloBody)
		j.assertNoSupersededServing(t, pending.Source.Generation)
	}) {
		return
	}
	if !t.Run("override_repair", func(t *testing.T) {
		j.repairOverride(t, rolledBack)
	}) {
		return
	}
	if !t.Run("retirement_recovery", func(t *testing.T) {
		j.runWorkloadRetirement(t)
	}) {
		return
	}
}

func (j *nativeGitOpsJourney) addRevision(t *testing.T, sha, image string, concurrency int) {
	t.Helper()
	raw, err := json.Marshal(j.original)
	if err != nil {
		t.Fatal(err)
	}
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	apiWorkload := definition.Workloads["api"]
	apiWorkload.Source.Image = image
	definition.Workloads["api"] = apiWorkload
	worker := definition.Workloads["worker"]
	binding := worker.QueueBindings["orders"]
	binding.MaxConcurrency = concurrency
	worker.QueueBindings["orders"] = binding
	definition.Workloads["worker"] = worker
	j.provider.add(t, sha, definition)
}

func (j *nativeGitOpsJourney) approve(t *testing.T, sha string) api.ApproveEnvironmentGitRevisionResponse {
	t.Helper()
	preview, err := j.client.PreviewEnvironmentGitRevision(t.Context(), j.project.Slug, "production",
		api.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatalf("review immutable revision: %v", err)
	}
	approved, err := j.client.ApproveEnvironmentGitRevision(t.Context(), j.project.Slug, "production",
		api.ApproveEnvironmentGitRevisionRequest{CommitSHA: sha, DefinitionDigest: preview.DefinitionDigest, ExpectedGeneration: preview.Generation})
	if err != nil {
		t.Fatalf("approve immutable revision: %v", err)
	}
	return approved
}

func (j *nativeGitOpsJourney) waitStatus(t *testing.T, phase string, timeout time.Duration, ready func(api.EnvironmentGitOpsStatusResponse) bool) api.EnvironmentGitOpsStatusResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var last api.EnvironmentGitOpsStatusResponse
	for {
		j.checkTraffic(t)
		current, err := j.client.GetEnvironmentGitOps(ctx, j.project.Slug, "production")
		if err != nil {
			// A deadline inside the request must still report the last
			// status that was actually read.
			if ctx.Err() != nil {
				t.Fatalf("%s timed out: source=%+v evidence=%+v", phase, last.Source, last.WorkloadEvidence)
			}
			t.Fatalf("%s status: %v", phase, err)
		}
		last = current
		if ready(last) {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s timed out: source=%+v evidence=%+v", phase, last.Source, last.WorkloadEvidence)
		case <-ticker.C:
		}
	}
}

func (j *nativeGitOpsJourney) waitActivated(t *testing.T, revision string, generation int64) api.EnvironmentGitOpsStatusResponse {
	t.Helper()
	return j.waitStatus(t, "native graph activation", 12*time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
		e := s.WorkloadEvidence
		return s.Source.ApprovedRevisionID == revision && s.Source.Generation == generation && e != nil &&
			e.RevisionID == revision && e.Generation == generation && e.Qualified && e.Activated &&
			e.CapturesRecorded == 2 && e.RestoresRecorded == 2 && e.SmokesRecorded == 2
	})
}

func (j *nativeGitOpsJourney) waitApplied(t *testing.T, revision string, generation int64) api.EnvironmentGitOpsStatusResponse {
	t.Helper()
	return j.waitStatus(t, "native graph serving", 4*time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
		return s.Source.AppliedRevisionID == revision && s.Source.Generation == generation && s.WorkloadEvidence != nil &&
			s.WorkloadEvidence.RevisionID == revision && s.WorkloadEvidence.Generation == generation && s.WorkloadEvidence.Serving
	})
}

func (j *nativeGitOpsJourney) sendQueue(t *testing.T, holdMilliseconds int) api.QueueSendResponse {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"id": "native-resilience", "gitops_hold_ms": holdMilliseconds})
	if err != nil {
		t.Fatal(err)
	}
	body, code := doReq(t, j.h, j.key, http.MethodPost, "/v1/apps/"+j.worker.Slug+"/queues/send",
		api.QueueSendRequest{Environment: "production", QueueName: "orders", Payload: payload})
	var accepted api.QueueSendResponse
	if code != http.StatusCreated || json.Unmarshal(body, &accepted) != nil || accepted.ID == "" || accepted.QueueBindingID == "" {
		t.Fatalf("customer queue admission: status=%d body=%s", code, body)
	}
	if j.bindingID != "" && accepted.QueueBindingID != j.bindingID {
		t.Fatal("revision transition replaced the scoped customer queue identity")
	}
	return accepted
}

func (j *nativeGitOpsJourney) waitQueue(t *testing.T, accepted api.QueueSendResponse, running bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		j.checkTraffic(t)
		invocation, err := j.store.InvocationByID(ctx, accepted.ID)
		if err != nil || invocation.AppID != j.worker.ID || invocation.Source != state.InvocationQueue ||
			invocation.EnvironmentID != j.source.EnvironmentID || invocation.DeploymentScope != "production" ||
			invocation.QueueName != "orders" || invocation.QueueBindingID != accepted.QueueBindingID {
			t.Fatalf("accepted queue identity changed: %+v %v", invocation, err)
		}
		if invocation.State == state.InvocationFailed || invocation.State == state.InvocationDeadLetter {
			t.Fatalf("accepted customer delivery failed: %+v", invocation)
		}
		if running && invocation.State == state.InvocationDispatching && invocation.ReceivedAt != nil {
			return
		}
		if invocation.State == state.InvocationCompleted {
			if running {
				t.Fatal("delivery completed before the fault boundary")
			}
			if invocation.Outcome == nil || *invocation.Outcome != state.OutcomeSuccess || invocation.Attempts == 0 {
				t.Fatalf("completed delivery has no success evidence: %+v", invocation)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("customer delivery did not reach running=%t: %+v", running, invocation)
		case <-ticker.C:
		}
	}
}

func (j *nativeGitOpsJourney) assertAuthority(t *testing.T, revision string, generation int64) {
	t.Helper()
	s, err := j.client.GetEnvironmentGitOps(t.Context(), j.project.Slug, "production")
	if err != nil || s.Source.ApprovedRevisionID != revision || s.Source.AppliedRevisionID != revision || s.Source.Generation != generation ||
		s.Source.SourceErrorCode != j.provider.outageCode() {
		t.Fatalf("source outage or restart changed approved/serving authority: %+v %v", s.Source, err)
	}
}

func (j *nativeGitOpsJourney) assertQueueContract(t *testing.T, concurrency int) {
	t.Helper()
	binding, err := j.store.QueueBindingByID(t.Context(), j.source.AccountID, j.worker.ID, j.bindingID)
	if err != nil || binding.MaxConcurrency != concurrency {
		t.Fatalf("reviewed queue contract: concurrency=%d, want %d, err=%v", binding.MaxConcurrency, concurrency, err)
	}
}

func (j *nativeGitOpsJourney) assertGatewayBody(t *testing.T, expected string) {
	t.Helper()
	body, status := doGetWithHost(t, j.h.HTTPClient(), gatewayAppURL(j.h, j.app.Slug), j.app.Slug+".apps.test.example", 30*time.Second)
	if status != http.StatusOK || !strings.Contains(string(body), expected) {
		t.Fatalf("healthy gateway lost its selected revision: status=%d body=%s", status, body)
	}
}

func (j *nativeGitOpsJourney) waitHTTPInFlight(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	prefix := fmt.Sprintf("gateway_app_inflight_requests{app=%q} ", j.app.ID)
	for {
		j.checkTraffic(t)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, j.h.GatewayControlURL+"/metrics/gateway-requests", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("observe customer requests at fault boundary: %v", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("read gateway demand: status=%d read=%v close=%v", response.StatusCode, readErr, closeErr)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, prefix) {
				inFlight, err := strconv.ParseFloat(strings.TrimPrefix(line, prefix), 64)
				if err == nil && inFlight >= 1 {
					t.Log("GitOps native resilience: observed customer HTTP demand before fault")
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("fault boundary had no observed customer HTTP request in flight")
		case <-ticker.C:
		}
	}
}

func (j *nativeGitOpsJourney) assertNoSupersededServing(t *testing.T, generation int64) {
	t.Helper()
	var count int
	if err := j.h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM environment_workload_serving_receipts r
		JOIN environment_workload_graphs g ON g.id=r.graph_id WHERE g.source_id=$1 AND g.generation=$2`, j.source.ID, generation).Scan(&count); err != nil || count != 0 {
		t.Fatalf("superseded preparation acquired serving authority: count=%d err=%v", count, err)
	}
}

func (j *nativeGitOpsJourney) repairOverride(t *testing.T, approved api.ApproveEnvironmentGitRevisionResponse) {
	t.Helper()
	_, err := j.client.CreateEnvironmentGitOpsOverride(t.Context(), j.project.Slug, "production", api.EnvironmentGitOpsOverrideRequest{
		Resource: "workload/api", Path: "variables/MODE", Reason: "native acceptance drift", ExpiresAt: time.Now().Add(15 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if body, code := doReq(t, j.h, j.key, http.MethodPut, "/v1/apps/"+j.app.Slug+"/env/MODE?scope=production",
		api.PutAppEnvRequest{Value: "console-drift"}); code != http.StatusOK {
		t.Fatalf("temporary console write: status=%d body=%s", code, body)
	}
	j.waitStatus(t, "override expiry repair", 4*time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
		values, err := j.store.ListAppEnvInScope(t.Context(), j.source.AccountID, j.app.ID, "production")
		return err == nil && len(values) == 1 && values[0].Key == "MODE" && values[0].Value == "reviewed" &&
			s.Source.AppliedRevisionID == approved.Revision.ID && s.Source.Generation == approved.Source.Generation &&
			len(s.Runs) > 0 && s.Runs[0].Status == "converged" && s.WorkloadEvidence != nil && s.WorkloadEvidence.Serving
	})
	j.assertGatewayBody(t, helloBody)
}

// Keep slow requests in flight through each fault and cutover. Failures are
// returned to the test goroutine; cancellation at teardown is the only ignored
// transport error. A superseded image body is never an allowed response.
func (j *nativeGitOpsJourney) startTraffic(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var completed atomic.Int64
	failed := make(chan error, 1)
	j.trafficFailures = failed
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			client := &http.Client{Timeout: 30 * time.Second}
			for ctx.Err() == nil {
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayAppURL(j.h, j.app.Slug)+"?gitops_delay_ms=2000", nil)
				if err != nil {
					select {
					case failed <- err:
					default:
					}
					return
				}
				request.Host = j.app.Slug + ".apps.test.example"
				response, err := client.Do(request)
				if err == nil {
					body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
					closeErr := response.Body.Close()
					if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK ||
						(!strings.Contains(string(body), helloBody) && !strings.Contains(string(body), gitOpsNextBody)) {
						err = errors.Join(fmt.Errorf("healthy traffic failed: status=%d body=%q", response.StatusCode, body), readErr, closeErr)
					}
				}
				if err != nil {
					if ctx.Err() == nil {
						select {
						case failed <- err:
						default:
						}
					}
					return
				}
				completed.Add(1)
			}
		})
	}
	return func() {
		cancel()
		workers.Wait()
		select {
		case err := <-failed:
			t.Error(err)
		default:
		}
		if completed.Load() < 2 {
			t.Error("resilience journey produced no sustained customer traffic")
		}
		t.Logf("GitOps native resilience: %d successful customer requests", completed.Load())
	}
}

func (j *nativeGitOpsJourney) checkTraffic(t *testing.T) {
	t.Helper()
	select {
	case err := <-j.trafficFailures:
		t.Fatalf("customer HTTP traffic failed during reconciliation: %v", err)
	default:
	}
}
