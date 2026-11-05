// Package rehearsal validates contained command capabilities (ADR-20261017201008).
package rehearsal

import (
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

func unavailable() *api.Problem {
	return api.ErrRehearsalNotEnabled("Contained rehearsal command execution")
}
