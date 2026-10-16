//go:build promotionacceptance && metal

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/routeprobe"
)

// A has eight active operations and a ninth destination. B/C have six each;
// D has four active and a fifth destination. E supplies an independent fleet
// queue rejection while the first four accounts occupy 126 or 127 waiting slots.
// This distinguishes account pressure from fleet pressure without changing any production quota or deadline.
var promotionAdmissionNativeTargets = [...]int{9, 6, 6, 5, 1}

type promotionAdmissionNativeJob struct {
	p                                     *environmentPromotionNative
	environment, id, key, token, baseline string
	admission                             *api.ProjectPromotionAdmission
}

func (j *promotionAdmissionNativeJob) status(t *testing.T) api.ProjectEnvironmentPromotionStatusResponse {
	t.Helper()
	s, err := j.p.owner.GetProjectEnvironmentPromotionStatus(t.Context(), j.p.project.Slug, j.environment, j.id)
	mustProjectWorkerNative(t, err)
	if s.Health == nil {
		t.Fatal("admission job lost health checkpoint")
	}
	return s
}
func (j *promotionAdmissionNativeJob) cancel(t *testing.T) api.ProjectPromotionControl {
	return j.p.controlPromotion(t, j.id, "abort", j.environment)
}
func (j *promotionAdmissionNativeJob) assertUnstarted(t *testing.T) {
	t.Helper()
	s := j.status(t)
	if s.Health.Admission == nil || s.Health.Admission.StartedAt != nil {
		t.Fatal("waiting operation started", j.id, s.Health.Phase)
	}
	for _, w := range s.Workloads {
		if w.TargetDeploymentID != "" {
			t.Fatal("waiting operation acquired a candidate")
		}
	}
	var n int
	mustProjectWorkerNative(t, j.p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE reason=$1", "environment promotion/"+j.id).Scan(&n))
	if n != 0 {
		t.Fatal("waiting operation allocated deployments/instances")
	}
	if j.admission != nil && (!s.Health.Admission.QueuedAt.Equal(j.admission.QueuedAt) || !s.Health.Admission.WaitDeadline.Equal(j.admission.WaitDeadline)) {
		t.Fatal("queue identity/deadline changed")
	}
}
func admitPromotionNative(t *testing.T, p *environmentPromotionNative, environment string) *promotionAdmissionNativeJob {
	t.Helper()
	preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, environment, "staging", true, true, false, false)
	mustProjectWorkerNative(t, err)
	if !preview.CanPromote {
		t.Fatalf("queue preview refused: %v", preview.BlockingReasons)
	}
	graph, err := p.owner.GetActiveProjectReleaseSet(t.Context(), p.project.Slug, environment)
	mustProjectWorkerNative(t, err)
	j := &promotionAdmissionNativeJob{p: p, environment: environment, key: uuid.NewString(), token: preview.PromotionToken, baseline: graph.ID}
	result, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, environment, api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: j.token}, j.key)
	mustProjectWorkerNative(t, err)
	if result.Status != "running" || result.PromotionID == "" {
		t.Fatal("admission missing durable operation")
	}
	j.id = result.PromotionID
	j.admission = j.status(t).Health.Admission
	return j
}

// The proxy only permits real gateway responses for explicitly selected revision
// pins. Blocked requests return an unattributed 503, never synthetic success.
type promotionAdmissionNativeFault struct {
	mu      sync.RWMutex
	allowed map[string]bool
}

func (f *promotionAdmissionNativeFault) allow(ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		f.allowed[id] = true
	}
}
func (f *promotionAdmissionNativeFault) block(ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		delete(f.allowed, id)
	}
}
func newPromotionAdmissionNativeFault(t *testing.T, p *environmentPromotionNative) *promotionAdmissionNativeFault {
	t.Helper()
	target, err := url.Parse(p.h.GatewayURL)
	mustProjectWorkerNative(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadGateway) }
	f := &promotionAdmissionNativeFault{allowed: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.RLock()
		allowed := f.allowed[r.Header.Get(routeprobe.DeploymentHeader)]
		f.mu.RUnlock()
		if !allowed {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	mustProjectWorkerNative(t, p.h.SetAPIDEnv("FAAS_ROUTE_PROBE_URL", server.URL))
	mustProjectWorkerNative(t, p.h.RestartAPID())
	return f
}

type promotionAdmissionNativePeaks struct {
	Active, Account, Total, TotalAccount, Queued, QueuedAccount int
	err                                                         error
}

func observePromotionAdmissionNative(ctx context.Context, p *environmentPromotionNative, out chan<- promotionAdmissionNativePeaks) {
	sample := promotionAdmissionNativePeaks{}
	defer func() { out <- sample }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var active, account, total, totalAccount, queued, queuedAccount int
		err := p.h.Pool.QueryRow(ctx, `SELECT coalesce(sum(ordinary),0)::int,coalesce(max(ordinary),0)::int,coalesce(sum(active),0)::int,coalesce(max(active),0)::int,coalesce(sum(queued),0)::int,coalesce(max(queued),0)::int FROM (SELECT p.account_id,count(*) FILTER(WHERE h.phase<>'queued' AND NOT w.urgent) ordinary,count(*) FILTER(WHERE h.phase<>'queued') active,count(*) FILTER(WHERE h.phase='queued') queued FROM project_promotion_health_work w JOIN project_environment_promotions p ON p.id=w.promotion_id JOIN project_environment_promotion_health h ON h.promotion_id=p.id GROUP BY p.account_id) inventory`).Scan(&active, &account, &total, &totalAccount, &queued, &queuedAccount)
		if err != nil {
			if ctx.Err() == nil {
				sample.err = err
			}
			return
		}
		sample.Active = max(sample.Active, active)
		sample.Account = max(sample.Account, account)
		sample.Total = max(sample.Total, total)
		sample.TotalAccount = max(sample.TotalAccount, totalAccount)
		sample.Queued = max(sample.Queued, queued)
		sample.QueuedAccount = max(sample.QueuedAccount, queuedAccount)
		// Exactly one recovery is permitted to bypass operation admission. VM quotas
		// remain unchanged; all other work is ordinary and must obey both caps.
		if active > api.ProjectPromotionAdmissionFleetActive || account > api.ProjectPromotionAdmissionAccountActive || total > api.ProjectPromotionAdmissionFleetActive+1 || totalAccount > api.ProjectPromotionAdmissionAccountActive+1 || queued > api.ProjectPromotionAdmissionFleetQueued || queuedAccount > api.ProjectPromotionAdmissionAccountQueued {
			sample.err = fmt.Errorf("native admission bounds exceeded: %+v", sample)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func promotionAdmissionNativeCounts(t *testing.T, p *environmentPromotionNative) (active, queued int) {
	t.Helper()
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE h.phase<>'queued'),count(*) FILTER(WHERE h.phase='queued') FROM project_promotion_health_work w JOIN project_environment_promotion_health h ON h.promotion_id=w.promotion_id`).Scan(&active, &queued))
	return
}

func TestProjectEnvironmentPromotionAdmissionMetal(t *testing.T) {
	qualifyPromotionAdmissionNativeCapacity(t)
	fixtures := []*environmentPromotionNative{newEnvironmentPromotionNativeConfigured(t, true, nil)}
	for range len(promotionAdmissionNativeTargets) - 1 {
		fixtures = append(fixtures, newEnvironmentPromotionNativeConfigured(t, true, fixtures[0].h))
	}
	first := fixtures[0]
	fault := newPromotionAdmissionNativeFault(t, first)
	t.Cleanup(func() {
		if first.apidStopped {
			if err := first.h.RestartAPID(); err != nil {
				t.Errorf("restore admission owner for shared fixture cleanup: %v", err)
			} else {
				first.apidStopped = false
			}
		}
	})

	destinations := preparePromotionAdmissionNativeTargets(t, fixtures)
	observerCtx, cancel := context.WithCancel(t.Context())
	observed := make(chan promotionAdmissionNativePeaks, 1)
	go observePromotionAdmissionNative(observerCtx, first, observed)
	var once sync.Once
	var peaks promotionAdmissionNativePeaks
	stop := func() { once.Do(func() { cancel(); peaks = <-observed }) }
	t.Cleanup(stop)
	var active, waiting []*promotionAdmissionNativeJob
	for a, p := range fixtures[:4] {
		limit := len(destinations[a])
		if a == 3 {
			limit = 3
		}
		for _, environment := range destinations[a][:limit] {
			j := admitPromotionNative(t, p, environment)
			if j.admission != nil {
				waiting = append(waiting, j)
			} else {
				active = append(active, j)
			}
		}
	}
	if len(active) != api.ProjectPromotionAdmissionFleetActive-1 || len(waiting) != 1 {
		t.Fatal("last-slot race fixture did not reserve exactly one fleet slot")
	}
	race := concurrentPromotionAdmissionNative(t, fixtures[3], destinations[3][3], fixtures[4], destinations[4][0])
	raceActive, raceQueued := 0, 0
	for _, j := range race {
		if j.admission == nil {
			raceActive++
			active = append(active, j)
		} else {
			raceQueued++
			waiting = append(waiting, j)
		}
	}
	if raceActive != 1 || raceQueued != 1 {
		t.Fatal("concurrent HTTP admissions exceeded the final fleet slot")
	}
	drift := admitPromotionNative(t, fixtures[3], destinations[3][4])
	waiting = append(waiting, drift)
	cancelled := admitPromotionNative(t, fixtures[4], destinations[4][0])
	waiting = append(waiting, cancelled)
	head := waiting[0]
	if len(active) != api.ProjectPromotionAdmissionFleetActive || len(waiting) != 4 || head.admission.Reason != "account_capacity" || drift.admission == nil || drift.admission.Reason != "fleet_capacity" {
		t.Fatal("native fixture did not independently saturate both limits")
	}
	var later *promotionAdmissionNativeJob

	// Fill all four queues through HTTP; duplicates are accepted frozen intents,
	// not fabricated state rows. They must acquire no candidate or VM resources.

	counts := map[string]int{}
	for _, j := range waiting {
		counts[j.p.accountID]++
	}
	for index, p := range fixtures[:4] {
		limit := api.ProjectPromotionAdmissionAccountQueued
		if index == 3 {
			limit -= counts[fixtures[4].accountID]
		}
		for counts[p.accountID] < limit {
			environment := destinations[index][0]
			if p == head.p {
				environment = head.environment
			}
			j := admitPromotionNative(t, p, environment)
			j.assertUnstarted(t)
			waiting = append(waiting, j)
			if p == head.p && later == nil {
				later = j
			}
			counts[p.accountID]++
		}
	}
	if len(waiting) != api.ProjectPromotionAdmissionFleetQueued {
		t.Fatal("fleet queue was not filled")
	}
	a, q := promotionAdmissionNativeCounts(t, first)
	if a != api.ProjectPromotionAdmissionFleetActive || q != api.ProjectPromotionAdmissionFleetQueued {
		t.Fatal("saturation inventory differs", a, q)
	}
	assertPromotionAdmissionNativeRejected(t, head.p, head.environment, "account")
	assertPromotionAdmissionNativeRejected(t, fixtures[4], destinations[4][0], "fleet")
	for _, j := range waiting {
		j.assertUnstarted(t)
	}
	mustProjectWorkerNative(t, first.h.RestartAPID())
	restartedAt := time.Now().UTC()
	var queueEvidence []map[string]any
	for _, j := range waiting {
		j.assertUnstarted(t)
		if j.status(t).Health.Phase != "queued" {
			t.Fatal("restart started saturated work")
		}
		queueEvidence = append(queueEvidence, map[string]any{"promotion_id": j.id, "account_id": j.p.accountID, "queued_at": j.admission.QueuedAt, "wait_deadline": j.admission.WaitDeadline})
	}
	if later == nil || later.p != head.p {
		t.Fatal("FIFO fixture ordering changed")
	}

	cancelled.cancel(t)
	cancelled.assertUnstarted(t)
	for _, j := range waiting {
		if j != head && j != later && j != drift && j != cancelled {
			j.cancel(t)
			j.assertUnstarted(t)
		}
	}
	changed := drift.p.config(t, drift.environment, "changed-while-queued")
	if activeCount, _ := promotionAdmissionNativeCounts(t, first); activeCount != api.ProjectPromotionAdmissionFleetActive {
		t.Fatal("saturation ended before dispatch fault")
	}

	active[0].cancel(t) // release precisely one A/fleet slot, preserving the others
	waitProjectWorkerNative(t, time.Minute, "validated queued dispatch", func() bool { return head.status(t).Health.Admission.StartedAt != nil })
	mustProjectWorkerNative(t, first.h.KillAPID())
	first.apidStopped = true
	dispatched := readPromotionAdmissionNativeHealth(t, head)
	if dispatched.Phase != "preflight" || dispatched.Admission.StartedAt == nil {
		t.Fatal("did not interrupt dispatch before preparation")
	}
	assertPromotionAdmissionNativeCandidateCount(t, head, 0)
	later.assertUnstarted(t)
	mustProjectWorkerNative(t, first.h.RestartAPID())
	first.apidStopped = false
	replay, err := head.p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), head.p.project.Slug, head.environment, api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: head.token}, head.key)
	mustProjectWorkerNative(t, err)
	if replay.PromotionID != head.id {
		t.Fatal("dispatch replay allocated a new operation")
	}
	later.cancel(t)
	later.assertUnstarted(t)
	waitProjectWorkerNative(t, 2*time.Minute, "candidate after dispatch restart", func() bool { return promotionAdmissionNativeCandidate(head.status(t)) != "" })
	h := head.status(t).Health
	if !h.StartedAt.Equal(dispatched.StartedAt) || !h.Deadline.Equal(dispatched.Deadline) || !h.Admission.WaitDeadline.Equal(head.admission.WaitDeadline) || h.Deadline.Sub(h.StartedAt) != api.ProjectReleaseVerificationTimeout {
		t.Fatal("dispatch/restart changed an original deadline")
	}
	fault.allow(promotionAdmissionNativeCandidate(head.status(t)), promotionAdmissionNativeBaseline(t, head))
	windows := waitPromotionAdmissionNativeSuccess(t, head)
	assertPromotionAdmissionNativeCandidateCount(t, head, 1)
	waitProjectWorkerNative(t, time.Minute, "queued intent drift fence", func() bool { return drift.status(t).Status == "failed" })
	drift.assertUnstarted(t)
	if drift.status(t).Health.Phase != "conflicted" {
		t.Fatal("changed waiting intent was not conflicted")
	}
	current, err := drift.p.owner.GetProjectEnvironmentConfig(t.Context(), drift.p.project.Slug, drift.environment)
	mustProjectWorkerNative(t, err)
	graph, err := drift.p.owner.GetActiveProjectReleaseSet(t.Context(), drift.p.project.Slug, drift.environment)
	mustProjectWorkerNative(t, err)
	if current.Version != changed.Version || graph.ID != drift.baseline {
		t.Fatal("queued drift overwrote operator intent")
	}
	cancelPromotionAdmissionNativeJobs(t, active[1:])
	recovery := qualifyPromotionAdmissionNativeRecovery(t, fixtures, destinations, fault)
	expiry := qualifyPromotionAdmissionNativeExpiry(t, first, destinations[0])
	stop()
	mustProjectWorkerNative(t, peaks.err)
	if peaks.Active != api.ProjectPromotionAdmissionFleetActive || peaks.Account != api.ProjectPromotionAdmissionAccountActive || peaks.Queued != api.ProjectPromotionAdmissionFleetQueued || peaks.QueuedAccount != api.ProjectPromotionAdmissionAccountQueued || peaks.Total != api.ProjectPromotionAdmissionFleetActive+1 || peaks.TotalAccount != api.ProjectPromotionAdmissionAccountActive+1 {
		t.Fatal("native saturation/recovery boundaries not observed", peaks)
	}
	for _, p := range fixtures {
		var builds, duplicates int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1", p.apps["frontend"].ID).Scan(&builds))
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM (SELECT reason FROM deployments WHERE app_id=$1 AND reason LIKE 'environment promotion/%' GROUP BY reason HAVING count(*)>1) duplicates", p.apps["frontend"].ID).Scan(&duplicates))
		if builds != p.buildCounts[p.apps["frontend"].ID] || duplicates != 0 {
			t.Fatal("native promotion rebuilt or duplicated a candidate")
		}
	}
	headStatus := head.status(t)
	source, err := head.p.store.DeploymentByID(t.Context(), headStatus.Workloads[0].SourceDeploymentID)
	mustProjectWorkerNative(t, err)
	candidate, err := head.p.store.DeploymentByID(t.Context(), headStatus.Workloads[0].TargetDeploymentID)
	mustProjectWorkerNative(t, err)
	if candidate.RootfsKey == "" || candidate.RootfsKey != source.RootfsKey || candidate.SourceSHA256 != source.SourceSHA256 || candidate.ImageDigest != source.ImageDigest {
		t.Fatal("queued promotion changed the approved artifact")
	}
	graph, err = head.p.owner.GetActiveProjectReleaseSet(t.Context(), head.p.project.Slug, head.environment)
	mustProjectWorkerNative(t, err)
	var publications int
	mustProjectWorkerNative(t, head.p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM project_release_members WHERE deployment_id=$1", candidate.ID).Scan(&publications))
	if graph.ID != headStatus.ReleaseGraph.TargetReleaseSetID || publications != 1 {
		t.Fatal("restart duplicated or lost queued publication")
	}

	for _, p := range fixtures[1:] {
		for _, app := range p.apps {
			mustProjectWorkerNative(t, p.owner.DeleteApp(t.Context(), app.Slug))
		}
		p.cleanupDone = true
	}
	first.cleanup(t)
	if t.Failed() {
		return
	}
	logPromotionNativeEvidence(t, map[string]any{"schema_version": 1, "scenario": "admission", "limits": map[string]any{"fleet_active": api.ProjectPromotionAdmissionFleetActive, "account_active": api.ProjectPromotionAdmissionAccountActive, "fleet_queued": api.ProjectPromotionAdmissionFleetQueued, "account_queued": api.ProjectPromotionAdmissionAccountQueued, "wait_seconds": int(api.ProjectPromotionAdmissionWaitTimeout.Seconds()), "health_seconds": int(api.ProjectReleaseVerificationTimeout.Seconds())}, "peaks": map[string]int{"ordinary_active": peaks.Active, "ordinary_account": peaks.Account, "total_active": peaks.Total, "total_account": peaks.TotalAccount, "queued": peaks.Queued, "queued_account": peaks.QueuedAccount}, "waiting": queueEvidence, "waiting_restart_at": restartedAt, "waiting_restart": true, "concurrent_admission": true, "no_waiting_candidates": true, "account_rejected": true, "fleet_rejected": true, "rejections_unaccepted": true, "fifo": true, "cancelled_id": cancelled.id, "cancelled_without_start": true, "drift_id": drift.id, "drift_conflicted": true, "drift_preserved": true, "dispatch": map[string]any{"promotion_id": head.id, "queued_at": head.admission.QueuedAt, "wait_deadline": head.admission.WaitDeadline, "started_at": dispatched.StartedAt, "health_deadline": dispatched.Deadline, "healthy_windows": windows, "restart_before_preparation": true, "idempotent_replay": true, "candidate_count": 1, "deadlines_preserved": true}, "recovery": recovery, "expiry": expiry, "artifact_reused": true, "publication_unique": true, "cleanup": true})
}

func preparePromotionAdmissionNativeTargets(t *testing.T, fixtures []*environmentPromotionNative) [][]string {
	t.Helper()
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	image, _ := e2etest.HelloImage("admission-baseline", "native baseline")
	ref := registry.AddImage("admission-baseline", image)
	result := make([][]string, len(fixtures))
	for index, p := range fixtures {
		preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "staging", "production", true, false, false, false)
		mustProjectWorkerNative(t, err)
		if !preview.CanPromote {
			t.Fatal("native staging snapshot refused", preview.BlockingReasons)
		}
		snapshot, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "staging", api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: preview.PromotionToken}, uuid.NewString())
		mustProjectWorkerNative(t, err)
		if snapshot.Status != "succeeded" {
			t.Fatal("staging snapshot was not published")
		}
		for i := range promotionAdmissionNativeTargets[index] {
			scope := fmt.Sprintf("admit-%d", i)
			_, err = p.owner.CreateProjectEnvironment(t.Context(), p.project.Slug, api.CreateProjectEnvironmentRequest{Slug: scope, Protected: new(false)})
			mustProjectWorkerNative(t, err)
			d, err := p.owner.Deploy(t.Context(), p.apps["frontend"].Slug, api.CreateDeploymentRequest{Image: ref, Environment: scope})
			mustProjectWorkerNative(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			_, err = e2etest.WaitForDeploymentLive(ctx, t, p.h.Pool, d.ID, 2*time.Minute)
			cancel()
			mustProjectWorkerNative(t, err)
			_, err = p.owner.PublishProjectReleaseSet(t.Context(), p.project.Slug, scope, api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: map[string]string{"frontend": d.ID}})
			mustProjectWorkerNative(t, err)
			result[index] = append(result[index], scope)
		}
	}
	for _, p := range fixtures {
		var builds int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1", p.apps["frontend"].ID).Scan(&builds))
		p.buildCounts[p.apps["frontend"].ID] = builds
	}

	return result
}
func assertPromotionAdmissionNativeRejected(t *testing.T, p *environmentPromotionNative, environment, scope string) {
	t.Helper()
	preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, environment, "staging", true, true, false, false)
	mustProjectWorkerNative(t, err)
	if !preview.CanPromote {
		t.Fatal("full queue preview failed")
	}
	key := uuid.NewString()
	_, err = p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, environment, api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken}, key)
	var problem *api.APIError
	limit := int64(api.ProjectPromotionAdmissionAccountQueued)
	if scope == "fleet" {
		limit = api.ProjectPromotionAdmissionFleetQueued
	}
	if !errors.As(err, &problem) || problem.Problem.Status != 429 || problem.Problem.Code != api.CodeProjectPromotionQueueFull || problem.Problem.Limit == nil || *problem.Problem.Limit != limit || problem.Problem.Observed == nil || *problem.Problem.Observed != limit || (problem.Problem.RetryAfterSeconds == nil || *problem.Problem.RetryAfterSeconds != api.ProjectPromotionAdmissionRetryAfterSeconds) {
		t.Fatalf("wrong %s queue rejection: %v", scope, err)
	}
	var n int
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_promotions WHERE account_id=$1 AND idempotency_key=$2", p.accountID, key).Scan(&n))
	if n != 0 {
		t.Fatal("rejected request persisted an operation")
	}
}
func readPromotionAdmissionNativeHealth(t *testing.T, j *promotionAdmissionNativeJob) *api.ProjectPromotionHealth {
	t.Helper()
	var raw []byte
	mustProjectWorkerNative(t, j.p.h.Pool.QueryRow(t.Context(), "SELECT payload FROM project_environment_promotion_health WHERE promotion_id=$1", j.id).Scan(&raw))
	var h api.ProjectPromotionHealth
	mustProjectWorkerNative(t, json.Unmarshal(raw, &h))
	return &h
}
func assertPromotionAdmissionNativeCandidateCount(t *testing.T, j *promotionAdmissionNativeJob, want int) {
	t.Helper()
	var n int
	mustProjectWorkerNative(t, j.p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE reason=$1", "environment promotion/"+j.id).Scan(&n))
	if n != want {
		t.Fatalf("promotion %s candidate count=%d want=%d", j.id, n, want)
	}
}
func promotionAdmissionNativeCandidate(s api.ProjectEnvironmentPromotionStatusResponse) string {
	if len(s.Workloads) != 1 {
		return ""
	}
	return s.Workloads[0].TargetDeploymentID
}
func promotionAdmissionNativeBaseline(t *testing.T, j *promotionAdmissionNativeJob) string {
	t.Helper()
	s := j.status(t)
	if len(s.Workloads) != 1 || s.Workloads[0].PreviousTargetDeploymentID == "" {
		t.Fatal("missing native baseline")
	}
	return s.Workloads[0].PreviousTargetDeploymentID
}
func waitPromotionAdmissionNativeSuccess(t *testing.T, j *promotionAdmissionNativeJob) []time.Time {
	t.Helper()
	var windows []time.Time
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "fresh queued health success", func() bool {
		s := j.status(t)
		h := s.Health
		if s.Status == "failed" {
			t.Fatalf("native queued promotion failed: %s", h.Message)
		}
		if (h.Phase == "verifying" || h.Phase == "finalizing" || h.Phase == "succeeded") && h.CheckedAt != nil && h.Verdict == "healthy" && (len(windows) == 0 || h.CheckedAt.After(windows[len(windows)-1])) {
			windows = append(windows, *h.CheckedAt)
		}
		return h.Phase == "succeeded"
	})
	if len(windows) < api.ProjectReleaseVerificationRounds {
		t.Fatal("queued success lacked fresh minute windows")
	}
	return windows
}
func cancelPromotionAdmissionNativeJobs(t *testing.T, jobs []*promotionAdmissionNativeJob) {
	t.Helper()
	for _, j := range jobs {
		if j.status(t).Status == "running" {
			j.cancel(t)
		}
	}
	waitProjectWorkerNative(t, time.Minute, "ordinary candidate retirement", func() bool {
		for _, j := range jobs {
			var n int
			mustProjectWorkerNative(t, j.p.h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM instances i JOIN deployments d ON d.id=i.deployment_id WHERE d.reason=$1 AND i.state NOT IN ('STOPPED','FAILED','PARKED')`, "environment promotion/"+j.id).Scan(&n))
			if n > 0 {
				return false
			}
		}
		return true
	})
}

func qualifyPromotionAdmissionNativeRecovery(t *testing.T, fixtures []*environmentPromotionNative, targets [][]string, fault *promotionAdmissionNativeFault) map[string]any {
	t.Helper()
	p := fixtures[0]
	urgent := admitPromotionNative(t, p, targets[0][0])
	waitProjectWorkerNative(t, 2*time.Minute, "recovery candidate", func() bool { return promotionAdmissionNativeCandidate(urgent.status(t)) != "" })
	baseline := promotionAdmissionNativeBaseline(t, urgent)
	candidate := promotionAdmissionNativeCandidate(urgent.status(t))
	fault.allow(baseline, candidate)
	waitProjectWorkerNative(t, 4*time.Minute, "recovery case cutover", func() bool {
		s := urgent.status(t)
		if s.Status == "failed" {
			t.Fatal("recovery fixture failed before cutover")
		}
		return s.Health.Phase == "verifying"
	})
	selected := urgent.status(t).ReleaseGraph.TargetReleaseSetID
	retained, err := p.store.ProjectReleaseSetByID(t.Context(), p.accountID, p.project.ID, urgent.environment, urgent.baseline)
	mustProjectWorkerNative(t, err)
	if retained.ExpiresAt == nil {
		t.Fatal("recovery baseline lacks bounded retention")
	}
	// Seven A requests plus B/C's six and D's four keep this operation at the
	// 24-operation boundary. All blocked responses are real, unattributed faults.
	ordinary := fillPromotionAdmissionNativeActive(t, fixtures, targets, true)
	active, _ := promotionAdmissionNativeCounts(t, p)
	if active != api.ProjectPromotionAdmissionFleetActive {
		t.Fatal("restoration was not requested under saturation")
	}
	fault.block(baseline, candidate)
	blocked := urgent.cancel(t)
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "real blocked restoration deadline", func() bool {
		s := urgent.status(t)
		if s.ReleaseGraph.RestoredTargetReleaseSetID != "" {
			t.Fatal("unknown evidence restored a baseline")
		}
		if s.Health.Control == nil || !s.Health.Control.Deadline.Equal(blocked.Deadline) {
			t.Fatal("blocked restoration deadline changed")
		}
		return s.Health.Phase == "rollback_failed" && s.Health.Control.Status == "blocked"
	})
	blockedAt := urgent.status(t).Health.Control.ResolvedAt
	if blockedAt == nil || blockedAt.Before(blocked.Deadline) {
		t.Fatal("blocked restoration did not exercise the full deadline")
	}
	cancelPromotionAdmissionNativeJobs(t, ordinary)
	ordinary = fillPromotionAdmissionNativeActive(t, fixtures, targets, false)
	active, _ = promotionAdmissionNativeCounts(t, p)
	if active != api.ProjectPromotionAdmissionFleetActive {
		t.Fatal("recovery did not start against a saturated fleet")
	}
	waiting := admitPromotionNative(t, fixtures[4], targets[4][0])
	waiting.assertUnstarted(t)
	receipt := p.controlPromotion(t, urgent.id, "recover", urgent.environment)
	if receipt.Request.RequestID == blocked.Request.RequestID {
		t.Fatal("recovery reused blocked intent")
	}
	active, _ = promotionAdmissionNativeCounts(t, p)
	var accountActive int
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM project_promotion_health_work w JOIN project_environment_promotion_health h ON h.promotion_id=w.promotion_id WHERE w.account_id=$1 AND h.phase<>'queued'", p.accountID).Scan(&accountActive))
	if active != api.ProjectPromotionAdmissionFleetActive+1 || accountActive != api.ProjectPromotionAdmissionAccountActive+1 {
		t.Fatal("recovery did not bypass admission")
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	waiting.assertUnstarted(t)
	// The candidate remains inaccessible: only actual baseline gateway probes may
	// qualify recovery, while ordinary jobs continue receiving dispatcher turns.
	fault.allow(baseline)
	var windows []time.Time
	ordinaryProgress := false
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "fresh recovery under fleet pressure", func() bool {
		s := urgent.status(t)
		h := s.Health
		if h.Control == nil || h.Control.Request != receipt.Request || !h.Control.Deadline.Equal(receipt.Deadline) {
			t.Fatal("recovery receipt changed on restart")
		}
		if h.CheckedAt != nil && h.Verdict == "healthy" && (len(windows) == 0 || h.CheckedAt.After(windows[len(windows)-1])) {
			windows = append(windows, *h.CheckedAt)
		}
		if !ordinaryProgress {
			for _, j := range ordinary {
				current := j.status(t)
				if current.Health.CheckedAt != nil && current.Health.CheckedAt.After(receipt.RequestedAt) {
					ordinaryProgress = true
					break
				}
			}
		}
		waiting.assertUnstarted(t)
		if h.Control.Status == "blocked" || h.Phase == "conflicted" {
			t.Fatal("native recovery failed")
		}
		return h.Control.Status == "restored"
	})
	if !ordinaryProgress || len(windows) < api.ProjectReleaseVerificationRounds {
		t.Fatal("recovery lacked ordinary progress or real healthy rounds")
	}
	s := urgent.status(t)
	graph, err := p.owner.GetActiveProjectReleaseSet(t.Context(), p.project.Slug, urgent.environment)
	mustProjectWorkerNative(t, err)
	if s.ReleaseGraph.RestoredTargetReleaseSetID == "" || graph.ID != s.ReleaseGraph.RestoredTargetReleaseSetID || len(graph.Members) != 1 || graph.Members[0].DeploymentID != baseline {
		t.Fatal("recovery did not restore the captured baseline")
	}
	retention, err := p.store.ProjectReleaseSetByID(t.Context(), p.accountID, p.project.ID, urgent.environment, urgent.baseline)
	mustProjectWorkerNative(t, err)
	if retention.ExpiresAt == nil || !retention.ExpiresAt.Equal(*retained.ExpiresAt) {
		t.Fatal("recovery extended original retention")
	}
	waiting.cancel(t)
	waiting.assertUnstarted(t)
	cancelPromotionAdmissionNativeJobs(t, ordinary)
	return map[string]any{"promotion_id": urgent.id, "request_id": receipt.Request.RequestID, "blocked_request_id": blocked.Request.RequestID, "blocked_requested_at": blocked.RequestedAt, "blocked_deadline": blocked.Deadline, "blocked_at": blockedAt, "requested_at": receipt.RequestedAt, "deadline": receipt.Deadline, "healthy_windows": windows, "active_before": api.ProjectPromotionAdmissionFleetActive, "active_after": api.ProjectPromotionAdmissionFleetActive + 1, "account_after": accountActive, "status": "restored", "selected_before": selected, "selected_after": graph.ID, "baseline_deployment": baseline, "restart": true, "waiting_preserved": true, "ordinary_progress": ordinaryProgress, "retention_preserved": true, "baseline_only": true}
}
func fillPromotionAdmissionNativeActive(t *testing.T, fixtures []*environmentPromotionNative, targets [][]string, urgentAlreadyActive bool) []*promotionAdmissionNativeJob {
	t.Helper()
	var jobs []*promotionAdmissionNativeJob
	for a, p := range fixtures[:4] {
		n := []int{8, 6, 6, 4}[a]
		start := 0
		if a == 0 {
			start = 1
			if urgentAlreadyActive {
				n--
			}
		}
		for i := range n {
			j := admitPromotionNative(t, p, targets[a][i+start])
			if j.admission != nil {
				t.Fatal("fresh ordinary fixture unexpectedly queued")
			}
			jobs = append(jobs, j)
		}
	}
	return jobs
}
func qualifyPromotionAdmissionNativeExpiry(t *testing.T, p *environmentPromotionNative, targets []string) map[string]any {
	t.Helper()
	var active []*promotionAdmissionNativeJob
	for _, scope := range targets[:api.ProjectPromotionAdmissionAccountActive] {
		j := admitPromotionNative(t, p, scope)
		if j.admission != nil {
			t.Fatal("expiry fixture unexpectedly queued active work")
		}
		active = append(active, j)
	}
	waiting := admitPromotionNative(t, p, targets[api.ProjectPromotionAdmissionAccountActive])
	waiting.assertUnstarted(t)
	if waiting.admission.WaitDeadline.Sub(waiting.admission.QueuedAt) != api.ProjectPromotionAdmissionWaitTimeout {
		t.Fatal("expiry fixture changed queue timeout")
	}
	mustProjectWorkerNative(t, p.h.KillAPID())
	p.apidStopped = true
	waitProjectWorkerNative(t, api.ProjectPromotionAdmissionWaitTimeout+time.Minute, "full queue timeout across APID outage", func() bool { return !time.Now().Before(waiting.admission.WaitDeadline) })
	before := readPromotionAdmissionNativeHealth(t, waiting)
	if before.Admission.StartedAt != nil || before.Phase != "queued" {
		t.Fatal("offline queue was advanced")
	}
	assertPromotionAdmissionNativeCandidateCount(t, waiting, 0)
	mustProjectWorkerNative(t, p.h.RestartAPID())
	p.apidStopped = false
	waitProjectWorkerNative(t, 2*time.Minute, "expired waiting operation fails before dispatch", func() bool { return waiting.status(t).Status == "failed" })
	observedAt := time.Now().UTC()
	waiting.assertUnstarted(t)
	h := waiting.status(t).Health
	if h.Admission.Reason != "queue_timeout" {
		t.Fatal("expired queue failed for the wrong reason")
	}
	graph, err := p.owner.GetActiveProjectReleaseSet(t.Context(), p.project.Slug, waiting.environment)
	mustProjectWorkerNative(t, err)
	if graph.ID != waiting.baseline {
		t.Fatal("queue expiry changed destination selection")
	}
	cancelPromotionAdmissionNativeJobs(t, active)
	return map[string]any{"promotion_id": waiting.id, "queued_at": waiting.admission.QueuedAt, "wait_deadline": waiting.admission.WaitDeadline, "observed_at": observedAt, "reason": h.Admission.Reason, "restart": true, "no_start": true, "baseline_preserved": true}
}

func concurrentPromotionAdmissionNative(t *testing.T, p *environmentPromotionNative, environment string, other *environmentPromotionNative, otherEnvironment string) []*promotionAdmissionNativeJob {
	t.Helper()
	ctx := t.Context()
	jobs := []*promotionAdmissionNativeJob{{p: p, environment: environment}, {p: other, environment: otherEnvironment}}
	for _, j := range jobs {
		preview, err := j.p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(ctx, j.p.project.Slug, j.environment, "staging", true, true, false, false)
		mustProjectWorkerNative(t, err)
		if !preview.CanPromote {
			t.Fatal("concurrent native preview refused")
		}
		graph, err := j.p.owner.GetActiveProjectReleaseSet(ctx, j.p.project.Slug, j.environment)
		mustProjectWorkerNative(t, err)
		j.baseline, j.key, j.token = graph.ID, uuid.NewString(), preview.PromotionToken
	}
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, j := range jobs {
		wg.Go(func() {
			<-start
			result, err := j.p.owner.PromoteProjectEnvironmentWithIdempotencyKey(ctx, j.p.project.Slug, j.environment, api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: j.token}, j.key)
			if err != nil {
				errs[i] = err
				return
			}
			j.id = result.PromotionID
			status, err := j.p.owner.GetProjectEnvironmentPromotionStatus(ctx, j.p.project.Slug, j.environment, j.id)
			if err != nil {
				errs[i] = err
				return
			}
			if status.Health == nil || result.Status != "running" {
				errs[i] = fmt.Errorf("concurrent admission lost health state")
				return
			}
			j.admission = status.Health.Admission
		})
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		mustProjectWorkerNative(t, err)
	}
	return jobs
}

func qualifyPromotionAdmissionNativeCapacity(t *testing.T) {
	t.Helper()
	limits := api.MustLimitsFor(api.PlanScale)
	// Baselines + eight ordinary candidates + one recovering candidate, plus
	// production/staging, fit within the fixture's ordinary per-app quota.
	if len(promotionAdmissionNativeTargets) != 5 {
		t.Fatal("five accounts are required to independently exercise the pending fleet bound")
	}
	total := 0
	for i, targets := range promotionAdmissionNativeTargets {
		active := []int{8, 6, 6, 4, 1}[i]
		extra := 0
		if i == 0 {
			extra = 1
		}
		instances := targets + active + extra + 2
		if instances > limits.MaxConcurrency {
			t.Fatal("native admission fixture exceeds app concurrency")
		}
		total += instances * (512 + api.PerVMOverheadMB)
	}
	if total > api.RAMAdmissionCeilingMB {
		t.Fatal("native admission fixture exceeds host RAM admission")
	}
	if api.ProjectPromotionAdmissionFleetActive != 8+6+6+4 || api.ProjectPromotionAdmissionAccountActive != 8 || api.ProjectPromotionAdmissionFleetQueued != 4*api.ProjectPromotionAdmissionAccountQueued {
		t.Fatal("native admission fixture no longer matches production bounds")
	}
}
