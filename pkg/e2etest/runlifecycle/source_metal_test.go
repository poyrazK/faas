//go:build metal

package runlifecycle_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestSourceRunLifecycleMetal exercises real HTTP admission, PostgreSQL,
// schedd, vmmd, guest-init and Node. It does not qualify sanitized snapshot
// capture/restore or attest a production runtime image's vulnerability scan.
func TestSourceRunLifecycleMetal(t *testing.T) {
	f := newRunFixture(t)
	var first api.ExecutionResponse
	request := runRequest(`import fs from 'node:fs'; export default async function(input,context) { console.log('run-stdout'); console.error('run-stderr'); fs.writeFileSync('/tmp/run-lifecycle-private','private'); fs.writeFileSync(context.output_dir+'/answer.txt','42'); return {answer:input.value+1,uid:process.getuid()}; }`)
	request.OutputFiles = []string{"answer.txt"}
	if !t.Run("source_result_artifact_and_usage", func(t *testing.T) {
		first = f.submit(t, "native-source-success", request)
		row := f.wait(t, first.ID, api.ExecutionStatusSucceeded)
		var result struct{ Answer, UID int }
		if err := json.Unmarshal(row.Result, &result); err != nil || result.Answer != 42 || result.UID != 1000 {
			t.Fatalf("source result: %s, %v", row.Result, err)
		}
		if !strings.Contains(row.Stdout, "run-stdout") || !strings.Contains(row.Stderr, "run-stderr") || row.Usage == nil || row.Usage.WallTimeMS <= 0 {
			t.Fatalf("output/usage missing: %+v", row)
		}
		digest := sha256.Sum256([]byte("42"))
		if len(row.Artifacts) != 1 || row.Artifacts[0].Name != "answer.txt" || string(row.Artifacts[0].Content) != "42" || row.Artifacts[0].SHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
			t.Fatalf("artifacts: %+v", row.Artifacts)
		}
	}) {
		return
	}
	if !t.Run("apid_restart_replays_original_receipt", func(t *testing.T) {
		if err := f.h.RestartAPID(); err != nil {
			t.Fatal(err)
		}
		replay := f.submit(t, "native-source-success", request)
		if replay.ID != first.ID || replay.Status != api.ExecutionStatusSucceeded {
			t.Fatalf("restart replay: %+v", replay)
		}
	}) {
		return
	}
	if !t.Run("fresh_guest_and_network_isolation", func(t *testing.T) {
		req := runRequest(`import fs from 'node:fs'; import os from 'node:os'; export default async function() { let blocked=false; try { await fetch('http://169.254.169.254/computeMetadata/v1/',{signal:AbortSignal.timeout(300)}); } catch { blocked=true; } return {isolated:!fs.existsSync('/tmp/run-lifecycle-private'),blocked,interfaces:Object.keys(os.networkInterfaces())}; }`)
		row := f.wait(t, f.submit(t, "native-isolation", req).ID, api.ExecutionStatusSucceeded)
		var result struct {
			Isolated, Blocked bool
			Interfaces        []string
		}
		if err := json.Unmarshal(row.Result, &result); err != nil || !result.Isolated || !result.Blocked {
			t.Fatalf("isolation: %s, %v", row.Result, err)
		}
		for _, name := range result.Interfaces {
			if name != "lo" {
				t.Fatalf("unexpected guest interface %q", name)
			}
		}
	}) {
		return
	}
	if !t.Run("guest_error_is_terminal", func(t *testing.T) {
		row := f.wait(t, f.submit(t, "native-language-error", runRequest(`export default async function(){ throw new Error('expected-native-error'); }`)).ID, api.ExecutionStatusFailed)
		if row.Failure == nil || row.Failure.Code != "guest_error" || !strings.Contains(row.Stderr, "expected-native-error") {
			t.Fatalf("guest error did not preserve its failure code and stderr: %+v", row)
		}
	}) {
		return
	}
	if !t.Run("output_is_bounded", func(t *testing.T) {
		req := runRequest(`export default async function(){ console.log('x'.repeat(131072)); return 42; }`)
		req.Limits.MaxOutputBytes = 1024
		row := f.wait(t, f.submit(t, "native-output-bound", req).ID, "terminal")
		if !row.OutputTruncated || len(row.Stdout)+len(row.Stderr)+len(row.Result) > 1024 {
			t.Fatalf("output cap not enforced: status=%s truncated=%v bytes=%d", row.Status, row.OutputTruncated, len(row.Stdout)+len(row.Stderr)+len(row.Result))
		}
	}) {
		return
	}
	sleep := runRequest(`export default async function(){ console.log('running-marker'); await new Promise(resolve=>setTimeout(resolve,120000)); return 1; }`)
	if !t.Run("running_cancel_tears_down", func(t *testing.T) {
		row := f.submit(t, "native-running-cancel", sleep)
		f.waitGuestStarted(t, row.ID)
		if _, err := f.client.CancelExecution(t.Context(), row.ID); err != nil {
			t.Fatal(err)
		}
		f.wait(t, row.ID, api.ExecutionStatusCancelled)
	}) {
		return
	}
	if !t.Run("deadline_does_not_extend", func(t *testing.T) {
		req := sleep
		limits := *sleep.Limits
		req.Limits = &limits
		req.Limits.TimeoutMS = 10000
		row := f.submit(t, "native-deadline", req)
		f.waitGuestStarted(t, row.ID)
		f.wait(t, row.ID, api.ExecutionStatusTimedOut)
		if replay := f.submit(t, "native-deadline", req); replay.ID != row.ID || replay.Status != api.ExecutionStatusTimedOut {
			t.Fatalf("deadline replay: %+v", replay)
		}
	}) {
		return
	}
	if !t.Run("queued_expiry_never_dispatches", func(t *testing.T) {
		if err := f.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		req := runRequest(`export default async function(){ throw new Error('expired work ran'); }`)
		req.Limits.TimeoutMS = 100
		row := f.submit(t, "native-queued-expiry", req)
		time.Sleep(300 * time.Millisecond)
		if err := f.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		settled := f.wait(t, row.ID, api.ExecutionStatusTimedOut)
		if settled.StartedAt != nil {
			t.Fatal("expired queued work started a guest")
		}
	}) {
		return
	}
	for _, owner := range []string{"schedd", "vmmd"} {
		if !t.Run(owner+"_restart_never_reexecutes_running_work", func(t *testing.T) {
			key := "native-" + owner + "-restart"
			row := f.submit(t, key, sleep)
			f.waitGuestStarted(t, row.ID)
			var err error
			if owner == "schedd" {
				err = f.h.RestartSchedd()
			} else {
				err = f.h.RestartVMMD()
			}
			if err != nil {
				t.Fatal(err)
			}
			settled := f.wait(t, row.ID, "terminal")
			if settled.Status == api.ExecutionStatusSucceeded {
				t.Fatalf("interrupted guest unexpectedly succeeded: %+v", settled)
			}
			if count := f.guestMarkerCount(t, row.ID); count != 1 {
				t.Fatalf("guest execution witness count after owner restart = %d, want 1", count)
			}
			if replay := f.submit(t, key, sleep); replay.ID != row.ID || !replay.Status.Terminal() {
				t.Fatalf("interrupted run was resubmitted: %+v", replay)
			}
		}) {
			return
		}
	}
	t.Run("payload_erasure_and_single_usage_entry", func(t *testing.T) { f.assertSettled(t) })
}
