package api

import (
	"fmt"
	"regexp"
	"strconv"
)

// ExecutionModeConflict is what a CodeExecutionModeConflict detail reports:
// the declared execution mode, the class the startup characterization
// observed, the port the workload bound and the code it exited with.
type ExecutionModeConflict struct {
	Mode, Class    string
	Port, ExitCode int
}

// Detail is the problem detail vmmd writes for the conflict. Parse reads it
// back from the detail or from any error text that carries it.
func (c ExecutionModeConflict) Detail() string {
	return fmt.Sprintf("execution mode %q conflicts with characterized class %q (port=%d, exit_code=%d)", c.Mode, c.Class, c.Port, c.ExitCode)
}

var executionModeConflictDetail = regexp.MustCompile(`execution mode "([a-z]+)" conflicts with characterized class "([a-z]*)" \(port=(-?[0-9]+), exit_code=(-?[0-9]+)\)`)

// ParseExecutionModeConflict finds an ExecutionModeConflict detail in text.
func ParseExecutionModeConflict(text string) (ExecutionModeConflict, bool) {
	m := executionModeConflictDetail.FindStringSubmatch(text)
	if m == nil {
		return ExecutionModeConflict{}, false
	}
	port, portErr := strconv.Atoi(m[3])
	exitCode, exitErr := strconv.Atoi(m[4])
	if portErr != nil || exitErr != nil {
		return ExecutionModeConflict{}, false
	}
	return ExecutionModeConflict{Mode: m[1], Class: m[2], Port: port, ExitCode: exitCode}, true
}

// ListeningWorker reports a worker that bound a port: it serves requests,
// so it should not run in worker mode.
func (c ExecutionModeConflict) ListeningWorker() bool {
	switch c.Class {
	case "http", "graphql", "grpc":
		return c.Mode == ExecutionModeWorker && c.Port > 0
	}
	return false
}
