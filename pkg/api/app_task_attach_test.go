package api

import "testing"

func TestAppTaskSessionBuiltinValidation(t *testing.T) {
	valid := []CreateAppTaskRequest{
		{Interactive: true, Command: []string{AppTaskCopyOutCommand, "/app/dist"}},
		{Interactive: true, Command: []string{AppTaskPortForwardCommand, "db.svc.gregale:5432"}},
		{Interactive: true, Command: []string{AppTaskPortForwardCommand, "[fd00::1]:8080"}},
	}
	for _, request := range valid {
		if _, problem := request.Resolve(); problem != nil {
			t.Fatalf("%v rejected: %s", request.Command, problem.Detail)
		}
	}
	invalid := map[string]CreateAppTaskRequest{
		"batch":     {Command: []string{AppTaskCopyOutCommand, "/app"}},
		"tty":       {Interactive: true, TTY: true, Command: []string{AppTaskCopyOutCommand, "/app"}},
		"no path":   {Interactive: true, Command: []string{AppTaskCopyOutCommand}},
		"extra arg": {Interactive: true, Command: []string{AppTaskCopyOutCommand, "/a", "/b"}},
		"no port":   {Interactive: true, Command: []string{AppTaskPortForwardCommand, "db"}},
		"bad port":  {Interactive: true, Command: []string{AppTaskPortForwardCommand, "db:70000"}},
		"shell":     {Interactive: true, CommandShell: true, Command: []string{AppTaskPortForwardCommand}},
	}
	for name, request := range invalid {
		if _, problem := request.Resolve(); problem == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestCreateAppTaskRequestInteractiveDefaults(t *testing.T) {
	resolved, problem := (CreateAppTaskRequest{Interactive: true, TTY: true}).Resolve()
	if problem != nil {
		t.Fatalf("Resolve: %v", problem.Detail)
	}
	if !resolved.Interactive || !resolved.TTY || len(resolved.Command) != 1 || resolved.Command[0] != AppTaskInteractiveDefaultCommand ||
		resolved.TimeoutSeconds != AppTaskInteractiveDefaultTimeoutSeconds || resolved.MaxOutputBytes != AppTaskDefaultMaxOutputBytes {
		t.Fatalf("resolved = %+v", resolved)
	}
	explicit, problem := (CreateAppTaskRequest{Interactive: true, Command: []string{"bash", "-l"}, TimeoutSeconds: 120}).Resolve()
	if problem != nil || explicit.Command[0] != "bash" || explicit.TimeoutSeconds != 120 || explicit.TTY {
		t.Fatalf("explicit = %+v, %v", explicit, problem)
	}
}

func TestCreateAppTaskRequestInteractiveRejections(t *testing.T) {
	for name, request := range map[string]CreateAppTaskRequest{
		"tty without interactive": {Command: []string{"sh"}, TTY: true},
		"output budget":           {Interactive: true, MaxOutputBytes: 2048},
		"verification selector":   {Interactive: true, VerificationDeploymentID: "6f9619ff-8b86-d011-b42d-00c04fc964ff"},
		"smoke selector":          {Interactive: true, SmokeDeploymentID: "6f9619ff-8b86-d011-b42d-00c04fc964ff"},
		"probe command":           {Interactive: true, Command: []string{AppTaskServiceBindingProbeCommand, "billing"}},
		"timeout above hard cap":  {Interactive: true, TimeoutSeconds: 3601},
	} {
		if _, problem := request.Resolve(); problem == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if _, problem := (CreateAppTaskRequest{}).Resolve(); problem == nil {
		t.Fatal("batch request without a command accepted")
	}
}

func TestIsAppTaskAttachPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/apps/api/tasks/6f9619ff/attach":  true,
		"/v1/apps/api/tasks/6f9619ff/attach/": false,
		"/v1/apps/api/tasks/6f9619ff":         false,
		"/v1/apps//tasks/6f9619ff/attach":     false,
		"/v1/apps/api/tasks//attach":          false,
		"/v1/apps/api/runs/6f9619ff/attach":   false,
		"/v1/apps/api/tasks/x/attach/extra":   false,
		"/v2/apps/api/tasks/6f9619ff/attach":  false,
	} {
		if got := IsAppTaskAttachPath(path); got != want {
			t.Fatalf("IsAppTaskAttachPath(%q) = %v, want %v", path, got, want)
		}
	}
	if got := AppTaskAttachPath("my api", "task/1"); got != "/v1/apps/my%20api/tasks/task%2F1/attach" {
		t.Fatalf("AppTaskAttachPath = %q", got)
	}
}

func TestAppTaskAttachCodec(t *testing.T) {
	msg := EncodeAppTaskAttachResize(40, 132)
	if msg[0] != AppTaskAttachResize {
		t.Fatalf("type byte = %#x", msg[0])
	}
	rows, cols, err := DecodeAppTaskAttachResize(msg[1:])
	if err != nil || rows != 40 || cols != 132 {
		t.Fatalf("resize = %d x %d, %v", rows, cols, err)
	}
	if _, _, err := DecodeAppTaskAttachResize([]byte{1}); err == nil {
		t.Fatal("short resize accepted")
	}
	exit := 3
	encoded, err := EncodeAppTaskAttachTerminal(AppTaskAttachTerminalMessage{Status: AppTaskStatusFailed, ExitCode: &exit})
	if err != nil || encoded[0] != AppTaskAttachTerminal {
		t.Fatalf("terminal encode = %v, %v", encoded, err)
	}
	decoded, err := DecodeAppTaskAttachTerminal(encoded[1:])
	if err != nil || decoded.Status != AppTaskStatusFailed || decoded.ExitCode == nil || *decoded.ExitCode != 3 {
		t.Fatalf("terminal decode = %+v, %v", decoded, err)
	}
	if _, err := DecodeAppTaskAttachTerminal([]byte(`{}`)); err == nil {
		t.Fatal("terminal without status accepted")
	}
}
