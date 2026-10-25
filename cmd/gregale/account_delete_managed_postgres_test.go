// adr: 20261024150500
package main_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// The hidden --include-postgres-databases flag asks apid to include the
// account's managed PostgreSQL databases; without it the request is unchanged.
func TestAccountDeleteIncludePostgresDatabasesFlag(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		query string
	}{
		{args: []string{"--yes"}, query: ""},
		{args: []string{"--yes", "--include-postgres-databases"}, query: "include_managed_postgres_databases=true"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			main.ResetJSONOut(t)
			var got []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
				main.WriteJSONTest(w, api.AccountDeletionResponse{Status: "deleted_pending"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			if code := main.CmdAccountDelete(tc.args); code != 0 {
				t.Fatalf("exit %d", code)
			}
			if want := "DELETE /v1/account?" + tc.query; len(got) != 1 || got[0] != want {
				t.Fatalf("requests %v, want [%s]", got, want)
			}
		})
	}
}

// TestAccountDeleteIncludePostgresDatabasesFlagIsHidden pins the dark launch:
// the flag parses (above) but no discovery surface names it.
func TestAccountDeleteIncludePostgresDatabasesFlagIsHidden(t *testing.T) {
	command, ok := main.LookupCliCommand("account")
	if !ok {
		t.Fatal("account command missing from CLI manifest")
	}
	var help, man, reference bytes.Buffer
	main.PrintLocalCommandHelpDetail(&help, command, true)
	main.RenderManCommand(&man, command)
	main.RenderMarkdownReference(&reference, []main.CliCommand{command})
	surfaces := map[string]string{"help": help.String(), "man": man.String(), "reference": reference.String()}
	for name, render := range map[string]func(io.Writer, main.CliCommand){
		"bash": main.RenderBashCommand, "zsh": main.RenderZshCommand,
		"fish": main.RenderFishCommand, "powershell": main.RenderPowershellCommand,
	} {
		var out bytes.Buffer
		render(&out, command)
		surfaces[name] = out.String()
	}
	for name, out := range surfaces {
		if strings.Contains(strings.ReplaceAll(out, `\-`, "-"), "include-postgres-databases") {
			t.Errorf("%s names the dark-launched --include-postgres-databases", name)
		}
	}
}
