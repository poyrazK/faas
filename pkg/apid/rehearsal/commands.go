// Package rehearsal validates contained command capabilities (ADR-20261017201008).
package rehearsal

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

func ValidateEnrollment(req api.RegisterScenarioTestRequest, enabled bool) *api.Problem {
	if req.CommandVersion == 0 {
		return nil
	}
	if req.CommandVersion != rehearsalcommand.Version || req.Simulation == nil {
		return api.ErrValidation("contained commands require protocol version 1 and simulation enrollment")
	}
	if !enabled {
		return unavailable()
	}
	return nil
}

func ValidateTask(req api.ResolvedCreateAppTaskRequest, enabled bool) *api.Problem {
	if !rehearsalcommand.IsCommand(req.Command) {
		return nil
	}
	if !enabled {
		return unavailable()
	}
	if _, err := rehearsalcommand.Decode(req.Command); err != nil {
		return api.ErrValidation("invalid contained command envelope")
	}
	if req.CommandShell || req.Interactive || req.TTY || req.VerificationDeploymentID != "" || req.SmokeDeploymentID != "" || req.MaxOutputBytes != rehearsalcommand.MaxReceiptBytes {
		return api.ErrValidation("contained commands require a noninteractive batch task and the protocol output budget")
	}
	return nil
}

// ValidateCronFire refuses a contained app's command-cron fire-now while
// rehearsal command execution is off: schedd would pin it as a contained
// command that no runner may execute (ADR-20261022194510).
func ValidateCronFire(commandCron, contained, enabled bool) *api.Problem {
	if commandCron && contained && !enabled {
		return unavailable()
	}
	return nil
}

func unavailable() *api.Problem {
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Contained commands unavailable", "The operator must qualify and enable rehearsal command execution before using this runner")
}
