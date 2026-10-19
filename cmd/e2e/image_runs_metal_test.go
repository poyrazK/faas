//go:build metal

package e2e

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// adr: 1790
// TestImageRunProcessMetal checks `docker run` process semantics on a real
// VM: an image run takes the image's USER, WORKDIR (created when missing),
// ENV and PATH (the CMD is a bare command name), and -u/-w override the user
// and directory.
func TestImageRunProcessMetal(t *testing.T) {
	h := newMetalHarness(t, "FAAS_IMAGE_RUNS=1")
	defer h.Close()
	h.mustSeedProcessImage(t, "busybox:image-process")
	image := h.imageRef(t, "busybox:image-process")

	defaults := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: image}, 5*time.Minute)
	h.mustFinishImageRun(t, defaults, "succeeded", 0, "uid=1234 gid=1234 cwd=/srv/app home=/home/app from_image=yes")

	overridden := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: image, User: "0", Workdir: "/tmp"}, time.Minute)
	h.mustFinishImageRun(t, overridden, "succeeded", 0, "uid=0 gid=0 cwd=/tmp home=/root from_image=yes")
}

// mustSeedProcessImage seeds e2etest.ProcessJobImage under imageRef.
func (h *MetalJobHarness) mustSeedProcessImage(t *testing.T, imageRef string) {
	t.Helper()
	repo, _, _ := strings.Cut(imageRef, ":")
	img, _ := e2etest.ProcessJobImage(repo)
	pinned := h.registry.AddImage(repo, img)
	h.mu.Lock()
	h.imageRefs[imageRef] = pinned
	h.mu.Unlock()
}

// adr: 1161
// TestImageRunsMetal drives `gregale run --image` end to end on real
// apid/imaged/schedd/vmmd: the first run pulls and caches the image and runs
// its own CMD; a second run of the same image reuses the cached artifact and
// replaces CMD with arguments, and its exit code reaches the task row.
func TestImageRunsMetal(t *testing.T) {
	h := newMetalHarness(t, "FAAS_IMAGE_RUNS=1")
	defer h.Close()
	h.MustSeedFakeImage(t, "busybox:image-run")
	image := h.imageRef(t, "busybox:image-run")

	first := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: image}, 5*time.Minute)
	if want := []string{"/job-fixture", "success"}; !reflect.DeepEqual(first.Command, want) {
		t.Fatalf("default command = %q, want the image CMD %q", first.Command, want)
	}
	h.mustFinishImageRun(t, first, "succeeded", 0, "job fixture succeeded")

	args := []string{"/job-fixture", "fail"}
	raw, status, _ := h.request(t, h.defaultKey, http.MethodPost, "/v1/image-runs", api.CreateImageRunRequest{Image: image, Args: &args})
	var second api.ImageRunResponse
	decodeOK(t, raw, status, &second, http.MethodPost, "/v1/image-runs")
	if status != http.StatusCreated || second.Job != first.Job || second.ImageDigest != first.ImageDigest {
		t.Fatalf("cached image run = %d %s; want an immediate run on runner %s", status, raw, first.Job)
	}
	h.mustFinishImageRun(t, second, "any-terminal", 1, "job fixture failed as requested")

	raw, status, _ = h.request(t, h.defaultKey, http.MethodGet, "/v1/jobs", nil)
	if status != http.StatusOK || strings.Contains(string(raw), first.Job) {
		t.Fatalf("GET /v1/jobs listed the runner job: %d %s", status, raw)
	}

	// Ctrl-C in `gregale run --image` cancels the run; a running task's VM
	// must stop rather than sleep out its timeout.
	sleep := []string{"/job-fixture", "sleep", "5m"}
	third := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: image, Args: &sleep}, time.Minute)
	h.mustWaitImageRunClaimed(t, third, 2*time.Minute)
	cancelPath := "/v1/jobs/" + third.Job + "/runs/" + third.RunID + "/cancel"
	if raw, status, _ := h.request(t, h.defaultKey, http.MethodPost, cancelPath, nil); status != http.StatusOK {
		t.Fatalf("POST %s: %d %s", cancelPath, status, raw)
	}
	h.MustWaitRunTerminal(t, &api.JobRunResponse{ID: third.RunID}, "cancelled", 2*time.Minute)
}

// mustWaitImageRunClaimed waits until the run's task has been claimed, i.e.
// its VM is booting or running.
func (h *MetalJobHarness) mustWaitImageRunClaimed(t *testing.T, resp api.ImageRunResponse, timeout time.Duration) {
	t.Helper()
	h.mu.Lock()
	h.runNames[resp.RunID] = resp.Job
	h.mu.Unlock()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if task, ok := findTask(h.listTasks(t, &api.JobRunResponse{ID: resp.RunID}), 0); ok && task.Status == "claimed" {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("run %s task was not claimed within %s", resp.RunID, timeout)
}

// mustStartImageRun repeats POST /v1/image-runs until the image is pulled
// and the run is queued.
func (h *MetalJobHarness) mustStartImageRun(t *testing.T, req api.CreateImageRunRequest, timeout time.Duration) api.ImageRunResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, status, _ := h.request(t, h.defaultKey, http.MethodPost, "/v1/image-runs", req)
		var resp api.ImageRunResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("POST /v1/image-runs: %d %s", status, raw)
		}
		switch status {
		case http.StatusCreated:
			return resp
		case http.StatusAccepted:
			if resp.ImageStatus == "failed" {
				t.Fatalf("image pull failed: %s", resp.ImageError)
			}
			time.Sleep(500 * time.Millisecond)
		default:
			t.Fatalf("POST /v1/image-runs: %d %s", status, raw)
		}
	}
	t.Fatalf("image %s was not ready within %s", req.Image, timeout)
	return api.ImageRunResponse{}
}

func (h *MetalJobHarness) mustFinishImageRun(t *testing.T, resp api.ImageRunResponse, want string, exitCode int, output string) {
	t.Helper()
	h.mu.Lock()
	h.runNames[resp.RunID] = resp.Job
	h.mu.Unlock()
	run := &api.JobRunResponse{ID: resp.RunID}
	h.MustWaitRunTerminal(t, run, want, 5*time.Minute)
	h.MustAssertTaskExitCodes(t, run, exitCode)
	path := "/v1/jobs/" + resp.Job + "/runs/" + resp.RunID + "/tasks/0/logs"
	raw, status, _ := h.request(t, h.defaultKey, http.MethodGet, path, nil)
	var logs api.JobTaskLogResponse
	decodeOK(t, raw, status, &logs, http.MethodGet, path)
	if !strings.Contains(logs.LogContent, output) {
		t.Fatalf("run %s output = %q, want %q", resp.RunID, logs.LogContent, output)
	}
}
