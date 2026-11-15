// adr: 680
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGunicornArbiter stands in for gunicorn.arbiter: murder_workers kills
// (records) every worker whose heartbeat is older than the timeout on
// time.monotonic(), as gunicorn 23 does.
const fakeGunicornArbiter = `
import time

class Arbiter:
    def __init__(self, timeout):
        self.timeout = timeout
        self.WORKERS = {}
        self.killed = []

    def murder_workers(self):
        for pid, worker in list(self.WORKERS.items()):
            if time.monotonic() - worker.tmp.last_update() <= self.timeout:
                continue
            self.killed.append(pid)
            del self.WORKERS[pid]
`

// gunicornGuardProbe drives the arbiter through a scripted monotonic clock:
// a normal tick, a 600 s freeze (a snapshot restore), then workers that
// really stop beating while the master keeps checking every second.
const gunicornGuardProbe = `
import json, time
clock = [1000.0]
time.monotonic = lambda: clock[0]
import gunicorn.arbiter as ga

class Tmp:
    def __init__(self): self.t = clock[0]; self.notified = 0
    def notify(self): self.t = clock[0]; self.notified += 1
    def last_update(self): return self.t

class Worker:
    def __init__(self): self.tmp = Tmp()

a = ga.Arbiter(timeout=30)
workers = [Worker(), Worker()]
a.WORKERS = {1: workers[0], 2: workers[1]}
steps = []
def tick(dt, beat=True):
    clock[0] += dt
    if beat:
        for w in a.WORKERS.values():
            w.tmp.t = clock[0] - 0.5
    a.murder_workers()
    steps.append(list(a.killed))

tick(1)
clock[0] += 600            # parked: neither master nor workers ran
a.murder_workers()
steps.append(list(a.killed))
# The workers now really hang: the master keeps checking every second and
# kills them once their heartbeat is older than --timeout.
for _ in range(35):
    clock[0] += 1
    a.murder_workers()
steps.append(list(a.killed))
print(json.dumps({
    "patched": getattr(ga.Arbiter.murder_workers, "_gregale_freeze_guard", False),
    "steps": steps,
    "notified": sorted(w.tmp.notified for w in workers),
}))
`

type gunicornGuardResult struct {
	Patched  bool    `json:"patched"`
	Steps    [][]int `json:"steps"`
	Notified []int   `json:"notified"`
}

func runGunicornGuardProbe(t *testing.T, extraEnv ...string) gunicornGuardResult {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	assets := restoreReseedAssetDir(t, false)
	fake := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fake, "gunicorn"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"__init__.py": "", "arbiter.py": fakeGunicornArbiter} {
		if err := os.WriteFile(filepath.Join(fake, "gunicorn", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// No barrier listens on the socket: the reseed thread gives up and the
	// guard is tested on its own.
	env := baseRuntimeEnv(assets, filepath.Join(fake, "no-barrier.sock"))
	env = setEnvValue(env, "PYTHONPATH", envValue(env, "PYTHONPATH")+string(os.PathListSeparator)+fake)
	env = append(env, extraEnv...)
	cmd := exec.Command(python, "-c", gunicornGuardProbe)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var res gunicornGuardResult
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		t.Fatalf("probe output %q: %v", out, err)
	}
	return res
}

// A restore advances the guest's monotonic clock by the parked time; the
// gunicorn master must not read that as hung workers and kill one on every
// wake. A worker that really stops is still killed one timeout later.
func TestGunicornFreezeGuardSkipsTheCheckAfterAFreeze(t *testing.T) {
	res := runGunicornGuardProbe(t)
	if !res.Patched {
		t.Fatal("gunicorn.arbiter.Arbiter.murder_workers was not wrapped")
	}
	want := [][]int{{}, {}, {1, 2}}
	if len(res.Steps) != len(want) {
		t.Fatalf("steps = %v, want %v", res.Steps, want)
	}
	for i := range want {
		if len(res.Steps[i]) != len(want[i]) {
			t.Fatalf("step %d killed %v, want %v (all steps %v)", i, res.Steps[i], want[i], res.Steps)
		}
	}
	if res.Notified[0] != 1 || res.Notified[1] != 1 {
		t.Fatalf("worker heartbeats refreshed %v times, want once each after the freeze", res.Notified)
	}
}

func TestGunicornFreezeGuardOptOut(t *testing.T) {
	res := runGunicornGuardProbe(t, "GREGALE_GUNICORN_FREEZE_GUARD=off")
	if res.Patched {
		t.Fatal("opt-out still wrapped murder_workers")
	}
	if len(res.Steps) < 2 || len(res.Steps[1]) != 2 {
		t.Fatalf("without the guard the freeze must kill both workers, got %v", res.Steps)
	}
}
