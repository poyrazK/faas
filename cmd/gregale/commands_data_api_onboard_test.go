package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"reflect"
	"testing"
	"time"
)

func TestDataAPIOnboardingStopsAndNeverClaimsActivationAfterFailure(t *testing.T) {
	steps := main.DataAPIOnboardingSteps("/project", "/bundle.json", "data-api-starter")
	for _, fail := range []int{-1, 0, 1, 2} {
		t.Run(stepsName(fail), func(t *testing.T) {
			var called, events []string
			code := main.RunDataAPIOnboarding(steps, func(step main.DataAPIOnboardingStep) int {
				called = append(called, step.Name)
				if len(called)-1 == fail {
					return 7
				}
				return 0
			}, func(name, status string, duration time.Duration) {
				if duration < 0 {
					t.Fatal("negative duration")
				}
				events = append(events, name+":"+status)
			})
			if fail < 0 {
				if code != 0 || !reflect.DeepEqual(events, []string{"scaffold:passed", "install:passed", "verify:passed", "local_activation:passed"}) {
					t.Fatalf("code=%d events=%v", code, events)
				}
			} else if code != 7 || len(called) != fail+1 || events[len(events)-1] != steps[fail].Name+":failed" {
				t.Fatalf("code=%d calls=%v events=%v", code, called, events)
			}
		})
	}
}

func stepsName(index int) string {
	if index < 0 {
		return "success"
	}
	return []string{"scaffold", "install", "verify"}[index]
}

func TestDataAPIOnboardingUsesCompleteAcceptanceAndLiteralPaths(t *testing.T) {
	root, bundle := "/project with spaces/$(touch unexpected)", "/approved bundle.json"
	steps := main.DataAPIOnboardingSteps(root, bundle, "data-api-starter")
	if !reflect.DeepEqual(steps[2].Args, []string{"--directory", root, "--port", "0", "--once", "--check", "--replay", "data-api.requests.json"}) {
		t.Fatal(steps[2])
	}
	if got := main.DataAPIOnboardingQuote("a'$(secret)`cmd`"); got != "'a'\"'\"'$(secret)`cmd`'" {
		t.Fatal(got)
	}
}

func TestDataAPIOnboardingInvalidInputsDoNotCreateProjects(t *testing.T) {
	for _, args := range [][]string{{}, {"--path", t.TempDir()}, {"--path", t.TempDir(), "--bundle", "missing.json"}, {"--path", ".", "--bundle", "missing.json"}, {"--path", "x", "--bundle", "x", "unexpected"}, {"--path", "x", "--bundle", "x", "--template", "unknown"}} {
		if main.CmdDataAPIOnboard(args) == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
