package frameworkprofile

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ImageCommandVersion = "v1"
	// ImageCommandProcessVersion adds the Compose process overrides
	// (ADR-1337). An imaged that predates it rejects the contract instead of
	// silently starting the image's own entrypoint, user or directory.
	ImageCommandProcessVersion = "v2"
)

// ImageCommand freezes the Compose replacement for OCI CMD. Nil inherits the
// artifact's CMD; a non-nil slice replaces it while retaining OCI ENTRYPOINT.
// Process, present only in v2, freezes the Compose entrypoint, user,
// working_dir and stop contract; a Process entrypoint also drops the image
// CMD, so Cmd is then the whole argument list.
// Its own version remains independent of advisory source-profile inference.
type ImageCommand struct {
	Version string              `json:"version"`
	Cmd     []string            `json:"cmd"`
	Process *api.ComposeProcess `json:"process,omitempty"`
}

// newImageCommand captures v1, or v2 when the service overrides its process.
func newImageCommand(command []string, process *api.ComposeProcess) (*ImageCommand, error) {
	captured := &ImageCommand{Version: ImageCommandVersion, Cmd: slices.Clone(command)}
	if process.IsZero() {
		return captured, nil
	}
	if err := process.Validate(); err != nil {
		return nil, err
	}
	captured.Version, captured.Process = ImageCommandProcessVersion, process.Clone()
	return captured, nil
}

// CaptureImageCommand builds a persisted image profile before work is queued.
func CaptureImageCommand(command []string) ([]byte, error) {
	return json.Marshal(Profile{Version: Version, Framework: "unknown", ImageCommand: &ImageCommand{
		Version: ImageCommandVersion, Cmd: slices.Clone(command),
	}})
}

// ImageCommandFromProfile distinguishes an inherited CMD captured at admission
// from a legacy deployment without a captured contract. Invalid captured
// commands fail closed; legacy advisory profiles retain their fallback behavior.
func ImageCommandFromProfile(profile []byte) (*ImageCommand, error) {
	var fields map[string]json.RawMessage
	if len(profile) == 0 || json.Unmarshal(profile, &fields) != nil {
		return nil, nil //nolint:nilerr // Legacy advisory profiles retain OCI defaults when undecodable.
	}
	raw, present := fields["image_command"]
	if !present {
		return nil, nil
	}
	var captured struct {
		Version string              `json:"version"`
		Cmd     json.RawMessage     `json:"cmd"`
		Process *api.ComposeProcess `json:"process"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, fmt.Errorf("decode captured image command: %w", err)
	}
	switch {
	case len(captured.Cmd) == 0:
		return nil, fmt.Errorf("unsupported captured image command contract")
	case captured.Version == ImageCommandVersion && captured.Process != nil,
		captured.Version == ImageCommandProcessVersion && captured.Process.IsZero(),
		captured.Version != ImageCommandVersion && captured.Version != ImageCommandProcessVersion:
		return nil, fmt.Errorf("unsupported captured image command contract")
	}
	if err := captured.Process.Validate(); err != nil {
		return nil, fmt.Errorf("decode captured image process: %w", err)
	}
	var command []string
	if err := json.Unmarshal(captured.Cmd, &command); err != nil {
		return nil, fmt.Errorf("decode captured image CMD: %w", err)
	}
	return &ImageCommand{Version: captured.Version, Cmd: command, Process: captured.Process}, nil
}

// PreserveImageRuntime updates inferred metadata without allowing the worker
// to replace or introduce admission-owned command and healthcheck contracts.
func PreserveImageRuntime(previous, updated []byte) ([]byte, error) {
	var next map[string]json.RawMessage
	if err := json.Unmarshal(updated, &next); err != nil {
		return nil, fmt.Errorf("decode image runtime profile: %w", err)
	}
	if next == nil {
		return nil, fmt.Errorf("image runtime profile must be an object")
	}
	delete(next, "image_command")
	delete(next, "image_healthcheck")
	var prior map[string]json.RawMessage
	if json.Unmarshal(previous, &prior) == nil {
		for _, key := range []string{"image_command", "image_healthcheck"} {
			if value, exists := prior[key]; exists {
				next[key] = value
			}
		}
	}
	return json.Marshal(next)
}
