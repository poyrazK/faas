//go:build linux

package cirunner

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/ci"

	"golang.org/x/sys/unix"
)

func TestGuestCommandUsesPinnedDirectoryAndExplicitEnvironment(t *testing.T) {
	root := configurationRoot(t)
	launch, err := prepareStep(root, ci.Job{Env: map[string]string{"MODE": "test"}}, ci.Step{Name: "test", Run: "synthetic only; never started"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = launch.Directory.Close() }()
	command := guestCommand(launch, io.Discard)
	if command.Dir != "/proc/self/fd/"+strconv.FormatUint(uint64(launch.Directory.Fd()), 10) || len(command.ExtraFiles) != 0 || !reflect.DeepEqual(command.Env, launch.Environment) || !reflect.DeepEqual(command.Args, []string{"/bin/sh", "-e", "-c", launch.Step.Run}) {
		t.Fatal("guest command did not use the prepared configuration")
	}
	launch.Environment[0] = "CHANGED=value"
	if reflect.DeepEqual(command.Env, launch.Environment) {
		t.Fatal("guest command aliases environment slice")
	}
}

func TestRunnerCannotExecuteOnHost(t *testing.T) {
	t.Setenv("FAAS_JOB", "0")
	if code, err := RunGuest(t.Context(), []string{"v1", "invalid"}); err == nil || code != 125 {
		t.Fatal("guest entrypoint accepted host invocation")
	}
}

func TestGuestArtifactOpenRejectsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile("regular", []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("regular", "link"); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir("reports", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("reports", "linked-reports"); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("reports/result", []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openArtifact(root, "regular")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	for _, name := range []string{"link", "linked-reports/result", "fifo", ".", "../regular"} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				file, err := openArtifact(root, name)
				if file != nil {
					_ = file.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe artifact accepted")
				}
			case <-time.After(time.Second):
				// Unblock a regressed FIFO opener before reporting the failure.
				fd, err := unix.Open(fifo, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
				if err == nil {
					_ = unix.Close(fd)
				}
				t.Fatal("artifact open blocked finalization")
			}
		})
	}
}
