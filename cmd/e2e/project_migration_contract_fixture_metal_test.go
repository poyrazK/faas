//go:build metal

package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func projectContractNativeFiles(binary []byte, mode string) fstest.MapFS {
	files := projectWorkerNativeFiles(binary, "progressive", false)
	files["Procfile"] = &fstest.MapFile{Data: []byte("release: /projectworker migration-expand\n")}
	files["gregale.yaml"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("release:\n  contract_command: /projectworker migration-%s\n", mode))}
	return files
}

func (f *projectWorkerNativeFixture) qualifyMigrationContract(t *testing.T, attempt string, accepted api.ApplyResponse, mode string) {
	t.Helper()
	receipt, err := f.owner.GetProjectSubmission(t.Context(), attempt)
	mustProjectWorkerNative(t, err)
	command := receipt.Apply.SafeRelease.ReleaseCommand
	if command == nil || command.Status != "succeeded" || command.Contract == nil || command.Contract.TaskID != "" {
		t.Fatalf("cleanup ran automatically or expansion missing: %+v", command)
	}
	expansion, err := f.owner.GetAppTask(t.Context(), command.Slug, command.TaskID)
	mustProjectWorkerNative(t, err)
	if expansion.ExitCode == nil || *expansion.ExitCode != 0 || !strings.Contains(expansion.StdoutTail, "native expansion generation=progressive mode=progressive-config") {
		t.Fatalf("expansion did not execute with candidate runtime: %+v", expansion)
	}
	if f.sql != nil {
		f.sql.beforeAdmission(t, f, command.Slug)
	}
	req := api.ProjectReleaseControlRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedGeneration: receipt.Apply.SafeRelease.Verification.Generation}
	if _, err = f.owner.ControlProjectRelease(t.Context(), attempt, "contract", req); err == nil {
		t.Fatal("cleanup bypassed real retained graph/code windows")
	}
	// Drain the fallback through the public intent API. Never mutate proof rows,
	// expiration timestamps, task leases or stabilization clocks in this suite.
	for _, build := range accepted.Builds {
		_, err = f.owner.PatchDeploymentsIdTraffic(t.Context(), build.DeploymentID, 100)
		mustProjectWorkerNative(t, err)
	}
	waitProjectWorkerNative(t, 45*time.Minute, "elapsed retention and drained old work", func() bool {
		current, readErr := f.owner.GetProjectSubmission(t.Context(), attempt)
		mustProjectWorkerNative(t, readErr)
		contract := current.Apply.SafeRelease.ReleaseCommand.Contract
		if contract == nil || contract.TaskID != "" {
			t.Fatal("cleanup started without explicit admission")
		}
		return contract.Status == "ready"
	})
	f.assertHTTP(t, "progressive", "progressive-config")
	// Freeze the scheduler so acceptance and a genuinely lost response can be
	// replayed before any execution, across an APID restart.
	mustProjectWorkerNative(t, f.h.KillSchedd())
	t.Cleanup(func() {
		if err := f.h.RestartSchedd(); err != nil {
			t.Errorf("restore scheduler: %v", err)
		}
	})
	transport := f.owner.HTTPClient().Transport
	drop := &contractLostResponseTransport{base: transport}
	f.owner.HTTPClient().Transport = drop
	_, err = f.owner.ControlProjectRelease(t.Context(), attempt, "contract", req)
	f.owner.HTTPClient().Transport = transport
	if err == nil || !drop.dropped {
		t.Fatalf("response loss did not follow successful durable admission: dropped=%t error=%v", drop.dropped, err)
	}
	admitted, err := f.owner.GetProjectSubmission(t.Context(), attempt)
	mustProjectWorkerNative(t, err)
	taskID := admitted.Apply.SafeRelease.ReleaseCommand.Contract.TaskID
	if taskID == "" {
		t.Fatal("admission has no durable task")
	}
	mustProjectWorkerNative(t, f.h.RestartAPID())
	replay, err := f.owner.ControlProjectRelease(t.Context(), attempt, "contract", req)
	mustProjectWorkerNative(t, err)
	if replay.Apply.SafeRelease.ReleaseCommand.Contract.TaskID != taskID {
		t.Fatal("lost response created another task")
	}
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	if mode == "interrupted" {
		waitProjectWorkerNative(t, 2*time.Minute, "running cleanup before owner crash", func() bool {
			task, readErr := f.owner.GetAppTask(t.Context(), command.Slug, taskID)
			mustProjectWorkerNative(t, readErr)
			return task.Status == api.AppTaskStatusRunning
		})
		if f.sql != nil {
			f.sql.awaitHeldTransaction(t)
		}
		// SIGKILL discards the owner before an exit receipt can be committed. Real
		// lease expiry and the new owner's sweeper must fence this execution forever.
		mustProjectWorkerNative(t, f.h.KillSchedd())
		time.Sleep(35 * time.Second)
		mustProjectWorkerNative(t, f.h.RestartSchedd())
	}
	want := map[string]string{"success": "succeeded", "failure": "failed", "interrupted": "uncertain"}[mode]
	waitProjectWorkerNative(t, 4*time.Minute, "terminal cleanup outcome", func() bool {
		current, readErr := f.owner.GetProjectSubmission(t.Context(), attempt)
		mustProjectWorkerNative(t, readErr)
		return current.Apply.SafeRelease.ReleaseCommand.Contract.Status == want
	})
	task, err := f.owner.GetAppTask(t.Context(), command.Slug, taskID)
	mustProjectWorkerNative(t, err)
	if task.AttemptCount != 1 || task.RetryMax != 0 {
		t.Fatalf("cleanup replayed: %+v", task)
	}
	if mode != "interrupted" {
		if !strings.Contains(task.StdoutTail, "native contract started generation=progressive mode=progressive-config") {
			t.Fatalf("wrong cleanup runtime: %+v", task)
		}
		exit := 0
		if mode == "failure" {
			exit = 23
		}
		if task.ExitCode == nil || *task.ExitCode != exit {
			t.Fatalf("guest exit was not preserved: %+v", task)
		}
	} else if task.ExitCode != nil {
		t.Fatalf("lost execution claimed a known exit: %+v", task)
	}
	if f.sql != nil {
		f.sql.afterOutcome(t, f, mode)
	}
	other := req
	other.RequestID = strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = f.owner.ControlProjectRelease(t.Context(), attempt, "contract", other); err == nil {
		t.Fatal("second cleanup admitted after terminal outcome")
	}
	old := map[string]string{}
	for _, member := range f.baseline.Members {
		for _, app := range f.apps {
			if app.ID == member.AppID {
				old[app.Slug] = member.DeploymentID
			}
		}
	}
	if _, err = f.owner.PublishProjectReleaseSet(t.Context(), f.project.Slug, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: old}); err == nil {
		t.Fatal("retired graph was republished")
	}
	// Exact old revision requests traverse the real gateway admission path.
	for _, member := range f.baseline.Members {
		app := f.apps["frontend"]
		if member.AppID != app.ID {
			continue
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.h.EdgeURL()+"/", nil)
		mustProjectWorkerNative(t, err)
		request.Host = app.Slug + ".apps.test.example"
		request.Header.Set(api.RevisionHeader, member.DeploymentID)
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		mustProjectWorkerNative(t, err)
		_ = response.Body.Close()
		if response.StatusCode < 400 || response.StatusCode >= 500 {
			t.Fatalf("retired revision response=%d", response.StatusCode)
		}
		instances, err := f.store.ListInstancesForApp(t.Context(), app.ID)
		mustProjectWorkerNative(t, err)
		for _, instance := range instances {
			if instance.DeploymentID == member.DeploymentID && instance.State != string(state.StateParked) && instance.State != string(state.StateStopped) && instance.State != string(state.StateFailed) {
				t.Fatalf("retired guest woke: %+v", instance)
			}
		}
	}
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	replay, err = f.owner.ControlProjectRelease(t.Context(), attempt, "contract", req)
	mustProjectWorkerNative(t, err)
	if replay.Apply.SafeRelease.ReleaseCommand.Contract.TaskID != taskID || replay.Apply.SafeRelease.ReleaseCommand.Contract.Status != want {
		t.Fatal("restart changed terminal cleanup receipt")
	}
	t.Logf("contract native: attempt=%s task=%s outcome=%s attempts=1 restart=true retirement=true", attempt, taskID, want)
}

// Drop a successful response only after APID has committed admission. The client
// sees a transport error and cannot use the original task acknowledgement.
type contractLostResponseTransport struct {
	base    http.RoundTripper
	dropped bool
}

func (d *contractLostResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := d.base.RoundTrip(request)
	if err != nil {
		return response, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		d.dropped = true
		return nil, io.ErrUnexpectedEOF
	}
	return response, nil
}
