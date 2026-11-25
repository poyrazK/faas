package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// The dry run names each service's release command and where it is declared
// (agent eval 2026-11-24, voting app: a worker's Procfile release: ran, but
// the plan never showed it).
func TestPrintPlanTextShowsReleaseCommand(t *testing.T) {
	plan := api.PlanResponse{ProjectSlug: "voting", CanApply: true, Workloads: []api.PlanWorkload{
		{Name: "worker", RootDir: "worker", Class: "worker", ReleaseCommand: "dotnet Worker.dll migrate", ReleaseCommandSource: "worker/Procfile release:"},
		{Name: "vote", RootDir: "vote", Class: "http"},
	}}
	var buf bytes.Buffer
	if exit := main.PrintPlanText(&buf, plan, nil, false); exit != 0 {
		t.Fatalf("exit %d", exit)
	}
	out := buf.String()
	if !strings.Contains(out, "      release: dotnet Worker.dll migrate   (worker/Procfile release:)") {
		t.Fatalf("release command missing:\n%s", out)
	}
	if strings.Count(out, "release:") != 2 {
		t.Fatalf("release shown for a service without one:\n%s", out)
	}
}
