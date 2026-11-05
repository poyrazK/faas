package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestMfaBrowserHandoffWithoutCLIKey(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("MFA browser handoff must not send a bearer request")
	}))
	defer api.Close()
	t.Setenv("FAAS_API", api.URL)
	previous := *main.NonInteractive
	*main.NonInteractive = false
	t.Cleanup(func() { *main.NonInteractive = previous })
	for _, action := range []string{"enroll", "confirm", "verify", "recover", "disable"} {
		t.Run(action, func(t *testing.T) {
			recorder := main.WithRecorder(t)
			stdout, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.CmdMfa([]string{action}); code != 0 {
				t.Fatalf("exit = %d: %s", code, stderr())
			}
			want := api.URL + "/dashboard/account/mfa?action=" + action
			if urls := *recorder.Urls(); len(urls) != 1 || urls[0] != want || !strings.Contains(stdout.String(), want) {
				t.Fatalf("browser handoff = %v, output %q", urls, stdout.String())
			}
		})
	}
}

func TestMfaLegacySecretsNeverEnterBrowserURLOrOutput(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("FAAS_API", "https://api.example.com")
	for _, args := range [][]string{
		{"confirm", "123456"}, {"verify", "--code", "123456"},
		{"recover", "PRIVATE-RECOVERY"}, {"disable", "--password", "private-password"},
		{"disable", "--recovery-code", "PRIVATE-RECOVERY"}, {"enroll", "--qr-out", "private-path"},
	} {
		t.Run(args[0], func(t *testing.T) {
			recorder := main.WithRecorder(t)
			stdout, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.CmdMfa(args); code != 0 {
				t.Fatalf("exit = %d: %s", code, stderr())
			}
			output := stdout.String() + stderr() + strings.Join(*recorder.Urls(), " ")
			for _, secret := range []string{"123456", "PRIVATE-RECOVERY", "private-password", "private-path"} {
				if strings.Contains(output, secret) {
					t.Fatal("legacy credential entered browser handoff output")
				}
			}
		})
	}
}

func TestMfaJSONAndNonInteractiveDescribeBrowserWork(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("FAAS_API", "https://api.example.com")
	recorder := main.WithRecorder(t)
	previousJSON, previousNonInteractive := *main.JsonOutput, *main.NonInteractive
	t.Cleanup(func() { *main.JsonOutput, *main.NonInteractive = previousJSON, previousNonInteractive })
	*main.JsonOutput, *main.NonInteractive = true, true
	stdout, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.CmdMfa([]string{"recover"}); code != 0 {
		t.Fatalf("exit = %d: %s", code, stderr())
	}
	var result struct {
		URL             string `json:"url"`
		Action          string `json:"action"`
		RequiresBrowser bool   `json:"requires_browser"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.RequiresBrowser || result.Action != "recover" || result.URL != "https://api.example.com/dashboard/account/mfa?action=recover" {
		t.Fatalf("browser JSON = %s: %v", stdout.String(), err)
	}
	if len(*recorder.Urls()) != 0 {
		t.Fatal("JSON mode launched a browser")
	}
}

func TestMfaRejectsInvalidArgumentsBeforeBrowserLaunch(t *testing.T) {
	main.SetupCLIRegression(t)
	recorder := main.WithRecorder(t)
	for _, args := range [][]string{nil, {"unknown"}, {"confirm", "one", "two"}, {"verify", "--code", "123456", "extra"}, {"recover", "--code", "secret", "extra"}, {"disable", "--password", "secret", "--recovery-code", "secret"}, {"enroll", "extra"}} {
		if code := main.CmdMfa(args); code == 0 {
			t.Fatalf("accepted invalid arguments %v", args)
		}
	}
	if len(*recorder.Urls()) != 0 {
		t.Fatal("invalid arguments launched a browser")
	}
}
