package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/sched"
)

// agentRunArch is the agent pool's architecture: images/agent-base is
// published for linux/amd64 only.
const agentRunArch = "amd64"

// agentRunnerFromEnv builds the ADR-20261027090010 agent runner when this schedd's
// agent_runs switch is on. Off (the default) returns nil; on with an
// incomplete configuration is a startup error, so a half-configured node
// never admits runs it cannot boot.
func agentRunnerFromEnv(engine *sched.Engine, router *sched.VMMRouter, getenv func(string) string) (*sched.AgentRunner, error) {
	if strings.TrimSpace(getenv("FAAS_SCHEDD_AGENT_RUNS")) != "1" {
		return nil, nil
	}
	runner, err := sched.NewAgentRunner(engine, router, agentRunConfigFromEnv(getenv))
	if err != nil {
		return nil, fmt.Errorf("schedd: FAAS_SCHEDD_AGENT_RUNS=1 needs FAAS_SCHEDD_AGENT_RUN_NODES: %w", err)
	}
	return runner, nil
}

// agentRunConfigFromEnv reads the agent runner's configuration. The drives
// default to where an agent-pool imaged stages them (FAAS_AGENT_BASE_REF);
// the key variables override them, as the metal tests do with local files.
func agentRunConfigFromEnv(getenv func(string) string) sched.AgentRunConfig {
	cfg := sched.AgentRunConfig{
		BaseKey:  strings.TrimSpace(getenv("FAAS_SCHEDD_AGENT_RUN_BASE_KEY")),
		LayerKey: strings.TrimSpace(getenv("FAAS_SCHEDD_AGENT_RUN_LAYER_KEY")),
		Nodes:    strings.Split(getenv("FAAS_SCHEDD_AGENT_RUN_NODES"), ","),
		// ADR-20261027090010 §2: preset snapshots, captured per node and machine shape.
		Snapshots: strings.TrimSpace(getenv("FAAS_SCHEDD_AGENT_RUN_SNAPSHOTS")) == "1",
	}
	if cfg.BaseKey == "" {
		cfg.BaseKey = sched.BaseKeyForArch(sched.AgentRunBaseRuntime, agentRunArch)
	}
	if cfg.LayerKey == "" {
		cfg.LayerKey = sched.AgentRunLayerKeyForArch(agentRunArch)
	}
	return cfg
}
