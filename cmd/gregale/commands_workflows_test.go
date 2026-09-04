package main

import (
	"strings"
	"testing"
)

func TestCmdWorkflows_NoArgs(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflows([]string{}) })
	if code != 1 {
		t.Errorf("cmdWorkflows(no args) = %d, want 1", code)
	}
	if !strings.Contains(captured, "gregale workflows") {
		t.Errorf("usage must mention 'gregale workflows'; got: %s", captured)
	}
}

func TestCmdWorkflows_UnknownSubcommand(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflows([]string{"unknown-sub"}) })
	if code != 1 {
		t.Errorf("cmdWorkflows(unknown) = %d, want 1", code)
	}
	if !strings.Contains(captured, "unknown workflows subcommand") {
		t.Errorf("usage must mention 'unknown workflows subcommand'; got: %s", captured)
	}
}

func TestCmdWorkflowsList_MissingApp(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflowsList([]string{}) })
	if code != 1 {
		t.Errorf("cmdWorkflowsList(no --app) = %d, want 1", code)
	}
	if !strings.Contains(captured, "--app <slug>") {
		t.Errorf("usage must mention '--app <slug>'; got: %s", captured)
	}
}

func TestCmdWorkflowsRun_MissingArgs(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdWorkflowsRun([]string{}) })
	if code != 1 {
		t.Errorf("cmdWorkflowsRun(no args) = %d, want 1", code)
	}
	if !strings.Contains(captured, "gregale workflows run") {
		t.Errorf("usage must mention 'gregale workflows run'; got: %s", captured)
	}
}

func TestCmdWorkflowsRun_InvalidJSON(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsRun([]string{"my-workflow", "--app", "my-app", "--input", "{bad-json"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsRun(bad json) = %d, want 1", code)
	}
	if !strings.Contains(captured, "must be valid JSON") {
		t.Errorf("expected JSON validation error, got: %s", captured)
	}
}

func TestCmdWorkflowsStatus_InvalidUUID(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsStatus([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsStatus(bad uuid) = %d, want 1", code)
	}
	if !strings.Contains(captured, "invalid run ID") {
		t.Errorf("expected invalid run ID error, got: %s", captured)
	}
}

func TestCmdWorkflowsSteps_InvalidUUID(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsSteps([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsSteps(bad uuid) = %d, want 1", code)
	}
	if !strings.Contains(captured, "invalid run ID") {
		t.Errorf("expected invalid run ID error, got: %s", captured)
	}
}

func TestCmdWorkflowsCancel_InvalidUUID(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsCancel([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsCancel(bad uuid) = %d, want 1", code)
	}
	if !strings.Contains(captured, "invalid run ID") {
		t.Errorf("expected invalid run ID error, got: %s", captured)
	}
}

func TestCmdWorkflowsEvents_MissingArgs(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdWorkflowsEvents([]string{"send", "not-enough-args"})
	})
	if code != 1 {
		t.Errorf("cmdWorkflowsEvents(not enough args) = %d, want 1", code)
	}
	if !strings.Contains(captured, "gregale workflows events send") {
		t.Errorf("usage must mention 'events send'; got: %s", captured)
	}
}
