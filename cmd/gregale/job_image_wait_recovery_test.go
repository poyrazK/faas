package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const jobImageWaitRef = "registry.example/nightly:v1"
const jobImageWaitDigest = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type jobImageWaitDiagnostic struct {
	api.Problem
	ExitCode  int    `json:"exit_code"`
	Category  string `json:"category"`
	RequestID string `json:"request_id"`
	Recovery  struct {
		Job, Phase          string
		Submitted           *bool  `json:"submitted"`
		JobID               string `json:"job_id"`
		ImageRef            string `json:"image_ref"`
		ImageResolvedDigest string `json:"image_resolved_digest"`
		InspectCommand      string `json:"inspect_command"`
		ResumeCommand       string `json:"resume_command"`
	} `json:"recovery"`
}

type jobImageWaitAPI struct {
	t                    *testing.T
	mode                 string
	initialFailure       bool
	headers              []string
	reads, posts, other  atomic.Int32
	polled, canceled     chan struct{}
	polledOnce, doneOnce sync.Once
}

func newJobImageWaitAPI(t *testing.T, mode string) (*jobImageWaitAPI, *httptest.Server) {
	t.Helper()
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("FAAS_JSON", "0")
	t.Setenv("FAAS_TOKEN", "fp_test_image-wait-private")
	f := &jobImageWaitAPI{t: t, mode: mode, headers: []string{jobWaitRequestID}, polled: make(chan struct{}), canceled: make(chan struct{})}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	return f, server
}

func (f *jobImageWaitAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	if r.Header.Get("Authorization") != "Bearer fp_test_image-wait-private" {
		f.t.Error("image wait changed credentials")
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs/nightly/runs" {
		f.posts.Add(1)
		if f.mode != "ready" && f.mode != "ready-initial" {
			f.t.Error("image wait submitted before readiness")
		}
		_ = json.NewEncoder(w).Encode(jobWaitTestRun("queued"))
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs/nightly" {
		f.other.Add(1)
		f.t.Errorf("image wait changed work or target: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	n := f.reads.Add(1)
	job := api.JobResponse{ID: jobWaitJobID, AccountID: "job-wait-account", Name: "nightly", Status: "active",
		ImageRef: jobImageWaitRef, ImageResolvedDigest: jobImageWaitDigest, ImageMaterializationStatus: "pending"}
	if f.mode == "digest-discovered" && n == 1 {
		job.ImageResolvedDigest = ""
	}
	if f.mode == "invalid-initial" {
		job.Name = "another-job"
	}
	if n > 1 || f.initialFailure {
		switch f.mode {
		case "block":
			f.polledOnce.Do(func() { close(f.polled) })
			<-r.Context().Done()
			f.doneOnce.Do(func() { close(f.canceled) })
			return
		case "transport":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				f.t.Error(err)
				return
			}
			_ = conn.Close()
			return
		case "malformed":
			_, _ = io.WriteString(w, `{"id":`)
			return
		case "unauthorized", "unavailable":
			status, code := 503, "service_unavailable"
			if f.mode == "unauthorized" {
				status, code = 401, "unauthorized"
			}
			for _, id := range f.headers {
				w.Header().Add("X-Faas-Request-Id", id)
			}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: status, Code: code, Title: "Read failed",
				Detail: "image detail fp_test_image-wait-private", Hint: "registry remediation fp_test_image-wait-private"})
			return
		case "changed-id":
			job.ID = "33333333-3333-4333-8333-333333333333"
		case "changed-name":
			job.Name = "another-job"
		case "changed-account":
			job.AccountID = "another-account"
		case "changed-ref":
			job.ImageRef = "registry.example/unselected:v2"
		case "changed-digest":
			job.ImageResolvedDigest = "sha256:changed"
		case "digest-discovered":
			if n > 2 {
				job.ImageResolvedDigest = "sha256:changed"
			}
		case "ready":
			job.ImageMaterializationStatus = "ready"
		case "failed":
			job.ImageMaterializationStatus = "failed"
			job.ImageMaterializationError = "registry refused fp_test_image-wait-private"
		case "unknown":
			job.ImageMaterializationStatus = "unknown"
		}
	}
	if f.mode == "ready-initial" {
		job.ImageMaterializationStatus = "ready"
	}
	_ = json.NewEncoder(w).Encode(job)
}

func jobImageWaitArgs(beforeSubmission bool, timeout, interval string) []string {
	args := []string{"--profile", "staging", "--json", "job"}
	if beforeSubmission {
		return append(args, "run", "nightly", "--tasks", "1", "--wait-ready", "--image-wait-timeout", timeout, "--image-poll-interval", interval)
	}
	return append(args, "get", "nightly", "--wait-ready", "--timeout", timeout, "--poll-interval", interval)
}

func assertJobImageWaitError(t *testing.T, beforeSubmission bool, stdout, stderr string, code int) jobImageWaitDiagnostic {
	t.Helper()
	var problem jobImageWaitDiagnostic
	decodeSingleJobWaitJSON(t, stderr, &problem)
	if problem.ExitCode != code || problem.Category == "" || problem.Recovery.Job != "nightly" || problem.Recovery.Phase != "image_wait" ||
		problem.Recovery.Submitted == nil || *problem.Recovery.Submitted || problem.Recovery.InspectCommand != "gregale --profile staging job get nightly" {
		t.Fatalf("lost image wait recovery: %s", stderr)
	}
	var receipt map[string]json.RawMessage
	decodeSingleJobWaitJSON(t, stdout, &receipt)
	wantFields := 4
	if beforeSubmission {
		wantFields = 6
		if string(receipt["phase"]) != `"image_wait"` || string(receipt["submitted"]) != "false" || string(receipt["job"]) != `"nightly"` || receipt["inspect_command"] == nil {
			t.Fatalf("lost pre-submission receipt: %s", stdout)
		}
	} else {
		var job api.JobResponse
		decodeSingleJobWaitJSON(t, string(receipt["job"]), &job)
		if job.ID != jobWaitJobID || job.Name != "nightly" || job.AccountID != "job-wait-account" || job.ImageRef != jobImageWaitRef {
			t.Fatalf("receipt switched to an unselected image: %s", stdout)
		}
		if receipt["resume_command"] == nil {
			t.Fatalf("lost legacy resume field: %s", stdout)
		}
	}
	if len(receipt) != wantFields || receipt["exit_code"] == nil || receipt["error"] == nil || strings.Contains(stdout+stderr, "fp_test_image-wait-private") {
		t.Fatalf("receipt shape changed or error leaked credentials: stdout=%s stderr=%s", stdout, stderr)
	}
	return problem
}

func TestJobImageWaitRecoveryFailures(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, tc := range []struct{ mode, code string }{
			{"unauthorized", "unauthorized"}, {"unavailable", "service_unavailable"}, {"transport", "transport_error"},
			{"malformed", "invalid_request"}, {"failed", "job_image_preparation_failed"}, {"unknown", "invalid_request"},
			{"changed-id", "job_image_changed"}, {"changed-name", "job_image_changed"}, {"changed-account", "job_image_changed"},
			{"changed-ref", "job_image_changed"}, {"changed-digest", "job_image_changed"}, {"digest-discovered", "job_image_changed"},
		} {
			t.Run(tc.mode+"/before-submission="+map[bool]string{false: "false", true: "true"}[before], func(t *testing.T) {
				f, _ := newJobImageWaitAPI(t, tc.mode)
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(jobImageWaitArgs(before, "1m", "1ms"))
				problem := assertJobImageWaitError(t, before, out.String(), stderr(), 1)
				if code != 1 || problem.Code != tc.code || f.posts.Load() != 0 || f.other.Load() != 0 || problem.Recovery.JobID != jobWaitJobID ||
					problem.Recovery.ImageRef != jobImageWaitRef || problem.Recovery.ImageResolvedDigest != jobImageWaitDigest {
					t.Fatalf("wrong cause or selected image: exit=%d stdout=%s stderr=%s", code, out, stderr())
				}
				if tc.mode == "unauthorized" || tc.mode == "unavailable" {
					wantStatus := map[string]int{"unauthorized": 401, "unavailable": 503}[tc.mode]
					if problem.Status != wantStatus || problem.RequestID != jobWaitRequestID || !strings.Contains(problem.Hint, "registry remediation") {
						t.Fatalf("API cause lost correlation or remediation: %s", stderr())
					}
				}
				if tc.code == "job_image_changed" && problem.Recovery.ResumeCommand != "" || tc.mode == "failed" && problem.Recovery.ResumeCommand != "" {
					t.Fatalf("changed/failed selection offered automatic resumption: %s", stderr())
				}
			})
		}
	}
}

func TestJobImageWaitRecoveryDeadlines(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, requestTimeout := range []bool{false, true} {
			t.Run(map[bool]string{false: "overall", true: "request"}[requestTimeout]+map[bool]string{false: "/get", true: "/run"}[before], func(t *testing.T) {
				mode, timeout, interval, want, cause := "pending", "250ms", "1h", 124, "job_image_wait_timeout"
				if requestTimeout {
					mode, timeout, interval, want, cause = "block", "1m", "1ms", 1, "transport_error"
				}
				f, _ := newJobImageWaitAPI(t, mode)
				args := jobImageWaitArgs(before, timeout, interval)
				if requestTimeout {
					args = append([]string{"--http-timeout", "250ms"}, args...)
				}
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(args)
				problem := assertJobImageWaitError(t, before, out.String(), stderr(), want)
				timeoutValue, _ := time.ParseDuration(timeout)
				intervalValue, _ := time.ParseDuration(interval)
				if code != want || problem.Code != cause || f.posts.Load() != 0 || f.other.Load() != 0 ||
					problem.Recovery.ResumeCommand != "gregale --profile staging job get nightly --wait-ready --timeout "+timeoutValue.String()+" --poll-interval "+intervalValue.String() {
					t.Fatalf("deadline lost stage or timing: exit=%d stdout=%s stderr=%s", code, out, stderr())
				}
			})
		}
	}
}

func TestJobImageWaitRecoveryUnverifiedSelection(t *testing.T) {
	for _, tc := range []struct {
		mode string
		code int
	}{{"unauthorized", 2}, {"unavailable", 3}, {"transport", 3}, {"malformed", 1}, {"invalid-initial", 1}} {
		for _, before := range []bool{false, true} {
			t.Run(tc.mode+map[bool]string{false: "/get", true: "/run"}[before], func(t *testing.T) {
				f, _ := newJobImageWaitAPI(t, tc.mode)
				f.initialFailure = true
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				want := tc.code
				if before {
					want = 1
				}
				code := main.Run(jobImageWaitArgs(before, "1m", "1ms"))
				var problem jobImageWaitDiagnostic
				if before {
					problem = assertJobImageWaitError(t, true, out.String(), stderr(), want)
				} else {
					decodeSingleJobWaitJSON(t, stderr(), &problem)
					if out.Len() != 0 || problem.Recovery.Phase != "image_wait" || problem.Recovery.Job != "nightly" || problem.Recovery.Submitted == nil || *problem.Recovery.Submitted ||
						problem.Recovery.InspectCommand != "gregale --profile staging job get nightly" {
						t.Fatalf("initial standalone read changed its receipt: stdout=%s stderr=%s", out, stderr())
					}
				}
				if code != want || problem.ExitCode != want || f.posts.Load() != 0 || f.reads.Load() != 1 || f.other.Load() != 0 || problem.Recovery.JobID != "" || problem.Recovery.ImageRef != "" || problem.Recovery.ResumeCommand != "" {
					t.Fatalf("invented an unverified image selection: exit=%d stderr=%s", code, stderr())
				}
			})
		}
	}
}

func TestJobImageWaitRecoveryReady(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, mode := range []string{"ready", "ready-initial"} {
			t.Run(mode+map[bool]string{false: "/get", true: "/run"}[before], func(t *testing.T) {
				f, _ := newJobImageWaitAPI(t, mode)
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(jobImageWaitArgs(before, "1m", "1ms"))
				var receipt map[string]json.RawMessage
				decodeSingleJobWaitJSON(t, out.String(), &receipt)
				if code != 0 || stderr() != "" || f.posts.Load() != map[bool]int32{false: 0, true: 1}[before] || f.other.Load() != 0 {
					t.Fatalf("readiness changed submission: exit=%d stdout=%s stderr=%s", code, out, stderr())
				}
				if !before && (len(receipt) != 3 || receipt["error"] != nil) || before && string(receipt["id"]) != `"`+jobWaitRunID+`"` {
					t.Fatalf("successful receipt changed: %s", out)
				}
			})
		}
	}
}

func TestJobImageWaitRecoveryInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real process interrupt delivery requires POSIX signals")
	}
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "get", true: "run"}[before], func(t *testing.T) {
			f, server := newJobImageWaitAPI(t, "block")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			args, _ := json.Marshal(jobImageWaitArgs(before, "1m", "1ms"))
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJobWaitRecoverySignalProcess$")
			cmd.Env = append(os.Environ(), "GREGALE_JOB_WAIT_HELPER=1", "GREGALE_JOB_WAIT_API="+server.URL,
				"GREGALE_JOB_WAIT_TOKEN=fp_test_image-wait-private", "GREGALE_JOB_WAIT_ARGS="+string(args))
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-f.polled:
			case err := <-done:
				t.Fatalf("child stopped before image polling: %v stderr=%s", err, &stderr)
			case <-ctx.Done():
				t.Fatal("child did not poll the selected image")
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			var exited *exec.ExitError
			if err := <-done; !errors.As(err, &exited) || exited.ExitCode() != 130 {
				t.Fatalf("interrupt changed exit: %v stdout=%s stderr=%s", err, &out, &stderr)
			}
			problem := assertJobImageWaitError(t, before, out.String(), stderr.String(), 130)
			if problem.Code != "job_image_wait_interrupted" || f.posts.Load() != 0 || f.other.Load() != 0 {
				t.Fatalf("interrupt submitted work: stdout=%s stderr=%s", &out, &stderr)
			}
			select {
			case <-f.canceled:
			case <-ctx.Done():
				t.Fatal("interrupt left the image read running")
			}
		})
	}
}
