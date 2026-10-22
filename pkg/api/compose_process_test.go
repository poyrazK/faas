// adr: 1337
package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalComposeStopSignal(t *testing.T) {
	for raw, want := range map[string]string{
		"SIGTERM": "SIGTERM", "term": "SIGTERM", "15": "SIGTERM", " INT ": "SIGINT", "3": "SIGQUIT",
		"HUP": "SIGHUP", "SIGUSR1": "SIGUSR1", "12": "SIGUSR2",
		"SIGKILL": "", "9": "", "SIGWINCH": "", "": "",
	} {
		if got := CanonicalComposeStopSignal(raw); got != want {
			t.Errorf("CanonicalComposeStopSignal(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestComposeProcessValidate(t *testing.T) {
	grace := func(s int) *int { return &s }
	entry := func(args ...string) *[]string { return &args }
	for _, tc := range []struct {
		name    string
		process *ComposeProcess
		want    string
	}{
		{"nil", nil, ""},
		{"full", &ComposeProcess{Entrypoint: entry("tini", "--"), User: "1000:1000", WorkingDir: "/srv", StopSignal: "SIGINT", StopGracePeriodS: grace(30)}, ""},
		{"cleared entrypoint", &ComposeProcess{Entrypoint: entry()}, ""},
		{"empty executable", &ComposeProcess{Entrypoint: entry("", "x")}, "entrypoint must name an executable"},
		{"NUL in entrypoint", &ComposeProcess{Entrypoint: entry("a\x00b")}, "NUL"},
		{"user with space", &ComposeProcess{User: "a b"}, "user must be"},
		{"group only", &ComposeProcess{User: ":staff"}, "user must be"},
		{"relative workdir", &ComposeProcess{WorkingDir: "srv"}, "working_dir"},
		{"unclean workdir", &ComposeProcess{WorkingDir: "/srv/"}, "working_dir"},
		{"non-canonical signal", &ComposeProcess{StopSignal: "TERM"}, "stop_signal"},
		{"undelivered signal", &ComposeProcess{StopSignal: "SIGKILL"}, "stop_signal"},
		{"zero grace", &ComposeProcess{StopGracePeriodS: grace(0)}, "stop_grace_period"},
		{"grace beyond absolute cap", &ComposeProcess{StopGracePeriodS: grace(301)}, "stop_grace_period"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.process.Validate()
			if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("Validate = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestComposeProcessValidatePlanCapsStopGrace(t *testing.T) {
	limits, _ := LimitsFor(PlanFree)
	within, beyond := limits.DefaultStopGracePeriodS, limits.DefaultStopGracePeriodS+1
	if err := (&ComposeProcess{StopGracePeriodS: &within}).ValidatePlan(PlanFree); err != nil {
		t.Fatalf("grace at the plan cap rejected: %v", err)
	}
	if err := (&ComposeProcess{StopGracePeriodS: &beyond}).ValidatePlan(PlanFree); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("grace beyond the plan cap accepted: %v", err)
	}
	if err := (*ComposeProcess)(nil).ValidatePlan(PlanFree); err != nil {
		t.Fatalf("nil process rejected: %v", err)
	}
}

func TestComposeProcessCloneAndJSONKeepClearedEntrypoint(t *testing.T) {
	cleared := []string{}
	grace := 10
	original := &ComposeProcess{Entrypoint: &cleared, User: "app", StopGracePeriodS: &grace}
	clone := original.Clone()
	if !reflect.DeepEqual(clone, original) || clone.Entrypoint == original.Entrypoint || clone.StopGracePeriodS == original.StopGracePeriodS {
		t.Fatalf("clone = %#v shares or differs from %#v", clone, original)
	}
	encoded, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"entrypoint":[]`) {
		t.Fatalf("cleared entrypoint encoded as %s; want []", encoded)
	}
	var decoded ComposeProcess
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Entrypoint == nil || len(*decoded.Entrypoint) != 0 {
		t.Fatalf("decoded %+v (%v); want a cleared, non-nil entrypoint", decoded, err)
	}
	if !(*ComposeProcess)(nil).IsZero() || !(&ComposeProcess{}).IsZero() || original.IsZero() {
		t.Fatal("IsZero misclassified a process")
	}
}
