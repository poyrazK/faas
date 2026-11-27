package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `job create --command "echo a; echo b"` parses to one argument that the
// guest execs as a program name, so every task fails (prod hunt r62). The CLI
// warns and names the shell form.
func TestJobCommandWarnsOnAOneArgumentShellLine(t *testing.T) {
	for _, tc := range []struct {
		value string
		warn  bool
	}{
		{"echo task=$I; sleep 2; echo done", true},
		{"python main.py", true},
		{"/app/run", false},
		{"/bin/sh,-c,echo a; echo b", false},
		{`["/bin/sh","-c","echo a; echo b"]`, false},
	} {
		_, stderr, restore := main.SwapIO(t)
		main.WarnShellLineCommand(main.ParseJobCommand(tc.value))
		restore()
		got := stderr()
		if tc.warn != strings.Contains(got, "/bin/sh,-c,<line>") {
			t.Errorf("--command %q: warn=%v, stderr %q", tc.value, tc.warn, got)
		}
	}
}

// Review of r58: a plan that does not allow private networks was told to
// "retry after the private-network API is available".
func TestNetworkDoctorPlanDeniedPrivateNetworkSaysUpgrade(t *testing.T) {
	linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/v1/apps/web":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "web", Status: "active"})
		case "/v1/apps/web/static-egress-ip":
			_ = json.NewEncoder(w).Encode(api.AppStaticEgressIPResponse{PlanAllowed: true})
		case "/v1/apps/web/network/private":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusPaymentRequired, Code: api.CodePlanPrivateNetworkNotAllowed, Title: "Plan"})
		case "/v1/apps/web/upstreams":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusServiceUnavailable, Code: api.CodeDataUpstreamsDisabled, Title: "Unavailable"})
		default:
			return false
		}
		return true
	})
	_, stdout, stderr := runCLI(t, "app", "web", "network", "doctor")
	if !strings.Contains(stdout, "gregale billing plan pro") || strings.Contains(stdout, "Retry after the private-network API") {
		t.Fatalf("plan-denied private network doctor:\n%s%s", stdout, stderr)
	}
}

// Review of r60: help lists `insight event` and `insight request`, but the
// verb check refused them, naming them as valid in the same line.
func TestInsightSingularVerbsRun(t *testing.T) {
	for _, verb := range []string{"event", "request"} {
		main.ResetJSONOut(t)
		stdoutBuf, stderr, restore := main.SwapIO(t)
		code := main.Run([]string{"insights", verb})
		restore()
		out := stdoutBuf.String() + stderr()
		if code == 0 || strings.Contains(out, "unknown subcommand") || !strings.Contains(out, "--project") {
			t.Fatalf("insights %s = %d, want its usage:\n%s", verb, code, out)
		}
	}
}
