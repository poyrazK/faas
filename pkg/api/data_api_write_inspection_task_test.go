package api

import (
	"strings"
	"testing"
)

func TestDataAPIWriteInspectionCommandAdmission(t *testing.T) {
	valid := []string{AppTaskDataAPIWriteInspectionCommand, "notes", strings.Repeat("a", 64)}
	if _, problem := (CreateAppTaskRequest{Command: valid}).Resolve(); problem != nil {
		t.Fatal(problem)
	}
	for _, req := range []CreateAppTaskRequest{
		{Command: valid, Interactive: true}, {Command: valid, CommandShell: true},
		{Command: valid[:2]}, {Command: []string{valid[0], "notes;drop", valid[2]}},
		{Command: []string{valid[0], "notes", strings.Repeat("A", 64)}},
	} {
		if _, problem := req.Resolve(); problem == nil {
			t.Fatalf("accepted invalid task %+v", req)
		}
	}
}
