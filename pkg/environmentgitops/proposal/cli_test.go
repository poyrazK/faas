package proposal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProviderCLIUsesFixedHostsStdinAndRedactsErrors(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, mode := range []string{"ok", "404", "failed", "more", "invalid"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				name, host := "gh", "github.com"
				if provider == "gitlab" {
					name, host = "glab", "gitlab.com"
				}
				status, response, exit, next := "200 OK", `{"id":42}`, "0", ""
				switch mode {
				case "404":
					status, exit = "404 Not Found", "1"
				case "failed":
					status, response, exit = "422 Unprocessable Entity", `{"message":"PRIVATE_PROVIDER_BODY"}`, "1"
				case "more":
					next = "X-Next-Page: 2\n"
				case "invalid":
					response = "PRIVATE_NOT_JSON"
				}
				script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > \"$PROPOSAL_ARGS\"\ncat > \"$PROPOSAL_INPUT\"\nprintf 'HTTP/2.0 %s\\n%sContent-Type: application/json\\n\\n%s'\nprintf 'PRIVATE_STDERR' >&2\nexit %s\n", status, next, response, exit)
				if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir+":/bin:/usr/bin")
				t.Setenv("PROPOSAL_ARGS", filepath.Join(dir, "args"))
				t.Setenv("PROPOSAL_INPUT", filepath.Join(dir, "input"))
				t.Setenv("GH_DEBUG", "api")
				t.Setenv("GLAB_DEBUG_HTTP", "1")
				c, err := CLI(provider, "shop/platform")
				if err != nil {
					t.Fatal(err)
				}
				var result struct{ ID int }
				err = c.Call(t.Context(), "POST", c.endpoint()+"/commits?per_page=2", map[string]string{"content": "PRIVATE_MANIFEST_BYTES"}, &result)
				if mode == "ok" && (err != nil || result.ID != 42) {
					t.Fatal(err, result)
				}
				if mode != "ok" && (err == nil || strings.Contains(err.Error(), "PRIVATE")) {
					t.Fatal("provider body leaked or accepted", err)
				}
				if mode == "404" && !errors.Is(err, ErrNotFound) {
					t.Fatal(err)
				}
				args, _ := os.ReadFile(filepath.Join(dir, "args"))
				input, _ := os.ReadFile(filepath.Join(dir, "input"))
				if !bytes.Contains(args, []byte("--hostname\n"+host)) || !bytes.Contains(args, []byte("--input\n-")) || bytes.Contains(args, []byte("PRIVATE")) || !bytes.Contains(input, []byte("PRIVATE_MANIFEST_BYTES")) {
					t.Fatal("unsafe provider invocation", string(args))
				}
			})
		}
	}
}

func TestProviderResponseBoundCannotBeBypassedByReaderFrom(t *testing.T) {
	var output boundedBuffer
	if _, err := io.Copy(&output, strings.NewReader(strings.Repeat("x", 4*api.EnvironmentGitOpsMaxDefinitionBytes+1))); err == nil {
		t.Fatal("unbounded response accepted")
	}
	if len(output.Bytes()) > 4*api.EnvironmentGitOpsMaxDefinitionBytes {
		t.Fatal("response bound bypassed")
	}
}
