package api

import (
	"strings"
	"testing"
)

func TestCreateAppTaskRequestResolve(t *testing.T) {
	request := CreateAppTaskRequest{Command: []string{"bin/task", "--once"}}
	resolved, problem := request.Resolve()
	if problem != nil {
		t.Fatalf("Resolve: %v", problem)
	}
	if resolved.TimeoutSeconds != AppTaskDefaultTimeoutSeconds || resolved.MaxOutputBytes != AppTaskDefaultMaxOutputBytes {
		t.Fatalf("defaults = timeout %d output %d", resolved.TimeoutSeconds, resolved.MaxOutputBytes)
	}
	resolved.Command[0] = "mutated"
	if request.Command[0] != "bin/task" {
		t.Fatal("resolved command aliases caller input")
	}
}

func TestCreateAppTaskRequestResolveRejectsOversizedCommand(t *testing.T) {
	_, problem := (CreateAppTaskRequest{Command: []string{strings.Repeat("x", AppTaskMaxCommandArgBytes+1)}}).Resolve()
	if problem == nil || problem.Status != 422 || problem.Code != CodeValidation {
		t.Fatalf("oversized command problem = %+v", problem)
	}
}
