package ciruntime_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/guest/cirunner"
	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
	"github.com/onebox-faas/faas/pkg/ci"
)

type configuredExecutor struct {
	calls   int
	mode    string
	outside string
	cancel  context.CancelFunc
	input   bool
}

func (e *configuredExecutor) Run(ctx context.Context, launch cirunner.StepExecution, logs io.Writer) (int, error) {
	e.calls++
	env := make(map[string]string)
	for _, item := range launch.Environment {
		name, value, _ := strings.Cut(item, "=")
		env[name] = value
	}
	work := env["GREGALE_CI_WORKSPACE"]
	if work == "" || env["HOME"] != work || env["CI"] != "true" || env["GREGALE_CI_CAPABILITY"] != "" || env["CI_HOST_ONLY"] != "" {
		return 125, errors.New("step environment is not isolated")
	}
	if launch.Step.Name == "setup" {
		if env["MODE"] != "setup" || filepath.Clean(launch.Directory.Name()) != work {
			return 125, errors.New("setup overrides were lost")
		}
		if e.mode == "cancel" {
			e.cancel()
			return 143, ctx.Err()
		}
		if e.mode == "symlink" {
			return 0, os.Symlink(e.outside, filepath.Join(work, "packages"))
		}
		if e.mode == "missing" {
			return 0, nil
		}
		return 0, os.MkdirAll(filepath.Join(work, "packages", "api"), 0o700)
	}
	if launch.Directory.Name() != filepath.Join(work, "packages", "api") {
		return 125, errors.New("working directory was lost")
	}
	if launch.Step.Name == "test" {
		if value, ok := env["EMPTY"]; !ok || value != "" || env["MODE"] != "step" || env["LITERAL"] != "$MODE\nliteral" {
			return 125, errors.New("step values were expanded or ignored")
		}
		if e.input {
			raw, err := os.ReadFile(filepath.Join(work, ci.ArtifactInputsDirectory, "report"))
			if err != nil || string(raw) != "synthetic test report" {
				return 125, errors.New("input path was made relative to step")
			}
		}
		if err := os.Mkdir(filepath.Join(work, "reports"), 0o700); err != nil {
			return 125, err
		}
		if err := os.WriteFile(filepath.Join(work, "reports", "unit.xml"), []byte("configured report"), 0o600); err != nil {
			return 125, err
		}
	} else if env["MODE"] != "job" || env["LITERAL"] != "" || env["EMPTY"] != "job" {
		return 125, errors.New("step configuration leaked into later step")
	}
	_, _ = logs.Write([]byte("configured step\n"))
	return 0, nil
}

func configureJob(job *ci.Job) {
	job.Env, job.WorkingDirectory = map[string]string{"MODE": "job", "EMPTY": "job"}, "packages/api"
	job.Steps = []ci.Step{
		{Name: "setup", Run: "synthetic setup", WorkingDirectory: ".", Env: map[string]string{"MODE": "setup"}},
		{Name: "test", Run: "synthetic test", Env: map[string]string{"MODE": "step", "EMPTY": "", "LITERAL": "$MODE\nliteral"}},
		{Name: "verify", Run: "synthetic verify"},
	}
	job.Artifacts = []string{"reports/**"}
}

func TestCIConfigurationHTTPSRunnerAndDirectoryFailures(t *testing.T) {
	t.Setenv("CI_HOST_ONLY", "fixture host value")
	for _, mode := range []string{"success", "missing", "symlink", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			job := ci.Job{Image: "registry.example/ci@sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60}
			configureJob(&job)
			def := ci.Definition{Version: 1, Repository: "gregale/runtime", Jobs: map[string]ci.Job{"test": job}}
			h := fixtureDefinition(t, &def)
			if h.view.Manifest.Version != 3 {
				t.Fatal("configured job did not require new runner")
			}
			// A fresh API/controller instance reconstructs the same immutable command.
			h.store = ci.NewPostgresStore(h.pool)
			h.backend.Store = h.store
			h.backend.Output = ci.SealedJobsOutput{Store: h.store, Blobs: h.objects}
			h.server.Close()
			h.server = httptest.NewTLSServer((ciruntime.Handler{Config: h.cfg, Store: h.store, Blobs: h.objects}).Routes())
			t.Cleanup(h.server.Close)
			transport, err := cirunner.NewTransport(h.server.URL, h.proof, h.server.Client())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			executor := &configuredExecutor{mode: mode, outside: t.TempDir(), cancel: func() {
				if _, err := h.store.CancelExecution(t.Context(), h.account, h.row.ID); err != nil {
					t.Fatal(err)
				}
				cancel()
			}}
			result, err := (cirunner.Runner{Manifest: h.view.Manifest, Transport: transport, Executor: executor, WorkParent: t.TempDir()}).Run(ctx)
			want, code := ci.StatusSucceeded, 0
			if mode == "missing" || mode == "symlink" {
				want, code = ci.StatusFailed, 125
			}
			if mode == "cancel" {
				want, code = ci.StatusCancelled, 143
			}
			if err != nil || result.Status != want || result.ExitCode == nil || *result.ExitCode != code {
				t.Fatalf("configured %s: status=%s err=%v", mode, result.Status, err)
			}
			if mode == "success" {
				if executor.calls != 3 || len(result.Artifacts) != 1 || result.Artifacts[0].Path != "reports/unit.xml" {
					t.Fatal("configured pipeline lost root-relative artifacts")
				}
			} else if executor.calls != 1 || result.Steps[2].Status != ci.StatusSkipped {
				t.Fatal("failed configuration or cancellation started a later step")
			}
			entries, err := os.ReadDir(executor.outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("unsafe directory wrote outside checkout")
			}
			h.tick(t)
			if h.row.Status != ci.StatusRunning && !(mode == "cancel" && h.row.Status == ci.StatusCancelling) {
				t.Fatal("guest configuration certified native stop")
			}
			h.nativeExit(t, want, code, true)
			for range 3 {
				h.tick(t)
			}
			if h.row.Status != want {
				t.Fatalf("native result changed configured outcome: %s", h.row.Status)
			}
		})
	}
}

func TestCIConfigurationWithDependencyInputs(t *testing.T) {
	h := consumerFixtureJob(t, configureJob)
	if h.view.Manifest.Version != 3 || len(h.view.Manifest.Inputs) != 1 {
		t.Fatal("configuration lost dependency input versioning")
	}
	transport, err := cirunner.NewTransport(h.server.URL, h.proof, h.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	executor := &configuredExecutor{input: true}
	result, err := (cirunner.Runner{Manifest: h.view.Manifest, Transport: transport, Executor: executor, WorkParent: t.TempDir()}).Run(t.Context())
	if err != nil || result.Status != ci.StatusSucceeded || executor.calls != 3 || len(result.Artifacts) != 1 {
		t.Fatalf("configured input consumer: %s calls=%d err=%v", result.Status, executor.calls, err)
	}
}
