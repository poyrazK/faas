package frameworkprofile

import (
	"encoding/json"
	"fmt"
	"slices"
)

const ImageCommandVersion = "v1"

// ImageCommand freezes the Compose replacement for OCI CMD. Nil inherits the
// artifact's CMD; a non-nil slice replaces it while retaining OCI ENTRYPOINT.
// Its own version remains independent of advisory source-profile inference.
type ImageCommand struct {
	Version string   `json:"version"`
	Cmd     []string `json:"cmd"`
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
		Version string          `json:"version"`
		Cmd     json.RawMessage `json:"cmd"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, fmt.Errorf("decode captured image command: %w", err)
	}
	if captured.Version != ImageCommandVersion || len(captured.Cmd) == 0 {
		return nil, fmt.Errorf("unsupported captured image command contract")
	}
	var command []string
	if err := json.Unmarshal(captured.Cmd, &command); err != nil {
		return nil, fmt.Errorf("decode captured image CMD: %w", err)
	}
	return &ImageCommand{Version: captured.Version, Cmd: command}, nil
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
