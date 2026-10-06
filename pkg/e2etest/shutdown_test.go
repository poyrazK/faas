package e2etest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHarnessShutdownKeepsRuntimeAliveWhileWorkersDrain(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		name := "direct-builder"
		if wrapped {
			name = "wrapped-imaged"
		}
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			alive, ready, result := filepath.Join(tmp, "runtime-alive"), filepath.Join(tmp, "worker-ready"), filepath.Join(tmp, "drained")
			runtimePath := filepath.Join(tmp, "vmmd")
			workerPath := filepath.Join(tmp, "builderd")
			if wrapped {
				workerPath = filepath.Join(tmp, "imaged")
			}
			// The worker needs its runtime during shutdown, just as imaged and
			// builderd need a live VMMD to destroy captures and cancelled builds.
			runtimeScript := "#!/bin/sh\ntrap 'rm -f \"$1\"; exit 0' TERM\ntouch \"$1\"\nwhile :; do sleep 0.1; done\n"
			workerScript := "#!/bin/sh\ntrap 'sleep 0.2; if [ -f \"$1\" ]; then touch \"$3\"; exit 0; else exit 1; fi' TERM\ntouch \"$2\"\nwhile :; do sleep 0.1; done\n"
			for path, script := range map[string]string{runtimePath: runtimeScript, workerPath: workerScript} {
				if err := os.WriteFile(path, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			runtimeProc := exec.Command(runtimePath, alive)
			workerProc := exec.Command(workerPath, alive, ready, result)
			if wrapped {
				// Match a privilege wrapper: Cmd.Path names the wrapper, while
				// an argument names the actual daemon that replaces it.
				workerProc = exec.Command("/bin/sh", "-c", `exec "$1" "$2" "$3" "$4"`, "wrapper", workerPath, alive, ready, result)
			}
			for _, proc := range []*exec.Cmd{runtimeProc, workerProc} {
				if err := proc.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if proc.ProcessState == nil {
						_ = proc.Process.Kill()
						_ = proc.Wait()
					}
				})
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				_, runtimeErr := os.Stat(alive)
				_, workerErr := os.Stat(ready)
				if runtimeErr == nil && workerErr == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("shutdown fixture did not become ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			h := &Harness{T: t, procs: []*exec.Cmd{runtimeProc, workerProc}}
			h.Stop()
			if _, err := os.Stat(result); err != nil {
				t.Fatal("worker lost its runtime before it could drain")
			}
			if !workerProc.ProcessState.Success() || !runtimeProc.ProcessState.Success() {
				t.Fatalf("shutdown failed: worker=%v runtime=%v", workerProc.ProcessState, runtimeProc.ProcessState)
			}
		})
	}
}
