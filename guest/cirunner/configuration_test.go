package cirunner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/ci"
	"golang.org/x/sys/unix"
)

func configurationRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func environmentValues(t *testing.T, values []string) map[string]string {
	t.Helper()
	got := make(map[string]string)
	for _, value := range values {
		name, text, ok := strings.Cut(value, "=")
		if _, exists := got[name]; exists || !ok {
			t.Fatal("malformed or duplicate step environment")
		}
		got[name] = text
	}
	return got
}

func TestStepConfigurationDefaultsOverridesAndIsolation(t *testing.T) {
	t.Setenv("CI_HOST_ONLY", "must not enter step")
	t.Setenv("GREGALE_CI_CAPABILITY", "must not enter step")
	root := configurationRoot(t)
	if err := root.MkdirAll("packages/api", 0o700); err != nil {
		t.Fatal(err)
	}
	job := ci.Job{Env: map[string]string{"MODE": "job", "PERSIST": "yes"}, WorkingDirectory: "packages/api"}
	step := ci.Step{Name: "test", Run: "synthetic", Env: map[string]string{"MODE": "", "LITERAL": "$PERSIST\nsecond line"}}
	launch, err := prepareStep(root, job, step)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = launch.Directory.Close() }()
	env := environmentValues(t, launch.Environment)
	if env["MODE"] != "" || env["PERSIST"] != "yes" || env["LITERAL"] != "$PERSIST\nsecond line" || env["HOME"] != root.Name() || env["GREGALE_CI_WORKSPACE"] != root.Name() || env["CI"] != "true" || env["LANG"] != "C.UTF-8" || env["PATH"] == "" || len(env) != 8 {
		t.Fatal("step environment lost explicit values or inherited host values")
	}
	if launch.Directory.Name() != filepath.Join(root.Name(), "packages/api") {
		t.Fatal("job working directory was ignored")
	}
	launch.Environment[0], launch.Step.Env["MODE"] = "MUTATED=value", "changed"
	again, err := prepareStep(root, job, ci.Step{Name: "next", WorkingDirectory: "."})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Directory.Close() }()
	next := environmentValues(t, again.Environment)
	if next["MODE"] != "job" || next["LITERAL"] != "" || step.Env["MODE"] != "" {
		t.Fatal("step environment mutation leaked to a later step or manifest")
	}
	info, err := again.Directory.Stat()
	rootInfo, rootErr := root.Stat(".")
	if err != nil || rootErr != nil || !os.SameFile(info, rootInfo) {
		t.Fatal("explicit dot did not override job directory")
	}
}

func TestStepWorkingDirectoryRejectsLinksAndSpecialFiles(t *testing.T) {
	root := configurationRoot(t)
	outside := t.TempDir()
	if err := root.MkdirAll("packages/api", 0o700); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"escape": outside, "contained": "packages", "packages/escape": outside} {
		if err := root.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := root.WriteFile("regular", []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root.Name(), "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "regular", "fifo", "escape", "escape/child", "contained/api", "packages/escape", "../outside", "packages/../api", "/tmp", "packages//api"} {
		file, err := openWorkingDirectory(root, name)
		if file != nil {
			_ = file.Close()
		}
		if err == nil {
			t.Errorf("unsafe working directory accepted: %q", name)
		}
	}
	if _, err := root.Stat("missing"); !os.IsNotExist(err) {
		t.Fatal("runner created missing directory")
	}
}

func TestStepWorkingDirectoryDescriptorSurvivesReplacement(t *testing.T) {
	root := configurationRoot(t)
	if err := root.MkdirAll("packages/api", 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := openWorkingDirectory(root, "packages/api")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	want, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("packages", "previous"); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink(t.TempDir(), "packages"); err != nil {
		t.Fatal(err)
	}
	got, err := file.Stat()
	if err != nil || !os.SameFile(want, got) {
		t.Fatal("directory descriptor followed replacement")
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("checkout descriptor would leak into workload")
	}
	if next, err := openWorkingDirectory(root, "packages/api"); err == nil {
		_ = next.Close()
		t.Fatal("next step followed replaced path")
	}
}

type configurationExecutor struct {
	calls     int
	root      *os.Root
	directory *os.File
}

func (e *configurationExecutor) Run(ctx context.Context, launch StepExecution, logs io.Writer) (int, error) {
	e.calls++
	e.directory = launch.Directory
	if launch.Step.Name == "setup" {
		return 0, e.root.MkdirAll("generated/tests", 0o700)
	}
	return 0, nil
}

func TestStepConfigurationRuntimeFailureAndCancellation(t *testing.T) {
	root := configurationRoot(t)
	executor := &configurationExecutor{root: root}
	job := ci.Job{WorkingDirectory: "generated/tests"}
	runner := Runner{Manifest: ci.JobsManifest{Job: ci.PlannedJob{Job: job}}, Executor: executor}
	var logs bytes.Buffer
	if code, err := runner.runStep(t.Context(), root, ci.Step{Name: "missing"}, &logs); code != 125 || err == nil || executor.calls != 0 {
		t.Fatal("missing directory started a command")
	}
	if code, err := runner.runStep(t.Context(), root, ci.Step{Name: "setup", WorkingDirectory: "."}, &logs); code != 0 || err != nil {
		t.Fatal("setup step could not create later directory")
	}
	if code, err := runner.runStep(t.Context(), root, ci.Step{Name: "test"}, &logs); code != 0 || err != nil || executor.calls != 2 {
		t.Fatal("later step could not use generated directory")
	}
	if _, err := executor.directory.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("step directory descriptor was not closed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if code, err := runner.runStep(ctx, root, ci.Step{Name: "cancelled"}, &logs); code != 143 || err == nil || executor.calls != 2 {
		t.Fatal("cancelled step reached executor")
	}
	if !reflect.DeepEqual(logs.Bytes(), []byte("CI step configuration could not be prepared\n")) {
		t.Fatal("configuration failure diagnostic changed")
	}
}
