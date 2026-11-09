package main

// Sandbox session guests (ADR-20261027090013 §4) boot from the shared
// agent-base drive0 with a platform layer whose only workload file is
// /etc/faas/sandbox.json. Unlike an agent run, a session serves many
// operations: every command, file operation and workspace archive arrives
// over its own sandboxproto connection, after the resume hook, so nothing
// tenant-specific is in the image or a snapshot. The server runs until vmmd
// stops the VM.
//
// Commands run as the guest user (uid 1000) or as root (§10): the VM is the
// boundary. File and archive paths come from the host, so they stay inside
// the workspace whatever user asked (os.Root refuses symlink escapes).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

const (
	sandboxManifestRelativePath = "etc/faas/sandbox.json"
	sandboxManifestKind         = "sandbox"
	sandboxManifestVersion      = 1
	// sandboxManifestVersionCommands carries a template's start and ready
	// commands (ADR-20261027090013 step 10); an older guest-init refuses it,
	// so only sessions that need the commands depend on this one.
	sandboxManifestVersionCommands = 2

	sandboxWorkspace = "/workspace"
	sandboxHome      = "/home/app"
	// sandboxUID is the guest runtime user (spec §4.8), who owns the
	// workspace and what the files API writes.
	sandboxUID = 1000
	// sandboxReadyStage is the console stage vmmd waits for before it
	// captures a preset snapshot or forwards the first operation.
	sandboxReadyStage = "sandbox-ready"
	// sandboxMaxOperations bounds the operations one session runs at once.
	sandboxMaxOperations = 64
)

type sandboxManifest struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	// StartCmd, ReadyCmd and ReadyTimeoutMs are a template's commands
	// (version 2): the start command runs in the background at boot, and
	// the session serves once the ready command exits 0.
	StartCmd       string `json:"start_cmd,omitempty"`
	ReadyCmd       string `json:"ready_cmd,omitempty"`
	ReadyTimeoutMs int64  `json:"ready_timeout_ms,omitempty"`
}

func validateSandboxManifest(data []byte) error {
	_, err := parseSandboxManifest(data)
	return err
}

func parseSandboxManifest(data []byte) (sandboxManifest, error) {
	var m sandboxManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("decode sandbox manifest: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return m, errors.New("sandbox manifest has trailing data")
	}
	commands := m.StartCmd != "" || m.ReadyCmd != "" || m.ReadyTimeoutMs != 0
	switch {
	case m.Kind != sandboxManifestKind || m.Version != sandboxManifestVersion && m.Version != sandboxManifestVersionCommands:
		return m, fmt.Errorf("sandbox manifest must be kind %q version %d or %d", sandboxManifestKind, sandboxManifestVersion, sandboxManifestVersionCommands)
	case commands && m.Version != sandboxManifestVersionCommands:
		return m, fmt.Errorf("sandbox manifest commands need version %d", sandboxManifestVersionCommands)
	case m.ReadyTimeoutMs < 0 || m.ReadyCmd != "" && m.ReadyTimeoutMs == 0:
		return m, errors.New("sandbox manifest ready command needs a positive ready_timeout_ms")
	}
	return m, nil
}

// sandboxServer runs a session's operations.
type sandboxServer struct {
	workspace string
	home      string
	// owner owns the files and directories the files API and archive
	// extraction create; -1 leaves them as guest-init made them (tests that
	// do not run as root).
	owner    int
	runner   sandboxRunner
	commands *sandboxCommands
	ops      chan struct{}
	now      func() time.Time
	log      *slog.Logger
}

func newSandboxServer(workspace, home string, owner int, runner sandboxRunner, log *slog.Logger) *sandboxServer {
	if log == nil {
		log = slog.Default()
	}
	s := &sandboxServer{workspace: workspace, home: home, owner: owner, runner: runner, ops: make(chan struct{}, sandboxMaxOperations),
		now: time.Now, log: log}
	s.commands = newSandboxCommands(runner, sandboxproto.MaxBackgroundCommands, s.now)
	return s
}

// serve runs one operation's connection.
func (s *sandboxServer) serve(ctx context.Context, conn net.Conn) {
	select {
	case s.ops <- struct{}{}:
		defer func() { <-s.ops }()
	default:
		c := sandboxproto.NewConn(conn)
		_ = c.WriteJSON(ctx, sandboxproto.FrameError, sandboxproto.ErrorFrame{Code: sandboxproto.CodeLimit,
			Message: fmt.Sprintf("the session runs at most %d operations at once", sandboxMaxOperations)})
		_ = c.Close()
		return
	}
	if err := sandboxproto.Serve(ctx, conn, s.handle); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Debug("sandbox: operation", "err", err)
	}
}

func (s *sandboxServer) handle(ctx context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	switch req.Op {
	case sandboxproto.OpExec:
		return s.exec(ctx, req, call)
	case sandboxproto.OpExecList:
		return s.commands.list(), nil
	case sandboxproto.OpExecOutput:
		return s.commands.output(req)
	case sandboxproto.OpExecKill:
		return s.commands.kill(req)
	case sandboxproto.OpArchiveCreate:
		return s.archiveCreate(req, call)
	case sandboxproto.OpArchiveExtract:
		return s.archiveExtract(req, call)
	case sandboxproto.OpPortConnect:
		return portConnect(ctx, req, call)
	}
	return s.fileOp(req, call)
}

// sandboxError is a refusal the host shows the caller.
func sandboxError(code, format string, args ...any) error {
	return &sandboxproto.GuestError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// fileError maps a filesystem error on name to the code the host shows.
func fileError(name string, err error) error {
	var errno syscall.Errno
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return sandboxError(sandboxproto.CodeNotFound, "%s does not exist", name)
	// fs.ErrExist matches ENOTEMPTY too, so it is checked first.
	case errors.As(err, &errno) && errno == syscall.ENOTEMPTY:
		return sandboxError(sandboxproto.CodeNotEmpty, "%s is not empty", name)
	case errors.Is(err, fs.ErrExist):
		return sandboxError(sandboxproto.CodeExists, "%s already exists", name)
	case errors.As(err, &errno) && errno == syscall.ENOTDIR:
		return sandboxError(sandboxproto.CodeNotADirectory, "a parent of %s is not a directory", name)
	case errors.As(err, &errno) && errno == syscall.EISDIR:
		return sandboxError(sandboxproto.CodeIsADirectory, "%s is a directory", name)
	// os.Root does not export its escape error; a symlink that leaves the
	// workspace is the only way left to escape it, since names were
	// checked first.
	case strings.Contains(err.Error(), "path escapes from parent"):
		return sandboxError(sandboxproto.CodeOutsideWorkspace, "%s leads outside the workspace", name)
	}
	return &sandboxproto.GuestError{Code: sandboxproto.CodeInternal}
}
