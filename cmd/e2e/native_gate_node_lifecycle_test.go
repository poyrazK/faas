package e2e

// The gate must stop its acceptance node on EVERY exit path.
//
// faas-acceptance-1 is billed per second while it runs, and the gate is the
// only thing that ever runs on it. The stop step used to be conditioned on
// this run having started the node:
//
//	if: always() && steps.compute-node.outputs.started == 'true'
//
// which leaks. A run cancelled mid-flight leaves the node up; every later run
// then observes RUNNING, records started=false, and declines to stop it as
// well. Runs 35010949241 and 35016399564 each skipped the stop step for that
// reason and the node billed for hours with no gate running.
//
// These tests pin the two halves of the fix: the stop step is reachable on
// cancellation and failure (always()), and it is not gated on who started the
// node — only on the explicit keep_node_running debugging opt-in.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeGateWorkflow(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e-native.yml"))
	if err != nil {
		t.Fatalf("read e2e-native workflow: %v", err)
	}
	return string(body)
}

// stopStepCondition returns the `if:` line guarding the node-stop step.
func stopStepCondition(t *testing.T, workflow string) string {
	t.Helper()
	const step = "- name: Stop the acceptance node"
	i := strings.Index(workflow, step)
	if i < 0 {
		t.Fatal("the gate has no step that stops the acceptance node; it would bill forever")
	}
	for _, line := range strings.Split(workflow[i:], "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "if:") {
			return trimmed
		}
	}
	t.Fatal("the node-stop step has no `if:` condition to inspect")
	return ""
}

func TestNativeGateStopsTheNodeOnEveryExitPath(t *testing.T) {
	cond := stopStepCondition(t, nativeGateWorkflow(t))

	// Without always(), a cancelled or failed run skips the step entirely —
	// which is the exact path that stranded the node.
	if !strings.Contains(cond, "always()") {
		t.Errorf("node-stop condition %q is not always(); a cancelled or failed run "+
			"would leave the node billing", cond)
	}
}

func TestNativeGateStopDoesNotDependOnWhoStartedTheNode(t *testing.T) {
	cond := stopStepCondition(t, nativeGateWorkflow(t))

	if strings.Contains(cond, "started") {
		t.Errorf("node-stop condition %q keys off which run started the node. "+
			"Once any run leaves the node up, every later run observes RUNNING "+
			"and declines to stop it too, so the leak is permanent.", cond)
	}
}

// The one legitimate reason to leave the node up — debugging on the host —
// must be an explicit dispatch input, so it can never happen by accident.
func TestNativeGateKeepRunningIsAnExplicitOptIn(t *testing.T) {
	workflow := nativeGateWorkflow(t)

	if !strings.Contains(workflow, "keep_node_running:") {
		t.Fatal("no keep_node_running dispatch input; the only way to keep the node " +
			"up for debugging would be to edit the workflow")
	}
	if !strings.Contains(stopStepCondition(t, workflow), "keep_node_running") {
		t.Error("the node-stop step ignores keep_node_running, so the input does nothing")
	}

	// A `default: true` would restore the leak under a different name.
	inputs := workflow[strings.Index(workflow, "keep_node_running:"):]
	if end := strings.Index(inputs, "jobs:"); end > 0 {
		inputs = inputs[:end]
	}
	if strings.Contains(inputs, "default: true") {
		t.Error("keep_node_running defaults to true; the node would be left running " +
			"on every ordinary run")
	}
}
