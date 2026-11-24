package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
)

const nextStandalonePlanFixture = `{"steps":[{"name":"build","commands":[{"cmd":"npm run build"}]}],"deploy":{"startCommand":"npm run start"}}`

// spec: §4.6
// ADR-20261124160000: the builder rewrites the plan of a Next.js app in its
// workspace, and leaves every other plan as Railpack wrote it.
func TestStampNextStandalonePlan(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "railpack-plan.json")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(plan, nextStandalonePlanFixture)
	write(filepath.Join(dir, "package.json"), `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.4.6"}}`)
	if err := stampNextStandalonePlan(plan, dir, 256); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "env NEXT_ADAPTER_PATH=") {
		t.Fatalf("plan not stamped: %s", got)
	}

	write(plan, nextStandalonePlanFixture)
	write(filepath.Join(dir, "package.json"), `{"scripts":{"start":"node index.js"}}`)
	if err := stampNextStandalonePlan(plan, dir, 256); !errors.Is(err, sharedruntime.ErrNotNextStandalone) {
		t.Fatalf("err = %v, want ErrNotNextStandalone", err)
	}
	if got, _ := os.ReadFile(plan); string(got) != nextStandalonePlanFixture {
		t.Fatalf("plan changed: %s", got)
	}
	if code := runNextStandalone([]string{plan, filepath.Join(dir, "missing"), "256"}); code != 0 {
		t.Fatalf("helper exit = %d, want 0: it never fails a build", code)
	}
}

func TestNextStandaloneBuild(t *testing.T) {
	m := api.BuildManifest{Framework: api.FrameworkRailpackNode, SharedRuntime: true, NextStandalone: true, AppLayerMaxMB: 256}
	if !nextStandaloneBuild(m) {
		t.Fatal("Railpack Node build on the shared runtime")
	}
	for name, mutate := range map[string]func(*api.BuildManifest){
		"no plan cap":      func(m *api.BuildManifest) { m.AppLayerMaxMB = 0 },
		"switched off":     func(m *api.BuildManifest) { m.NextStandalone = false },
		"own runtime base": func(m *api.BuildManifest) { m.SharedRuntime = false },
		"function":         func(m *api.BuildManifest) { m.Function = true },
		"watch mode":       func(m *api.BuildManifest) { m.DevWatchCommand = "npm run dev" },
		"dockerfile":       func(m *api.BuildManifest) { m.Framework = api.FrameworkDockerfile },
	} {
		c := m
		mutate(&c)
		if nextStandaloneBuild(c) {
			t.Errorf("%s: standalone build requested", name)
		}
	}
}
