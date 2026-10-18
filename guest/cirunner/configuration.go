package cirunner

import (
	"context"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/ci"
	"golang.org/x/sys/unix"
)

func (r Runner) runStep(ctx context.Context, root *os.Root, step ci.Step, logs io.Writer) (int, error) {
	launch, err := prepareStep(root, r.Manifest.Job.Job, step)
	if err != nil {
		_, _ = logs.Write([]byte("CI step configuration could not be prepared\n"))
		return 125, err
	}
	defer func() { _ = launch.Directory.Close() }()
	if err := ctx.Err(); err != nil {
		return 143, err
	}
	return r.Executor.Run(ctx, launch, logs)
}

func prepareStep(root *os.Root, job ci.Job, step ci.Step) (StepExecution, error) {
	relative := step.WorkingDirectory
	if relative == "" {
		relative = job.WorkingDirectory
	}
	directory, err := openWorkingDirectory(root, relative)
	if err != nil {
		return StepExecution{}, err
	}
	env := map[string]string{
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME": root.Name(), "LANG": "C.UTF-8", "CI": "true",
		"GREGALE_CI_WORKSPACE": root.Name(),
	}
	maps.Copy(env, job.Env)
	maps.Copy(env, step.Env)
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]string, 0, len(env))
	for _, name := range names {
		values = append(values, name+"="+env[name])
	}
	step.Env = maps.Clone(step.Env)
	return StepExecution{Step: step, Directory: directory, Environment: values}, nil
}

// Anchor every component to an already-open checkout directory. No-follow,
// directory-only opens reject links, regular files and FIFOs without blocking;
// the returned descriptor survives pathname replacement before command start.
func openWorkingDirectory(root *os.Root, relative string) (*os.File, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, errTransfer
	}
	if relative == "" || relative == "." {
		return directory, nil
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
			_ = directory.Close()
			return nil, errTransfer
		}
		fd, err := unix.Openat(int(directory.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = directory.Close()
		if err != nil {
			return nil, errTransfer
		}
		directory = os.NewFile(uintptr(fd), filepath.Join(root.Name(), relative))
	}
	return directory, nil
}
