package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261111114059 slice 3: an environment an agent creates from another
// copies each workload's settings, so it is priced as the standing cost of
// the always-on instances it copies. A branch keeps no min_instances floor
// (ADR-20261031193635), but its services and workers still run replicas.

// workloadFloor is the floor a workload's environment settings keep warm.
func workloadFloor(settings state.ProjectEnvironmentWorkloadSettings, branch bool) int {
	floor := settings.MinInstances
	if branch {
		floor = 0
	}
	if p := settings.ScalingPolicy; p != nil {
		floor = max(floor, p.MinInstances, p.MaxReachableMinInstances())
	}
	return max(floor, modeFloor(apiManifestFromState(settings.Manifest), settings.ScalingPolicy != nil))
}

// environmentCloneStanding is what copying source's workloads commits a
// month: each project app's settings in source (its own settings where
// source has none of its own).
func (s *server) environmentCloneStanding(r *http.Request, acct state.Account, project state.Project, source string, branch bool) (int64, error) {
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		return 0, err
	}
	reader, _ := s.store.(state.ProjectEnvironmentWorkloadSpecReader)
	var total int64
	for _, app := range apps {
		settings, err := state.WorkloadSettingsFromApp(app)
		if reader != nil {
			spec, specErr := reader.ProjectEnvironmentWorkloadSpec(r.Context(), acct.ID, project.ID, source, app.ID)
			switch {
			case specErr == nil:
				settings, err = spec.Settings, nil
			case !errors.Is(specErr, state.ErrNotFound):
				return 0, specErr
			}
		}
		if err != nil {
			return 0, err
		}
		total += standingMillicents(workloadFloor(settings, branch), settings.RAMMB, acct.Plan)
	}
	return total, nil
}

// statusRecorder passes a response through and keeps its status, so a
// charge is committed only when the handler it wraps succeeded.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// chargeEnvironmentClone prices an environment created from source. Only
// agent keys are charged, so other callers skip reading the workloads.
func (s *server) chargeEnvironmentClone(w http.ResponseWriter, r *http.Request, acct state.Account, project state.Project, source string, branch bool) (func(), bool) {
	if source == "" || s.authMw == nil || !s.authMw.ChargesAgentBudget(r) {
		return s.noCharge(w, r, acct.Plan)
	}
	cost, err := s.environmentCloneStanding(r, acct, project, source, branch)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "price environment clone",
			"Gregale could not price this environment's warm instances against the agent's budget.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return func() {}, true
	}
	return s.chargeAgentBudget(w, r, acct.Plan, 0, cost)
}
