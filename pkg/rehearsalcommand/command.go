// Package rehearsalcommand defines the bounded, deployment-attached command
// protocol for contained rehearsals (ADR-1352).
package rehearsalcommand

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

const (
	Version          = 1
	Executable       = "__gregale_rehearsal_command_v1__"
	MaxPayloadBytes  = 15 * 1024
	MaxInputBytes    = 8 * 1024
	MaxFiles         = 32
	MaxOutputBytes   = 8 * 1024
	MaxBehaviorBytes = 24 * 1024
	MaxTriggerBytes  = 4 * 1024
	MaxReceiptBytes  = 64 * 1024
)

var runPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Spec is persisted verbatim in the immutable app-task command. Inputs are
// regular files from the reviewed recipe, overlaid only in the disposable VM.
type Spec struct {
	Version      int               `json:"version"`
	RunID        string            `json:"run_id"`
	DeploymentID string            `json:"deployment_id"`
	Command      []string          `json:"command"`
	Env          map[string]string `json:"env,omitempty"`
	Files        map[string][]byte `json:"files,omitempty"`
}

func IsCommand(argv []string) bool { return len(argv) > 0 && argv[0] == Executable }

func (s Spec) Validate() error {
	if s.Version != Version || !runPattern.MatchString(s.RunID) || !uuidPattern.MatchString(s.DeploymentID) {
		return errors.New("invalid rehearsal command version or identity")
	}
	if len(s.Command) == 0 || len(s.Command) > 64 || strings.TrimSpace(s.Command[0]) == "" || s.Command[0] == Executable {
		return errors.New("invalid rehearsal command argv")
	}
	for _, arg := range s.Command {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return errors.New("invalid rehearsal command argument")
		}
	}
	if len(s.Env) > 32 || len(s.Files) > MaxFiles {
		return errors.New("rehearsal command input count exceeds protocol bound")
	}
	for key, value := range s.Env {
		if !envPattern.MatchString(key) || strings.ContainsRune(value, 0) || len(value) > 2048 || reservedEnv(key) {
			return fmt.Errorf("invalid rehearsal command environment key %q", key)
		}
	}
	total := 0
	for name, data := range s.Files {
		if !ValidPath(name) {
			return errors.New("rehearsal input must be a clean relative file path")
		}
		total += len(data)
	}
	if total > MaxInputBytes {
		return errors.New("rehearsal inputs exceed protocol byte bound")
	}
	return nil
}

func reservedEnv(key string) bool {
	upper := strings.ToUpper(key)
	return strings.HasPrefix(upper, "FAAS_") || strings.HasPrefix(upper, "GREGALE_") && !strings.HasPrefix(upper, "GREGALE_TEST_") ||
		upper == "GREGALE_TEST_BEHAVIOR_OUTPUT" || upper == "GREGALE_TEST_TRIGGER_OUTPUT" ||
		upper == "HTTP_PROXY" || upper == "HTTPS_PROXY" || upper == "ALL_PROXY" || upper == "NO_PROXY"
}

func ValidPath(name string) bool {
	return name != "" && name != "." && len(name) <= 512 && !strings.ContainsAny(name, "\\\x00") && !path.IsAbs(name) && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}

func Encode(s Spec) ([]string, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxPayloadBytes {
		return nil, errors.New("rehearsal command exceeds protocol byte bound")
	}
	argv := []string{Executable}
	// Existing app-task framing limits each argument to 4096 bytes. Split on
	// UTF-8 boundaries so the API's JSON encoding preserves the exact payload.
	for len(raw) > 0 {
		n := min(len(raw), 4000)
		for n < len(raw) && raw[n]&0xc0 == 0x80 {
			n--
		}
		argv = append(argv, string(raw[:n]))
		raw = raw[n:]
	}
	return argv, nil
}

func Decode(argv []string) (Spec, error) {
	var s Spec
	if !IsCommand(argv) || len(argv) < 2 || len(argv) > 5 {
		return s, errors.New("invalid rehearsal command envelope")
	}
	raw := strings.Join(argv[1:], "")
	if len(raw) > MaxPayloadBytes {
		return s, errors.New("rehearsal command exceeds protocol byte bound")
	}
	if err := decode([]byte(raw), &s); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func (s Spec) SHA256() string {
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Receipt is produced by guest-init after the command exits. The host records
// these exact bytes when VM cleanup and all relay obligations have completed.
// Behavior remains application-reported evidence.
type Receipt struct {
	Version         int    `json:"version"`
	TaskID          string `json:"task_id"`
	SpecSHA256      string `json:"spec_sha256"`
	Stdout          []byte `json:"stdout,omitempty"`
	Stderr          []byte `json:"stderr,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	// Null means absent; an empty base64 string means a present empty file.
	// Preserve that distinction so a later empty output invalidates an earlier
	// observation rather than silently retaining it.
	Behavior []byte `json:"behavior"`
	Trigger  []byte `json:"trigger"`
}

func (r Receipt) Marshal() ([]byte, error) {
	if !uuidPattern.MatchString(r.TaskID) || r.Version != Version || len(r.SpecSHA256) != 64 || len(r.Stdout)+len(r.Stderr) > MaxOutputBytes || len(r.Behavior) > MaxBehaviorBytes || len(r.Trigger) > MaxTriggerBytes {
		return nil, errors.New("invalid rehearsal command receipt")
	}
	if _, err := hex.DecodeString(r.SpecSHA256); err != nil {
		return nil, errors.New("invalid command digest")
	}
	raw, err := json.Marshal(r)
	if len(raw) > MaxReceiptBytes {
		return nil, errors.New("rehearsal command receipt exceeds byte bound")
	}
	return raw, err
}

func ParseReceipt(raw []byte, taskID string, s Spec) (Receipt, error) {
	var r Receipt
	if len(raw) > MaxReceiptBytes {
		return r, errors.New("rehearsal command receipt exceeds byte bound")
	}
	if err := decode(raw, &r); err != nil {
		return r, err
	}
	if _, err := r.Marshal(); err != nil {
		return r, err
	}
	if r.TaskID != taskID || r.SpecSHA256 != s.SHA256() {
		return r, errors.New("rehearsal command receipt identity differs")
	}
	return r, nil
}

func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing rehearsal protocol data")
	}
	return nil
}
