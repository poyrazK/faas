package main

import (
	"context"
	_ "embed"
	"errors"
	"os/exec"
	"strings"
	"time"
)

//go:embed task_execution_bindings.mjs
var mcpTaskExecutionBindingVerifier string

func verifyMCPTaskExecutionBindings(ctx context.Context, p mcpNativeReleasePlan, profile mcpTaskExecutionProfile) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", mcpTaskExecutionBindingVerifier)
	command.Dir = p.WorkerPath
	// The read-only database preflight never receives an API credential.
	command.Env = mcpTaskExecutionChildEnv(profile, false)
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != "{\"verified\":true}" {
		return errors.New("Task binding role separation was not verified")
	}
	return nil
}
