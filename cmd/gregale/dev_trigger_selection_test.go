package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// ChronoLake finding 34(2): the documented form put the source directory and
// the URL path both on --path after the verb, and the URL path overwrote the
// directory. --source and --name now select the environment after the verb.
func TestCmdDevTriggerSelectionAfterVerb(t *testing.T) {
	for _, args := range [][]string{
		{"trigger", "invoke", "--source", ".", "--name", "demo-api", "--method", "GET", "--path", "/healthz"},
		{"trigger", "invoke", "--source=.", "--name=demo-api", "--method", "GET", "--path", "/healthz"},
		{"trigger", "--path", ".", "invoke", "--name", "demo-api", "--method", "GET", "--path", "/healthz"},
	} {
		fake := &devTriggerAPI{}
		setupDevTriggerCLI(t, fake)
		_, restoreOut := main.CaptureStdout(t)
		stderr, restoreErr := main.CaptureStderr(t)
		code := main.CmdDev(args)
		restoreErr()
		restoreOut()
		if code != 0 {
			t.Fatalf("cmdDev(%v) = %d; stderr=%q", args, code, stderr.String())
		}
		if len(fake.invokes) != 1 || fake.invokes[0].Method != "GET" || fake.invokes[0].Path != "/healthz" {
			t.Fatalf("cmdDev(%v) invokes = %+v, want one GET /healthz", args, fake.invokes)
		}
	}
}

func TestCmdDevTriggerRejectsAmbiguousPath(t *testing.T) {
	fake := &devTriggerAPI{}
	dir := setupDevTriggerCLI(t, fake)
	stderr, restoreErr := main.CaptureStderr(t)
	defer restoreErr()
	for _, args := range [][]string{
		{"trigger", "invoke", "--path", dir, "--name", "demo-api", "--method", "GET", "--path", "/healthz"},
		{"trigger", "--source", dir, "invoke", "--source", "elsewhere", "--name", "demo-api"},
		{"trigger", "--name", "demo-api", "invoke", "--name", "other-api"},
	} {
		if code := main.CmdDev(args); code == 0 {
			t.Errorf("cmdDev(%v) = 0, want failure", args)
		}
	}
	if !strings.Contains(stderr.String(), "--source DIR") {
		t.Fatalf("ambiguous --path error does not name --source: %q", stderr.String())
	}
	if len(fake.requests) != 0 {
		t.Fatalf("rejected triggers reached the API: %v", fake.requests)
	}
}
