package frameworkprofile

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

const ImageHealthcheckVersion = "v1"

// ImageHealthcheck captures even an absent override, so later app edits cannot
// change how a queued image inherits its immutable artifact's healthcheck.
type ImageHealthcheck struct {
	Version  string                  `json:"version"`
	Override *api.ComposeHealthcheck `json:"override"`
}

func CaptureImageRuntime(command []string, check *api.ComposeHealthcheck) ([]byte, error) {
	if err := check.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(Profile{Version: Version, Framework: "unknown",
		ImageCommand:     &ImageCommand{Version: ImageCommandVersion, Cmd: slices.Clone(command)},
		ImageHealthcheck: &ImageHealthcheck{Version: ImageHealthcheckVersion, Override: check.Clone()},
	})
}

func ImageHealthcheckFromProfile(profile []byte) (*ImageHealthcheck, error) {
	var fields map[string]json.RawMessage
	if len(profile) == 0 || json.Unmarshal(profile, &fields) != nil {
		return nil, nil //nolint:nilerr // Legacy advisory profiles retain artifact defaults when undecodable.
	}
	raw, present := fields["image_healthcheck"]
	if !present {
		return nil, nil
	}
	var captured struct {
		Version  string          `json:"version"`
		Override json.RawMessage `json:"override"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, fmt.Errorf("decode captured image healthcheck: %w", err)
	}
	if captured.Version != ImageHealthcheckVersion || len(captured.Override) == 0 {
		return nil, fmt.Errorf("unsupported captured image healthcheck contract")
	}
	var check *api.ComposeHealthcheck
	if err := json.Unmarshal(captured.Override, &check); err != nil {
		return nil, fmt.Errorf("decode captured healthcheck override: %w", err)
	}
	if err := check.Validate(); err != nil {
		return nil, fmt.Errorf("invalid captured healthcheck override: %w", err)
	}
	return &ImageHealthcheck{Version: captured.Version, Override: check}, nil
}
