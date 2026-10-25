package main

// adr: 20261024085817 — a rolling compute release never returns a node to
// service on a runtime base generation its serving vmmd cannot boot, and its
// drain wait outlasts the drain's park point.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

func nodeJoinPlaybook(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// nodeJoinTask finds a task by name anywhere in node_join.yml, including
// block, rescue and always sections.
func nodeJoinTask(t *testing.T, name string) map[string]any {
	t.Helper()
	var plays []map[string]any
	if err := yaml.Unmarshal([]byte(nodeJoinPlaybook(t)), &plays); err != nil {
		t.Fatalf("parse node_join.yml: %v", err)
	}
	var find func(tasks []any) map[string]any
	find = func(tasks []any) map[string]any {
		for _, raw := range tasks {
			task, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if task["name"] == name {
				return task
			}
			for _, section := range []string{"block", "rescue", "always"} {
				if nested, ok := task[section].([]any); ok {
					if found := find(nested); found != nil {
						return found
					}
				}
			}
		}
		return nil
	}
	for _, play := range plays {
		if tasks, ok := play["tasks"].([]any); ok {
			if task := find(tasks); task != nil {
				return task
			}
		}
	}
	t.Fatalf("node_join.yml has no task %q", name)
	return nil
}

func yamlText(t *testing.T, v any) string {
	t.Helper()
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestNodeJoinOrdersRuntimeBasePreStageAroundTheDrainBarrier(t *testing.T) {
	playbook := nodeJoinPlaybook(t)
	order := []string{
		"Extract the signed candidate for runtime-base pre-staging",
		"Compare the serving vmmd's runtime base generations with the candidate's",
		"Defer runtime-base pre-staging past the drain when a generation changes",
		"Start the release-bound runtime-base pre-stage",
		// --activate-prepared starts at the marker check, so the generation
		// check must be repeated after it.
		"Verify the prepared rollout marker",
		"Compare runtime base generations before draining",
		"Decide whether this rollout restarts vmmd without draining",
		"Begin graceful drain of the existing node before release installation",
		"Wait for the existing node to reach an empty maintenance hold",
		"Re-check the serving vmmd's runtime base generations after the failed drain",
		"Restore an originally active node after graceful drain failure",
		"Keep the node drained when its serving release can no longer start guests",
		"Pre-stage changed runtime bases behind the drain barrier",
		"Stop stale compute-only services before replacing the active release",
	}
	last := -1
	for _, name := range order {
		at := strings.Index(playbook, "- name: "+name)
		if at < 0 {
			t.Fatalf("node_join.yml is missing %q", name)
		}
		if at < last {
			t.Fatalf("%q is out of order", name)
		}
		last = at
	}
}

func TestNodeJoinGatesEveryRuntimeBaseDecisionOnTheGenerationCheck(t *testing.T) {
	cases := []struct {
		task   string
		tokens []string
	}{
		{task: "Compare the serving vmmd's runtime base generations with the candidate's",
			tokens: []string{"base-generation-check", "--serving-pid", "--candidate-env-file", "/etc/faas/runtime-bases.env", "not in [0, 3, 4]"}},
		{task: "Defer runtime-base pre-staging past the drain when a generation changes",
			tokens: []string{"faas_join_base_generations.rc | default(0)) in [3, 4]"}},
		{task: "Start the release-bound runtime-base pre-stage",
			tokens: []string{"not (faas_join_defer_runtime_prestage | default(false) | bool)"}},
		{task: "Compare runtime base generations before draining",
			tokens: []string{"base-generation-check", "--serving-pid", "not in [0, 3, 4]"}},
		// A generation change always drains: the no-drain restart would keep
		// placing guests while the cache flips.
		{task: "Decide whether this rollout restarts vmmd without draining",
			tokens: []string{"faas_join_drain_base_generations.rc | default(0)) == 0"}},
		{task: "Pre-stage changed runtime bases behind the drain barrier",
			tokens: []string{"faas-imaged-prestage.service", "faas_join_drain_base_generations.rc | default(0)) in [3, 4]"}},
		// The incident: re-activation only while the serving vmmd's
		// generations are still the cached ones; an unanswered check keeps
		// the node drained.
		{task: "Re-check the serving vmmd's runtime base generations after the failed drain",
			tokens: []string{"base-generation-check", "failed_when: false"}},
		{task: "Restore an originally active node after graceful drain failure",
			tokens: []string{"faas_join_rescue_base_generations.rc | default(1)) in [0, 3]", "node_join_drain_rollback"}},
		{task: "Keep the node drained when its serving release can no longer start guests",
			tokens: []string{"faas_join_rescue_base_generations.rc | default(1)) not in [0, 3]", "other_generation", "faas_compute_node_drain_status.stdout"}},
		{task: "Abort after restoring the pre-rollout lifecycle",
			tokens: []string{"faas_compute_node_drain_status.stdout"}},
	}
	for _, tc := range cases {
		t.Run(tc.task, func(t *testing.T) {
			text := yamlText(t, nodeJoinTask(t, tc.task))
			// Folded scalars wrap; compare on single spaces.
			flat := strings.Join(strings.Fields(text), " ")
			for _, token := range tc.tokens {
				if !strings.Contains(flat, token) {
					t.Errorf("task %q is missing %q:\n%s", tc.task, token, text)
				}
			}
		})
	}
}

// The drain wait must outlast the arbiter's park point (api.DrainParkAfterSeconds)
// plus the drain parks of a node at its admission ceiling of 1 GiB instances.
// Each park withdraws the instance from routing (one route round), waits for
// its in-flight requests (bounded) and destroys it; DrainParkConcurrency of
// them run at once. Otherwise the rollout gives up on a drain that is about
// to finish.
func TestNodeJoinDrainWaitOutlastsTheDrainParkPoint(t *testing.T) {
	task := nodeJoinTask(t, "Wait for the existing node to reach an empty maintenance hold")
	retries, rok := task["retries"].(int)
	delay, dok := task["delay"].(int)
	if !rok || !dok {
		t.Fatalf("drain wait retries=%v delay=%v, want integers", task["retries"], task["delay"])
	}
	const destroySeconds = 10 // sched.DestroyTimeout
	fullNode := api.RAMAdmissionCeilingMB / (1024 + api.PerVMOverheadMB)
	rounds := (fullNode + api.DrainParkConcurrency - 1) / api.DrainParkConcurrency
	parkSeconds := rounds * (api.ServiceRouteConvergenceTimeoutSeconds + api.ServiceReplicaDrainTimeoutSeconds + destroySeconds)
	if wait := retries * delay; wait < api.DrainParkAfterSeconds+parkSeconds {
		t.Fatalf("drain wait %d s, want at least DrainParkAfterSeconds %d + %d s for %d rounds of drain parks",
			wait, api.DrainParkAfterSeconds, parkSeconds, rounds)
	}
}

// adr: 20261024191249 — production-us 2026-10-24 (rc.293): the rolled fsn-2
// could not adopt base-minimal's new generation, and the play still let the
// controller return it to service. The adoption gate runs after the
// installed daemons are ready and before the play reports readiness, which
// is what the controller's activation waits for.
func TestNodeJoinGatesActivationOnRuntimeBaseAdoption(t *testing.T) {
	playbook := nodeJoinPlaybook(t)
	order := []string{
		"Pre-stage changed runtime bases behind the drain barrier",
		"Enable and restart the compute-only daemon set",
		"Require dependency-aware readiness after release installation",
		"Require the installed vmmd to adopt every configured runtime base before activation",
		"Read the installed vmmd process before the runtime base adoption check",
		"Wait for the installed vmmd to adopt every configured runtime base",
		"Keep the node drained when its vmmd cannot adopt a configured runtime base",
		"Report local node readiness to the controller",
	}
	last := -1
	for _, name := range order {
		at := strings.Index(playbook, "- name: "+name)
		if at < 0 {
			t.Fatalf("node_join.yml is missing %q", name)
		}
		if at < last {
			t.Fatalf("%q is out of order", name)
		}
		last = at
	}

	cases := []struct {
		task   string
		tokens []string
	}{
		// A drained or fresh node only: a no-drain restart changes no
		// generation (its decision requires the generation check's 0).
		{task: "Require the installed vmmd to adopt every configured runtime base before activation",
			tokens: []string{"when: not (faas_join_skip_drain | bool)", "rescue:"}},
		// The installed binary judges the installed vmmd's configuration.
		{task: "Wait for the installed vmmd to adopt every configured runtime base",
			tokens: []string{"/opt/faas/current/bin/vmmd", "base-adoption-check", "--serving-pid", "faas_join_installed_vmmd_pid.stdout", "until: faas_join_base_adoption.rc == 0"}},
		// The failure leaves the node drained, says so, and names each base
		// (yaml.Marshal doubles the single quotes).
		{task: "Keep the node drained when its vmmd cannot adopt a configured runtime base",
			tokens: []string{"LEFT DRAINED", "faas_join_base_adoption.rc | default(1)) == 3", "selectattr(''verdict'', ''equalto'', ''stale'')", "503 scan_critical", "ADR-20261024191249"}},
	}
	for _, tc := range cases {
		t.Run(tc.task, func(t *testing.T) {
			text := yamlText(t, nodeJoinTask(t, tc.task))
			flat := strings.Join(strings.Fields(text), " ")
			for _, token := range tc.tokens {
				if !strings.Contains(flat, token) {
					t.Errorf("task %q is missing %q:\n%s", tc.task, token, text)
				}
			}
		})
	}

	// The rescue never activates: the controller does that, and only after
	// the play succeeds.
	rescue := yamlText(t, nodeJoinTask(t, "Require the installed vmmd to adopt every configured runtime base before activation")["rescue"])
	if strings.Contains(rescue, "activate") {
		t.Fatalf("the adoption gate's rescue must not activate the node:\n%s", rescue)
	}
	// Bounded: the gate fits the join-node budget it shares with the drain.
	wait := nodeJoinTask(t, "Wait for the installed vmmd to adopt every configured runtime base")
	retries, rok := wait["retries"].(int)
	delay, dok := wait["delay"].(int)
	if !rok || !dok || retries*delay > 300 || retries*delay < 60 {
		t.Fatalf("adoption wait retries=%v delay=%v, want a bound between 1 and 5 minutes", wait["retries"], wait["delay"])
	}
}
