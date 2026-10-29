package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
)

// Agent run guests (ADR-20261027090010 §3, §6) boot from the shared agent-base drive0
// with a platform layer whose only workload file is /etc/faas/agent.json.
// The command and inputs arrive later over agentrunproto, after the resume
// hook, so nothing tenant-specific is ever in the image or a snapshot.
const (
	agentManifestRelativePath = "etc/faas/agent.json"
	agentManifestKind         = "agent"
	agentManifestVersion      = 1

	agentInputDir   = "/input"
	agentWorkDir    = "/work"
	agentOutputDir  = "/work/output"
	agentResultName = "result.json"
	// agentUID is the guest runtime user (spec §4.8).
	agentUID = 1000
)

type agentManifest struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

func validateAgentManifest(data []byte) error {
	var m agentManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("decode agent manifest: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("agent manifest has trailing data")
	}
	if m.Kind != agentManifestKind || m.Version != agentManifestVersion {
		return fmt.Errorf("agent manifest must be kind %q version %d", agentManifestKind, agentManifestVersion)
	}
	return nil
}

// stageAgentInputs writes the request's files under dir, read-only for the
// harness: owned by guest-init's user, directories 0755 and files 0644. The
// paths were validated by agentrunproto; os.Root keeps them inside dir anyway.
func stageAgentInputs(dir string, files []agentrunproto.File) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	for _, f := range files {
		if err := agentrunproto.ValidRelativePath(f.Path); err != nil {
			return fmt.Errorf("input %q: %w", f.Path, err)
		}
		if parent := path.Dir(f.Path); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return fmt.Errorf("create input directory %s: %w", parent, err)
			}
		}
		if err := root.WriteFile(f.Path, f.Content, 0o644); err != nil {
			return fmt.Errorf("write input %s: %w", f.Path, err)
		}
	}
	return nil
}

// agentOutputs is what a successful run returns, or why it may not.
type agentOutputs struct {
	result      json.RawMessage
	files       []agentrunproto.OutputFile
	failureCode string
}

// collectAgentOutputs reads dir after the harness has exited: result.json
// becomes the inline result and every other regular file a bounded output
// file. Symlinks and special files are skipped, never followed; os.Root keeps
// every read inside dir.
func collectAgentOutputs(dir string, req agentrunproto.Request) (agentOutputs, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return agentOutputs{}, nil
	}
	if err != nil {
		return agentOutputs{}, fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	var out agentOutputs
	total := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		if name == agentResultName {
			data, tooBig, err := readBounded(root, name, req.MaxResultBytes)
			if err != nil {
				return err
			}
			if tooBig || !agentrunproto.IsJSONObject(data) {
				out.failureCode = agentrunproto.FailureInvalidResult
				return fs.SkipAll
			}
			out.result = data
			return nil
		}
		if agentrunproto.ValidRelativePath(name) != nil || len(out.files) == req.MaxOutputFiles {
			out.failureCode = agentrunproto.FailureOutputLimit
			return fs.SkipAll
		}
		data, tooBig, err := readBounded(root, name, req.MaxOutputBytes-total)
		if err != nil {
			return err
		}
		if tooBig {
			out.failureCode = agentrunproto.FailureOutputLimit
			return fs.SkipAll
		}
		total += len(data)
		out.files = append(out.files, agentrunproto.NewOutputFile(name, data))
		return nil
	})
	if err != nil {
		return agentOutputs{}, fmt.Errorf("collect outputs: %w", err)
	}
	if out.failureCode != "" {
		return agentOutputs{failureCode: out.failureCode}, nil
	}
	return out, nil
}

// readBounded reads at most limit bytes of name, reporting whether the file
// holds more.
func readBounded(root *os.Root, name string, limit int) ([]byte, bool, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return nil, true, nil
	}
	return data, false, nil
}

// agentRunStatus maps the job supervisor's exit report to a run state. The
// kernel OOM killer's SIGKILL looks like any other signal death, so the
// guest's oom_kill counter decides it.
func agentRunStatus(payload JobExitPayload, oomKilled bool) (agentrunproto.Status, string) {
	switch payload.ErrorClass {
	case "succeeded":
		return agentrunproto.StatusSucceeded, ""
	case "timeout":
		return agentrunproto.StatusTimedOut, ""
	case "cancelled":
		return agentrunproto.StatusCancelled, ""
	}
	if oomKilled && (payload.Signal == 9 || payload.ExitCode == 137) {
		return agentrunproto.StatusOutOfMemory, ""
	}
	if payload.ErrorClass == "infra" && (payload.ExitCode == 126 || payload.ExitCode == 127) {
		return agentrunproto.StatusFailed, agentrunproto.FailureInfra
	}
	return agentrunproto.StatusFailed, ""
}

// oomKillCount reads the kernel's oom_kill counter from /proc/vmstat text.
func oomKillCount(vmstat io.Reader) int64 {
	scanner := bufio.NewScanner(vmstat)
	for scanner.Scan() {
		if name, value, ok := strings.Cut(scanner.Text(), " "); ok && name == "oom_kill" {
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err == nil {
				return n
			}
		}
	}
	return 0
}

// agentRunEnv is the harness environment: a fixed base, then the request's
// variables (the preset's env and the relay URLs the host sets).
func agentRunEnv(home string, requested map[string]string) []string {
	env := map[string]string{
		"PATH":   "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME":   home,
		"USER":   "app",
		"LANG":   "C.UTF-8",
		"TMPDIR": "/tmp",
	}
	for k, v := range requested {
		env[k] = v
	}
	// The relay URL is platform-owned and set after the run's values, like
	// an app's (ADR-989); the model base URLs agentd sets point under it.
	env["GREGALE_OUTBOUND_URL"] = OutboundRelayEndpoint
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
