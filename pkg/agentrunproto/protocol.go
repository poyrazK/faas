// Package agentrunproto is the host/guest wire contract for one agent run
// (ADR-20261027090010 §3 and §6).
//
// vmmd dials the guest's agent listener only after a cold boot (or, later, a
// restore) has completed the resume hook, so the command, the prompt and the
// inputs enter the VM after it is up and never sit in a snapshot. One request
// frame is followed by bounded stdout/stderr frames and exactly one result or
// error frame; meanwhile the host may send one cancel frame. A connection is
// never reused. Outputs are bounded and inline, in the style of ADR-382; vmmd
// never mounts a filesystem the guest wrote.
//
// The frame header matches the other one-shot vsock protocols (executionproto,
// apptaskproto): a 4-byte big-endian type and a 4-byte big-endian length.
package agentrunproto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	Version uint16 = 1
	// VsockPort is the guest listener reserved for agent runs. It is distinct
	// from resume (1024), job exit (1026), framework ready (1027), job
	// control (1028), health (1029), execution (1030), app task (1031) and
	// the outbound relay's host port (1032).
	VsockPort uint32 = 1033

	FrameRequest uint32 = 1
	FrameStdout  uint32 = 2
	FrameStderr  uint32 = 3
	FrameResult  uint32 = 4
	FrameError   uint32 = 5
	// FrameCancel is the only frame the host sends after the request: the
	// guest stops the harness with SIGTERM, then SIGKILL after the grace.
	FrameCancel uint32 = 6

	MaxRunIDBytes = 128
	// MaxCommandArgBytes fits the rendered prompt, which a preset passes as
	// one argument ({prompt}): the template plus the run's own prompt. apid
	// still bounds a version's stored arguments by AgentHarnessArgMaxBytes.
	MaxCommandArgBytes     = api.AgentPromptTemplateMaxBytes + api.AgentRunPromptMaxBytes
	MaxCommandTotalBytes   = 256 << 10
	MaxEnvEntries          = 64
	MaxEnvValueBytes       = 32 << 10
	MaxInputPathBytes      = 512
	MaxInputPathDepth      = 8
	MaxFailureMessageBytes = 512
	// MaxTimeoutSec is the largest plan timeout (AgentRunTimeoutMaxSec);
	// admission applies the plan's own.
	MaxTimeoutSec = 3600
	// MaxResultBytes is the largest plan AgentResultMaxBytes.
	MaxResultBytes = 1 << 20

	frameHeaderBytes = 8
	streamChunkBytes = 64 << 10
	// JSON carries file content as base64; the frame caps leave room for it.
	maxRequestFrameBytes = api.AgentRunInputFilesMaxBytes*4/3 + 2*MaxCommandTotalBytes + 256<<10
	maxResultFrameBytes  = (api.AgentRunOutputFilesMaxBytes+MaxResultBytes)*4/3 + 256<<10
)

var (
	ErrInvalidRequest      = errors.New("agent run protocol: invalid request")
	ErrInvalidResult       = errors.New("agent run protocol: invalid result")
	ErrFrameTooLarge       = errors.New("agent run protocol: frame too large")
	ErrOutputLimitExceeded = errors.New("agent run protocol: output limit exceeded")
	ErrUnexpectedFrame     = errors.New("agent run protocol: unexpected frame")
	ErrGuestFailed         = errors.New("agent run protocol: guest failed")
)

// Status is how a run ended, in the agent_runs terminal states (ADR-20261027090010 §4).
type Status string

// OutboundRelayEndpoint is the loopback base URL guest-init serves in every
// agent run VM (ADR-989, ADR-20261027090010 §7). It mirrors guest-init's own
// constant; agentd builds the harness's model base URLs from it.
const OutboundRelayEndpoint = "http://127.0.0.1:2774"

const (
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusTimedOut    Status = "timed_out"
	StatusOutOfMemory Status = "out_of_memory"
	StatusCancelled   Status = "cancelled"
)

// Valid reports whether s is a terminal run state.
func (s Status) Valid() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusTimedOut, StatusOutOfMemory, StatusCancelled:
		return true
	}
	return false
}

// Failure codes the guest reports with a failed status.
const (
	FailureInfra         = "infra"          // the harness could not start
	FailureInvalidResult = "invalid_result" // output/result.json is not a JSON object within the limit
	FailureOutputLimit   = "output_limit"   // output/ holds more files or bytes than the request allows
	// FailureCheckout: the input drive does not hold the requested checkout,
	// or it could not be unpacked. FailureCheckoutTooLarge: it unpacks past
	// the run's limits.
	FailureCheckout         = "checkout"
	FailureCheckoutTooLarge = "checkout_too_large"
)

var (
	envName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	failureCode  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// File is one input file, written read-only under /input at Path.
type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// Request is the run's command and inputs. The limits are the account's plan
// limits, already resolved by the host.
type Request struct {
	Version        uint16            `json:"version"`
	RunID          string            `json:"run_id"`
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env,omitempty"`
	Files          []File            `json:"files,omitempty"`
	TimeoutSec     int               `json:"timeout_sec"`
	MaxLogBytes    int               `json:"max_log_bytes"`
	MaxResultBytes int               `json:"max_result_bytes"`
	MaxOutputFiles int               `json:"max_output_files"`
	MaxOutputBytes int               `json:"max_output_bytes"`
	// Checkout names the archive on the run's input drive, unpacked at
	// /input/repo (ADR-20261027090010 §3). Absent: the run has no input drive.
	Checkout *Checkout `json:"checkout,omitempty"`
}

// CheckoutDir is where a checkout lands, under the input directory.
const CheckoutDir = "repo"

// Validate applies the guest-boundary checks; both sides call it.
func (r Request) Validate() error {
	switch {
	case r.Version != Version:
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidRequest, r.Version)
	case strings.TrimSpace(r.RunID) == "" || len(r.RunID) > MaxRunIDBytes || !utf8.ValidString(r.RunID) || strings.ContainsRune(r.RunID, 0):
		return fmt.Errorf("%w: run_id is empty or too long", ErrInvalidRequest)
	case len(r.Command) == 0 || len(r.Command) > api.AgentHarnessArgsMax || strings.TrimSpace(r.Command[0]) == "":
		return fmt.Errorf("%w: command must have 1 to %d arguments", ErrInvalidRequest, api.AgentHarnessArgsMax)
	case r.TimeoutSec < 1 || r.TimeoutSec > MaxTimeoutSec:
		return fmt.Errorf("%w: timeout_sec is outside 1-%d", ErrInvalidRequest, MaxTimeoutSec)
	case r.MaxLogBytes < 0 || r.MaxLogBytes > api.AgentRunLogMaxBytes:
		return fmt.Errorf("%w: max_log_bytes is outside 0-%d", ErrInvalidRequest, api.AgentRunLogMaxBytes)
	case r.MaxResultBytes < 0 || r.MaxResultBytes > MaxResultBytes:
		return fmt.Errorf("%w: max_result_bytes is outside 0-%d", ErrInvalidRequest, MaxResultBytes)
	case r.MaxOutputFiles < 0 || r.MaxOutputFiles > api.AgentRunOutputFilesMax:
		return fmt.Errorf("%w: max_output_files is outside 0-%d", ErrInvalidRequest, api.AgentRunOutputFilesMax)
	case r.MaxOutputBytes < 0 || r.MaxOutputBytes > api.AgentRunOutputFilesMaxBytes:
		return fmt.Errorf("%w: max_output_bytes is outside 0-%d", ErrInvalidRequest, api.AgentRunOutputFilesMaxBytes)
	case len(r.Env) > MaxEnvEntries:
		return fmt.Errorf("%w: at most %d environment entries", ErrInvalidRequest, MaxEnvEntries)
	case len(r.Files) > api.AgentRunInputFilesMax:
		return fmt.Errorf("%w: at most %d input files", ErrInvalidRequest, api.AgentRunInputFilesMax)
	}
	total := 0
	for i, arg := range r.Command {
		if len(arg) > MaxCommandArgBytes || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("%w: command argument %d is invalid", ErrInvalidRequest, i)
		}
		total += len(arg)
	}
	if total > MaxCommandTotalBytes {
		return fmt.Errorf("%w: the command holds %d bytes; at most %d", ErrInvalidRequest, total, MaxCommandTotalBytes)
	}
	for name, value := range r.Env {
		if !envName.MatchString(name) || len(value) > MaxEnvValueBytes || strings.ContainsRune(value, 0) {
			return fmt.Errorf("%w: environment entry %q is invalid", ErrInvalidRequest, name)
		}
	}
	if err := validateFiles(r.Files); err != nil {
		return err
	}
	if r.Checkout != nil {
		if err := r.Checkout.Validate(); err != nil {
			return err
		}
		for _, f := range r.Files {
			if f.Path == CheckoutDir || strings.HasPrefix(f.Path, CheckoutDir+"/") {
				return fmt.Errorf("%w: input %q is inside the checkout", ErrInvalidRequest, f.Path)
			}
		}
	}
	return nil
}

func validateFiles(files []File) error {
	seen := make(map[string]bool, len(files))
	total := 0
	for _, f := range files {
		if err := ValidRelativePath(f.Path); err != nil {
			return fmt.Errorf("%w: input %q: %w", ErrInvalidRequest, f.Path, err)
		}
		if seen[f.Path] {
			return fmt.Errorf("%w: input %q listed twice", ErrInvalidRequest, f.Path)
		}
		seen[f.Path] = true
		total += len(f.Content)
	}
	for p := range seen {
		// A file may not also be the directory of another input.
		for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				return fmt.Errorf("%w: input %q is also a directory", ErrInvalidRequest, dir)
			}
		}
	}
	if total > api.AgentRunInputFilesMaxBytes {
		return fmt.Errorf("%w: inputs hold %d bytes; at most %d", ErrInvalidRequest, total, api.AgentRunInputFilesMaxBytes)
	}
	return nil
}

// ValidRelativePath accepts a clean, relative, slash-separated path of
// printable components, the shape of an input or output file name.
func ValidRelativePath(p string) error {
	switch {
	case p == "" || len(p) > MaxInputPathBytes:
		return errors.New("path is empty or too long")
	case !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\\"):
		return errors.New("path has invalid characters")
	case path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == "..":
		return errors.New("path must be clean and relative")
	case strings.Count(p, "/")+1 > MaxInputPathDepth:
		return fmt.Errorf("path is deeper than %d components", MaxInputPathDepth)
	}
	for _, part := range strings.Split(p, "/") {
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return errors.New("path has control characters")
			}
		}
	}
	return nil
}

// OutputFile is one regular file from /work/output, returned inline.
type OutputFile struct {
	Name    string `json:"name"`
	Size    int    `json:"size"`
	SHA256  string `json:"sha256"`
	Content []byte `json:"content"`
}

// NewOutputFile builds an OutputFile with its size and digest.
func NewOutputFile(name string, content []byte) OutputFile {
	sum := sha256.Sum256(content)
	return OutputFile{Name: name, Size: len(content), SHA256: "sha256:" + hex.EncodeToString(sum[:]), Content: content}
}

// Result is the guest's terminal report. Outputs (Result and Files) are
// present only when the run succeeded: a run that did not succeed delivers
// nothing (ADR-20261027090010 §8). Stdout and Stderr are filled in by the client from
// the stream frames.
type Result struct {
	Status             Status          `json:"status"`
	ExitCode           int32           `json:"exit_code"`
	Signal             int32           `json:"signal,omitempty"`
	FailureCode        string          `json:"failure_code,omitempty"`
	FailureMessage     string          `json:"failure_message,omitempty"`
	Result             json.RawMessage `json:"result,omitempty"`
	Files              []OutputFile    `json:"files,omitempty"`
	LogTruncated       bool            `json:"log_truncated,omitempty"`
	StartedAtUnixNano  int64           `json:"started_at_unix_nano,omitempty"`
	FinishedAtUnixNano int64           `json:"finished_at_unix_nano,omitempty"`
	Stdout             []byte          `json:"-"`
	Stderr             []byte          `json:"-"`
}

// Validate checks a result against the request it answers.
func (r Result) Validate(req Request) error {
	switch {
	case !r.Status.Valid():
		return fmt.Errorf("%w: status %q", ErrInvalidResult, r.Status)
	case r.FailureCode != "" && !failureCode.MatchString(r.FailureCode):
		return fmt.Errorf("%w: failure_code %q", ErrInvalidResult, r.FailureCode)
	case len(r.FailureMessage) > MaxFailureMessageBytes:
		return fmt.Errorf("%w: failure_message too long", ErrInvalidResult)
	case r.Status != StatusSucceeded && (len(r.Result) > 0 || len(r.Files) > 0):
		return fmt.Errorf("%w: a run that did not succeed returns no outputs", ErrInvalidResult)
	case len(r.Result) > req.MaxResultBytes:
		return fmt.Errorf("%w: result is %d bytes; at most %d", ErrOutputLimitExceeded, len(r.Result), req.MaxResultBytes)
	case len(r.Files) > req.MaxOutputFiles:
		return fmt.Errorf("%w: %d output files; at most %d", ErrOutputLimitExceeded, len(r.Files), req.MaxOutputFiles)
	}
	if len(r.Result) > 0 && !IsJSONObject(r.Result) {
		return fmt.Errorf("%w: result is not a JSON object", ErrInvalidResult)
	}
	total := 0
	seen := make(map[string]bool, len(r.Files))
	for _, f := range r.Files {
		sum := sha256.Sum256(f.Content)
		switch {
		case ValidRelativePath(f.Name) != nil || seen[f.Name]:
			return fmt.Errorf("%w: output name %q", ErrInvalidResult, f.Name)
		case f.Size != len(f.Content) || !sha256Digest.MatchString(f.SHA256) || f.SHA256 != "sha256:"+hex.EncodeToString(sum[:]):
			return fmt.Errorf("%w: output %q size or digest does not match", ErrInvalidResult, f.Name)
		}
		seen[f.Name] = true
		total += f.Size
	}
	if total > req.MaxOutputBytes {
		return fmt.Errorf("%w: outputs hold %d bytes; at most %d", ErrOutputLimitExceeded, total, req.MaxOutputBytes)
	}
	return nil
}

// IsJSONObject reports whether data is exactly one JSON object.
func IsJSONObject(data []byte) bool {
	var object map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if dec.Decode(&object) != nil || object == nil {
		return false
	}
	_, err := dec.Token()
	return errors.Is(err, io.EOF)
}

// ErrorFrame is the guest's report when it cannot run the request at all.
type ErrorFrame struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// OutputReceiver sees each stdout or stderr chunk as it arrives. Returning an
// error ends the exchange; the caller then tears the VM down.
type OutputReceiver func(ctx context.Context, stream string, chunk []byte) error

// Run sends req on conn and reads until the result. Closing cancel asks the
// guest to stop the harness; Run still waits for the guest's result, so ctx
// must bound the whole exchange.
func Run(ctx context.Context, conn net.Conn, req Request, receive OutputReceiver, cancel <-chan struct{}) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	defer closeOnCancel(ctx, conn)()
	body, err := json.Marshal(req)
	if err != nil {
		return Result{}, fmt.Errorf("encode request: %w", err)
	}
	w := &frameWriter{conn: conn}
	if err := w.write(ctx, FrameRequest, body); err != nil {
		return Result{}, fmt.Errorf("send request: %w", err)
	}
	if cancel != nil {
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-cancel:
				_ = w.write(ctx, FrameCancel, nil)
			case <-done:
			}
		}()
	}
	var stdout, stderr bytes.Buffer
	logged := 0
	for {
		frameType, payload, err := readFrame(ctx, conn, maxResultFrameBytes)
		if err != nil {
			return Result{}, err
		}
		switch frameType {
		case FrameStdout, FrameStderr:
			if logged += len(payload); logged > req.MaxLogBytes {
				return Result{}, fmt.Errorf("%w: guest streamed more than %d log bytes", ErrOutputLimitExceeded, req.MaxLogBytes)
			}
			stream, buf := "stdout", &stdout
			if frameType == FrameStderr {
				stream, buf = "stderr", &stderr
			}
			buf.Write(payload)
			if receive != nil {
				if err := receive(ctx, stream, payload); err != nil {
					return Result{}, err
				}
			}
		case FrameResult:
			var result Result
			if err := decodeStrict(payload, &result); err != nil {
				return Result{}, fmt.Errorf("%w: %w", ErrInvalidResult, err)
			}
			if err := result.Validate(req); err != nil {
				return Result{}, err
			}
			result.Stdout, result.Stderr = stdout.Bytes(), stderr.Bytes()
			return result, nil
		case FrameError:
			var e ErrorFrame
			if err := decodeStrict(payload, &e); err != nil {
				return Result{}, fmt.Errorf("%w: undecodable error frame", ErrGuestFailed)
			}
			return Result{}, fmt.Errorf("%w: %s", ErrGuestFailed, safeText(e.Code))
		default:
			return Result{}, fmt.Errorf("%w: type %d", ErrUnexpectedFrame, frameType)
		}
	}
}

// Handler runs one validated request in the guest. stdout and stderr stream
// to the host and drop output past the request's log budget. cancelled
// closes when the host asks the run to stop.
type Handler func(ctx context.Context, req Request, stdout, stderr io.Writer, cancelled <-chan struct{}) (Result, error)

// Serve reads one request from conn, runs handler, and writes its result.
func Serve(ctx context.Context, conn net.Conn, handler Handler) error {
	if handler == nil {
		return errors.New("agent run protocol: nil handler")
	}
	defer closeOnCancel(ctx, conn)()
	w := &frameWriter{conn: conn}
	frameType, payload, err := readFrame(ctx, conn, maxRequestFrameBytes)
	if err != nil {
		return err
	}
	var req Request
	if frameType != FrameRequest {
		_ = w.writeError(ctx, "unexpected_frame", "")
		return fmt.Errorf("%w: type %d before the request", ErrUnexpectedFrame, frameType)
	}
	if err := decodeStrict(payload, &req); err != nil {
		_ = w.writeError(ctx, "invalid_request", "")
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err := req.Validate(); err != nil {
		_ = w.writeError(ctx, "invalid_request", "")
		return err
	}
	cancelled := make(chan struct{})
	go watchCancel(ctx, conn, cancelled)
	budget := &logBudget{remaining: req.MaxLogBytes}
	stdout := &streamWriter{ctx: ctx, w: w, frame: FrameStdout, budget: budget}
	stderr := &streamWriter{ctx: ctx, w: w, frame: FrameStderr, budget: budget}
	result, err := handler(ctx, req, stdout, stderr, cancelled)
	if err != nil {
		_ = w.writeError(ctx, "guest_failed", "")
		return err
	}
	result.LogTruncated = result.LogTruncated || budget.truncated()
	if err := result.Validate(req); err != nil {
		_ = w.writeError(ctx, "invalid_result", "")
		return err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	return w.write(ctx, FrameResult, body)
}

// watchCancel closes cancelled on the host's cancel frame. Reading ends when
// the connection closes; any other frame is ignored.
func watchCancel(ctx context.Context, conn net.Conn, cancelled chan struct{}) {
	var once sync.Once
	for {
		frameType, _, err := readFrame(ctx, conn, 0)
		if err != nil {
			return
		}
		if frameType == FrameCancel {
			once.Do(func() { close(cancelled) })
		}
	}
}

type logBudget struct {
	mu        sync.Mutex
	remaining int
	dropped   bool
}

// take returns how many of n bytes may still be sent.
func (b *logBudget) take(n int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.remaining {
		n = b.remaining
		b.dropped = true
	}
	b.remaining -= n
	return n
}

func (b *logBudget) truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// streamWriter frames one stream. It reports every write as complete so the
// harness never blocks on a full budget; dropped bytes set LogTruncated.
type streamWriter struct {
	ctx    context.Context
	w      *frameWriter
	frame  uint32
	budget *logBudget
}

func (s *streamWriter) Write(p []byte) (int, error) {
	n := len(p)
	keep := s.budget.take(n)
	for sent := 0; sent < keep; {
		end := min(sent+streamChunkBytes, keep)
		if err := s.w.write(s.ctx, s.frame, p[sent:end]); err != nil {
			return 0, err
		}
		sent = end
	}
	return n, nil
}

// frameWriter serializes frames from concurrent writers.
type frameWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (w *frameWriter) writeError(ctx context.Context, code, message string) error {
	body, err := json.Marshal(ErrorFrame{Code: code, Message: message})
	if err != nil {
		return err
	}
	return w.write(ctx, FrameError, body)
}

func (w *frameWriter) write(ctx context.Context, frameType uint32, body []byte) error {
	if len(body) > maxResultFrameBytes {
		return ErrFrameTooLarge
	}
	var hdr [frameHeaderBytes]byte
	binary.BigEndian.PutUint32(hdr[0:4], frameType)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := w.conn.Write(append(hdr[:], body...)); err != nil {
		return contextError(ctx, err)
	}
	return nil
}

func readFrame(ctx context.Context, conn net.Conn, maxBytes int) (uint32, []byte, error) {
	var hdr [frameHeaderBytes]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return 0, nil, contextError(ctx, err)
	}
	frameType := binary.BigEndian.Uint32(hdr[0:4])
	n := binary.BigEndian.Uint32(hdr[4:8])
	if int64(n) > int64(maxBytes) {
		return 0, nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, n)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, nil, contextError(ctx, err)
	}
	return frameType, body, nil
}

func decodeStrict(data []byte, v any) error {
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

// closeOnCancel closes conn when ctx ends, which unblocks reads and writes.
func closeOnCancel(ctx context.Context, conn net.Conn) func() {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func() { stop() }
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return err
}

// safeText bounds guest-supplied text before it reaches a host error.
func safeText(s string) string {
	if !failureCode.MatchString(s) {
		return "unknown"
	}
	return s
}
