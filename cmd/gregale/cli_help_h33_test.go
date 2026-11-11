package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 33 H33-4: `release list --help` read `gregale release list <alias>
// [flags]`, as if the alias child were required; the list runs without it.
func TestHelpShowsChildOfARunnableVerbAsOptional(t *testing.T) {
	list, ok := main.LookupCliCommand("deployments")
	if !ok {
		t.Fatal("release list (deployments) is not in the CLI manifest")
	}
	var buf bytes.Buffer
	main.PrintLocalCommandHelp(&buf, list)
	if !strings.Contains(buf.String(), " [alias] [flags]") {
		t.Fatalf("release list usage:\n%s", buf.String())
	}
}

// Hunt 33 H33-13: the hint named the legacy `events publish-app-verify`.
func TestEventPublicationVerifyHintNamesTheCommand(t *testing.T) {
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	defer func() { *main.OsStderr = old }()
	if code := main.CmdEvents([]string{"publish-app-verify", "h33-hnode"}); code == 0 {
		t.Fatal("verify without --file succeeded")
	}
	if !strings.Contains(stderr.String(), "use event publication verify APP --file PATH") {
		t.Fatalf("hint: %s", stderr.String())
	}
}

// Hunt 33 H33-2: `--json account dpa get` printed the plain-text template.
func TestAccountDPAJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Data Processing Agreement {{customer}}\n"))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	defer func() { *main.OsStdout = old }()
	prev := *main.JsonOutput
	*main.JsonOutput = true
	defer func() { *main.JsonOutput = prev }()
	if code := main.CmdAccount([]string{"dpa"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stdout.String())
	}
	var out struct {
		Text     string `json:"text"`
		Template bool   `json:"template"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || !out.Template || !strings.Contains(out.Text, "{{customer}}") {
		t.Fatalf("dpa --json = %q (%v)", stdout.String(), err)
	}
}
