package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAgentRunnerFromEnv(t *testing.T) {
	engine, err := sched.NewEngine(context.Background(), state.NewMemStore(), sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	router := sched.NewVMMRouter(nil, nil, nil)
	env := func(values map[string]string) func(string) string { return func(k string) string { return values[k] } }
	if runner, err := agentRunnerFromEnv(engine, router, env(nil)); runner != nil || err != nil {
		t.Fatalf("off by default: %v %v", runner, err)
	}
	if _, err := agentRunnerFromEnv(engine, router, env(map[string]string{"FAAS_SCHEDD_AGENT_RUNS": "1"})); err == nil {
		t.Fatal("a configuration without agent nodes started")
	}
	runner, err := agentRunnerFromEnv(engine, router, env(map[string]string{"FAAS_SCHEDD_AGENT_RUNS": "1",
		"FAAS_SCHEDD_AGENT_RUN_NODES": "agents-1, agents-2"}))
	if err != nil || runner == nil {
		t.Fatalf("configured: %v %v", runner, err)
	}
}

// adr: 20261027090010 — the agent drives default to the keys imaged stages them at.
func TestAgentRunConfigDefaultsToImagedDrives(t *testing.T) {
	env := func(values map[string]string) func(string) string { return func(k string) string { return values[k] } }
	cfg := agentRunConfigFromEnv(env(nil))
	if cfg.BaseKey != "base/runner-agent-amd64.ext4" || cfg.LayerKey != "base/agent-layer-v1-amd64.ext4" {
		t.Fatalf("default drives = %q, %q", cfg.BaseKey, cfg.LayerKey)
	}
	cfg = agentRunConfigFromEnv(env(map[string]string{"FAAS_SCHEDD_AGENT_RUN_BASE_KEY": " /srv/agent-base.ext4 ",
		"FAAS_SCHEDD_AGENT_RUN_LAYER_KEY": "/srv/agent-layer.ext4"}))
	if cfg.BaseKey != "/srv/agent-base.ext4" || cfg.LayerKey != "/srv/agent-layer.ext4" {
		t.Fatalf("overridden drives = %q, %q", cfg.BaseKey, cfg.LayerKey)
	}
}
