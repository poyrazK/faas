// adr: 192
// spec: §6.3
package fcvm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPreBootStageTimings_SkipLearnAndChangedIdentity(t *testing.T) {
	for _, kind := range []string{"known identical", "learn identical", "changed instance"} {
		t.Run(kind, func(t *testing.T) {
			sessions, mountRoot := fakeLoopMounts(t)
			v := newStagingVMM(t, "captured")
			oldEnv := []byte(`{"FAAS_INSTANCE_ID":"captured"}`)
			if err := v.stagePreBootFiles("captured", nil, nil, oldEnv, learnInputs.resolver, false); err != nil {
				t.Fatal(err)
			}
			key := v2CaptureKey("timing")
			v.preBoot.captured("captured", key)
			env := oldEnv
			if kind == "learn identical" {
				v.preBoot = newPreBootLedger()
			} else if kind == "changed instance" {
				// A fresh per-instance drive fixture avoids overwriting 0400
				// files as an unprivileged test user. The capture ledger stays.
				sessions, mountRoot = fakeLoopMounts(t)
				env = []byte(`{"FAAS_INSTANCE_ID":"restored"}`)
			}
			v2 := newStagingVMMFrom(t, v, "restored")
			before := *sessions
			timings := preBootStageTimings{Write: time.Hour, FilesWritten: 100}
			skipped, err := v2.stagePreBootFilesUnless("restored", key, nil, nil, env, learnInputs.resolver, false, &timings)
			if err != nil || skipped != (kind == "known identical") {
				t.Fatalf("skip=%v err=%v", skipped, err)
			}
			if timings.FilesTotal != 2 || timings.Prepare <= 0 {
				t.Fatalf("payload preparation missing: %+v", timings)
			}
			if kind == "known identical" {
				if *sessions != before || timings.Check != 0 || timings.Write != 0 || timings.FilesWritten != 0 {
					t.Fatalf("skip retained stale timings or performed file work: %+v", timings)
				}
			} else {
				if *sessions-before != 1 || timings.Check <= 0 {
					t.Fatalf("mounted check not measured: %+v", timings)
				}
				if kind == "learn identical" && (timings.Write != 0 || timings.FilesWritten != 0) {
					t.Fatalf("identical files were charged to writes: %+v", timings)
				}
				if kind == "changed instance" && (timings.Write <= 0 || timings.FilesWritten != 2) {
					t.Fatalf("changed identity did not retain all writes: %+v", timings)
				}
			}
			got, err := os.ReadFile(filepath.Join(*mountRoot, apiEnvPath))
			if err != nil || string(got) != string(env) {
				t.Fatalf("instance env differs: %v", err)
			}
		})
	}
}

func TestPreBootStageTimings_EarlyReturnClearsPriorWork(t *testing.T) {
	for _, resolver := range []string{"", "8.8.8.8"} {
		t.Run("resolver="+resolver, func(t *testing.T) {
			sessions, _ := fakeLoopMounts(t)
			v := NewJailerVMM(t.TempDir(), 0) // neither case may resolve a drive
			timings := preBootStageTimings{loopMountTimings: loopMountTimings{Mount: time.Hour, Unmount: time.Hour}, Check: time.Hour, Write: time.Hour, FilesWritten: 100}
			skipped, err := v.stagePreBootFilesUnless("empty", "", nil, nil, nil, resolver, false, &timings)
			if skipped || (err != nil) != (resolver != "") || *sessions != 0 {
				t.Fatalf("unexpected early return: skip=%v err=%v sessions=%d", skipped, err, *sessions)
			}
			if timings.Mount != 0 || timings.Unmount != 0 || timings.Check != 0 || timings.Write != 0 || timings.FilesTotal != 0 || timings.FilesWritten != 0 {
				t.Fatalf("early return retained stale work: %+v", timings)
			}
		})
	}
}

func TestPreBootStageTimings_CountsOnlyCompletedWrites(t *testing.T) {
	_, mountRoot := fakeLoopMounts(t)
	v := newStagingVMM(t, "partial")
	// Prevent resolver replacement after the env writer succeeds.
	blocked := filepath.Join(*mountRoot, serviceDiscoveryResolverPath)
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var timings preBootStageTimings
	_, err := v.stagePreBootFilesUnless("partial", "", nil, nil, []byte(`{"K":"v"}`), learnInputs.resolver, false, &timings)
	if err == nil || timings.FilesTotal != 2 || timings.FilesWritten != 1 || timings.Write <= 0 || timings.Check != 0 {
		t.Fatalf("partial write attribution: %+v, %v", timings, err)
	}
}
