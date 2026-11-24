package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

func TestRestartWaves(t *testing.T) {
	depth := map[string]int{"vmmd": 0, "apid": 0, "schedd": 1, "meterd": 1, "githubd": 1, "outboundd": 1, "gatewayd-internal": 2}
	for _, tc := range []struct {
		name  string
		order []string
		limit int
		want  [][]string
	}{
		{"one daemon", []string{"meterd"}, 4, [][]string{{"meterd"}}},
		{"vmmd alone ahead of its depth", []string{"vmmd", "apid", "schedd"}, 4,
			[][]string{{"vmmd"}, {"apid"}, {"schedd"}}},
		{"a depth together, the next after it", []string{"apid", "schedd", "meterd", "githubd", "gatewayd-internal"}, 4,
			[][]string{{"apid"}, {"schedd", "meterd", "githubd"}, {"gatewayd-internal"}}},
		{"capped", []string{"schedd", "meterd", "githubd", "outboundd"}, 2,
			[][]string{{"schedd", "meterd"}, {"githubd", "outboundd"}}},
		{"limit 1 is one at a time", []string{"schedd", "meterd"}, 1, [][]string{{"schedd"}, {"meterd"}}},
		{"unknown daemon alone in place", []string{"schedd", "mystery", "meterd"}, 4,
			[][]string{{"schedd"}, {"mystery"}, {"meterd"}}},
		{"an order that interleaves depths keeps its order", []string{"schedd", "apid", "meterd"}, 4,
			[][]string{{"schedd"}, {"apid"}, {"meterd"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := restartWaves(tc.order, depth, tc.limit); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("restartWaves(%v, %d) = %v, want %v", tc.order, tc.limit, got, tc.want)
			}
		})
	}
}

// TestRestartWavesOverTheRegistryNeverRestartADependencyBesideItsDependent:
// over the real restart order, a daemon's wave comes strictly after the wave
// of every daemon it lists in Lifecycle.After, and vmmd is always alone.
func TestRestartWavesOverTheRegistryNeverRestartADependencyBesideItsDependent(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	depth, err := daemonunitspec.RestartDepth()
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{2, 4, 16} {
		waveOf := map[string]int{}
		for i, wave := range restartWaves(order, depth, limit) {
			for _, service := range wave {
				waveOf[service] = i
			}
			if len(wave) > 1 && containsString(wave, "vmmd") {
				t.Errorf("limit %d: vmmd restarts beside %v", limit, wave)
			}
		}
		for _, entry := range daemonunitspec.Registry {
			for _, dep := range entry.Lifecycle.After {
				if waveOf[entry.Name] <= waveOf[dep] {
					t.Errorf("limit %d: %s (wave %d) is not after its dependency %s (wave %d)",
						limit, entry.Name, waveOf[entry.Name], dep, waveOf[dep])
				}
			}
		}
		if len(waveOf) != len(order) {
			t.Errorf("limit %d: waves cover %d daemons, order has %d", limit, len(waveOf), len(order))
		}
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestRestartParallelFromEnv(t *testing.T) {
	for _, tc := range []struct {
		value string
		set   bool
		want  int
		err   bool
	}{
		{set: false, want: 0},
		{value: "", set: true, want: 0},
		{value: "4", set: true, want: 4},
		{value: "0", set: true, err: true},
		{value: "17", set: true, err: true},
		{value: "four", set: true, err: true},
	} {
		if tc.set {
			t.Setenv(restartParallelEnv, tc.value)
		} else {
			t.Setenv(restartParallelEnv, "")
			os.Unsetenv(restartParallelEnv)
		}
		got, err := restartParallelFromEnv()
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("%s=%q (set %v): got %d, %v; want %d, error %v", restartParallelEnv, tc.value, tc.set, got, err, tc.want, tc.err)
		}
	}
}

// parallelHarness records restarts and readiness probes from concurrent
// restartOne calls; ready blocks each probe until release is closed, so a
// test can see which daemons were in flight together.
type parallelHarness struct {
	mu        sync.Mutex
	inFlight  map[string]bool
	together  [][]string
	restarted []string
	failing   map[string]bool
}

func (h *parallelHarness) runtime(t *testing.T, order []string, limit int) hostRuntime {
	t.Helper()
	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	runCommand = func(_ context.Context, name string, args ...string) error {
		if name == "systemctl" && len(args) >= 2 && args[0] == "restart" {
			h.mu.Lock()
			h.restarted = append(h.restarted, strings.TrimSuffix(strings.TrimPrefix(args[1], "faas-"), ".service"))
			h.mu.Unlock()
		}
		return nil
	}
	binaryDir := t.TempDir()
	for _, service := range order {
		if err := os.WriteFile(filepath.Join(binaryDir, service), []byte("candidate"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.inFlight = map[string]bool{}
	return hostRuntime{
		binaryDir:       binaryDir,
		serviceOrder:    order,
		readyTimeout:    time.Second,
		restartParallel: limit,
		waitReadyOverride: func(_ context.Context, service string) error {
			h.mu.Lock()
			h.inFlight[service] = true
			h.mu.Unlock()
			// Long enough for the rest of a wave to reach readiness too.
			time.Sleep(20 * time.Millisecond)
			h.mu.Lock()
			var now []string
			for s := range h.inFlight {
				now = append(now, s)
			}
			sort.Strings(now)
			h.together = append(h.together, now)
			delete(h.inFlight, service)
			h.mu.Unlock()
			if h.failing[service] {
				return errors.New("not ready")
			}
			return nil
		},
	}
}

func TestRestartInWavesRestartsADepthTogetherAndWaitsForIt(t *testing.T) {
	order := []string{"vmmd", "apid", "schedd", "meterd", "githubd", "gatewayd-internal"}
	h := &parallelHarness{}
	r := h.runtime(t, order, 4)
	if err := r.Restart(context.Background(), manifestWithServices(order...)); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	sort.Strings(h.restarted)
	want := append([]string(nil), order...)
	sort.Strings(want)
	if !reflect.DeepEqual(h.restarted, want) {
		t.Fatalf("restarted %v, want %v", h.restarted, want)
	}
	sawDepthTogether := false
	for _, now := range h.together {
		if containsString(now, "vmmd") && len(now) > 1 {
			t.Errorf("vmmd was in flight beside %v", now)
		}
		if containsString(now, "gatewayd-internal") && len(now) > 1 {
			t.Errorf("gatewayd-internal was in flight beside its dependencies: %v", now)
		}
		if len(now) == 3 {
			sawDepthTogether = true
		}
	}
	if !sawDepthTogether {
		t.Errorf("schedd, meterd and githubd never restarted together: %v", h.together)
	}
}

func TestRestartInWavesFinishesTheWaveThenStops(t *testing.T) {
	order := []string{"apid", "schedd", "meterd", "gatewayd-internal"}
	h := &parallelHarness{failing: map[string]bool{"schedd": true}}
	r := h.runtime(t, order, 4)
	err := r.Restart(context.Background(), manifestWithServices(order...))
	if err == nil || !strings.Contains(err.Error(), "restart schedd") {
		t.Fatalf("Restart error = %v, want the failed schedd", err)
	}
	if containsString(h.restarted, "gatewayd-internal") {
		t.Errorf("the wave after a failure must not start: restarted %v", h.restarted)
	}
	if !containsString(h.restarted, "meterd") {
		t.Errorf("the rest of the failed wave must finish: restarted %v", h.restarted)
	}
}

func TestRestartWithoutParallelStaysOneAtATime(t *testing.T) {
	order := []string{"apid", "schedd", "meterd"}
	h := &parallelHarness{}
	r := h.runtime(t, order, 0)
	if err := r.Restart(context.Background(), releasebundle.Manifest{}); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !reflect.DeepEqual(h.restarted, order) {
		t.Fatalf("restarted %v, want %v in order", h.restarted, order)
	}
	for _, now := range h.together {
		if len(now) > 1 {
			t.Fatalf("one-at-a-time restart had %v in flight together", now)
		}
	}
}
