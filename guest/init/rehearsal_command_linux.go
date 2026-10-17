//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

// Only guest-init interprets the reserved envelope. The host never constructs
// a shell program from recipe data or mounts a checkout in this VM.
func executeRehearsalCommand(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	spec, err := rehearsalcommand.Decode(req.Command)
	if err != nil || req.Version != apptaskproto.RehearsalVersion || req.CommandShell || req.Interactive || req.MaxOutputBytes < rehearsalcommand.MaxReceiptBytes {
		return appTaskInfraFailure("rehearsal_protocol_error", "invalid contained command envelope", 65), nil
	}
	inner := req
	inner.Command = spec.Command
	commandEnv := make(map[string]string, len(apiEnv)+len(spec.Env))
	for k, v := range apiEnv {
		commandEnv[k] = v
	}
	for k, v := range spec.Env {
		commandEnv[k] = v
	}
	cmd, credential, failure := prepareAppTaskCommand(inner, manifest, secrets, commandEnv)
	if failure != nil {
		return *failure, nil
	}
	if err := installRehearsalInputs(cmd.Dir, spec.Files, credential); err != nil {
		return appTaskInfraFailure("rehearsal_input_invalid", "contained command inputs could not be installed", 65), nil
	}
	// guest-private scratch, never a shared host directory
	scratch, err := os.MkdirTemp("", "gregale-rehearsal-")
	if err != nil {
		return appTaskInfraFailure("rehearsal_output_unavailable", "contained command output directory unavailable", 65), nil
	}
	// disposable guest-private scratch
	defer func() { _ = os.RemoveAll(scratch) }()
	if credential != nil {
		if err := os.Chown(scratch, int(credential.Uid), int(credential.Gid)); err != nil {
			return appTaskInfraFailure("rehearsal_output_unavailable", "contained command output ownership unavailable", 65), nil
		}
	}
	overrides := make(map[string]string, len(spec.Env)+2)
	for k, v := range spec.Env {
		overrides[k] = v
	}
	overrides["GREGALE_TEST_BEHAVIOR_OUTPUT"] = path.Join(scratch, "behavior.json")
	overrides["GREGALE_TEST_TRIGGER_OUTPUT"] = path.Join(scratch, "trigger.json")
	cmd.Env = overlayRehearsalEnv(cmd.Env, overrides)
	cmd.WaitDelay = 100 * time.Millisecond
	output := &rehearsalCommandOutput{remaining: rehearsalcommand.MaxOutputBytes}
	cmd.Stdout, cmd.Stderr = rehearsalCommandWriter{output, false}, rehearsalCommandWriter{output, true}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: credential}
	if err := cmd.Start(); err != nil {
		return appTaskStartFailure(err), nil
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-wait:
	case <-ctx.Done():
		_ = signalJobProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
		<-wait
		return apptaskproto.Result{}, ctx.Err()
	}
	// Do not let a normal child holding an output descriptor delay collection.
	// The host subsequently destroys the entire VM, including detached groups.
	_ = signalJobProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
	behavior, behaviorErr := readRehearsalArtifact(scratch, "behavior.json", rehearsalcommand.MaxBehaviorBytes)
	trigger, triggerErr := readRehearsalArtifact(scratch, "trigger.json", rehearsalcommand.MaxTriggerBytes)
	if behaviorErr != nil || triggerErr != nil {
		return appTaskInfraFailure("rehearsal_output_invalid", "contained command output is not a bounded regular file", 65), nil
	}
	output.mu.Lock()
	receipt := rehearsalcommand.Receipt{Version: rehearsalcommand.Version, TaskID: req.TaskID, SpecSHA256: spec.SHA256(), Stdout: output.stdout.Bytes(), Stderr: output.stderr.Bytes(), OutputTruncated: output.truncated, Behavior: behavior, Trigger: trigger}
	raw, err := receipt.Marshal()
	output.mu.Unlock()
	if err != nil {
		return appTaskInfraFailure("rehearsal_protocol_error", "contained command receipt exceeds protocol bounds", 65), nil
	}
	if _, err := stdout.Write(raw); err != nil {
		return apptaskproto.Result{}, err
	}
	return appTaskResultFromWait(cmd, waitErr), nil
}

func overlayRehearsalEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		k, _, _ := strings.Cut(item, "=")
		if _, ok := overrides[k]; !ok {
			out = append(out, item)
		}
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+overrides[k])
	}
	return out
}

func installRehearsalInputs(dir string, files map[string][]byte, credential *syscall.Credential) error {
	// confined to the disposable guest application directory
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for name, data := range files {
		if !rehearsalcommand.ValidPath(name) {
			return errors.New("invalid input path")
		}
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			prefix := strings.Join(parts[:i], "/")
			info, err := root.Lstat(prefix)
			if errors.Is(err, os.ErrNotExist) {
				if err = root.Mkdir(prefix, 0755); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return errors.New("input parent is not a directory")
			}
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0644)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("input is not regular")
		}
		if err == nil {
			err = f.Chmod(0644)
		}
		if err == nil && credential != nil {
			err = f.Chown(int(credential.Uid), int(credential.Gid))
		}
		if err == nil {
			_, err = f.Write(data)
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func readRehearsalArtifact(dir, name string, limit int) ([]byte, error) {
	// output is confined to guest-private scratch
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("invalid artifact")
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(raw) > limit {
		return nil, errors.New("artifact too large")
	}
	return raw, err
}

type rehearsalCommandOutput struct {
	mu             sync.Mutex
	stdout, stderr bytes.Buffer
	remaining      int
	truncated      bool
}
type rehearsalCommandWriter struct {
	o      *rehearsalCommandOutput
	stderr bool
}

func (w rehearsalCommandWriter) Write(p []byte) (int, error) {
	w.o.mu.Lock()
	defer w.o.mu.Unlock()
	n := min(len(p), w.o.remaining)
	w.o.truncated = w.o.truncated || n < len(p)
	w.o.remaining -= n
	if w.stderr {
		_, _ = w.o.stderr.Write(p[:n])
	} else {
		_, _ = w.o.stdout.Write(p[:n])
	}
	return len(p), nil
}
