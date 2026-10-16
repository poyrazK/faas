package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// ProjectPromotionHealthPolicy is the bounded read-only probe contract included
// in an environment promotion's preview and approval identity.
type ProjectPromotionHealthPolicy struct {
	Workers   []*ProjectReleaseWorker          `json:"workers,omitempty"`
	Worker    *ProjectReleaseWorker            `json:"worker,omitempty"`
	Migration *ProjectPromotionMigrationPolicy `json:"migration,omitempty"`
	Probes    []ProjectReleaseProbe            `json:"probes"`
}

// ProjectPromotionHealth reports durable health progress without challenge
// tokens, request bodies, credentials or application response bodies.
type ProjectPromotionHealth struct {
	Workers         []ProjectPromotionWorkerHealth     `json:"workers,omitempty"`
	Admission       *ProjectPromotionAdmission         `json:"admission,omitempty"`
	Control         *ProjectPromotionControl           `json:"control,omitempty"`
	Worker          *ProjectReleaseWorker              `json:"worker,omitempty"`
	WorkerHealth    *ProjectWorkerHealth               `json:"worker_health,omitempty"`
	Recovery        *ProjectPromotionMigrationRecovery `json:"recovery,omitempty"`
	Migration       *ProjectReleaseCommand             `json:"migration,omitempty"`
	Policy          ProjectPromotionHealthPolicy       `json:"policy"`
	Phase           string                             `json:"phase"`
	Generation      int64                              `json:"generation"`
	StartedAt       time.Time                          `json:"started_at"`
	Deadline        time.Time                          `json:"deadline"`
	CheckedAt       *time.Time                         `json:"checked_at,omitempty"`
	Verdict         string                             `json:"verdict"`
	HealthyRounds   int                                `json:"healthy_rounds"`
	RegressedRounds int                                `json:"regressed_rounds"`
	Message         string                             `json:"message,omitempty"`
	Evidence        []ProjectReleaseProbeEvidence      `json:"evidence,omitempty"`
}

// ProjectPromotionMigrationPolicy authorizes one declared, backward-compatible
// command. Command bytes and destination runtime inputs stay in private state.
type ProjectPromotionMigrationPolicy struct {
	AppID         string `json:"app_id"`
	Slug          string `json:"slug"`
	CommandHash   string `json:"command_hash"`
	Compatibility string `json:"compatibility"`
}

// Multiple workers use an explicit set; the legacy fields remain exclusive to
// single-worker operations so older clients cannot silently approve a subset.
type ProjectPromotionWorkerHealth struct {
	Worker *ProjectReleaseWorker `json:"worker"`
	Health *ProjectWorkerHealth  `json:"health"`
}

func ProjectPromotionWorkers(policy *ProjectPromotionHealthPolicy) []*ProjectReleaseWorker {
	if policy == nil {
		return nil
	}
	return ProjectReleaseWorkerPolicies(policy.Worker, policy.Workers)
}
func ProjectReleaseWorkerPolicies(worker *ProjectReleaseWorker, workers []*ProjectReleaseWorker) []*ProjectReleaseWorker {
	if len(workers) > 0 {
		return workers
	}
	if worker != nil {
		return []*ProjectReleaseWorker{worker}
	}
	return nil
}
func ProjectPromotionHasWorker(policy ProjectPromotionHealthPolicy, app string) bool {
	for _, w := range ProjectPromotionWorkers(&policy) {
		if w != nil && w.AppID == app {
			return true
		}
	}
	return false
}
func ProjectPromotionWorkerSetID(worker *ProjectReleaseWorker, workers []*ProjectReleaseWorker) string {
	list := ProjectReleaseWorkerPolicies(worker, workers)
	if len(list) == 0 {
		return ProjectReleaseWorkerPolicyID(nil)
	}
	if len(list) == 1 {
		return ProjectReleaseWorkerPolicyID(list[0])
	}
	ids := make([]string, 0, len(list))
	for _, w := range list {
		ids = append(ids, ProjectReleaseWorkerPolicyID(w))
	}
	raw, _ := json.Marshal(ids)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
