package e2etest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// The stall rule, exercised without Postgres: it is what decides whether a
// build that has gone quiet is reported in three minutes or sixteen.

func TestProgressTracker_SilenceForTheWindowIsAStall(t *testing.T) {
	start := time.Unix(0, 0)
	p := newProgressTracker(start, 3*time.Minute, time.Hour)
	sig := buildSignature{deployment: state.DeployBuilding, build: state.BuildRunning, logBytes: 100}

	if err := p.observe(start, sig); err != nil {
		t.Fatalf("first observation reported %v", err)
	}
	if err := p.observe(start.Add(2*time.Minute+59*time.Second), sig); err != nil {
		t.Fatalf("just inside the window reported %v", err)
	}
	err := p.observe(start.Add(3*time.Minute), sig)
	if !errors.Is(err, errBuildStalled) {
		t.Fatalf("three silent minutes reported %v, want %v; a wedged build would "+
			"instead burn the whole ceiling and report nothing", err, errBuildStalled)
	}
}

// Every extra byte of log is progress. This is the case that distinguishes a
// slow cold build (still writing) from a wedged one (silent).
func TestProgressTracker_LogGrowthResetsTheClock(t *testing.T) {
	start := time.Unix(0, 0)
	p := newProgressTracker(start, 3*time.Minute, time.Hour)
	sig := buildSignature{deployment: state.DeployBuilding, build: state.BuildRunning, logBytes: 100}
	_ = p.observe(start, sig)

	// Keep writing one byte every two minutes for a long time: never stalls.
	now := start
	for i := 1; i <= 10; i++ {
		now = now.Add(2 * time.Minute)
		sig.logBytes++
		if err := p.observe(now, sig); err != nil {
			t.Fatalf("at %s with the log still growing: %v; a slow but healthy "+
				"cold build would be killed", now.Sub(start), err)
		}
	}
}

// A status transition with no log output yet is still progress — apid
// enqueuing the build, builderd picking it up.
func TestProgressTracker_StatusTransitionIsProgress(t *testing.T) {
	start := time.Unix(0, 0)
	p := newProgressTracker(start, 3*time.Minute, time.Hour)

	_ = p.observe(start, buildSignature{deployment: state.DeployBuilding})
	_ = p.observe(start.Add(2*time.Minute), buildSignature{deployment: state.DeployBuilding, build: state.BuildQueued})
	_ = p.observe(start.Add(4*time.Minute), buildSignature{deployment: state.DeployBuilding, build: state.BuildRunning})
	// 5m59s: only 1m59s since the last transition.
	if err := p.observe(start.Add(5*time.Minute+59*time.Second), buildSignature{deployment: state.DeployBuilding, build: state.BuildRunning}); err != nil {
		t.Fatalf("status transitions did not count as progress: %v", err)
	}
}

// The ceiling catches a build that never stops writing but never finishes;
// it must win over "still making progress".
func TestProgressTracker_CeilingWinsOverProgress(t *testing.T) {
	start := time.Unix(0, 0)
	p := newProgressTracker(start, 3*time.Minute, 15*time.Minute)
	sig := buildSignature{deployment: state.DeployBuilding, build: state.BuildRunning}

	now := start
	for now.Before(start.Add(15 * time.Minute)) {
		now = now.Add(time.Minute)
		sig.logBytes++ // always progressing
		if err := p.observe(now, sig); err != nil {
			if now.Sub(start) < 15*time.Minute {
				t.Fatalf("gave up at %s before the ceiling: %v", now.Sub(start), err)
			}
			if !errors.Is(err, errBuildCeiling) {
				t.Fatalf("at the ceiling got %v, want %v", err, errBuildCeiling)
			}
			return
		}
	}
	t.Fatal("a build that wrote for the full ceiling was never stopped")
}

// silentFor is what the failure message prints; it must measure from the last
// change, not from the start.
func TestProgressTracker_SilentForMeasuresFromLastChange(t *testing.T) {
	start := time.Unix(0, 0)
	p := newProgressTracker(start, time.Hour, time.Hour)
	_ = p.observe(start, buildSignature{logBytes: 1})
	_ = p.observe(start.Add(10*time.Minute), buildSignature{logBytes: 2})
	if got := p.silentFor(start.Add(12 * time.Minute)); got != 2*time.Minute {
		t.Fatalf("silentFor = %s, want 2m (last change was at 10m)", got)
	}
}

func TestDefaults_StallWindowIsWellUnderTheCeiling(t *testing.T) {
	// The whole point is failing wedges early. If the window approached the
	// ceiling the tracker would degrade back into a plain deadline.
	if DefaultBuildStallWindow*3 > DefaultBuildCeiling {
		t.Fatalf("stall window %s is not well under the ceiling %s", DefaultBuildStallWindow, DefaultBuildCeiling)
	}
}

// The window must clear the slowest build actually observed on the acceptance
// node, or healthy builds are reported as stalls — which is what a 3-minute
// window did to all 16 builds in gate run 35137856640.
func TestDefaults_StallWindowClearsTheSlowestObservedBuild(t *testing.T) {
	const slowestObserved = 271 * time.Second // enqueue→imaged, run 35137856640
	if DefaultBuildStallWindow <= slowestObserved {
		t.Fatalf("stall window %s does not clear the slowest observed build (%s); "+
			"a healthy build would be reported as wedged", DefaultBuildStallWindow, slowestObserved)
	}
}

// The tail is the diagnosis a stall report carries; each unreadable case must
// say so rather than print nothing.
func TestBuildLogTail(t *testing.T) {
	dir := t.TempDir()

	if got := buildLogTail(state.Deployment{}, state.Build{}); !strings.Contains(got, "no build log path") {
		t.Errorf("no path: %q", got)
	}
	if got := buildLogTail(state.Deployment{}, state.Build{LogPath: filepath.Join(dir, "missing.log")}); !strings.Contains(got, "unreadable") {
		t.Errorf("missing file: %q", got)
	}

	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := buildLogTail(state.Deployment{}, state.Build{LogPath: empty}); !strings.Contains(got, "is empty") {
		t.Errorf("empty file: %q", got)
	}

	big := filepath.Join(dir, "big.log")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 10000)+"LAST-LINE"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := buildLogTail(state.Deployment{}, state.Build{LogPath: big})
	if !strings.HasSuffix(got, "LAST-LINE") || len(got) > 4096+200 {
		t.Errorf("large file was not trimmed to a tail ending in the last line (len=%d)", len(got))
	}
	if size := buildLogSize(state.Deployment{}, state.Build{LogPath: big}); size != 10009 {
		t.Errorf("buildLogSize = %d, want 10009", size)
	}
}

// The progress signal must read the DEPLOYMENT row's log path: that is where
// builderd streams lines. Reading the build row's (empty) path reported every
// healthy build as a stall with log=0 bytes.
func TestBuildLogSize_ReadsTheDeploymentLogPath(t *testing.T) {
	dir := t.TempDir()
	depLog := filepath.Join(dir, "build.log")
	if err := os.WriteFile(depLog, []byte("detected framework: node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dep := state.Deployment{LogPath: depLog}
	build := state.Build{} // builderd leaves this empty during the build

	if got := buildLogSize(dep, build); got != 25 {
		t.Fatalf("buildLogSize = %d, want 25: the signal is reading the build row's "+
			"empty LogPath instead of the deployment's, so a healthy build looks silent", got)
	}
	if got := buildLogTail(dep, build); !strings.Contains(got, "detected framework") {
		t.Fatalf("tail did not come from the deployment log: %q", got)
	}
	// Fall back to the build row only when the deployment has no path.
	if got := buildLogSize(state.Deployment{}, state.Build{LogPath: depLog}); got != 25 {
		t.Fatalf("fallback to build.LogPath = %d, want 25", got)
	}
}
