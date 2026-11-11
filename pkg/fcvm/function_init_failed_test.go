package fcvm

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261110190000: a function whose init hook threw is a named deploy
// failure, from both the supervisor's crash output and a characterization
// report; any other startup exit stays app_startup_crash.
func TestFunctionInitFailureIsNamed(t *testing.T) {
	initErr := api.FunctionInitFailedMarker + " Error: DATABASE_URL is not set\n"

	v := NewJailerVMM(t.TempDir(), 0)
	ring := v.registerRing("inst")
	for _, line := range []string{initErr, "guest-init: app crash-looped after 3 restart(s): exit status 1\n"} {
		if _, err := ring.Write("stderr", []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	p := api.AsProblem(v.guestStoppedDuringStartup(Lease{Instance: "inst"}))
	if p == nil || p.Code != api.CodeFunctionInitFailed || p.Status != 422 || !strings.Contains(p.Detail, "DATABASE_URL") {
		t.Fatalf("supervised init failure = %#v, want 422 %s carrying the error", p, api.CodeFunctionInitFailed)
	}

	_, err := characterizationBootOutcome(api.CharacterizationReport{ExitCode: 1, LogTail: initErr}, api.ExecutionModeRequest, false)
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeFunctionInitFailed {
		t.Fatalf("characterized init failure = %#v, want %s", err, api.CodeFunctionInitFailed)
	}
	_, err = characterizationBootOutcome(api.CharacterizationReport{ExitCode: 1, LogTail: "boom"}, api.ExecutionModeRequest, false)
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeAppStartupCrash {
		t.Fatalf("plain startup exit = %#v, want %s", err, api.CodeAppStartupCrash)
	}
	if !api.IsCustomerCausedDeploymentFailure(api.CodeFunctionInitFailed) {
		t.Errorf("%s must be customer-caused", api.CodeFunctionInitFailed)
	}
}
