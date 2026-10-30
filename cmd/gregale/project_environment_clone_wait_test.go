// adr: 590
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func cloneWaitFixture() api.ProjectEnvironmentCloneOperationResponse {
	return api.ProjectEnvironmentCloneOperationResponse{OperationID: "op-1", ProjectSlug: "shop", SourceEnvironment: "production", TargetEnvironment: "stage",
		SourceRevisionHash: strings.Repeat("a", 64), Status: "pending", Revision: 1}
}

func TestEnvCreateFullWaitReturnsOneReceipt(t *testing.T) {
	main.ResetJSONOut(t)
	oldOut, oldErr, oldInterval := (*main.OsStdout), (*main.OsStderr), (*main.ProjectEnvironmentClonePollInterval)
	var out, errOut bytes.Buffer
	(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput), (*main.ProjectEnvironmentClonePollInterval) = &out, &errOut, true, time.Millisecond
	t.Cleanup(func() {
		(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput), (*main.ProjectEnvironmentClonePollInterval) = oldOut, oldErr, false, oldInterval
	})
	op, calls := cloneWaitFixture(), 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.URL.Path != "/v1/projects/shop/environment-clones" {
				t.Errorf("partial route: %s", r.URL.Path)
			}
			var req api.CreateProjectEnvironmentRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Full {
				t.Errorf("request = %+v, %v", req, err)
			}
			w.WriteHeader(http.StatusAccepted)
			main.WriteJSONTest(w, api.ProjectEnvironmentResponse{Slug: "stage", CloneOperation: &op})
			return
		}
		calls++
		current := op
		current.Revision = int64(calls + 1)
		current.Status = "copying"
		if calls == 2 {
			current.Status = "ready"
		}
		main.WriteJSONTest(w, current)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_FULL_ENVIRONMENT_CLONES_ENABLED", "1")
	if code := main.EnvCreate([]string{"--full", "--wait", "stage", "--from", "production", "--project", "shop", "--timeout", "1"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	var receipt main.ProjectEnvironmentCloneWaitReceipt
	d := json.NewDecoder(&out)
	if err := d.Decode(&receipt); err != nil || !receipt.Succeeded || receipt.Clone.Status != "ready" || calls != 2 {
		t.Fatalf("receipt = %+v, calls = %d, error = %v", receipt, calls, err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF || errOut.Len() != 0 {
		t.Fatalf("extra output = %v, stderr = %s", err, errOut.String())
	}
}

// Full clones are not admitted yet, so the CLI hides their commands and flags
// from discovery and refuses them in dispatch unless they are turned on.
func TestFullEnvironmentCloneCLIHiddenUntilEnabled(t *testing.T) {
	visible := func() (cloneStatus bool, fullFlags []string, examples []string) {
		for _, command := range main.VisibleCliCommands() {
			if command.Name != "env" {
				continue
			}
			for _, sub := range command.Subcommands {
				switch sub.Name {
				case "clone-status":
					cloneStatus = true
				case "create":
					examples = sub.Examples
					for _, flag := range sub.Flags {
						if flag.Name == "full" || flag.Name == "wait" || flag.Name == "timeout" {
							fullFlags = append(fullFlags, flag.Name)
						}
					}
				}
			}
		}
		return cloneStatus, fullFlags, examples
	}
	t.Setenv("FAAS_FULL_ENVIRONMENT_CLONES_ENABLED", "")
	if cloneStatus, fullFlags, examples := visible(); cloneStatus || len(fullFlags) != 0 || strings.Contains(strings.Join(examples, " "), "--full") {
		t.Fatalf("hidden full clone surfaced: clone-status=%v flags=%v examples=%v", cloneStatus, fullFlags, examples)
	}
	if code := main.EnvCreate([]string{"--full", "stage", "--from", "production", "--project", "shop"}); code != 1 {
		t.Fatalf("hidden --full accepted: exit %d", code)
	}
	if code := main.CmdEnv([]string{"clone-status", "op-1", "--project", "shop"}); code != 1 {
		t.Fatalf("hidden clone-status dispatched: exit %d", code)
	}
	t.Setenv("FAAS_FULL_ENVIRONMENT_CLONES_ENABLED", "1")
	if cloneStatus, fullFlags, _ := visible(); !cloneStatus || len(fullFlags) != 3 {
		t.Fatalf("enabled full clone hidden: clone-status=%v flags=%v", cloneStatus, fullFlags)
	}
}

func TestProjectEnvironmentCloneWaitTimeoutFailureAndIdentity(t *testing.T) {
	oldInterval := (*main.ProjectEnvironmentClonePollInterval)
	(*main.ProjectEnvironmentClonePollInterval) = time.Millisecond
	t.Cleanup(func() { (*main.ProjectEnvironmentClonePollInterval) = oldInterval })
	for _, vector := range []string{"timeout", "failed", "compensated", "identity", "revision", "unknown", "cancelled"} {
		t.Run(vector, func(t *testing.T) {
			initial := cloneWaitFixture()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := initial
				switch vector {
				case "failed", "compensated":
					current.Status = vector
				case "identity":
					current.TargetEnvironment = "other"
				case "revision":
					current.Revision = 0
				case "unknown":
					current.Status = "unexpected"
				}
				main.WriteJSONTest(w, current)
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if vector == "cancelled" {
				cancel()
			}
			// Failure and identity checks need a response, not a short deadline race.
			timeout := 5 * time.Second
			if vector == "timeout" {
				timeout = 20 * time.Millisecond
			}
			op, timedOut, err := main.WaitForProjectEnvironmentClone(ctx, main.NewClient(srv.URL, "test"), initial, timeout)
			wantError := vector == "identity" || vector == "revision" || vector == "unknown" || vector == "cancelled"
			if (err != nil) != wantError || timedOut != (vector == "timeout") || (vector == "failed" || vector == "compensated") && op.Status != vector {
				t.Fatalf("status = %s, timeout = %t, error = %v", op.Status, timedOut, err)
			}
		})
	}
}
