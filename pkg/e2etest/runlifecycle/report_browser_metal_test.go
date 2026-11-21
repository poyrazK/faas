//go:build metal

package runlifecycle_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
)

type reportBrowserStatus struct {
	PageClosed, DeliveryReady, BrowserPassed bool
	RerunPosts, ClosedPolls                  int
}

func TestSourceHostedBrowserReportMetal(t *testing.T) {
	fixture := newHostedReportFixture(t, func(task *api.PublishExecutionTaskRequest) {
		if task.Name != "gr-demo-merge" {
			return
		}
		const signature = "export default async function merge(input, context) {"
		if !strings.Contains(task.Definition.Source, signature) {
			t.Fatal("test merge handler signature changed")
		}
		// Keep both attempts pinned to the same task. Allow the test to cancel
		// the first Run only after the guest reports entrypoint execution.
		task.Definition.Source = strings.Replace(task.Definition.Source, signature,
			signature+"\n  console.log('running-marker');\n  await new Promise(resolve => setTimeout(resolve, 7500));", 1)
	})
	base, process := startReportBrowserBackend(t, fixture)
	t.Logf("report browser fixture ready on %s", base)
	failedMerge := cancelStartedReportMerge(t, fixture)
	t.Log("cancelled the first merge after its Firecracker guest executed the entrypoint")
	status := awaitReportBrowser(t, base, process, true)
	requireReportBrowserEvidence(t, fixture, failedMerge, status)
	fixture.run.assertSettled(t)
}

func startReportBrowserBackend(t *testing.T, fixture hostedReportFixture) (string, <-chan error) {
	t.Helper()
	ctx := t.Context()
	b := fixture.backend
	b.completionSecret = uuid.NewString() + uuid.NewString()
	port := requiredHostedEnv(t, "FAAS_TEST_REPORT_BROWSER_PORT")
	base := "http://127.0.0.1:" + port
	ca := filepath.Join(t.TempDir(), "storage-ca.pem")
	requireReport(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.edge.Certificate().Raw}), 0600))
	cmd := exec.CommandContext(ctx, b.node, "../../../examples/gregale-run/test/hosted-page-server.mjs", b.bundle, port)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+b.dsn, "GREGALE_API_URL="+b.base, "GREGALE_TEST_TOKEN="+b.token,
		"REPORT_ENDPOINT_ID="+b.endpoint, "REPORT_COMPLETION_WEBHOOK_SECRET="+b.completionSecret, "NODE_EXTRA_CA_CERTS="+ca)
	requireReport(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	awaitReportBrowser(t, base, done, false)
	subscribeHostedReport(t, fixture.run, fixture.store, base+"/report-completion", b.completionSecret)
	return base, done
}

func awaitReportBrowser(t *testing.T, base string, process <-chan error, waitDone bool) reportBrowserStatus {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	path := "/__fixture/health"
	timeout := 10 * time.Second
	if waitDone {
		path = "/__fixture/status"
		timeout = 4 * time.Minute
	}
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(150 * time.Millisecond) {
		select {
		case err := <-process:
			t.Fatalf("report browser backend stopped early: %v", err)
		default:
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
		requireReport(t, err)
		request.Header.Set("Cookie", "report-fixture=owner")
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		var status reportBrowserStatus
		ready := response.StatusCode == http.StatusNoContent && !waitDone
		if waitDone && response.StatusCode == http.StatusOK {
			err = json.NewDecoder(response.Body).Decode(&status)
			ready = err == nil && status.BrowserPassed
		}
		requireReport(t, response.Body.Close())
		if ready {
			return status
		}
	}
	t.Fatal("browser fixture did not reach the expected phase")
	return reportBrowserStatus{}
}

func cancelStartedReportMerge(t *testing.T, fixture hostedReportFixture) string {
	t.Helper()
	f := fixture.run
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		runs, err := fixture.store.ListExecutions(t.Context(), f.accountID, 10, 0)
		requireReport(t, err)
		for _, run := range runs {
			if !strings.HasSuffix(run.StepLabel, ":p:2:a:1") {
				continue
			}
			f.waitGuestStarted(t, run.ID)
			_, err := f.client.CancelExecution(t.Context(), run.ID)
			requireReport(t, err)
			f.wait(t, run.ID, api.ExecutionStatusCancelled)
			f.runs[run.ID] = true
			return run.ID
		}
	}
	t.Fatal("the submitted report did not admit its first merge")
	return ""
}

func requireReportBrowserEvidence(t *testing.T, fixture hostedReportFixture, failedMerge string, status reportBrowserStatus) {
	t.Helper()
	f := fixture.run
	ctx := t.Context()
	if !status.PageClosed || !status.DeliveryReady || !status.BrowserPassed || status.RerunPosts != 2 || status.ClosedPolls != 0 {
		t.Fatalf("browser did not verify the approved rerun and closed-page delivery: %+v", status)
	}
	var parentID, childID string
	requireReport(t, fixture.db.QueryRow(ctx, `SELECT (SELECT request_id FROM report_requests r JOIN report_regional_pipelines p
		ON p.thread_id=r.thread_id WHERE p.parent_thread_id IS NULL),
		(SELECT request_id FROM report_requests r JOIN report_regional_pipelines p ON p.thread_id=r.thread_id
		WHERE p.parent_thread_id IS NOT NULL AND p.report IS NOT NULL)`).Scan(&parentID, &childID))
	parent, _ := hostedRegionalWorkflow(t, f, fixture.db, fixture.backend.endpoint, "owner", parentID)
	child, _ := hostedRegionalWorkflow(t, f, fixture.db, fixture.backend.endpoint, "owner", childID)
	if parent.Pipeline.Completion == nil || parent.Pipeline.Completion.Outcome != "failed" ||
		child.Pipeline.Completion == nil || child.Pipeline.Completion.Outcome != "succeeded" ||
		child.Pipeline.Rerun == nil || child.Pipeline.Rerun.ParentID != parent.ID {
		t.Fatal("expected one failed parent and one successful child")
	}
	original := hostedRegionalStep(t, parent, "merge")
	if len(original.Attempts) != 1 || original.Attempts[0].RunID != failedMerge ||
		original.Attempts[0].Status != api.ExecutionStatusCancelled {
		t.Fatal("the parent receipt did not record its cancelled merge Run")
	}
	replacement := hostedRegionalStep(t, child, "merge")
	if replacement.Outcome != "succeeded" || len(replacement.Attempts) != 1 {
		t.Fatal("the rerun did not execute exactly one successful merge")
	}
	expected := []string{failedMerge, replacement.Attempts[0].RunID}
	for _, label := range []string{"north", "south"} {
		before, after := hostedRegionalStep(t, parent, label), hostedRegionalStep(t, child, label)
		if before.Outcome != "succeeded" || len(before.Attempts) != 1 || before.OutputSHA256 == "" ||
			after.ReusedRunID != before.Attempts[0].RunID || before.OutputSHA256 != after.OutputSHA256 || len(after.Attempts) != 0 {
			t.Fatalf("the child did not reuse the original %s Run and fingerprint", label)
		}
		expected = append(expected, before.Attempts[0].RunID)
	}
	runs, err := fixture.store.ListExecutions(ctx, f.accountID, 10, 0)
	requireReport(t, err)
	if len(runs) != len(expected) {
		t.Fatalf("expected four physical Runs, got %d", len(runs))
	}
	for _, run := range runs {
		if !slices.Contains(expected, run.ID) || (run.ID != failedMerge && run.Status != api.ExecutionStatusSucceeded) {
			t.Fatal("unexpected physical Run")
		}
		f.runs[run.ID] = true
	}
	fixture.object.mu.Lock()
	writes, reads, report := fixture.object.writes, fixture.object.reads, bytes.Clone(fixture.object.body)
	fixture.object.mu.Unlock()
	if writes != 1 || reads != 1 || !bytes.Contains(report, []byte("4 orders · 8 units · 6996 USD cents")) {
		t.Fatal("expected one copy and one private signed download with the sample totals")
	}
	parentUsage, err := runpipeline.ReadUsage(ctx, fixture.store, f.accountID, parent.Pipeline)
	requireReport(t, err)
	childUsage, err := runpipeline.ReadUsage(ctx, fixture.store, f.accountID, child.Pipeline)
	requireReport(t, err)
	if parentUsage.NewRuns != 3 || parentUsage.SettledRuns != 3 || parentUsage.ReusedSteps != 0 ||
		childUsage.NewRuns != 1 || childUsage.SettledRuns != 1 || childUsage.ReusedSteps != 2 {
		t.Fatalf("Report Agent usage differs from pipeline lineage: parent=%+v child=%+v", parentUsage, childUsage)
	}
	data, err := json.Marshal(map[string]any{"schema": "gregale.report-agent-browser/v1", "physical_runs": len(runs),
		"merge_guest_started": true, "merge_failure": "cancelled", "approved_repeat_steps": []string{"merge"},
		"reused_steps": []string{"north", "south"}, "provider_writes": writes, "provider_reads": reads,
		"closed_page_polls": status.ClosedPolls, "rerun_posts": status.RerunPosts, "report_bytes": len(report),
		"parent_usage": parentUsage, "child_usage": childUsage,
		"report_sha256": fmt.Sprintf("%x", sha256.Sum256(report)), "parent_pipeline_id": parent.ID, "child_pipeline_id": child.ID})
	requireReport(t, err)
	requireReport(t, os.WriteFile(requiredHostedEnv(t, "FAAS_TEST_REPORT_MEASUREMENTS"), append(data, '\n'), 0600))
	t.Log("browser rerun verified; four physical Runs, one artifact, one signed download")
}
