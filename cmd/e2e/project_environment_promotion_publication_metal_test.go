//go:build promotionacceptance && metal

package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/internal/promotionfault"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicationCrashEvidence struct {
	Point               string    `json:"point"`
	PromotionID         string    `json:"promotion_id"`
	ReleaseID           string    `json:"release_id"`
	SelectedGraph       string    `json:"selected_graph"`
	PID                 int       `json:"pid"`
	ObservedAt          time.Time `json:"observed_at"`
	KilledAt            time.Time `json:"killed_at"`
	RestartedAt         time.Time `json:"restarted_at"`
	Phase               string    `json:"phase"`
	Generation          int64     `json:"generation"`
	RegressedRounds     int       `json:"regressed_rounds"`
	Deadline            time.Time `json:"deadline"`
	BarrierDeadline     time.Time `json:"barrier_deadline"`
	Committed           bool      `json:"committed"`
	CheckpointPreserved bool      `json:"checkpoint_preserved"`
}

func TestProjectEnvironmentPromotionPublicationMetal(t *testing.T) {
	p := newEnvironmentPromotionNative(t)
	fault := newPromotionPublicationFault(t, p)
	recovered := p.qualifyPublicationCrash(t, fault, false)
	p.refreshControlBaseline(t)
	newer := p.qualifyPublicationCrash(t, fault, true)
	p.cleanup(t)
	if !t.Failed() {
		logPromotionNativeEvidence(t, map[string]any{"schema_version": 1, "scenario": "publication_crashes", "recovered": recovered, "newer_intent": newer, "cleanup": true})
	}
}

// Six real builds are reused across two independent public admissions. No
// checkpoint, timestamp, publication, probe or worker-result row is injected.
func (p *environmentPromotionNative) qualifyPublicationCrash(t *testing.T, fault *promotionPublicationFault, newer bool) map[string]any {
	t.Helper()
	p.baselineSamples(t)
	baseline := p.baseline.ID
	before := p.publicationCount(t)
	id := p.admitControlPromotion(t)
	points := []string{promotionfault.CutoverBeforeCommit, promotionfault.CutoverAfterCommit, promotionfault.RollbackBeforeCommit, promotionfault.RollbackAfterCommit}
	if newer {
		points = []string{promotionfault.RollbackAfterCommit}
	}
	fault.arm(id, points[0])
	var events []publicationCrashEvidence
	var cutover string
	if !newer {
		for index, point := range points[:2] {
			hit := fault.await(t, p, point)
			expected := baseline
			if point == promotionfault.CutoverAfterCommit {
				expected, cutover = hit.ReleaseID, hit.ReleaseID
			}
			event := p.inspectPublicationBarrier(t, hit, expected)
			p.killPublicationOwner(t, hit, &event)
			fault.arm(id, points[index+1])
			p.restartPublicationOwner(t, &event)
			events = append(events, event)
		}
	} else {
		waitProjectWorkerNative(t, 5*time.Minute, "uninterrupted cutover before rollback acknowledgement fault", func() bool {
			s := p.status(t, id)
			if s.Health.Phase == "preflight" || s.Health.Phase == "cutover" {
				return false
			}
			if s.Health.Phase != "verifying" || s.ReleaseGraph == nil {
				t.Fatal("promotion did not cut over", s.Health.Phase)
			}
			cutover = s.ReleaseGraph.TargetReleaseSetID
			return cutover != ""
		})
	}
	p.assertGraph(t, cutover)
	p.assertConfig(t, p.sourceConfig.ConfigHash)
	p.assertHTTP(t, "tested", "tested-config")
	pins := map[string][2]string{}
	for index := 0; index < api.ProjectWorkerHealthMinAttempts; index++ {
		job := p.job(t, fmt.Sprintf("publication-candidate-%t-%d", newer, index), "tested", "tested-config")
		release, deployment := p.pin(t, job.ID)
		if release != cutover {
			t.Fatal("candidate job used a mixed release graph")
		}
		pins[job.ID] = [2]string{release, deployment}
	}
	_, status := projectWorkerNativeRequest(t, p.h.EdgeURL()+"/control/regress", p.apps["frontend"].Slug+".apps.test.example", http.MethodPost)
	if status != http.StatusNoContent {
		t.Fatal("candidate refused critical-probe regression")
	}
	rollbackPoints := points
	if !newer {
		rollbackPoints = points[2:]
	}
	var restored, selected, config string
	for index, point := range rollbackPoints {
		hit := fault.await(t, p, point)
		expected := cutover
		if point == promotionfault.RollbackAfterCommit {
			expected, restored = hit.ReleaseID, hit.ReleaseID
		}
		event := p.inspectPublicationBarrier(t, hit, expected)
		if newer {
			// The transaction has committed but the controller has not received its
			// result. New customer intent arrives through the public API here.
			p.refreshControlBaseline(t)
			selected = p.baseline.ID
			config = p.config(t, "production", "operator-config").ConfigHash
			if selected == restored {
				t.Fatal("new operator graph did not replace the restored graph")
			}
			if config == p.targetConfig.ConfigHash || config == p.sourceConfig.ConfigHash {
				t.Fatal("new operator configuration did not change the document")
			}
		}
		p.killPublicationOwner(t, hit, &event)
		if index+1 < len(rollbackPoints) {
			fault.arm(id, rollbackPoints[index+1])
		}
		p.restartPublicationOwner(t, &event)
		events = append(events, event)
	}
	var final api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, 5*time.Minute, "publication checkpoint recovery after process death", func() bool {
		final = p.status(t, id)
		if newer {
			return final.Health.Phase == "rollback_failed"
		}
		return final.Health.Phase == "failed" && final.RollbackStatus == "rolled_back"
	})
	if !newer {
		selected, config = restored, p.targetConfig.ConfigHash
	}
	p.assertGraph(t, selected)
	p.assertConfig(t, config)
	if !newer {
		p.assertHTTP(t, "baseline", "baseline-config")
		p.job(t, "publication-restored-admission", "baseline", "baseline-config")
	}
	for invocation, pin := range pins {
		job, err := p.store.InvocationByID(t.Context(), invocation)
		mustProjectWorkerNative(t, err)
		release, deployment := p.pin(t, invocation)
		if job.State != state.InvocationCompleted || job.Attempts != 1 || pin != [2]string{release, deployment} {
			t.Fatal("publication recovery replayed or retargeted completed work")
		}
	}
	p.assertArtifacts(t, final)
	publications := p.publicationCount(t) - before
	want := 2
	if newer {
		want++
	}
	if publications != want {
		t.Fatal("process death duplicated or lost a publication", publications)
	}
	// A further owner restart cannot reopen the terminal operation or overwrite
	// the later operator graph/configuration.
	mustProjectWorkerNative(t, p.h.RestartAPID())
	time.Sleep(2 * api.ProjectPromotionHealthPollInterval)
	again := p.status(t, id)
	if again.Health.Phase != final.Health.Phase || p.publicationCount(t)-before != want {
		t.Fatal("terminal restart duplicated publication or reopened recovery")
	}
	p.assertGraph(t, selected)
	p.assertConfig(t, config)
	return map[string]any{"promotion_id": id, "baseline_graph": baseline, "cutover_graph": cutover, "restored_graph": restored,
		"selected_graph": selected, "config_hash": config, "phase": final.Health.Phase, "events": events, "publications": publications,
		"candidate_jobs": len(pins), "candidate_pins_preserved": true, "completed_jobs_not_replayed": true, "artifact_reused": true,
		"graph_config_consistent": true, "terminal_restart": true, "newer_intent_preserved": newer}
}

func (p *environmentPromotionNative) publicationCount(t *testing.T) int {
	t.Helper()
	var count int
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM project_release_sets WHERE project_id=$1 AND environment_slug='production'", p.project.ID).Scan(&count))
	return count
}

func (p *environmentPromotionNative) inspectPublicationBarrier(t *testing.T, hit promotionPublicationHit, selected string) publicationCrashEvidence {
	t.Helper()
	p.assertGraph(t, selected)
	promotion, _, err := p.store.ProjectEnvironmentPromotionByID(t.Context(), p.accountID, p.project.Slug, "production", hit.PromotionID)
	mustProjectWorkerNative(t, err)
	health, err := p.store.ProjectEnvironmentPromotionHealth(t.Context(), p.accountID, hit.PromotionID)
	mustProjectWorkerNative(t, err)
	if health == nil {
		t.Fatal("publication lost its durable checkpoint")
	}
	committed := strings.HasSuffix(hit.Point, "after_commit")
	var exists bool
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_release_sets WHERE id=$1)", hit.ReleaseID).Scan(&exists))
	if exists != committed {
		t.Fatal("publication visibility does not match the commit boundary")
	}
	config := p.sourceConfig.ConfigHash
	phase := "rolling_back"
	if strings.HasPrefix(hit.Point, "cutover_") {
		phase = "cutover"
		if committed {
			phase = "verifying"
		} else {
			config = p.targetConfig.ConfigHash
		}
		if committed != (promotion.TargetReleaseSetID == hit.ReleaseID) {
			t.Fatal("cutover graph and receipt were not atomic")
		}
	} else {
		if health.RegressedRounds < api.ProjectReleaseVerificationRounds {
			t.Fatal("publication rollback lacked two real regression rounds")
		}
		if committed {
			config = p.targetConfig.ConfigHash
		}
		if committed != (promotion.RestoredTargetReleaseSetID == hit.ReleaseID) {
			t.Fatal("rollback graph and receipt were not atomic")
		}
	}
	if health.Phase != phase {
		t.Fatal("publication checkpoint crossed the wrong boundary", health.Phase, phase)
	}
	p.assertConfigOffline(t, config)
	return publicationCrashEvidence{Point: hit.Point, PromotionID: hit.PromotionID, ReleaseID: hit.ReleaseID, SelectedGraph: selected, PID: hit.PID,
		ObservedAt: hit.ObservedAt, Phase: phase, Generation: health.Generation, RegressedRounds: health.RegressedRounds, Deadline: health.Deadline, BarrierDeadline: hit.Deadline, Committed: committed}
}

func (p *environmentPromotionNative) killPublicationOwner(t *testing.T, hit promotionPublicationHit, event *publicationCrashEvidence) {
	t.Helper()
	mustProjectWorkerNative(t, p.h.KillAPID())
	p.apidStopped = true
	event.KilledAt = time.Now().UTC()
	_ = hit.conn.Close()
	if !event.KilledAt.Before(hit.Deadline) {
		t.Fatal("APID was not killed inside the actual action deadline")
	}
	health, err := p.store.ProjectEnvironmentPromotionHealth(t.Context(), p.accountID, hit.PromotionID)
	mustProjectWorkerNative(t, err)
	if health == nil || health.Phase != event.Phase || health.Generation != event.Generation || !health.Deadline.Equal(event.Deadline) {
		t.Fatal("process death altered a persisted phase, generation or deadline")
	}
	event.CheckpointPreserved = true
}

func (p *environmentPromotionNative) restartPublicationOwner(t *testing.T, event *publicationCrashEvidence) {
	t.Helper()
	mustProjectWorkerNative(t, p.h.RestartAPID())
	p.apidStopped = false
	event.RestartedAt = time.Now().UTC()
}
