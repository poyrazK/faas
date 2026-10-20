//go:build promotionacceptance && metal

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

// The fault proxy interrupts real gateway requests. It never returns a successful
// probe, forges a gateway attribution header, or writes runtime/health evidence.
type promotionNativeProbeFault struct {
	blocked    atomic.Bool
	delayed    atomic.Bool
	delayAfter atomic.Bool
	started    chan struct{}
	active     atomic.Int32
}

func newPromotionNativeProbeFault(t *testing.T, p *environmentPromotionNative) *promotionNativeProbeFault {
	t.Helper()
	target, err := url.Parse(p.h.GatewayURL)
	mustProjectWorkerNative(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadGateway) }
	fault := &promotionNativeProbeFault{started: make(chan struct{}, 1)}
	baseline := map[string]bool{}
	for _, member := range p.baseline.Members {
		baseline[member.DeploymentID] = true
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		// After-response faults must wait for an actual candidate request;
		// holding a baseline probe would leave the dark frontend unwoken.
		if fault.delayed.Load() && fault.delayAfter.Load() && !baseline[response.Request.Header.Get(routeprobe.DeploymentHeader)] && strings.HasPrefix(response.Request.Host, p.apps["frontend"].Slug+".") {
			select {
			case fault.started <- struct{}{}:
			default:
			}
			select {
			case <-response.Request.Context().Done():
			case <-time.After(20 * time.Second):
			}
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fault.blocked.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if fault.delayed.Load() && !fault.delayAfter.Load() && strings.HasPrefix(r.Host, p.apps["frontend"].Slug+".") {
			fault.active.Add(1)
			defer fault.active.Add(-1)
			select {
			case fault.started <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Second):
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	mustProjectWorkerNative(t, p.h.SetAPIDEnv("FAAS_ROUTE_PROBE_URL", server.URL))
	mustProjectWorkerNative(t, p.h.RestartAPID())
	return fault
}

func TestProjectEnvironmentPromotionControlsMetal(t *testing.T) {
	p := newEnvironmentPromotionNative(t)
	fault := newPromotionNativeProbeFault(t, p)
	// One set of real source builds, three independent public admissions.
	early := p.qualifyEarlyAbort(t, fault)
	restored := p.qualifyOperatorRestoration(t, fault, false)
	recovered := p.qualifyOperatorRestoration(t, fault, true)
	p.cleanup(t)
	if t.Failed() {
		return
	}
	logPromotionNativeEvidence(t, map[string]any{"schema_version": 1, "scenario": "operator_controls", "early": early, "restored": restored, "recovered": recovered, "cleanup": true})
}

func logPromotionNativeEvidence(t *testing.T, evidence any) {
	t.Helper()
	raw, err := json.Marshal(evidence)
	mustProjectWorkerNative(t, err)
	t.Logf("environment promotion native: %s", raw)
}

func (p *environmentPromotionNative) refreshControlBaseline(t *testing.T) {
	t.Helper()
	current, err := p.owner.GetActiveProjectReleaseSet(t.Context(), p.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	deployments := map[string]string{}
	for _, member := range current.Members {
		for name, app := range p.apps {
			if app.ID == member.AppID {
				deployments[name] = member.DeploymentID
			}
		}
	}
	p.baseline, err = p.owner.PublishProjectReleaseSet(t.Context(), p.project.Slug, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: deployments})
	mustProjectWorkerNative(t, err)
}

func (p *environmentPromotionNative) admitControlPromotion(t *testing.T) string {
	t.Helper()
	for _, app := range p.apps {
		var count int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE app_id=$1", app.ID).Scan(&count))
		p.counts[app.ID] = count
	}
	preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "production", "staging", true, true, false, true)
	mustProjectWorkerNative(t, err)
	if !preview.CanPromote {
		t.Fatalf("operator native admission refused: %v", preview.BlockingReasons)
	}
	accepted, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken}, uuid.NewString())
	mustProjectWorkerNative(t, err)
	if accepted.Status != "running" || accepted.PromotionID == "" {
		t.Fatal("missing durable operator promotion")
	}
	return accepted.PromotionID
}

func (p *environmentPromotionNative) controlPromotion(t *testing.T, id, action string, environment ...string) api.ProjectPromotionControl {
	t.Helper()
	scope := "production"
	if len(environment) > 0 {
		scope = environment[0]
	}
	requestID := strings.ReplaceAll(uuid.NewString(), "-", "")
	var receipt api.ProjectPromotionControl
	waitProjectWorkerNative(t, 10*time.Second, "generation-bound "+action, func() bool {
		s, err := p.owner.GetProjectEnvironmentPromotionStatus(t.Context(), p.project.Slug, scope, id)
		mustProjectWorkerNative(t, err)
		if s.Health == nil {
			t.Fatal("operator promotion lost health checkpoint")
		}
		receipt, err = p.owner.ControlProjectEnvironmentPromotion(t.Context(), p.project.Slug, scope, id, action, api.ProjectPromotionControlRequest{RequestID: requestID, ExpectedGeneration: s.Health.Generation})
		if err != nil {
			// Only a concurrent checkpoint advance permits reinspection. Other errors fail.
			var problem *api.APIError
			if errors.As(err, &problem) && problem.Problem.Status == http.StatusConflict {
				return false
			}
			mustProjectWorkerNative(t, err)
		}
		return err == nil
	})
	return receipt
}

func (p *environmentPromotionNative) replayControl(t *testing.T, id string, want api.ProjectPromotionControl) {
	t.Helper()
	got, err := p.owner.ControlProjectEnvironmentPromotion(t.Context(), p.project.Slug, "production", id, want.Action, want.Request)
	mustProjectWorkerNative(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("operator replay changed the durable receipt")
	}
}

func (p *environmentPromotionNative) qualifyEarlyAbort(t *testing.T, fault *promotionNativeProbeFault) map[string]any {
	t.Helper()
	p.refreshControlBaseline(t)
	p.baselineSamples(t)
	fault.delayed.Store(true)
	id := p.admitControlPromotion(t)
	var candidate state.Instance
	waitProjectWorkerNative(t, time.Minute, "pre-cutover candidate startup", func() bool {
		s := p.status(t, id)
		if s.Health.Phase != "preflight" {
			t.Fatal("candidate cut over before early abort")
		}
		if s.Health.Worker == nil || s.Health.Worker.InstanceID == "" {
			return false
		}
		candidate = p.worker(t, s.Health.Worker.DeploymentID)
		return true
	})
	// The owner is inside a real probe when a different request aborts it.
	select {
	case <-fault.started:
	case <-time.After(time.Minute):
		t.Fatal("no in-flight native probe")
	}
	receipt := p.controlPromotion(t, id, "abort")
	if receipt.Status != "aborted" {
		t.Fatal("pre-cutover abort did not resolve immediately")
	}
	fault.delayed.Store(false)
	p.replayControl(t, id, receipt)
	p.assertGraph(t, p.baseline.ID)
	p.assertConfig(t, p.targetConfig.ConfigHash)
	waitProjectWorkerNative(t, 2*time.Minute, "aborted candidate VM and namespace retirement", func() bool {
		current, err := p.store.InstanceByID(t.Context(), candidate.ID)
		mustProjectWorkerNative(t, err)
		if current.State != string(state.StateStopped) && current.State != string(state.StateFailed) {
			return false
		}
		_, err = os.Lstat(filepath.Join("/run/netns", candidate.Netns))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return errors.Is(err, os.ErrNotExist)
	})
	retired, err := p.store.DeploymentByID(t.Context(), candidate.DeploymentID)
	mustProjectWorkerNative(t, err)
	if retired.Status != "superseded" || retired.TrafficPercent != 0 {
		t.Fatal("aborted candidate remained eligible")
	}

	// Observe beyond the canceled probe and the following scheduling poll.
	until := time.Now().Add(25 * time.Second)
	for time.Now().Before(until) {
		s := p.status(t, id)
		if s.Health.Phase != "aborted" || s.Status != "failed" || s.ReleaseGraph != nil && s.ReleaseGraph.TargetReleaseSetID != "" {
			t.Fatal("late owner revived an aborted promotion")
		}
		p.assertGraph(t, p.baseline.ID)
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(time.Second):
		}
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	p.replayControl(t, id, receipt)
	p.assertGraph(t, p.baseline.ID)
	p.assertConfig(t, p.targetConfig.ConfigHash)
	aborted := p.status(t, id)
	waitProjectWorkerNative(t, 2*time.Minute, "all owned aborted candidates retire", func() bool {
		for _, workload := range aborted.Workloads {
			deployment, e := p.store.DeploymentByID(t.Context(), workload.TargetDeploymentID)
			mustProjectWorkerNative(t, e)
			if deployment.Status != "superseded" || deployment.TrafficPercent != 0 {
				return false
			}
			instances, e := p.store.ListInstancesForApp(t.Context(), deployment.AppID)
			mustProjectWorkerNative(t, e)
			for _, instance := range instances {
				if instance.DeploymentID == deployment.ID && instance.State != string(state.StateStopped) && instance.State != string(state.StateFailed) && instance.State != string(state.StateParked) {
					return false
				}
			}
		}
		return true
	})
	return map[string]any{"promotion_id": id, "request_id": receipt.Request.RequestID, "status": receipt.Status, "restart": true, "late_probe_fenced": true, "candidate_retired": true, "graph_config_preserved": true}
}

func (p *environmentPromotionNative) qualifyOperatorRestoration(t *testing.T, fault *promotionNativeProbeFault, blocked bool) map[string]any {
	t.Helper()
	p.refreshControlBaseline(t)
	// Keep baseline failures below its real 20% health ceiling before admission.
	for i := 0; i < 30; i++ {
		p.job(t, fmt.Sprintf("control-baseline-%s-%d", uuid.NewString(), i), "baseline", "baseline-config")
	}
	var retries []string
	if blocked {
		for i := 0; i < api.ProjectWorkerHealthMinAttempts; i++ {
			p.controlWorker(t, p.baselineWorker(t), "fail-next", http.StatusNoContent)
			payload, err := json.Marshal(map[string]any{"label": fmt.Sprintf("recovery-retry-%d", i)})
			mustProjectWorkerNative(t, err)
			queued, err := p.owner.QueueSend(t.Context(), p.apps["processor"].Slug, api.QueueSendRequest{Environment: "production", QueueName: "orders", Payload: payload, RetryPolicy: &api.RetryPolicyDTO{MaxAttempts: 2, BaseSeconds: 840, MaxSeconds: 840}})
			mustProjectWorkerNative(t, err)
			waitProjectWorkerNative(t, time.Minute, "real pinned baseline retry", func() bool {
				inv, e := p.store.InvocationByID(t.Context(), queued.ID)
				mustProjectWorkerNative(t, e)
				return inv.State == state.InvocationPending && inv.Attempts == 1
			})
			release, _ := p.pin(t, queued.ID)
			if release != p.baseline.ID {
				t.Fatal("recovery retry was not pinned to baseline")
			}
			retries = append(retries, queued.ID)
		}
	}
	id := p.admitControlPromotion(t)
	var cutover api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, 5*time.Minute, "operator scenario cutover", func() bool {
		s := p.status(t, id)
		if s.Health.Phase == "preflight" || s.Health.Phase == "cutover" {
			return false
		}
		if s.Health.Phase != "verifying" || s.ReleaseGraph == nil || s.ReleaseGraph.TargetReleaseSetID == "" {
			t.Fatal("operator scenario failed before cutover")
		}
		cutover = s
		return true
	})
	p.assertArtifacts(t, cutover)
	retained, err := p.store.ProjectReleaseSetByID(t.Context(), p.accountID, p.project.ID, "production", p.baseline.ID)
	mustProjectWorkerNative(t, err)
	if retained.ExpiresAt == nil {
		t.Fatal("cutover did not bound baseline retention")
	}
	p.assertHTTP(t, "tested", "tested-config")
	// A candidate job must remain pinned after the operator restores the baseline.
	p.controlWorker(t, p.worker(t, cutover.Health.Worker.DeploymentID), "fail-next", http.StatusNoContent)
	payload, err := json.Marshal(map[string]any{"label": "candidate-control-retry"})
	mustProjectWorkerNative(t, err)
	queued, err := p.owner.QueueSend(t.Context(), p.apps["processor"].Slug, api.QueueSendRequest{Environment: "production", QueueName: "orders", Payload: payload, RetryPolicy: &api.RetryPolicyDTO{MaxAttempts: 2, BaseSeconds: 180, MaxSeconds: 180}})
	mustProjectWorkerNative(t, err)
	waitProjectWorkerNative(t, time.Minute, "candidate retry pin", func() bool {
		inv, e := p.store.InvocationByID(t.Context(), queued.ID)
		mustProjectWorkerNative(t, e)
		return inv.State == state.InvocationPending && inv.Attempts == 1
	})
	candidateRelease, candidateDeployment := p.pin(t, queued.ID)
	if candidateRelease != cutover.ReleaseGraph.TargetReleaseSetID {
		t.Fatal("candidate job did not pin selected graph")
	}
	_, status := projectWorkerNativeRequest(t, p.h.EdgeURL()+"/control/regress", p.apps["frontend"].Slug+".apps.test.example", http.MethodPost)
	if status != http.StatusNoContent {
		t.Fatal("operator scenario did not regress candidate HTTP health")
	}
	_, status = projectWorkerNativeRequest(t, p.h.EdgeURL()+"/critical", p.apps["frontend"].Slug+".apps.test.example", http.MethodGet)
	if status != http.StatusServiceUnavailable {
		t.Fatal("candidate critical endpoint was not regressed")
	}
	if blocked {
		fault.blocked.Store(true)
	}
	receipt := p.controlPromotion(t, id, "abort")
	original := receipt
	mustProjectWorkerNative(t, p.h.RestartAPID())
	if blocked {
		waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "full real restoration deadline", func() bool {
			s := p.status(t, id)
			p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
			if s.ReleaseGraph.RestoredTargetReleaseSetID != "" {
				t.Fatal("unknown evidence restored the baseline")
			}
			if s.Health.Control == nil || !s.Health.Control.Deadline.Equal(receipt.Deadline) {
				t.Fatal("restart extended control deadline")
			}
			return s.Health.Phase == "rollback_failed" && s.Health.Control.Status == "blocked"
		})
		s := p.status(t, id)
		original = *s.Health.Control
		if original.ResolvedAt == nil || original.ResolvedAt.Before(original.Deadline) {
			t.Fatal("restoration blocked before its real deadline")
		}
		fault.blocked.Store(false)
		receipt = p.controlPromotion(t, id, "recover")
		if receipt.Request.RequestID == original.Request.RequestID || !receipt.Deadline.After(original.Deadline) {
			t.Fatal("recovery did not create a new bounded decision")
		}
		mustProjectWorkerNative(t, p.h.RestartAPID())
		p.replayControl(t, id, original)
	}
	currentRetention, err := p.store.ProjectReleaseSetByID(t.Context(), p.accountID, p.project.ID, "production", p.baseline.ID)
	mustProjectWorkerNative(t, err)
	if currentRetention.ExpiresAt == nil || !currentRetention.ExpiresAt.Equal(*retained.ExpiresAt) {
		t.Fatal("operator control extended original retention")
	}
	baseline := p.worker(t, cutover.Health.Policy.Worker.PreviousDeploymentID)
	p.killWorkerVM(t, baseline)
	replacement := p.replacement(t, baseline)
	if replacement.ID == baseline.ID {
		t.Fatal("baseline VM did not recover")
	}
	var windows []time.Time
	var final api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "fresh baseline-only restoration", func() bool {
		final = p.status(t, id)
		h := final.Health
		if h.CheckedAt != nil && h.Verdict == "healthy" && (len(windows) == 0 || h.CheckedAt.After(windows[len(windows)-1])) {
			windows = append(windows, *h.CheckedAt)
		}
		if h.Control == nil || h.Control.Request.RequestID != receipt.Request.RequestID || !h.Control.Deadline.Equal(receipt.Deadline) {
			t.Fatal("restoration lost receipt identity or deadline")
		}
		if h.Control.Status == "restored" {
			return true
		}
		if h.Control.Status != "pending" {
			t.Fatalf("operator restoration ended: %s", h.Control.Status)
		}
		p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
		return false
	})
	if len(windows) < api.ProjectReleaseVerificationRounds || final.ReleaseGraph == nil || final.ReleaseGraph.RestoredTargetReleaseSetID == "" {
		t.Fatal("restoration lacked fresh baseline rounds")
	}
	p.assertGraph(t, final.ReleaseGraph.RestoredTargetReleaseSetID)
	p.assertConfig(t, p.targetConfig.ConfigHash)
	p.assertHTTP(t, "baseline", "baseline-config")
	p.job(t, "restored-control-admission", "baseline", "baseline-config")
	terminal := *final.Health.Control
	mustProjectWorkerNative(t, p.h.RestartAPID())
	p.replayControl(t, id, terminal)
	if blocked {
		p.replayControl(t, id, original)
	}
	for _, retry := range append(retries, queued.ID) {
		waitProjectWorkerNative(t, 4*time.Minute, "retained pinned retry completion", func() bool {
			inv, e := p.store.InvocationByID(t.Context(), retry)
			mustProjectWorkerNative(t, e)
			return inv.State == state.InvocationCompleted && inv.Attempts == 2
		})
	}
	release, deployment := p.pin(t, queued.ID)
	if release != candidateRelease || deployment != candidateDeployment {
		t.Fatal("operator restoration retargeted an admitted job")
	}
	p.assertArtifacts(t, final)
	result := map[string]any{"promotion_id": id, "request_id": terminal.Request.RequestID, "status": terminal.Status, "requested_at": terminal.RequestedAt, "deadline": terminal.Deadline, "restart": true, "healthy_windows": windows, "retained_recovered": true, "graph_config_restored": true, "candidate_pin_preserved": true, "artifact_reused": true, "retention_preserved": true, "candidate_regressed": true, "blocked": blocked}
	if blocked {
		result["blocked_request_id"] = original.Request.RequestID
		result["blocked_requested_at"] = original.RequestedAt
		result["blocked_at"] = original.ResolvedAt
		result["blocked_deadline"] = original.Deadline
		result["blocked_receipt_replayed"] = true
		result["baseline_retries"] = len(retries)
	}
	return result
}
