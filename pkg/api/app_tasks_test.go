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

func TestAppTaskCommandLineQuotesWhatRan(t *testing.T) {
	for _, tc := range []struct {
		task AppTaskResponse
		want string
	}{
		{AppTaskResponse{Command: []string{"sh", "-c", "echo a; rm b"}}, `sh -c 'echo a; rm b'`},
		{AppTaskResponse{Command: []string{"bin/migrate", "--to=head"}}, `bin/migrate --to=head`},
		{AppTaskResponse{Command: []string{"echo", "it's", ""}}, `echo 'it'"'"'s' ''`},
		{AppTaskResponse{Command: []string{"echo a; rm b"}, CommandShell: true}, `echo a; rm b`},
	} {
		if got := tc.task.CommandLine(); got != tc.want {
			t.Errorf("CommandLine(%q) = %s, want %s", tc.task.Command, got, tc.want)
		}
	}
}
