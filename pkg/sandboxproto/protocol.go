// Package sandboxproto is the host/guest wire contract of a sandbox session
// (ADR-20261027090013 §4): the operations guest-init's sandbox server runs
// for vmmd, many per session.
//
// Every operation is one vsock connection to VsockPort. The host sends one
// request frame and, for an upload, data frames and a data-end frame; a
// command's stdin, resize and kill frames may follow while it runs. The
// guest answers with stdout, stderr or data frames and exactly one result
// or error frame, then closes. File paths are resolved inside the guest and
// confined to the workspace; no host component parses a guest filesystem.
//
// The frame header matches the other vsock protocols (agentrunproto,
// apptaskproto): a 4-byte big-endian type and a 4-byte big-endian length.
package sandboxproto

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// VsockPort is the guest listener of a sandbox session. It is distinct
	// from resume (1024), job exit (1026) through agent runs (1033) and the
	// app task fork-exec helper (1034).
	VsockPort uint32 = 1035

	// Host to guest.
	FrameRequest  uint32 = 1
	FrameStdin    uint32 = 6
	FrameStdinEOF uint32 = 7
	FrameResize   uint32 = 8
	FrameKill     uint32 = 9
	FrameDataEnd  uint32 = 11
	// Guest to host.
	FrameStdout uint32 = 2
	FrameStderr uint32 = 3
	FrameResult uint32 = 4
	FrameError  uint32 = 5
	// Either way: file contents and workspace archives.
	FrameData uint32 = 10

	frameHeaderBytes = 8
	// ChunkBytes is the largest stream or data frame a side writes.
	ChunkBytes = 64 << 10
	// maxControlFrameBytes bounds request, result, error and resize frames.
	maxControlFrameBytes = 4 << 20
	maxFrameBytes        = max(maxControlFrameBytes, ChunkBytes)
)

// Operations.
const (
	OpExec       = "exec"
	OpExecList   = "exec_list"
	OpExecOutput = "exec_output"
	OpExecKill   = "exec_kill"
	// OpExecInput writes to a background command's stdin, ends its input,
	// or resizes its terminal (ADR-20261027090013 step 12). The request
	// carries the bytes, so a caller that cannot hold a stream open, such as
	// an HTTP client, can drive a terminal command between output polls.
	OpExecInput = "exec_input"
	// OpSessionEnv replaces the session's environment (ADR-20261027090013
	// step 13): variables every later command gets, under its own request's
	// env. guest-init keeps them in memory only; the result names them and
	// never carries their values.
	OpSessionEnv = "session_env"
	// OpCodeRun runs code in a code context's Jupyter kernel
	// (ADR-20261027090013 step 14). Its stdout and stderr stream as frames;
	// the result carries rich outputs, the error and the execution count.
	OpCodeRun = "code_run"
	// OpCodeContext creates, lists, restarts or removes code contexts.
	OpCodeContext    = "code_context"
	OpFileRead       = "file_read"
	OpFileWrite      = "file_write"
	OpFileList       = "file_list"
	OpFileStat       = "file_stat"
	OpFileRemove     = "file_remove"
	OpFileMkdir      = "file_mkdir"
	OpFileRename     = "file_rename"
	OpArchiveCreate  = "archive_create"
	OpArchiveExtract = "archive_extract"
	// OpPortConnect opens a TCP connection to a guest port on loopback and
	// splices it with the operation's data frames (ADR-20261027090013 §11):
	// the host's data frames go to the port, a data-end frame half-closes
	// it, and what the port sends comes back as data frames. The result
	// follows once both directions are done.
	OpPortConnect = "port_connect"
	// OpTemplateStart runs a template's start and ready commands in a
	// session restored from its warm snapshot (ADR-20261027090013 §12),
	// which was captured before them; it answers once the ready command
	// passes. Only vmmd sends it, once, right after the restore: callers'
	// requests that name it are refused (IsHostOnly).
	OpTemplateStart = "template_start"
)

// IsHostOnly reports whether only vmmd may send op, never a caller.
func IsHostOnly(op string) bool { return op == OpTemplateStart }

// Command users (ADR-20261027090013 §10).
const (
	RunAsUser = "user"
	RunAsRoot = "root"
)

// Limits of one operation. The plan's own limits (pkg/api/limits.go
// SandboxExec*) are applied by the host and are never above these.
const (
	MaxArgv          = 1024
	MaxCommandBytes  = 256 << 10
	MaxEnvEntries    = 128
	MaxEnvValueBytes = 32 << 10
	// MaxEnvBytes bounds one env (names and values together), so a
	// command's env under the session's stays well inside execve's limit.
	MaxEnvBytes       = 256 << 10
	MaxPathBytes      = 4096
	MaxTimeoutMillis  = 3600 * 1000
	MaxOutputBytes    = 16 << 20
	MaxListDepth      = 16
	MaxListEntries    = 10_000
	MaxWriteBytes     = 1 << 30
	MaxArchiveEntries = 1_000_000
	MaxArchiveBytes   = 64 << 30
	// MaxBackgroundCommands bounds a session's background commands, running
	// or finished and not yet forgotten.
	MaxBackgroundCommands = 32
	// MaxInputBytes bounds one exec_input's stdin bytes.
	MaxInputBytes = 1 << 20
	// MaxCodeBytes bounds one code_run's code.
	MaxCodeBytes = 1 << 20
	// StopGrace is how long a timed-out command has between SIGTERM and
	// SIGKILL.
	StopGrace = 2 * time.Second
)

// Error codes the guest reports.
const (
	CodeInvalidRequest   = "invalid_request"
	CodeNotFound         = "not_found"
	CodeExists           = "exists"
	CodeOutsideWorkspace = "outside_workspace"
	CodeNotADirectory    = "not_a_directory"
	CodeIsADirectory     = "is_a_directory"
	CodeNotEmpty         = "not_empty"
	CodeTooLarge         = "too_large"
	CodeLimit            = "limit"
	CodeUnsupported      = "unsupported"
	CodeStartFailed      = "start_failed"
	CodeInternal         = "internal"
	// CodePortClosed: nothing listens on the guest port a port_connect
	// names.
	CodePortClosed = "port_closed"
	// CodeCommandEnded: exec_input named a command that has exited, or
	// whose input was already ended.
	CodeCommandEnded = "command_ended"
	// CodeInputStalled: exec_input's write timed out because the command
	// stopped reading. Part of the input may have been written, so a
	// client must not resend it whole.
	CodeInputStalled = "input_stalled"
)

var (
	ErrInvalidRequest  = errors.New("sandbox protocol: invalid request")
	ErrFrameTooLarge   = errors.New("sandbox protocol: frame too large")
	ErrUnexpectedFrame = errors.New("sandbox protocol: unexpected frame")
)

// Request is one operation. Fields an operation does not use must be empty.
type Request struct {
	Op string `json:"op"`

	// exec
	Argv           []string          `json:"argv,omitempty"`
	Workdir        string            `json:"workdir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TTY            bool              `json:"tty,omitempty"`
	Rows           uint16            `json:"rows,omitempty"`
	Cols           uint16            `json:"cols,omitempty"`
	Background     bool              `json:"background,omitempty"`
	TimeoutMillis  int64             `json:"timeout_ms,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty"`
	RunAs          string            `json:"run_as,omitempty"`

	// exec_output, exec_kill
	Handle       string `json:"handle,omitempty"`
	StdoutOffset int64  `json:"stdout_offset,omitempty"`
	StderrOffset int64  `json:"stderr_offset,omitempty"`
	Signal       string `json:"signal,omitempty"`

	// exec_input. Rows and Cols, when set, resize the command's terminal;
	// then Input is written to its stdin, and CloseInput ends it.
	Input      []byte `json:"input,omitempty"`
	CloseInput bool   `json:"close_input,omitempty"`

	// files and archives
	Path      string `json:"path,omitempty"`
	To        string `json:"to,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Parents   bool   `json:"parents,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
	Depth     int    `json:"depth,omitempty"`
	MaxBytes  int64  `json:"max_bytes,omitempty"`
	// MaxEntries bounds an extracted archive.
	MaxEntries int `json:"max_entries,omitempty"`

	// port_connect
	Port int `json:"port,omitempty"`

	// code_run and code_context. Context names a code context (empty: the
	// default one); Action is a CodeAction; Language and Workdir go with
	// create. TimeoutMillis and MaxOutputBytes bound a run.
	Code     string `json:"code,omitempty"`
	Context  string `json:"context,omitempty"`
	Action   string `json:"action,omitempty"`
	Language string `json:"language,omitempty"`

	// template_start; the ready timeout is TimeoutMillis.
	StartCmd string `json:"start_cmd,omitempty"`
	ReadyCmd string `json:"ready_cmd,omitempty"`
}

// handleRE is a background command's handle.
var handleRE = regexp.MustCompile(`^c[0-9a-f]{16}$`)

// envNameRE is an environment variable name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Validate checks a request before the guest acts on it.
func (r Request) Validate() error {
	var err error
	switch r.Op {
	case OpExec:
		err = r.validateExec()
	case OpExecList:
	case OpExecOutput:
		err = firstError(validHandle(r.Handle), nonNegative("stdout_offset", r.StdoutOffset), nonNegative("stderr_offset", r.StderrOffset),
			bounded("max_bytes", r.MaxBytes, MaxOutputBytes))
	case OpExecKill:
		err = firstError(validHandle(r.Handle), validSignal(r.Signal))
	case OpExecInput:
		err = firstError(validHandle(r.Handle), r.validateInput())
	case OpSessionEnv:
		err = validEnv(r.Env)
	case OpCodeRun:
		err = r.validateCodeRun()
	case OpCodeContext:
		err = r.validateCodeContext()
	case OpFileRead:
		err = firstError(validPath("path", r.Path), bounded("max_bytes", r.MaxBytes, MaxWriteBytes))
	case OpFileWrite:
		err = firstError(validPath("path", r.Path), validMode(r.Mode), bounded("max_bytes", r.MaxBytes, MaxWriteBytes))
	case OpFileList:
		err = firstError(validPath("path", r.Path), boundedInt("depth", r.Depth, MaxListDepth))
	case OpFileStat, OpFileRemove:
		err = validPath("path", r.Path)
	case OpFileMkdir:
		err = firstError(validPath("path", r.Path), validMode(r.Mode))
	case OpFileRename:
		err = firstError(validPath("path", r.Path), validPath("to", r.To))
	case OpArchiveCreate:
		err = firstError(validPath("path", r.Path), positive("max_bytes", r.MaxBytes, MaxArchiveBytes))
	case OpArchiveExtract:
		err = firstError(validPath("path", r.Path), positive("max_bytes", r.MaxBytes, MaxArchiveBytes),
			positiveInt("max_entries", r.MaxEntries, MaxArchiveEntries))
	case OpPortConnect:
		err = ValidPreviewPort(r.Port)
	case OpTemplateStart:
		err = r.validateTemplateStart()
	default:
		err = fmt.Errorf("unknown op %q", r.Op)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return nil
}

// Code context actions (code_context).
const (
	CodeActionCreate  = "create"
	CodeActionList    = "list"
	CodeActionRestart = "restart"
	CodeActionRemove  = "remove"
)

// codeContextRE is a code context's ID: "default" or a driver-made hex ID.
var codeContextRE = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

// codeLanguageRE is a code context's language name.
var codeLanguageRE = regexp.MustCompile(`^[a-z0-9_+-]{1,32}$`)

func validCodeContext(c string, required bool) error {
	if c == "" && !required {
		return nil
	}
	if !codeContextRE.MatchString(c) {
		return errors.New("context is not a code context ID")
	}
	return nil
}

func (r Request) validateCodeRun() error {
	if len(r.Code) > MaxCodeBytes || strings.IndexByte(r.Code, 0) >= 0 || !utf8.ValidString(r.Code) {
		return fmt.Errorf("code must be at most %d bytes of UTF-8 without NUL", MaxCodeBytes)
	}
	return firstError(validCodeContext(r.Context, false), positive("timeout_ms", r.TimeoutMillis, MaxTimeoutMillis),
		positive("max_output_bytes", r.MaxOutputBytes, MaxOutputBytes))
}

func (r Request) validateCodeContext() error {
	switch r.Action {
	case CodeActionCreate:
		if r.Language != "" && !codeLanguageRE.MatchString(r.Language) {
			return errors.New("language is not a language name")
		}
		if len(r.Workdir) > MaxPathBytes || strings.IndexByte(r.Workdir, 0) >= 0 {
			return errors.New("workdir is too long or holds NUL")
		}
		return nil
	case CodeActionList:
		return nil
	case CodeActionRestart, CodeActionRemove:
		return validCodeContext(r.Context, true)
	}
	return fmt.Errorf("action must be one of %s, %s, %s or %s", CodeActionCreate, CodeActionList, CodeActionRestart, CodeActionRemove)
}

// validEnv checks a command's or the session's environment.
func validEnv(env map[string]string) error {
	if len(env) > MaxEnvEntries {
		return fmt.Errorf("env takes at most %d entries", MaxEnvEntries)
	}
	total := 0
	for k, v := range env {
		if !envNameRE.MatchString(k) || len(v) > MaxEnvValueBytes || strings.IndexByte(v, 0) >= 0 {
			return fmt.Errorf("env %q is not a valid variable", k)
		}
		total += len(k) + len(v) + 2
	}
	if total > MaxEnvBytes {
		return fmt.Errorf("env takes at most %d bytes, got %d", MaxEnvBytes, total)
	}
	return nil
}

func (r Request) validateInput() error {
	switch {
	case len(r.Input) > MaxInputBytes:
		return fmt.Errorf("input must be at most %d bytes, got %d", MaxInputBytes, len(r.Input))
	case (r.Rows == 0) != (r.Cols == 0):
		return errors.New("rows and cols go together")
	case len(r.Input) == 0 && !r.CloseInput && r.Rows == 0:
		return errors.New("exec_input needs input, close_input, or rows and cols")
	}
	return nil
}

func (r Request) validateTemplateStart() error {
	if r.StartCmd == "" && r.ReadyCmd == "" {
		return errors.New("template_start needs a start or a ready command")
	}
	for _, cmd := range []string{r.StartCmd, r.ReadyCmd} {
		if len(cmd) > MaxCommandBytes || strings.IndexByte(cmd, 0) >= 0 || !utf8.ValidString(cmd) {
			return fmt.Errorf("a template command must be at most %d bytes of UTF-8 without NUL", MaxCommandBytes)
		}
	}
	if r.ReadyCmd != "" && r.TimeoutMillis <= 0 {
		return errors.New("a ready command needs a positive timeout_ms")
	}
	return bounded("timeout_ms", r.TimeoutMillis, MaxTimeoutMillis)
}

func (r Request) validateExec() error {
	if len(r.Argv) == 0 || len(r.Argv) > MaxArgv || r.Argv[0] == "" {
		return fmt.Errorf("argv must have 1-%d entries and a command", MaxArgv)
	}
	total := 0
	for _, arg := range r.Argv {
		if total += len(arg); total > MaxCommandBytes || strings.IndexByte(arg, 0) >= 0 || !utf8.ValidString(arg) {
			return fmt.Errorf("argv must be at most %d bytes of UTF-8 without NUL", MaxCommandBytes)
		}
	}
	if err := validEnv(r.Env); err != nil {
		return err
	}
	if len(r.Workdir) > MaxPathBytes || strings.IndexByte(r.Workdir, 0) >= 0 {
		return errors.New("workdir is too long or holds NUL")
	}
	if r.RunAs != "" && r.RunAs != RunAsUser && r.RunAs != RunAsRoot {
		return errors.New("run_as must be user or root")
	}
	// A background command may run on a terminal (step 12): exec_input
	// drives it between output polls.
	if !r.TTY && (r.Rows != 0 || r.Cols != 0) {
		return errors.New("rows and cols need tty")
	}
	if (r.Rows == 0) != (r.Cols == 0) {
		return errors.New("rows and cols go together")
	}
	return firstError(positive("timeout_ms", r.TimeoutMillis, MaxTimeoutMillis), positive("max_output_bytes", r.MaxOutputBytes, MaxOutputBytes))
}

// WorkspacePath returns p relative to the workspace root, or ok false when
// p names a place outside it. "", "." and the root itself are the root.
// Absolute paths must be under root; relative ones are under it already.
func WorkspacePath(root, p string) (string, bool) {
	if p == "" {
		return ".", true
	}
	if path.IsAbs(p) {
		rest, ok := strings.CutPrefix(path.Clean(p), root)
		if !ok || (rest != "" && rest[0] != '/') {
			return "", false
		}
		p = strings.TrimPrefix(rest, "/")
		if p == "" {
			return ".", true
		}
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func validPath(field, p string) error {
	if len(p) > MaxPathBytes || strings.IndexByte(p, 0) >= 0 || !utf8.ValidString(p) {
		return fmt.Errorf("%s must be at most %d bytes of UTF-8 without NUL", field, MaxPathBytes)
	}
	return nil
}

func validHandle(h string) error {
	if !handleRE.MatchString(h) {
		return errors.New("handle is not a command handle")
	}
	return nil
}

func validSignal(s string) error {
	if s != "" && s != "TERM" && s != "KILL" && s != "INT" {
		return errors.New("signal must be TERM, KILL or INT")
	}
	return nil
}

func validMode(mode uint32) error {
	if mode&^0o7777 != 0 {
		return errors.New("mode takes permission bits only")
	}
	return nil
}

func positive(field string, v, max int64) error {
	if v < 1 || v > max {
		return fmt.Errorf("%s must be 1-%d", field, max)
	}
	return nil
}

func positiveInt(field string, v, max int) error { return positive(field, int64(v), int64(max)) }

func bounded(field string, v, max int64) error {
	if v < 0 || v > max {
		return fmt.Errorf("%s must be 0-%d", field, max)
	}
	return nil
}

func boundedInt(field string, v, max int) error { return bounded(field, int64(v), int64(max)) }

func nonNegative(field string, v int64) error {
	if v < 0 {
		return fmt.Errorf("%s must not be negative", field)
	}
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// CodeOutput is one rich output of a run: a display or the run's value
// (IsMainResult), as MIME type to value.
type CodeOutput struct {
	Data         map[string]json.RawMessage `json:"data"`
	IsMainResult bool                       `json:"is_main_result,omitempty"`
}

// CodeError is the exception a run raised.
type CodeError struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Traceback string `json:"traceback"`
}

// CodeRunResult answers code_run. Its stdout and stderr came as frames.
type CodeRunResult struct {
	Results         []CodeOutput `json:"results"`
	Error           *CodeError   `json:"error,omitempty"`
	ExecutionCount  *int         `json:"execution_count,omitempty"`
	TimedOut        bool         `json:"timed_out,omitempty"`
	OutputTruncated bool         `json:"output_truncated,omitempty"`
}

// CodeContext is a code context: its own kernel, language and directory.
type CodeContext struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Cwd      string `json:"cwd"`
}

// CodeContextResult answers code_context: the created context, or the
// session's contexts for list; nothing for restart and remove.
type CodeContextResult struct {
	Context  *CodeContext  `json:"context,omitempty"`
	Contexts []CodeContext `json:"contexts,omitempty"`
}

// SessionEnvResult answers session_env: the names now set, sorted.
type SessionEnvResult struct {
	Names []string `json:"names"`
}

// ExecResult ends a command run in the foreground, or names a background
// one by its Handle.
type ExecResult struct {
	Handle          string `json:"handle,omitempty"`
	ExitCode        int    `json:"exit_code"`
	Signal          int    `json:"signal,omitempty"`
	TimedOut        bool   `json:"timed_out,omitempty"`
	Killed          bool   `json:"killed,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	WallTimeMillis  int64  `json:"wall_time_ms"`
}

// Command is a background command.
type Command struct {
	Handle  string   `json:"handle"`
	Argv    []string `json:"argv"`
	Running bool     `json:"running"`
	// TTY: the command runs on a terminal, whose output is all stdout.
	TTY           bool  `json:"tty,omitempty"`
	ExitCode      int   `json:"exit_code,omitempty"`
	Signal        int   `json:"signal,omitempty"`
	StartedAtUnix int64 `json:"started_at_unix_ms"`
}

// ExecList answers exec_list.
type ExecList struct {
	Commands []Command `json:"commands"`
}

// ExecOutput is a background command's output since the requested offsets.
// Older output than the guest keeps is reported as skipped.
type ExecOutput struct {
	Command
	Stdout        []byte `json:"stdout"`
	Stderr        []byte `json:"stderr"`
	StdoutOffset  int64  `json:"stdout_offset"`
	StderrOffset  int64  `json:"stderr_offset"`
	SkippedOutput bool   `json:"skipped_output,omitempty"`
}

// File types.
const (
	TypeFile    = "file"
	TypeDir     = "dir"
	TypeSymlink = "symlink"
	TypeOther   = "other"
)

// FileInfo describes one workspace entry. Path is relative to the workspace
// root.
type FileInfo struct {
	Path       string `json:"path"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Mode       uint32 `json:"mode"`
	ModUnixMs  int64  `json:"mtime_unix_ms"`
	LinkTarget string `json:"link_target,omitempty"`
}

// FileList answers file_list.
type FileList struct {
	Entries   []FileInfo `json:"entries"`
	Truncated bool       `json:"truncated,omitempty"`
}

// TransferResult ends a file read or write: the bytes moved.
type TransferResult struct {
	Bytes     int64 `json:"bytes"`
	Truncated bool  `json:"truncated,omitempty"`
}

// ArchiveResult ends an archive's creation or extraction.
type ArchiveResult struct {
	Bytes   int64  `json:"bytes"`
	Entries int    `json:"entries"`
	SHA256  string `json:"sha256,omitempty"`
}

// ErrorFrame is the guest's report that an operation failed.
type ErrorFrame struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// GuestError is an operation the guest refused or failed.
type GuestError struct {
	Code    string
	Message string
}

func (e *GuestError) Error() string {
	if e.Message == "" {
		return "sandbox guest: " + e.Code
	}
	return "sandbox guest: " + e.Code + ": " + e.Message
}

// ResizeFrame resizes a command's terminal.
type ResizeFrame struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// Conn frames one operation's connection. Writes are safe from many
// goroutines; reads are not.
type Conn struct {
	conn net.Conn
	mu   sync.Mutex
}

// NewConn wraps conn.
func NewConn(conn net.Conn) *Conn { return &Conn{conn: conn} }

// Write sends one frame.
func (c *Conn) Write(ctx context.Context, frameType uint32, body []byte) error {
	if len(body) > maxFrameBytes {
		return ErrFrameTooLarge
	}
	var hdr [frameHeaderBytes]byte
	binary.BigEndian.PutUint32(hdr[0:4], frameType)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := c.conn.Write(append(hdr[:], body...)); err != nil {
		return contextError(ctx, err)
	}
	return nil
}

// WriteJSON sends v as one frame.
func (c *Conn) WriteJSON(ctx context.Context, frameType uint32, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > maxControlFrameBytes {
		return ErrFrameTooLarge
	}
	return c.Write(ctx, frameType, body)
}

// Read receives one frame.
func (c *Conn) Read(ctx context.Context) (uint32, []byte, error) {
	var hdr [frameHeaderBytes]byte
	if _, err := io.ReadFull(c.conn, hdr[:]); err != nil {
		return 0, nil, contextError(ctx, err)
	}
	frameType := binary.BigEndian.Uint32(hdr[0:4])
	n := binary.BigEndian.Uint32(hdr[4:8])
	if int64(n) > maxFrameBytes {
		return 0, nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, n)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return 0, nil, contextError(ctx, err)
	}
	return frameType, body, nil
}

// Close closes the connection.
func (c *Conn) Close() error { return c.conn.Close() }

// DecodeStrict decodes one JSON value and nothing after it, refusing unknown
// fields.
func DecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// errorCodeRE bounds a guest-supplied error code before it reaches a host
// error.
var errorCodeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// maxErrorMessageBytes bounds a guest-supplied message.
const maxErrorMessageBytes = 512

func guestError(e ErrorFrame) *GuestError {
	code := e.Code
	if !errorCodeRE.MatchString(code) {
		code = CodeInternal
	}
	message := strings.ToValidUTF8(e.Message, "")
	if len(message) > maxErrorMessageBytes {
		message = strings.ToValidUTF8(message[:maxErrorMessageBytes], "")
	}
	return &GuestError{Code: code, Message: message}
}

// ValidPreviewPort reports whether a guest port may be exposed: a TCP port
// that is not one of guest-init's own listeners.
func ValidPreviewPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be 1-65535; got %d", port)
	}
	if slices.Contains(api.SandboxReservedPorts, port) {
		return fmt.Errorf("port %d is reserved for the platform", port)
	}
	return nil
}

// PortResult ends a port connection: the bytes sent each way.
type PortResult struct {
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
}
